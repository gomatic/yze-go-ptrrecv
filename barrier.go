package ptrrecv

import (
	"go/ast"
	"go/token"
	"go/types"
)

// What counts as a BARRIER: a point at which the object the receiver points at
// may have changed since the call began. Everything alias.go decides is decided
// against one of these, and the set is the whole content of the clause — each
// member below was reproduced as a program whose answer changes when the star is
// deleted, not reasoned about.

// builtinName is a builtin function's name as an identifier spells it.
type builtinName string

// The builtins that are not simply computation. close hands control to other
// code: closing a channel wakes every goroutine blocked on it, and any of them
// may hold an alias. The other four WRITE through their first argument and are
// judged as the assignments they stand for — REPRODUCED, which is why they are
// here: `func (c *C) FillFrom(other *C, src []byte) byte { copy(other.buf[:],
// src); return c.buf[0] }` called as c.FillFrom(c, …) has no assignment
// statement anywhere, was reported, and deleting the star took "fill sees: 7"
// to "fill sees: 0" with go vet silent both ways. Every other builtin computes
// from operands already in hand and returns.
const (
	closeBuiltin  builtinName = "close"
	copyBuiltin   builtinName = "copy"
	clearBuiltin  builtinName = "clear"
	deleteBuiltin builtinName = "delete"
	appendBuiltin builtinName = "append"
)

// firstBarrier is the earliest position past which the object may no longer hold
// what it held when the call began, or token.NoPos for a body that leaves it
// alone.
func (w bodyWalk) firstBarrier(root ast.Node) token.Pos {
	first := token.NoPos
	ast.Inspect(root, func(n ast.Node) bool {
		at := w.barrierEnd(n)
		if at.IsValid() && (!first.IsValid() || at < first) {
			first = at
		}
		return true
	})
	return first
}

// hasBarrier reports whether a subtree holds any barrier at all.
func (w bodyWalk) hasBarrier(root ast.Node) bool { return w.firstBarrier(root).IsValid() }

// barrierEnd is the position past which a node's effect is done, and token.NoPos
// for a node that is no barrier. A call's is its closing paren, because
// arguments are evaluated before the call and a receiver read among them is a
// read the call has not yet had a chance to disturb. A channel operation's is
// its own end: another goroutine holding an alias may run at it, and a range
// receives or calls its iterator once per turn. An assignment's is its own end
// when what it writes to is storage the caller may share.
func (w bodyWalk) barrierEnd(n ast.Node) token.Pos {
	switch x := n.(type) {
	case *ast.CallExpr:
		return w.callBarrier(x)
	case *ast.UnaryExpr:
		return arrowBarrier(x)
	case *ast.SendStmt:
		return x.End()
	case *ast.AssignStmt:
		return w.writeBarrier(x.End(), x.Lhs...)
	case *ast.IncDecStmt:
		return w.writeBarrier(x.End(), x.X)
	case *ast.RangeStmt:
		return w.rangeBarrier(x)
	}
	return token.NoPos
}

// callBarrier is a call's closing paren unless the walk can see through it. A
// builtin that writes through its first argument is asked the same question an
// assignment to that argument would be asked, so `copy(local, src)` is no more a
// barrier than `local[0] = src[0]`.
func (w bodyWalk) callBarrier(call *ast.CallExpr) token.Pos {
	if !w.isTransparent(call) {
		return call.Rparen
	}
	if !anyExpr(builtinWriteTargets(call), w.referencesWhereTheCallerCanSee) {
		return token.NoPos
	}
	return call.Rparen
}

// referencesWhereTheCallerCanSee asks of a builtin's argument what
// writesWhereTheCallerCanSee asks of an assignment target, with one difference
// that decides real code: a builtin writes what its argument REFERS TO, so a
// bare slice, map, pointer or channel variable is not local storage however
// locally it was declared. `delete(other, k)` on a map parameter empties the
// caller's map; `other = nil` on the same parameter changes nothing outside.
func (w bodyWalk) referencesWhereTheCallerCanSee(target ast.Expr) bool {
	if _, isIdent := target.(*ast.Ident); isIdent && w.indirects(target) {
		return true
	}
	return w.writesWhereTheCallerCanSee(target)
}

