// Package ptrrecv provides a go/analysis analyzer serving the gomatic Go
// immutability standard's preference for value receivers: a method takes a
// pointer receiver only when something requires one.
//
// It reports a pointer receiver only where NOTHING requires it, which is
// narrower than the standard it serves and deliberately so. The standard
// sanctions three justifications — machinery that cannot be copied, a method
// whose contract is to mutate the receiver, and an interface that demands a
// pointer — and a diagnostic on any of the three names a remedy its author
// cannot take, which is answered with a baseline rather than a fix. So all
// three are exempt here, each by a check stated below, and every finding this
// analyzer emits carries the rewrite that resolves it.
//
// # What is reported
//
// A method whose receiver is written `*T` and for which all of the following
// hold. Each is an exemption in its own right, and each is listed here because
// an exemption the doc comment does not state gets no case, no forgery probe
// and no reviewer.
//
//  1. The receiver type does not have to be a pointer, by four criteria that
//     apply to the receiver type ITSELF and, transitively, to its struct fields
//     and array elements:
//
//     a. It is not named by -allow (below).
//
//     b. It does not satisfy the go vet copylocks Locker shape — a POINTER
//     method set with nullary Lock and Unlock, and a value method set without
//     them. This is the `type noCopy struct{}` marker idiom. It is FORGEABLE
//     BY DESIGN: two empty methods silence the rule for the type and for
//     everything holding it, and they also hand the type to go vet, which
//     then refuses every copy of it. The forgery acquires the property.
//
//     c. It is not a standard-library type whose API is entirely pointer-based
//     — every method needs a pointer, none is available on a value. That is
//     bytes.Buffer, strings.Builder, bufio.Writer/Reader/Scanner,
//     math/rand.Rand, every sync primitive and every sync/atomic type,
//     derived from the types rather than listed by name. time.Time,
//     time.Duration and os.File have value methods and are judged normally.
//
//     d. It is not a type parameter, whose instantiation may be any of the
//     above (Box[sync.Mutex]).
//
//     SCOPE LIMITATION, deliberate: slices, maps, channels and pointers are NOT
//     walked, because copying one of those duplicates only the header or the
//     pointer and both copies still refer to the same pointee. Arrays ARE
//     walked, because an array stores its elements inline. A struct holding
//     []sync.Mutex is therefore copyable and IS reported.
//
//  2. The method implements no decode/bind contract. Nine well-known names are
//     exempt, each only with the parameter list and single error result its
//     interface dictates: UnmarshalJSON, UnmarshalText, UnmarshalBinary and
//     GobDecode take []byte; UnmarshalTOML and Scan take any; Set takes string;
//     UnmarshalXML takes *xml.Decoder and xml.StartElement; UnmarshalYAML takes
//     either func(any) error or *yaml.Node. A right-named method with the wrong
//     parameters implements nothing and is reported.
//
//  3. Replacing the pointer receiver with a value receiver provably preserves
//     the method's behaviour. This is where the standard's second and third
//     justifications are honoured: a method that assigns through its receiver,
//     takes its address, mentions it bare, or calls a pointer-receiver method
//     on it needs the pointer to have any effect at all, so it is exempt — as
//     is a bodyless method, whose receiver ABI is invisible here.
//
// # The fix
//
// Every diagnostic carries a SuggestedFix deleting the receiver's "*", which is
// the same condition as (3): the analyzer reports exactly the receivers it can
// rewrite. Applying it only widens the method set, so callers keep compiling.
//
// # Configuration
//
// The -allow flag (setting "allow") takes a comma-separated list of
// fully-qualified type names — pkgpath.Name, e.g. "example.com/x.Pool" — naming
// types that must not be copied and that criterion (1c) cannot derive, because
// they are not in the standard library. Entries are trimmed; an entry that is
// not pkgpath.Name is refused with ErrInvalidAllowEntry rather than accepted as
// a name nothing matches.
//
// Configuring it exempts the named type as a RECEIVER as well as a field: the
// criterion is applied to the receiver type itself first, so "-allow=x.T"
// silences every method on T. It is a disablement channel, and a silent one —
// a configured exemption prints nothing and ratchets nothing.
package ptrrecv

import (
	"fmt"
	"go/ast"
	"go/types"

	goyze "github.com/gomatic/go-yze"
	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/passes/inspect"
	"golang.org/x/tools/go/ast/inspector"
)

// Analyzer reports pointer-receiver methods on types that need no pointer.
var Analyzer = newAnalyzer()

func newAnalyzer() *analysis.Analyzer {
	a := &analysis.Analyzer{
		Name:     "ptrrecv",
		Doc:      "reports pointer-receiver methods that nothing requires a pointer for",
		Requires: []*analysis.Analyzer{inspect.Analyzer},
		Run:      run,
	}
	a.Flags.Var(&configuredAllow, "allow", "comma-separated extra fully-qualified no-copy types (pkgpath.Name)")
	return a
}

// Registration declares this analyzer to the yze framework.
var Registration = goyze.Registration{
	Name:       "ptrrecv",
	Categories: []goyze.Category{"immutability"},
	URL:        "https://docs.gomatic.dev/yze/ptrrecv",
	Analyzer:   Analyzer,
}

// run reports each unjustified pointer-receiver method.
func run(pass *analysis.Pass) (any, error) {
	judge := judgement{allow: configuredAllow.set(), own: pass.Pkg, module: modulePath(pass)}
	insp := pass.ResultOf[inspect.Analyzer].(*inspector.Inspector)
	insp.Preorder([]ast.Node{(*ast.FuncDecl)(nil)}, func(n ast.Node) {
		check(pass, judge, n.(*ast.FuncDecl))
	})
	return nil, nil
}

// check reports a pointer-receiver method whose type needs no pointer and whose
// value-receiver rewrite preserves its behaviour — which is the same condition,
// so the diagnostic always carries the fix that answers it.
func check(pass *analysis.Pass, judge judgement, fn *ast.FuncDecl) {
	star, recv := pointerReceiver(pass, fn)
	if recv == nil || judge.requiresPointer(recv) || decoderMethod(pass, fn) {
		return
	}
	fix := fixes(pass, fn, star)
	if fix == nil {
		return
	}
	pass.Report(analysis.Diagnostic{
		Pos: fn.Recv.List[0].Pos(),
		Message: fmt.Sprintf(
			"pointer receiver on %s should be a value receiver; the type holds no field that "+
				"requires a pointer and the method never writes through it",
			recv.Obj().Name(),
		),
		SuggestedFixes: fix,
	})
}

// pointerReceiver returns the receiver's star expression and the named base
// type of fn's receiver when fn is a method with a pointer receiver, and nils
// otherwise. The receiver type is unaliased first: since Go 1.23 a receiver
// written through a type alias (e.g. "type Alias = Inner; func (Alias) M()")
// resolves to *types.Alias, so a bare *types.Named assertion would panic on
// otherwise valid code.
func pointerReceiver(pass *analysis.Pass, fn *ast.FuncDecl) (*ast.StarExpr, *types.Named) {
	if fn.Recv == nil {
		return nil, nil
	}
	star, ok := fn.Recv.List[0].Type.(*ast.StarExpr)
	if !ok {
		return nil, nil
	}
	named, _ := types.Unalias(pass.TypesInfo.TypeOf(star.X)).(*types.Named)
	return star, named
}
