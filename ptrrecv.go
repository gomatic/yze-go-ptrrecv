// Package ptrrecv provides a go/analysis analyzer enforcing the gomatic Go
// immutability standard: methods use value receivers, never pointer receivers,
// unless the receiver type transitively contains a field that cannot be copied
// (a sync primitive, atomic, buffer, or builder).
package ptrrecv

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"strings"

	goyze "github.com/gomatic/go-yze"
	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/passes/inspect"
	"golang.org/x/tools/go/ast/inspector"
)

// noCopyTypes are the standard-library types whose presence in a struct makes a
// pointer receiver legitimate, because they must not be copied after first use.
var noCopyTypes = map[string]bool{
	"sync.Mutex":          true,
	"sync.RWMutex":        true,
	"sync.WaitGroup":      true,
	"sync.Once":           true,
	"sync.Pool":           true,
	"sync.Map":            true,
	"sync.Cond":           true,
	"sync/atomic.Int32":   true,
	"sync/atomic.Int64":   true,
	"sync/atomic.Uint32":  true,
	"sync/atomic.Uint64":  true,
	"sync/atomic.Bool":    true,
	"sync/atomic.Uintptr": true,
	"sync/atomic.Pointer": true,
	"sync/atomic.Value":   true,
	"bytes.Buffer":        true,
	"strings.Builder":     true,
}

// allowExtra is the configurable allow-list of additional fully-qualified
// no-copy types (pkgpath.Name), set via the -allow flag or analyzer config.
var allowExtra string

// Analyzer reports pointer-receiver methods on types that need no pointer.
var Analyzer = newAnalyzer()

func newAnalyzer() *analysis.Analyzer {
	a := &analysis.Analyzer{
		Name:     "ptrrecv",
		Doc:      "reports pointer-receiver methods unless the receiver type contains a no-copy field",
		Requires: []*analysis.Analyzer{inspect.Analyzer},
		Run:      run,
	}
	a.Flags.StringVar(&allowExtra, "allow", "", "comma-separated extra fully-qualified no-copy types (pkgpath.Name)")
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
	allow := buildAllow(allowCSV(allowExtra))
	insp := pass.ResultOf[inspect.Analyzer].(*inspector.Inspector)
	insp.Preorder([]ast.Node{(*ast.FuncDecl)(nil)}, func(n ast.Node) {
		check(pass, allow, n.(*ast.FuncDecl))
	})
	return nil, nil
}

// allowCSV is the raw comma-separated -allow flag value listing extra
// fully-qualified no-copy types.
type allowCSV string

// buildAllow merges the baked-in no-copy types with the configured extras.
func buildAllow(extra allowCSV) map[string]bool {
	allow := make(map[string]bool, len(noCopyTypes))
	for name := range noCopyTypes {
		allow[name] = true
	}
	for _, name := range splitNonEmpty(extra) {
		allow[name] = true
	}
	return allow
}

func splitNonEmpty(value allowCSV) []string {
	if value == "" {
		return nil
	}
	return strings.Split(string(value), ",")
}