// builtinWriteTargets is the argument a writing builtin writes through, and
// nothing for every other call.
func builtinWriteTargets(call *ast.CallExpr) []ast.Expr {
	if len(call.Args) == 0 || !writesThroughItsArgument(ast.Unparen(call.Fun)) {
		return nil
	}
	return call.Args[:1]
}

// writesThroughItsArgument reports whether e names a builtin that writes into
// storage its first argument reaches.
func writesThroughItsArgument(e ast.Expr) bool {
	return isBuiltinNamed(e, copyBuiltin) || isBuiltinNamed(e, clearBuiltin) ||
		isBuiltinNamed(e, deleteBuiltin) || isBuiltinNamed(e, appendBuiltin)
}

// arrowBarrier is a channel receive's end, and token.NoPos for every other
// unary operator: negation, complement, address-of and dereference compute from
// operands already in hand and hand control nowhere. This is the two-way split
// of a many-way domain that yze/enumdiscrim's own doc comment sanctions and
// reports anyway; .stickler.yaml records why its baseline carries it.
func arrowBarrier(x *ast.UnaryExpr) token.Pos {
	if x.Op != token.ARROW {
		return token.NoPos
	}
	return x.End()
}

// rangeBarrier is the end of the expression a range statement hands control to,
// since each turn of such a loop leaves the method. A range over a slice, map,
// array, string or integer computes from storage already in hand and is not one
// — unless it ASSIGNS its iteration variables into storage the caller may share,
// which is a write with no assignment statement to carry it: `for live.n = range
// xs {}` followed by `return c.n` was reported, and deleting the star took
// "range-assign sees: 2" to "range-assign sees: 1" with go vet silent both ways.
// A `:=` range declares fresh locals, which writesWhereTheCallerCanSee answers
// on its own with no need to read the token.
func (w bodyWalk) rangeBarrier(x *ast.RangeStmt) token.Pos {
	if !w.rangeHandsControlAway(x) && !anyExpr(presentExprs(x.Key, x.Value), w.writesWhereTheCallerCanSee) {
		return token.NoPos
	}
	return x.X.End()
}

// presentExprs drops the parts a statement leaves out — `for range xs` has
// neither iteration variable.
func presentExprs(parts ...ast.Expr) []ast.Expr {
	present := make([]ast.Expr, 0, len(parts))
	for _, part := range parts {
		if part != nil {
			present = append(present, part)
		}
	}
	return present
}

// isTransparent reports whether a call runs only what the walk itself reads: a
// conversion is not a call at all, a builtin other than close computes and
// returns, and a function literal invoked where it is written is walked like any
// other subtree — its own calls are barriers in their own right.
func (w bodyWalk) isTransparent(call *ast.CallExpr) bool {
	fun := ast.Unparen(call.Fun)
	kind, resolved := w.info.Types[fun]
	if resolved && (kind.IsType() || kind.IsBuiltin()) {
		return !isBuiltinNamed(fun, closeBuiltin)
	}
	lit, isLiteral := fun.(*ast.FuncLit)
	return isLiteral && w.contained[lit]
}

// isBuiltinNamed reports whether e is the identifier for a given builtin. A
// user declaration of the same name is not a builtin, so isTransparent has
// already sent it down the opaque path.
func isBuiltinNamed(e ast.Expr, name builtinName) bool {
	id, isIdent := e.(*ast.Ident)
	return isIdent && id.Name == string(name)
}

// rangeHandsControlAway reports whether each turn of a range statement leaves
// the method: a receive, for a channel, and a call of the iterator, for a
// function. The iterator case is not visible as a CallExpr anywhere in the AST —
// `for range Seq` calls Seq with a yield function the compiler synthesises — and
// missing it was reproduced end to end: an iterator that mutated the receiver
// through a package-level alias between two yields kept the finding, and
// deleting the star took the program from "sum: 100" to "sum: 2" with go vet
// silent both ways.
//
// A type the pass could not resolve is treated as one of the two, because a
// missed finding is fine and a wrong one is not — the same reading slicesAnArray
// gives an unresolved slice operand.
func (w bodyWalk) rangeHandsControlAway(x *ast.RangeStmt) bool {
	ranged := w.info.TypeOf(x.X)
	if ranged == nil {
		return true
	}
	switch ranged.Underlying().(type) {
	case *types.Chan, *types.Signature:
		return true
	}
	return false
}
