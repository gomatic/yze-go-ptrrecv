package ptrrecv

// White-box tests for the value-receiver rewrite's safety, which is this
// analyzer's reporting condition. A verdict of "safe" that is wrong does not
// produce a merely noisy diagnostic — it tells the author to make a change that
// alters what the program does, which is answered with a baseline.

import (
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/tools/go/analysis"
)

// rewritten type-checks src and reports rewriteSafe for the method named M,
// which is how the walk is driven without a whole analysistest package.
func rewritten(t *testing.T, src string) bool {
	t.Helper()

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "p.go", "package p\n"+src, 0)
	require.NoError(t, err)

	info := &types.Info{
		Defs:       map[*ast.Ident]types.Object{},
		Uses:       map[*ast.Ident]types.Object{},
		Types:      map[ast.Expr]types.TypeAndValue{},
		Selections: map[*ast.SelectorExpr]*types.Selection{},
	}
	conf := types.Config{Importer: importer.Default()}
	_, err = conf.Check("p", fset, []*ast.File{file}, info)
	require.NoError(t, err)

	for _, decl := range file.Decls {
		fn, isFunc := decl.(*ast.FuncDecl)
		if isFunc && fn.Name.Name == "M" {
			return rewriteSafe(&analysis.Pass{TypesInfo: info}, fn)
		}
	}
	require.FailNow(t, "the fixture must declare a method M")
	return false
}

// bodies is the receiver type and the two helper methods every case below
// shares, so each case is only the body under judgement.
const bodies = `type T struct {
	n  int
	xs []int
	a  [4]byte
	c  chan struct{}
}

func (r *T) bump() { r.n++ }

func (r T) get() int { return r.n }

func take(f func() int) {}

`

// TestRewriteSafeRejectsBodylessMethods exercises the fn.Body == nil guard
// directly: a bodyless method (implemented in assembly) cannot appear in an
// analysistest fixture, because the package would not type-check without its
// assembly file. It must return before touching the pass, hence nil is a valid
// pass here.
func TestRewriteSafeRejectsBodylessMethods(t *testing.T) {
	t.Parallel()

	assert.False(t, rewriteSafe(nil, &ast.FuncDecl{}))
}

// TestFuncLitSafeRefusesAReceiverThatOutlivesTheCall names the clause a
// reproduction bought: `func (c *Counter) Reader() func() int { return func()
// int { return c.n } }` was reported and rewritten, and the program printed
// "closure sees: 2" before and "closure sees: 0" after, with `go vet` silent
// both times. The literal captures the RECEIVER, so a read inside one that
// outlives the call is as unsafe as a write.
//
// The near side of the boundary is the sharper half: a literal invoked where it
// is written cannot outlive the call, and refusing those too would withdraw the
// rule from every method that uses a closure at all.
func TestFuncLitSafeRefusesAReceiverThatOutlivesTheCall(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		body string
		why  string
		safe bool
	}{
		{
			body: "func (r *T) M() func() int { return func() int { return r.n } }", safe: false,
			why: "a returned literal reads the receiver after the method has gone",
		},
		{
			body: "func (r *T) M() { go func() { <-r.c }() }", safe: false,
			why: "and so does one started with go, which is a call syntactically",
		},
		{
			body: "func (r *T) M() { take(func() int { return r.n }) }", safe: false,
			why: "a literal handed to another function may be stored by it",
		},
		{
			body: "func (r *T) M() func() int { return func() func() int { return func() int { return r.n } }() }",
			safe: false, why: "an escaping literal nested inside a contained one is still escaping",
		},
		{
			body: "func (r *T) M() int { return func() int { return r.n }() }", safe: true,
			why: "a literal invoked where it is written runs before the method returns",
		},
		{
			body: "func (r *T) M() int { total := 0; defer func() { total = r.n }(); return total }", safe: true,
			why: "and so does a deferred one, which is what `defer` means",
		},
		{
			body: "func (r *T) M() func() int { return func() int { return 7 } }", safe: true,
			why: "a literal that mentions nothing of the receiver captures nothing of it",
		},
		{
			body: "func (r *T) M() func() { return func() { r.bump() } }", safe: false,
			why: "and a pointer-receiver call inside one is refused twice over",
		},
	} {
		assert.Equal(t, tc.safe, rewritten(t, bodies+tc.body), tc.why)
	}
}

