// Package ptrrecv provides a go/analysis analyzer enforcing the gomatic Go
// immutability standard: methods use value receivers, never pointer receivers,
// unless the receiver type transitively contains a field that cannot be copied
// (a sync primitive, atomic, buffer, or builder).
package ptrrecv

import (
	"go/ast"
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
	allow := buildAllow(allowExtra)
	insp := pass.ResultOf[inspect.Analyzer].(*inspector.Inspector)
	insp.Preorder([]ast.Node{(*ast.FuncDecl)(nil)}, func(n ast.Node) {
		check(pass, allow, n.(*ast.FuncDecl))
	})
	return nil, nil
}

// buildAllow merges the baked-in no-copy types with the configured extras.
func buildAllow(extra string) map[string]bool {
	allow := make(map[string]bool, len(noCopyTypes))
	for name := range noCopyTypes {
		allow[name] = true
	}
	for _, name := range splitNonEmpty(extra) {
		allow[name] = true
	}
	return allow
}

func splitNonEmpty(value string) []string {
	if value == "" {
		return nil
	}
	return strings.Split(value, ",")
}

// check reports a pointer-receiver method whose type needs no pointer.
func check(pass *analysis.Pass, allow map[string]bool, fn *ast.FuncDecl) {
	recv := pointerReceiver(pass, fn)
	if recv == nil || requiresPointer(allow, recv) {
		return
	}
	pass.Reportf(
		fn.Recv.List[0].Pos(),
		"pointer receiver on %s should be a value receiver; the type holds no field that requires a pointer",
		recv.Obj().Name(),
	)
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
	star, ok := fn.Recv.List[0].Type.(*ast.StarExpr)
	if !ok {
		return nil
	}
	named, _ := types.Unalias(pass.TypesInfo.TypeOf(star.X)).(*types.Named)
	return named
}

// requiresPointer reports whether t is a struct transitively containing a no-copy
// field.
func requiresPointer(allow map[string]bool, t types.Type) bool {
	st, ok := t.Underlying().(*types.Struct)
	if !ok {
		return false
	}
	for i := range st.NumFields() {
		if fieldRequiresPointer(allow, st.Field(i).Type()) {
			return true
		}
	}
	return false
}

// fieldRequiresPointer reports whether a field's type is itself a no-copy type, a
// struct that transitively contains one, or an array whose element type does.
// An array stores its elements inline, so copying it copies each element; a
// no-copy element therefore makes the whole array — and its enclosing struct —
// uncopyable (as go vet copylocks treats it). Slices, maps, channels, and
// pointers are deliberately not walked: they are references whose copy duplicates
// only the header/pointer, never the pointee, so they leave the struct copyable.
func fieldRequiresPointer(allow map[string]bool, ft types.Type) bool {
	if isNoCopy(allow, ft) {
		return true
	}
	if arr, ok := ft.Underlying().(*types.Array); ok {
		return fieldRequiresPointer(allow, arr.Elem())
	}
	return requiresPointer(allow, ft)
}

// isNoCopy reports whether ft names an allow-listed no-copy type.
func isNoCopy(allow map[string]bool, ft types.Type) bool {
	named, ok := ft.(*types.Named)
	if !ok || named.Obj().Pkg() == nil {
		return false
	}
	return allow[named.Obj().Pkg().Path()+"."+named.Obj().Name()]
}
