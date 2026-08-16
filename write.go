package ptrrecv

import (
	"go/ast"
	"go/token"
	"go/types"
)

// Which WRITES are barriers, which is the half of the clause that needs no call
// at all. `live.n = 99; sink = c.n`, with `var live *C` set to the same object,
// contains no call, no function literal and no defer, and every other clause
// calls the rewrite safe — it was reported, and deleting the star took "sink:
// 99" to "sink: 1" with go vet silent both ways.
//
// Two questions decide it, and keeping them apart is what keeps the answer
// sound: WHERE the write lands — the variable's own storage, or wherever a
// pointer leads — and, only when that is the variable's own storage, WHAT could
// be in it.

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
//
// A dereference — `*p = 1` — needs no case of its own and is measured not to
// have one: it falls to the default, where a StarExpr is not an identifier, so
// it names no variable this function declares and the answer is already the
// conservative one. Deleting a `case *ast.StarExpr: return true` written here
// changed no fixture's verdict, which is what put this paragraph here instead.
func (w bodyWalk) writesWhereTheCallerCanSee(target ast.Expr) bool {
	root := w.storageRoot(target)
	if root == nil {
		return true
	}
	return !isBlank(root) && !w.isFunctionLocal(root) && w.reachesTheReceiver(w.info.TypeOf(root))
}

// storageRoot is the variable whose own storage an assignment target writes, or
// nil when the chain crosses an indirection and the storage written is wherever
// some pointer happens to lead. The distinction is what lets the type test below
// be sound: without an indirection the write lands in the named variable, and
// with one it may land anywhere, including on a field of the receiver reached
// through a pointer whose type says nothing about it — `*other = 1` where other
// is a *int is a write into the receiver when the caller passed &c.n.
func (w bodyWalk) storageRoot(target ast.Expr) ast.Expr {
	for {
		switch x := target.(type) {
		case *ast.ParenExpr:
			target = x.X
		case *ast.SelectorExpr:
			target = w.throughIndirection(x.X)
		case *ast.IndexExpr:
			target = w.throughIndirection(x.X)
		case *ast.SliceExpr:
			target = w.throughIndirection(x.X)
		case *ast.StarExpr:
			return nil
		case nil:
			return nil
		default:
			return target
		}
	}
}

// reachesTheReceiver reports whether storage of this type could BE, or could
// hold, the value the receiver points at. It is asked only of a variable whose
// own storage is written, and not of one reached through a pointer, so its answer
// is about that variable and not about wherever something might lead: a
// package-level `var calls int` bumped by `calls++` is not a barrier for a
// method on *C, and withholding a finding for it was the absurd end of a clause
// that asked only where the storage was and not what was in it.
//
// A type the pass could not resolve counts, and so does any interface or type
// parameter, because either could be holding the receiver.
func (w bodyWalk) reachesTheReceiver(held types.Type) bool {
	return w.reaches(held, map[types.Type]bool{})
}

// reaches walks a type looking for the receiver's own. The seen set is what
// makes a self-referential type — `type Node struct{ next *Node }`, the ordinary
// shape of any tree — terminate rather than recur forever.
func (w bodyWalk) reaches(held types.Type, seen map[types.Type]bool) bool {
	if held == nil || w.base == nil {
		return true
	}
	if seen[held] {
		return false
	}
	seen[held] = true
	return types.Identical(held, w.base) || w.componentReaches(held.Underlying(), seen)
}

// componentReaches walks one level of a composite type. A func type is not
// walked: writing to a variable holding a closure writes the header and not
// what the closure captured. A type PARAMETER needs no case of its own and is
// measured not to have one — go/types answers TypeParam.Underlying() with the
// constraint's interface, so every parameter arrives here as one, and a
// `case *types.TypeParam` written beside the interface survives deletion.
func (w bodyWalk) componentReaches(under types.Type, seen map[types.Type]bool) bool {
	switch x := under.(type) {
	case *types.Pointer:
		return w.reaches(x.Elem(), seen)
	case *types.Slice:
		return w.reaches(x.Elem(), seen)
	case *types.Array:
		return w.reaches(x.Elem(), seen)
	case *types.Chan:
		return w.reaches(x.Elem(), seen)
	case *types.Map:
		return w.reaches(x.Key(), seen) || w.reaches(x.Elem(), seen)
	case *types.Struct:
		return w.structReaches(x, seen)
	case *types.Interface:
		return true
	}
	return false
}

// structReaches walks a struct's fields, which hold their values inline.
func (w bodyWalk) structReaches(held *types.Struct, seen map[types.Type]bool) bool {
	for i := range held.NumFields() {
		if w.reaches(held.Field(i).Type(), seen) {
			return true
		}
	}
	return false
}

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
