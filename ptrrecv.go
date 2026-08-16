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
// three are exempt here, each by a check stated below.
//
// # What is reported
//
// A method whose receiver is written `*T` and for which all of the following
// hold. Each is an exemption in its own right, and each is listed here because
// an exemption the doc comment does not state gets no case, no forgery probe
// and no reviewer.
//
//  1. The receiver type does not have to be a pointer, by five criteria that
//     apply to the receiver type ITSELF and, transitively, to its struct fields
//     and array elements:
//
//     a. It is not named by -allow (below).
//
//     b. It does not satisfy the go vet copylocks Locker shape — a POINTER
//     method set with nullary Lock and Unlock, and a value method set without
//     them. This is the `type noCopy struct{}` marker idiom, and it is
//     FORGEABLE: two empty methods, or one `_ noCopy` field, silence the rule
//     for the type and for everything holding it. Forging it makes go vet
//     refuse every COPY of the type — but a type with a pointer-based API is
//     never copied, so on the population this rule reports the marker costs
//     nothing. An open hole with no discriminator, not a sanctioned escape.
//
//     c. It is not a standard-library type whose API is entirely pointer-based
//     — every method needs a pointer, none is available on a value. That is
//     bytes.Buffer, strings.Builder, bufio.Writer/Reader/Scanner,
//     math/rand.Rand, every sync primitive and every sync/atomic type,
//     derived from the types rather than listed by name. time.Time,
//     time.Duration and os.File have value methods and are judged normally.
//
//     THIS CRITERION DOES NOT APPLY TO A TYPE THE PACKAGE UNDER ANALYSIS
//     DECLARES, and cannot: the criterion IS "every method takes a pointer",
//     which is the very shape this rule reports, so applying it to the
//     package's own types would exempt every type all of whose methods are
//     pointer methods, and the rule would report almost nothing. Measured by
//     deleting the exclusion — 24 of the fixture's expectations go silent,
//     Timed, Scalar, SliceGuarded, Honest, Setting and NoErr among them, and
//     the count over the Go 1.26.6 standard library falls from 1709 locations
//     to 161. The visible cost of keeping it is that bytes.Buffer's and
//     strings.Builder's OWN read-only methods are reported when the standard
//     library is the tree under analysis — nine locations, on the two types
//     this clause names by name.
//     That is a real inconsistency with no available fix, disclosed here
//     rather than hidden, and reachable only by analysing the standard library
//     itself: every other module's own packages are excluded by module path
//     before this criterion is consulted.
//
//     d. It is not a type parameter, whose instantiation may be any of the
//     above (Box[sync.Mutex]).
//
//     e. It is not a type DECLARED HERE and written over a standard-library
//     type that criterion (1c) exempts — `type MyBuf bytes.Buffer`. Such a
//     type takes the original's representation and leaves every method behind,
//     so the method set (1c) reads has nothing to say about it, and the
//     ORIGINAL is asked instead. `type Timestamp time.Time` is written over a
//     type with 49 value methods and is judged normally.
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
//     GobDecode take []byte; UnmarshalTOML and Scan take any, and Scan is also
//     fmt.Scanner's (fmt.ScanState, rune); Set takes string; UnmarshalXML takes
//     *xml.Decoder and xml.StartElement; UnmarshalYAML takes any of func(any)
//     error, *yaml.Node or []byte — yaml.v2's, yaml.v3's and goccy/go-yaml's. A
//     right-named method with the wrong parameters implements nothing and is
//     reported.
//
//  3. Replacing the pointer receiver with a value receiver preserves what the
//     method body observes. This is where the standard's second and third
//     justifications are honoured: a method that assigns through its receiver,
//     takes its address, mentions it bare, or calls a pointer-receiver method
//     on it needs the pointer to have any effect at all, so it is exempt — as
//     is a bodyless method, whose receiver ABI is invisible here.
//
//     A receiver mentioned inside a function literal that can OUTLIVE the call
//     — returned, stored, passed on, or started with `go` — is exempt for the
//     same reason even when the mention is only a read: the literal captures
//     the receiver itself, so a pointer receiver observes the caller's value
//     live while a value receiver observes a copy frozen at call time. A
//     literal invoked where it is written, `defer` included, runs before the
//     method returns and is walked like any other subtree; parentheses around
//     it change nothing.
//
//     And the method must not still be OBSERVING its receiver after anything
//     could have changed the object, because the copy is made at CALL time: a
//     read that follows a call, a channel operation, a `close`, a range over a
//     channel or an iterator, or a write the method makes anywhere but into its
//     own local storage, sees what an alias left there, which a value receiver
//     never does. `func (c *C) WithStep(step Step) Count { step(); return c.n }` is
//     reported by every clause above and its remedy takes the program from 99
//     to 1. A read in a loop or a `defer` counts as following the barrier
//     however early it is written; arguments are evaluated before their call
//     and do not. alias.go carries the two reproductions, and the cost of the
//     clause is 273 of the 1431 source-only locations this rule reported across
//     the Go 1.26.6 standard library, and 298 of the 1709 with test files.
//
//  4. The receiver is small enough to copy — at most the -max bound (below),
//     128 bytes by default. Nothing in (1) asks how BIG a type is, so a
//     [1<<15]uint32 field multiplies the copy by 32768 unseen, and a value
//     receiver on the resulting 640 KiB struct overflows the goroutine stack.
//     size.go carries the reproduction and the measurement behind the default.
//
//     THE WIDTH IS MEASURED ON A STATED LAYOUT, gc/amd64, and not on the
//     platform under analysis. A width made of words moves with GOARCH while the
//     bound is in bytes, so the driver's own sizes make the VERDICT move:
//     `struct{ w [20]uintptr; n int }` is 168 bytes on a 64-bit target and 84 on
//     a 32-bit one, and the same binary on the same untagged file was silent
//     under GOARCH=amd64 and reported under GOARCH=386. One TYPE now measures
//     the same everywhere, which is all a layout can buy: a type whose own
//     dimensions come from a platform-dependent constant is a different type
//     before this analyzer sees it, and size.go records that residue.
//
//     A GENERIC RECEIVER IS MEASURED, on its narrowest instance, with the empty
//     struct substituted for every type parameter it stands on. Declining to
//     measure it — which is what a type parameter's absent width used to buy —
//     turned this criterion off for anything carrying one: a `[T any]` on the
//     declaration took a 131080-byte receiver from withheld to reported, and
//     the remedy from 977µs to 26.8s over two million calls. A width go/types
//     will not state, which only a generic declaration reaches, is costly. The
//     substitution itself changes no verdict this analyzer reaches, which
//     size.go measures rather than assumes.
//
// # There is no automatic rewrite, deliberately
//
// This analyzer attaches no SuggestedFix, so `--fix` never rewrites a receiver.
// It reports; a person deletes the `*` and runs the tests.
//
// The reason is that deleting the `*` moves the method out of *T's method set
// and into T's, and no analysis of one package can decide what that costs. A T
// VALUE that satisfied nothing then satisfies fmt.Stringer, error,
// json.Marshaler, sort.Interface or whichever interface the method's name and
// signature belong to, and every type assertion, type switch, %v verb and
// encoder dispatch on a T value in any OTHER package changes answer. Those
// packages are ones the analyzer never loads, and the set of interfaces a name
// might belong to is open — any package may declare one — so no list of names
// closes it.
//
// Measured, each end to end with `go vet` silent throughout: `func (m *Money)
// MarshalJSON()` held by value in an Invoice takes json.Marshal from
// {"Total":{"Cents":1234}} to {"Total":"12.34"}; `func (t *Temp) String()`
// takes fmt.Println(Temp{21}) from {21} to 21C and a fmt.Stringer assertion on
// a Temp value from false to true; and on the standard library itself, %v of a
// strings.Builder VALUE starts printing its contents. Re-measured on Go 1.26.6
// with criterion (3)'s alias clause in force: this rule reports 1411 receivers
// with test files included and 1158 without, and 272 of them carry a method
// name belonging to a published interface — 55 Error, 45 String, 32 Size, 23
// Len, 21 Close, 21 Unwrap, 19 Read, 17 Write and the rest — which is an upper
// bound on the exposure and not a count of harmful ones. The three figures were
// 1709, 1431 and 338 before the alias clause narrowed the population.
//
// An earlier revision claimed the rewrite "only widens the method set, so
// callers keep compiling". That is true about compiling and false about
// behaviour, which is why the claim is gone rather than qualified.
//
// # Configuration
//
// The -allow flag (setting "allow") takes a comma-separated list of
// fully-qualified type names — pkgpath.Name, e.g. "example.com/x.Pool" —
// types that must not be copied and that criterion (1c) cannot derive, because
// they are not in the standard library. Entries are trimmed; an entry that is
// not pkgpath.Name is refused with ErrInvalidAllowEntry rather than accepted as
// a name nothing matches.
//
// Configuring it exempts the named type as a RECEIVER as well as a field: the
// criterion is applied to the receiver type itself first, so "-allow=x.T"
// silences every method on T. It is a disablement channel, and a silent one —
// a configured exemption prints nothing and ratchets nothing.
//
// The -max flag (setting "max") is criterion (4)'s bound in bytes, 128 by
// default. A value that is not an integer of at least one pointer width (8, on
// the layout above) is refused with ErrInvalidMaxCopy rather than accepted as a
// bound nothing can honour: below that the bound withholds findings on
// receivers whose copy is narrower than the pointer it replaces, which is not a
// bound on cost but a refusal to judge, and it prints nothing while doing it.
// `-max=0` was accepted and took the standard library from 1431 findings to 65.
// Raising it is a disablement channel of the same kind as -allow, and lowering
// it within the permitted range is one too — how much either may move is the
// owner's to set, and is not decided here.
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
	a.Flags.Var(&configuredMaxCopy, "max", "maximum receiver size in bytes whose copy by value is judged free")
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
	judge := newJudgement(pass)
	insp := pass.ResultOf[inspect.Analyzer].(*inspector.Inspector)
	insp.Preorder([]ast.Node{(*ast.FuncDecl)(nil)}, func(n ast.Node) {
		check(pass, judge, n.(*ast.FuncDecl))
	})
	return nil, nil
}

