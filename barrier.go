package ptrrecv

// What counts as a BARRIER: a point at which the object the receiver points at
// may have changed since the call began. Everything alias.go decides is decided
// against one of these, and the set is the whole content of the clause — each
// member below was reproduced as a program whose answer changes when the star is
// deleted, not reasoned about.

import (
	"go/ast"
	"go/token"
	"go/types"
)

// closeBuiltin is the one builtin that hands control to other code: closing a
// channel wakes every goroutine blocked on it, and any of them may hold an
// alias. Every other builtin computes and returns.
const closeBuiltin = "close"

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

// callBarrier is a call's closing paren unless the walk can see through it.
func (w bodyWalk) callBarrier(call *ast.CallExpr) token.Pos {
	if w.isTransparent(call) {
		return token.NoPos
	}
	return call.Rparen
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
// array, string or integer computes from storage already in hand and is not one.
func (w bodyWalk) rangeBarrier(x *ast.RangeStmt) token.Pos {
	if !w.rangeHandsControlAway(x) {
		return token.NoPos
	}
	return x.X.End()
}

// writeBarrier is the end of an assignment that could reach the storage the
// receiver points at, and token.NoPos for one that writes somewhere the caller
// does not share.
//
// REPRODUCED, which is why this clause exists and why a call is not the only
// barrier: `func (c *C) Touch() { live.n = 99; sink = c.n }`, with `var live
// *C` set to the same object, contains no call, no function literal and no
// defer, and every other clause calls the rewrite safe. It was reported, and
// deleting the star took the program from "sink: 99" to "sink: 1" with go vet
// silent both ways. The same write inside one deferred literal, read by
// another, does it in LIFO order with no statement in the body at all.
func (w bodyWalk) writeBarrier(end token.Pos, targets ...ast.Expr) token.Pos {
	if !anyExpr(targets, w.writesWhereTheCallerCanSee) {
		return token.NoPos
	}
	return end
}

// writesWhereTheCallerCanSee reports whether an assignment target could name
// storage the caller shares with the receiver. A variable this function declares
// did not exist when the caller took the address it passed, so it is not one.
// Anything reached by dereferencing a pointer, indexing a slice or reading a map
// can, and so can a package-level variable, which is what `var live *C` and
// `live.n = 99` are made of.
func (w bodyWalk) writesWhereTheCallerCanSee(target ast.Expr) bool {
	for {
		switch x := target.(type) {
		case *ast.ParenExpr:
			target = x.X
		case *ast.SelectorExpr:
			target = w.throughIndirection(x.X)
		case *ast.IndexExpr:
			target = w.throughIndirection(x.X)
		case nil:
			return true
		default:
			return !isBlank(target) && !w.isFunctionLocal(target)
		}
	}
}

// A dereference — `*p = 1` — needs no case of its own and is measured not to
// have one: it falls to the default, where a StarExpr is not an identifier, so
// it names no variable this function declares and the answer is already the
// conservative one. Deleting a `case *ast.StarExpr: return true` written above
// changed no fixture's verdict, which is what put this paragraph here instead.

// throughIndirection is the inner expression to keep walking, or nil — which
// writesWhereTheCallerCanSee reads as "the caller can see this" — when the link
// reaches storage the expression does not itself hold.
func (w bodyWalk) throughIndirection(inner ast.Expr) ast.Expr {
	if w.indirects(inner) {
		return nil
	}
	return inner
}

// indirects reports whether selecting or indexing through e reaches storage e
// does not itself hold. A type the pass could not resolve is treated as one,
// because a missed finding is fine and a wrong one is not.
func (w bodyWalk) indirects(e ast.Expr) bool {
	held := w.info.TypeOf(e)
	if held == nil {
		return true
	}
	switch held.Underlying().(type) {
	case *types.Pointer, *types.Slice, *types.Map:
		return true
	}
	return false
}

// isBlank reports whether e is the blank identifier, which names no storage.
func isBlank(e ast.Expr) bool {
	id, isIdent := e.(*ast.Ident)
	return isIdent && id.Name == "_"
}

// isFunctionLocal reports whether e names a variable declared inside a function
// rather than at package level, which is decided by the scope the object hangs
// from.
func (w bodyWalk) isFunctionLocal(e ast.Expr) bool {
	id, isIdent := e.(*ast.Ident)
	if !isIdent {
		return false
	}
	declared := w.declaredObject(id)
	return declared != nil && declared.Pkg() != nil && declared.Parent() != nil &&
		declared.Parent() != declared.Pkg().Scope()
}

// declaredObject resolves an identifier that may be either a use or, for `:=`, a
// definition.
func (w bodyWalk) declaredObject(id *ast.Ident) types.Object {
	if used := w.info.Uses[id]; used != nil {
		return used
	}
	return w.info.Defs[id]
}

// anyExpr reports whether any expression satisfies a predicate.
func anyExpr(exprs []ast.Expr, holds func(ast.Expr) bool) bool {
	for _, e := range exprs {
		if holds(e) {
			return true
		}
	}
	return false
}

// isTransparent reports whether a call runs only what the walk itself reads: a
// conversion is not a call at all, a builtin other than close computes and
// returns, and a function literal invoked where it is written is walked like any
// other subtree — its own calls are barriers in their own right.
func (w bodyWalk) isTransparent(call *ast.CallExpr) bool {
	fun := ast.Unparen(call.Fun)
	kind, resolved := w.info.Types[fun]
	if resolved && (kind.IsType() || kind.IsBuiltin()) {
		return !isCloseBuiltin(fun)
	}
	lit, isLiteral := fun.(*ast.FuncLit)
	return isLiteral && w.contained[lit]
}

// isCloseBuiltin reports whether e is the close builtin's identifier.
func isCloseBuiltin(e ast.Expr) bool {
	id, isIdent := e.(*ast.Ident)
	return isIdent && id.Name == closeBuiltin
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
