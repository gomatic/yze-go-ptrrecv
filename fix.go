package ptrrecv

import (
	"go/ast"
	"go/token"
	"go/types"

	"golang.org/x/tools/go/analysis"
)

// fixes returns the value-receiver rewrite (deleting the receiver's "*") when it
// is provably behavior-preserving for fn, and nil otherwise. The yze driver
// applies fixes to packages loaded WITHOUT test files, so a fix must stay safe
// for unseen callers: dropping the "*" only widens the method set (both T and
// *T satisfy value-receiver methods), which keeps callers compiling — but it
// silently mutates a copy if the method writes through its receiver. A missing
// fix is fine; a wrong fix is not.
func fixes(pass *analysis.Pass, fn *ast.FuncDecl, star *ast.StarExpr) []analysis.SuggestedFix {
	if !fixable(pass, fn) {
		return nil
	}
	return []analysis.SuggestedFix{{
		Message:   "change to a value receiver",
		TextEdits: []analysis.TextEdit{{Pos: star.Star, End: star.Star + 1}},
	}}
}

// fixable reports whether removing the receiver's "*" preserves fn's behavior.
// A bodyless method (implemented in assembly) is never fixable — the receiver
// ABI is invisible here; an unnamed receiver always is — the body cannot reach
// it at all.
func fixable(pass *analysis.Pass, fn *ast.FuncDecl) bool {
	if fn.Body == nil {
		return false
	}
	recv := receiverObject(pass, fn)
	if recv == nil {
		return true
	}
	return bodySafe(pass.TypesInfo, recv, fn.Body)
}

// receiverObject resolves fn's receiver identifier to its declared object, or
// nil when the receiver is unnamed (or blank, which types.Info.Defs maps to nil).
func receiverObject(pass *analysis.Pass, fn *ast.FuncDecl) types.Object {
	names := fn.Recv.List[0].Names
	if len(names) == 0 {
		return nil
	}
	return pass.TypesInfo.Defs[names[0]]
}

// bodySafe walks the method body and reports whether every use of the receiver
// keeps the value-receiver rewrite behavior-preserving.
func bodySafe(info *types.Info, recv types.Object, body *ast.BlockStmt) bool {
	safe := true
	ast.Inspect(body, func(n ast.Node) bool {
		isSafe, shouldDescend := visit(info, recv, n)
		safe = safe && isSafe
		return safe && shouldDescend
	})
	return safe
}

// visit classifies one body node: isSafe is false when the node breaks fix
// safety; shouldDescend is false when the node's children need no further
// inspection. Any use of the receiver other than a field read or a
// value-receiver method call is conservatively unsafe — including a bare
// receiver mention (`return r`, `f(r)`, `*r`), whose pointer-typed semantics
// the rewrite would change.
func visit(info *types.Info, recv types.Object, n ast.Node) (isSafe, shouldDescend bool) {
	switch x := n.(type) {
	case *ast.AssignStmt:
		return lhsSafe(info, recv, x.Lhs), true
	case *ast.IncDecStmt:
		return !rootIsRecv(info, recv, x.X), true
	case *ast.UnaryExpr:
		return addrSafe(info, recv, x), true
	case *ast.RangeStmt:
		return rangeSafe(info, recv, x), true
	case *ast.SelectorExpr:
		return selectorSafe(info, recv, x)
	case *ast.Ident:
		return info.Uses[x] != recv, true
	}
	return true, true
}

// lhsSafe reports whether no assignment target is rooted in the receiver: a
// `recv.f = …`, `recv.f.g = …`, or `recv.xs[i] = …` write would mutate a copy
// after the rewrite (or, for an index, is skipped conservatively).
func lhsSafe(info *types.Info, recv types.Object, lhs []ast.Expr) bool {
	for _, e := range lhs {
		if rootIsRecv(info, recv, e) {
			return false
		}
	}
	return true
}

// addrSafe reports whether a unary expression avoids taking the address of the
// receiver or of anything reachable through it (`&recv`, `&recv.f`) — such a
// pointer could outlive the call and observe or apply mutation.
func addrSafe(info *types.Info, recv types.Object, x *ast.UnaryExpr) bool {
	return x.Op != token.AND || !rootIsRecv(info, recv, x.X)
}

// rangeSafe reports whether a range statement avoids assigning its iteration
// variables into the receiver (`for recv.i = range xs`).
func rangeSafe(info *types.Info, recv types.Object, r *ast.RangeStmt) bool {
	if r.Tok != token.ASSIGN {
		return true
	}
	return !rootIsRecv(info, recv, r.Key) && !rootIsRecv(info, recv, r.Value)
}

// selectorSafe validates a selector on the receiver: field reads and
// value-receiver method calls are safe; a pointer-receiver method call may
// mutate the receiver through its own receiver, so it is not. A safe selector
// prunes descent so the receiver identifier under it is not re-flagged as a
// bare use.
func selectorSafe(info *types.Info, recv types.Object, sel *ast.SelectorExpr) (isSafe, shouldDescend bool) {
	if !isRecv(info, recv, sel.X) {
		return true, true
	}
	return !selectsPointerMethod(info, sel), false
}

// selectsPointerMethod reports whether sel selects a method declared with a
// pointer receiver.
func selectsPointerMethod(info *types.Info, sel *ast.SelectorExpr) bool {
	s, ok := info.Selections[sel]
	if !ok || s.Kind() != types.MethodVal {
		return false
	}
	_, ptr := s.Obj().Type().(*types.Signature).Recv().Type().(*types.Pointer)
	return ptr
}

// rootIsRecv reports whether the receiver identifier is the root of e's
// selector/index/deref chain (e.g. recv, recv.f, recv.f.g, recv.xs[i], *recv).
func rootIsRecv(info *types.Info, recv types.Object, e ast.Expr) bool {
	for {
		switch x := e.(type) {
		case *ast.ParenExpr:
			e = x.X
		case *ast.SelectorExpr:
			e = x.X
		case *ast.IndexExpr:
			e = x.X
		case *ast.StarExpr:
			e = x.X
		default:
			return isRecv(info, recv, e)
		}
	}
}

// isRecv reports whether e is an identifier resolving to the receiver object.
func isRecv(info *types.Info, recv types.Object, e ast.Expr) bool {
	id, ok := e.(*ast.Ident)
	return ok && info.Uses[id] == recv
}
