package ptrrecv

import (
	"go/ast"
	"go/token"
	"go/types"

	"golang.org/x/tools/go/analysis"
)

// Whether replacing a pointer receiver with a value receiver preserves what the
// method observes, which is the reporting criterion: the analyzer reports only
// receivers whose rewrite is provably behaviour-preserving inside the method.
//
// It does NOT decide that the rewrite is behaviour-preserving for the program —
// nothing in a single pass can, which is why no SuggestedFix is attached. See
// the package doc comment.
//
// The walk is conservative in one direction on purpose: a receiver use it
// cannot classify makes the method unreportable. A missed finding costs a
// finding; a finding whose remedy breaks the program manufactures a baseline.

// rewriteSafe reports whether replacing fn's pointer receiver with a value
// receiver preserves what the method body observes. A bodyless method
// (implemented in assembly) is never safe — the receiver ABI is invisible here;
// an unnamed receiver always is — the body cannot reach it at all.
//
// Two questions, because the walk below answers only the first: what the body
// DOES to the receiver, and — in alias.go — what the body still OBSERVES about
// it after handing control somewhere the walk cannot follow.
func rewriteSafe(pass *analysis.Pass, fn *ast.FuncDecl) bool {
	if fn.Body == nil {
		return false
	}
	recv := receiverObject(pass, fn)
	if recv == nil {
		return true
	}
	walk := bodyWalk{
		info:      pass.TypesInfo,
		recv:      recv,
		base:      pointedAt(recv),
		contained: containedFuncLits(fn.Body),
	}
	return walk.nodeSafe(fn.Body) && walk.aliasSafe(fn.Body)
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

// bodyWalk is one method body's decision context: the pass's type information,
// the receiver object every use is compared against, the type it points at, and
// the function literals that cannot outlive the call.
type bodyWalk struct {
	info      *types.Info
	recv      types.Object
	base      types.Type
	contained map[*ast.FuncLit]bool
}

// pointedAt is the type the receiver points at, which is what a write has to be
// able to reach before it can disturb what the method observes. Every receiver
// this analyzer looks at is written *T, so the assertion holds; a receiver
// whose type the pass could not resolve yields nil, which barrier.go reads as
// "anything could reach it".
func pointedAt(recv types.Object) types.Type {
	pointer, isPointer := types.Unalias(recv.Type()).(*types.Pointer)
	if !isPointer {
		return nil
	}
	return pointer.Elem()
}

// nodeSafe walks a subtree (the method body, or an index expression inside a
// receiver-rooted chain) and reports whether every use of the receiver keeps
// the value-receiver rewrite behaviour-preserving.
func (w bodyWalk) nodeSafe(root ast.Node) bool {
	safe := true
	ast.Inspect(root, func(n ast.Node) bool {
		isSafe, shouldDescend := w.visit(n)
		safe = safe && isSafe
		return safe && shouldDescend
	})
	return safe
}

// visit classifies one body node: isSafe is false when the node breaks rewrite
// safety; shouldDescend is false when the node's children need no further
// inspection. Any use of the receiver other than a field read or a
// value-receiver method call is conservatively unsafe — including a bare
// receiver mention (`return r`, `f(r)`, `*r`), whose pointer-typed semantics
// the rewrite would change.
func (w bodyWalk) visit(n ast.Node) (isSafe, shouldDescend bool) {
	switch x := n.(type) {
	case *ast.FuncLit:
		return w.funcLitSafe(x)
	case *ast.AssignStmt:
		return w.lhsSafe(x.Lhs), true
	case *ast.IncDecStmt:
		return !w.rootIsRecv(x.X), true
	case *ast.UnaryExpr:
		return w.addrSafe(x), true
	case *ast.RangeStmt:
		return w.rangeSafe(x), true
	case *ast.SliceExpr:
		return w.sliceSafe(x), true
	case *ast.SelectorExpr:
		return w.selectorSafe(x)
	case *ast.Ident:
		return w.info.Uses[x] != w.recv, true
	}
	return true, true
}

// lhsSafe reports whether no assignment target is rooted in the receiver: a
// `recv.f = …`, `recv.f.g = …`, or `recv.xs[i] = …` write would mutate a copy
// after the rewrite (or, for an index, is skipped conservatively).
func (w bodyWalk) lhsSafe(lhs []ast.Expr) bool {
	for _, e := range lhs {
		if w.rootIsRecv(e) {
			return false
		}
	}
	return true
}

// addrSafe reports whether a unary expression avoids taking the address of the
// receiver or of anything reachable through it (`&recv`, `&recv.f`) — such a
// pointer could outlive the call and observe or apply mutation.
func (w bodyWalk) addrSafe(x *ast.UnaryExpr) bool {
	return x.Op != token.AND || !w.rootIsRecv(x.X)
}

// sliceSafe reports whether a slice expression avoids slicing an ARRAY reached
// through the receiver. Slicing an addressable array is an implicit address-of:
// the slice aliases the array's own storage, so after the rewrite it aliases a
// COPY, and every write through it lands nowhere while every read sees stale
// bytes. `copy(recv.buf[:], src)` and `s := recv.buf[:]; s[0] = 0` both become
// silent no-ops that go vet has nothing to say about — measured on image/gif's
// own decoder, whose (*decoder).readBlock fills d.tmp[:n] for other methods to
// read and whose test suite fails once the receiver is a value.
func (w bodyWalk) sliceSafe(x *ast.SliceExpr) bool {
	if !w.rootIsRecv(x.X) {
		return true
	}
	return !slicesAnArray(w.info.TypeOf(x.X))
}

// slicesAnArray reports whether slicing t takes an address. An array stores its
// elements inline, so a slice of one points into the array itself; a slice or a
// string is already a header over storage every copy shares, so slicing one
// changes nothing. A type the pass could not resolve is treated as an array,
// because a missed finding is fine and a wrong one is not.
func slicesAnArray(t types.Type) bool {
	if t == nil {
		return true
	}
	_, isArray := t.Underlying().(*types.Array)
	return isArray
}

// rangeSafe reports whether a range statement avoids assigning its iteration
// variables into the receiver (`for recv.i = range xs`).
func (w bodyWalk) rangeSafe(r *ast.RangeStmt) bool {
	if r.Tok != token.ASSIGN {
		return true
	}
	return !w.rootIsRecv(r.Key) && !w.rootIsRecv(r.Value)
}

// selectorSafe validates a selector whose chain is rooted at the receiver by
// checking the ENTIRE chain at once (chainSafe) — so a pointer-receiver method
// selected at ANY depth (`recv.f.Inc`, `recv.xs[i].Inc`) is seen, not just one
// selected directly on the receiver. A receiver-rooted chain prunes descent so
// the receiver identifier under it is not re-flagged as a bare use; a selector
// not rooted at the receiver is left to the normal walk.
func (w bodyWalk) selectorSafe(sel *ast.SelectorExpr) (isSafe, shouldDescend bool) {
	if !w.rootIsRecv(sel) {
		return true, true
	}
	return w.chainSafe(sel), false
}

// chainSafe reports whether a receiver-rooted selector/index chain keeps the
// rewrite behaviour-preserving: no link may select a pointer-receiver method
// (which could mutate the receiver through its own receiver), every index
// expression's content must itself be safe, and the chain must reach the
// receiver identifier through those links alone. Anything else in the chain —
// an explicit `*recv` deref — is conservatively unsafe: a missed finding is
// fine, a wrong one is not.
func (w bodyWalk) chainSafe(e ast.Expr) bool {
	switch x := e.(type) {
	case *ast.ParenExpr:
		return w.chainSafe(x.X)
	case *ast.SelectorExpr:
		return !selectsPointerMethod(w.info, x) && w.chainSafe(x.X)
	case *ast.IndexExpr:
		return w.nodeSafe(x.Index) && w.chainSafe(x.X)
	case *ast.Ident:
		return true
	}
	return false
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
//
// The deref link decides no VERDICT on its own — measured by deleting it, every
// fixture reports identically, because any expression containing `*recv`
// contains the identifier `recv`, which the bare-mention rule already refuses.
// It is kept because it is what routes `(*recv).f` into chainSafe, whose
// `default: return false` is the explicit statement that an unclassified link
// is unsafe; delete this case and that default becomes unreachable and the
// gate's coverage threshold goes red. So the pair is defended by the gate
// rather than by a verdict, which is the honest description of it.
func (w bodyWalk) rootIsRecv(e ast.Expr) bool {
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
			return w.isRecv(e)
		}
	}
}

// isRecv reports whether e is an identifier resolving to the receiver object.
func (w bodyWalk) isRecv(e ast.Expr) bool {
	id, ok := e.(*ast.Ident)
	return ok && w.info.Uses[id] == w.recv
}