// TestContainedFuncLitsSeparatesWhatRunsNowFromWhatRunsLater names the split
// funcLitSafe turns on, at the syntax rather than through a verdict: `go
// func(){}()` is a CallExpr whose Fun is a literal, exactly like an immediate
// invocation, so the go statement has to be subtracted by name or every
// goroutine reads as contained.
func TestContainedFuncLitsSeparatesWhatRunsNowFromWhatRunsLater(t *testing.T) {
	t.Parallel()

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "p.go", `package p
func m() {
	func() {}()
	defer func() {}()
	go func() {}()
	_ = func() {}
	take(func() {})
}
func take(f func()) {}
`, 0)
	require.NoError(t, err)

	contained := containedFuncLits(file.Decls[0].(*ast.FuncDecl).Body)

	var seen []bool
	ast.Inspect(file.Decls[0], func(n ast.Node) bool {
		if lit, isLit := n.(*ast.FuncLit); isLit {
			seen = append(seen, contained[lit])
		}
		return true
	})
	assert.Equal(t, []bool{true, true, false, false, false}, seen,
		"invoked and deferred literals are contained; a goroutine, a stored one and an argument are not")
}

// TestBodyWalkVisitClassifiesEveryUseOfTheReceiver names the rest of the walk's
// contract in one place. Each case is a use the rewrite either survives or does
// not, and the negative half is the load-bearing one: any use the walk cannot
// classify has to come out unsafe, because a missed finding costs a finding and
// a wrong one costs a program.
func TestBodyWalkVisitClassifiesEveryUseOfTheReceiver(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		body string
		why  string
		safe bool
	}{
		{body: "func (r *T) M() int { return r.n }", safe: true, why: "a field read survives the copy"},
		{body: "func (r *T) M() int { return r.get() }", safe: true, why: "and so does a value-receiver call"},
		{body: "func (r *T) M() []int { return r.xs[:1] }", safe: true, why: "slicing a SLICE field shares its backing array"},
		{body: "func (r *T) M() { r.n = 1 }", safe: false, why: "an assignment would land in a copy"},
		{body: "func (r *T) M() { r.n++ }", safe: false, why: "and so would an increment"},
		{body: "func (r *T) M() *int { return &r.n }", safe: false, why: "an address could outlive the call"},
		{body: "func (r *T) M() { for r.n = range r.xs {} }", safe: false, why: "a range target is an assignment"},
		{body: "func (r *T) M() []byte { return r.a[:] }", safe: false, why: "slicing an ARRAY field is an address-of"},
		{body: "func (r *T) M() { r.bump() }", safe: false, why: "a pointer-receiver call may mutate"},
		{body: "func (r *T) M() *T { return r }", safe: false, why: "a bare mention changes type"},
		{body: "func (r *T) M() int { return (*r).n }", safe: false, why: "and an explicit deref would not compile"},
	} {
		assert.Equal(t, tc.safe, rewritten(t, bodies+tc.body), tc.why)
	}
}

// TestSlicesAnArrayTreatsAnUnresolvedTypeAsOne names slicesAnArray's default.
// Every expression in a real pass has a type, so the fixtures cannot produce an
// unresolved one — and if one ever appears, slicing it must be judged unsafe:
// a missed finding costs a finding, a wrong one calls `copy(recv.buf[:], src)`
// behaviour-preserving when the rewrite makes it a silent no-op.
func TestSlicesAnArrayTreatsAnUnresolvedTypeAsOne(t *testing.T) {
	t.Parallel()

	assert.True(t, slicesAnArray(nil), "an unresolved type is judged the unsafe way")
	assert.True(t, slicesAnArray(types.NewArray(types.Typ[types.Byte], 4)), "an array stores its elements inline")
	assert.False(t, slicesAnArray(types.NewSlice(types.Typ[types.Byte])), "a slice header is shared by every copy")
	assert.False(t, slicesAnArray(types.Typ[types.String]), "and so is a string's backing array")
}
