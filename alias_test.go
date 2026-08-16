package ptrrecv

// White-box tests for the alias barrier. Everything the barrier decides about
// real code is driven through the analyzer by the fixtures in
// testdata/src/a/a.go, on both sides of every clause; what is left here is the
// one reading no well-typed fixture can produce.

import (
	"go/ast"
	"go/types"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestRangeHandsControlAwayTreatsATypeItCannotResolveAsALeavingOne pins the direction the
// walk errs in when the pass has no type for the ranged expression. No fixture
// can reach it — analysistest type-checks its packages — and the direction is
// the whole content of the branch: reading an unresolved range as one that stays
// inside the method would drop the barrier and report it, which is the wrong one
// of the two mistakes available.
func TestRangeHandsControlAwayTreatsATypeItCannotResolveAsALeavingOne(t *testing.T) {
	t.Parallel()

	walk := bodyWalk{info: &types.Info{Types: map[ast.Expr]types.TypeAndValue{}}}
	unresolved := &ast.RangeStmt{X: ast.NewIdent("ch"), Body: &ast.BlockStmt{}}

	assert.True(t, walk.rangeHandsControlAway(unresolved))
	assert.True(t, walk.barrierEnd(unresolved).IsValid(),
		"an unresolved range is a barrier, so a receiver read in its body is withheld")
}

// TestWritesWhereTheCallerCanSeeErrsTowardsTheCallerOnWhatItCannotRead pins the
// two readings no well-typed assignment target produces. An assignment target is
// an identifier, a selector, an index or a dereference; nothing else is
// assignable, so neither an unresolved operand nor a non-identifier root arrives
// from a fixture. Both must answer "the caller can see this", because the cost of
// that answer is a finding and the cost of the other is a remedy that changes the
// program.
func TestWritesWhereTheCallerCanSeeErrsTowardsTheCallerOnWhatItCannotRead(t *testing.T) {
	t.Parallel()

	walk := bodyWalk{info: &types.Info{
		Types: map[ast.Expr]types.TypeAndValue{},
		Uses:  map[*ast.Ident]types.Object{},
		Defs:  map[*ast.Ident]types.Object{},
	}}
	unresolved := &ast.SelectorExpr{X: ast.NewIdent("holder"), Sel: ast.NewIdent("field")}
	notAnIdentifier := &ast.CompositeLit{}

	assert.True(t, walk.indirects(ast.NewIdent("holder")), "a type the pass has no answer for indirects")
	assert.True(t, walk.writesWhereTheCallerCanSee(unresolved))
	assert.False(t, walk.isFunctionLocal(notAnIdentifier), "only an identifier names a declared variable")
	assert.True(t, walk.writesWhereTheCallerCanSee(notAnIdentifier))
}