// newJudgement is one pass's decision context. The layout is judgedSizes and
// never pass.TypesSizes, which is the whole content of the platform clause: the
// driver hands the pass the sizes of the platform under analysis, and reading
// them here is what made the same file silent under GOARCH=amd64 and reported
// under GOARCH=386.
func newJudgement(pass *analysis.Pass) judgement {
	return judgement{
		allow:  configuredAllow.set(),
		own:    pass.Pkg,
		module: modulePath(pass),
		sizes:  judgedSizes,
	}
}

// check reports a pointer-receiver method nothing requires a pointer for.
func check(pass *analysis.Pass, judge judgement, fn *ast.FuncDecl) {
	recv := pointerReceiver(pass, fn)
	if recv == nil || !judge.reportable(pass, fn, recv) {
		return
	}
	pass.Report(analysis.Diagnostic{
		Pos: fn.Recv.List[0].Pos(),
		Message: fmt.Sprintf(
			"pointer receiver on %s should be a value receiver; the type holds no field that "+
				"requires a pointer and the method never writes through it. Deleting the * also "+
				"moves this method into %s's value method set, so check what a value of it is "+
				"asserted to satisfy elsewhere",
			recv.Obj().Name(), recv.Obj().Name(),
		),
	})
}

// reportable applies the four criteria of the package doc comment to one
// pointer-receiver method: the type does not have to be a pointer, the receiver
// is small enough to copy, the method implements no decode/bind contract, and
// the value-receiver rewrite preserves what the body observes.
func (j judgement) reportable(pass *analysis.Pass, fn *ast.FuncDecl, recv *types.Named) bool {
	return !j.requiresPointer(recv) &&
		!j.copyIsCostly(recv) &&
		!decoderMethod(pass, fn) &&
		rewriteSafe(pass, fn)
}

// pointerReceiver returns the named base type of fn's receiver when fn is a
// method with a pointer receiver, and nil otherwise. The receiver type is
// unaliased first: since Go 1.23 a receiver written through a type alias (e.g.
// "type Alias = Inner; func (Alias) M()") resolves to *types.Alias, so a bare
// *types.Named assertion would panic on otherwise valid code.
func pointerReceiver(pass *analysis.Pass, fn *ast.FuncDecl) *types.Named {
	if fn.Recv == nil {
		return nil
	}
	star, isPointer := fn.Recv.List[0].Type.(*ast.StarExpr)
	if !isPointer {
		return nil
	}
	named, _ := types.Unalias(pass.TypesInfo.TypeOf(star.X)).(*types.Named)
	return named
}
