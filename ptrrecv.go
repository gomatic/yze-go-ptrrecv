// Package ptrrecv provides a go/analysis analyzer enforcing the gomatic Go
// immutability standard: methods use value receivers, never pointer receivers,
// unless the receiver type transitively contains a field that cannot be copied
// (a sync primitive, atomic, buffer, or builder).
package ptrrecv

import (
	"fmt"
	"go/ast"
	"go/types"
	"strings"

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
