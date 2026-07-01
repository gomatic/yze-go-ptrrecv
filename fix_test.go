package ptrrecv

import (
	"go/ast"
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
