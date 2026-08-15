package ptrrecv

import (
	"go/ast"
	"go/types"
	"testing"

	"github.com/stretchr/testify/assert"
)

// A bodyless method (implemented in assembly) cannot appear in the analysistest
// fixtures — the package would not type-check without its assembly file — so the
// fn.Body == nil guard is exercised directly. It must return before touching the
// pass, hence nil is a valid pass here.
func TestFixableRejectsBodylessMethods(t *testing.T) {
	assert.False(t, fixable(nil, &ast.FuncDecl{}))
}

// TestSlicesAnArrayTreatsAnUnresolvedTypeAsOne names slicesAnArray's default.
// Every expression in a real pass has a type, so the fixtures cannot produce an
// unresolved one — and if one ever appears, slicing it must be judged unsafe:
// a missing fix costs a finding, a wrong one rewrites `copy(recv.buf[:], src)`
// into a silent no-op.
func TestSlicesAnArrayTreatsAnUnresolvedTypeAsOne(t *testing.T) {
	assert.True(t, slicesAnArray(nil), "an unresolved type is judged the unsafe way")
	assert.True(t, slicesAnArray(types.NewArray(types.Typ[types.Byte], 4)), "an array stores its elements inline")
	assert.False(t, slicesAnArray(types.NewSlice(types.Typ[types.Byte])), "a slice header is shared by every copy")
	assert.False(t, slicesAnArray(types.Typ[types.String]), "and so is a string's backing array")
}