// check reports a pointer-receiver method whose type needs no pointer, attaching
// the value-receiver rewrite when it is provably behavior-preserving.
func check(pass *analysis.Pass, allow map[string]bool, fn *ast.FuncDecl) {
	star, recv := pointerReceiver(pass, fn)
	if recv == nil || requiresPointer(allow, recv) || decoderMethod(pass, fn) {
		return
	}
	pass.Report(analysis.Diagnostic{
		Pos: fn.Recv.List[0].Pos(),
		Message: fmt.Sprintf(
			"pointer receiver on %s should be a value receiver; the type holds no field that requires a pointer",
			recv.Obj().Name(),
		),
		SuggestedFixes: fixes(pass, fn, star),
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

// requiresPointer reports whether t must not be copied: it is itself a no-copy
// type — allow-listed or satisfying the vet copylocks locker shape — or it
// transitively holds one through struct fields and array elements.
func requiresPointer(allow map[string]bool, t types.Type) bool {
	if isNoCopy(allow, t) || lockerShape(t) {
		return true
	}
	return componentsRequirePointer(allow, t)
}

// componentsRequirePointer descends into the component types whose copy copies
// the component itself: struct fields and array elements (an array stores its
// elements inline, so a no-copy element makes the whole array — and its
// enclosing struct — uncopyable, as go vet copylocks treats it). Slices, maps,
// channels, and pointers are deliberately not walked: they are references whose
// copy duplicates only the header/pointer, never the pointee, so they leave the
// type copyable.
func componentsRequirePointer(allow map[string]bool, t types.Type) bool {
	switch u := t.Underlying().(type) {
	case *types.Struct:
		return anyFieldRequiresPointer(allow, u)
	case *types.Array:
		return requiresPointer(allow, u.Elem())
	}
	return false
}

// anyFieldRequiresPointer reports whether any field of st is a no-copy type or
// transitively holds one.
func anyFieldRequiresPointer(allow map[string]bool, st *types.Struct) bool {
	for i := range st.NumFields() {
		if requiresPointer(allow, st.Field(i).Type()) {
			return true
		}
	}
	return false
}

// isNoCopy reports whether ft names an allow-listed no-copy type. The type is
// unaliased first so an aliased primitive (`type MuAlias = sync.Mutex`) is
// still recognized instead of falling through to its plain underlying struct.
func isNoCopy(allow map[string]bool, ft types.Type) bool {
	named, ok := types.Unalias(ft).(*types.Named)
	if !ok || named.Obj().Pkg() == nil {
		return false
	}
	return allow[named.Obj().Pkg().Path()+"."+named.Obj().Name()]
}

// lockerIface is the sync.Locker shape — nullary Lock and Unlock — built
// structurally so the check needs no import of sync's export data.
var lockerIface = newLockerIface()

func newLockerIface() *types.Interface {
	nullary := types.NewSignatureType(nil, nil, nil, nil, nil, false)
	iface := types.NewInterfaceType([]*types.Func{
		types.NewFunc(token.NoPos, nil, "Lock", nullary),
		types.NewFunc(token.NoPos, nil, "Unlock", nullary),
	}, nil)
	iface.Complete()
	return iface
}

// lockerShape reports whether t is uncopyable by the vet copylocks criterion:
// its POINTER method set satisfies the Locker shape while its value method set
// does not — the `type noCopy struct{}` marker idiom and the sync primitives
// themselves. Satisfying the shape requires the pointer receivers, so such a
// type's methods (Lock/Unlock included) are legitimate. A type whose VALUE is
// already a Locker is freely copyable and stays flagged.
func lockerShape(t types.Type) bool {
	return types.Implements(types.NewPointer(t), lockerIface) && !types.Implements(t, lockerIface)
}

// decoderNames are the well-known decode/bind interface methods whose pointer
// receiver is dictated by the contract itself: the interface writes INTO the
// receiver and returns only an error, so a value receiver cannot implement it
// (encoding.TextUnmarshaler, json/yaml/xml Unmarshalers, gob, sql.Scanner,
// flag.Value's Set).
var decoderNames = map[string]bool{
	"UnmarshalJSON":   true,
	"UnmarshalYAML":   true,
	"UnmarshalText":   true,
	"UnmarshalBinary": true,
	"UnmarshalXML":    true,
	"UnmarshalTOML":   true,
	"GobDecode":       true,
	"Scan":            true,
	"Set":             true,
}

// decoderMethod reports whether fn is a decode/bind contract method: a
// well-known name AND the contract shape — exactly one result, of the builtin
// error interface, verified semantically. An ordinary setter that happens to be
// called Set (no error result), a multi-error signature (`(a, b error)`), or a
// result naming a package-level `type error` shadow stays reported.
func decoderMethod(pass *analysis.Pass, fn *ast.FuncDecl) bool {
	if !decoderNames[fn.Name.Name] {
		return false
	}
	sig := pass.TypesInfo.Defs[fn.Name].Type().(*types.Signature)
	return sig.Results().Len() == 1 && isBuiltinError(sig.Results().At(0).Type())
}

// isBuiltinError reports whether t is the universe's error interface itself.
func isBuiltinError(t types.Type) bool {
	return types.Identical(t, types.Universe.Lookup("error").Type())
}
