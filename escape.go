package ptrrecv

import (
	"go/ast"
)

// Whether a function literal in a method body can OUTLIVE the call, which is the
// one question that makes a read of the receiver as unsafe as a write. A literal
// captures the receiver ITSELF, so with a pointer receiver it observes the
// caller's value live and with a value receiver it observes a copy frozen at
// call time.

// containedFuncLits collects the function literals in a body that cannot
// outlive the call: a literal invoked where it is written, including under
// `defer`, runs before the method returns and therefore sees the receiver alive
// either way. Every other literal — returned, assigned, stored in a field,
// passed as an argument, or started with `go` — may run after the method has
// returned, and with a value receiver it would have captured a copy made at
// call time. `go func(){…}()` is a call syntactically and is excluded by name.
func containedFuncLits(root ast.Node) map[*ast.FuncLit]bool {
	contained := map[*ast.FuncLit]bool{}
	started := map[*ast.CallExpr]bool{}
	ast.Inspect(root, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.GoStmt:
			started[x.Call] = true
		case *ast.CallExpr:
			if lit, isLit := ast.Unparen(x.Fun).(*ast.FuncLit); isLit && !started[x] {
				contained[lit] = true
			}
		}
		return true
	})
	return contained
}

// funcLitSafe reports whether a function literal keeps the rewrite
// behaviour-preserving. A literal that cannot outlive the call is walked like
// any other subtree. A literal that CAN — returned, stored, passed on, or
// started with `go` — captures the receiver itself, so with a pointer receiver
// it observes the caller's value live and with a value receiver it observes a
// copy frozen at call time; any mention of the receiver inside one is therefore
// unsafe however read-only it is.
//
// REPRODUCED, which is why this case exists: `func (c *Counter) Reader() func()
// int { return func() int { return c.n } }` was reported and rewritten, and the
// program printed "closure sees: 2" before and "closure sees: 0" after, with
// `go vet` silent. selectorSafe prunes descent on a receiver-rooted selector,
// so the receiver identifier under `c.n` was never visited as a bare mention —
// which is why this case is taken BEFORE the selector reaches it. The `go`
// variant prints the same pair.
//
// The shouldDescend it returns decides no VERDICT and no case can kill it —
// measured by flipping it to true, which leaves every fixture reporting
// identically. It is reached only when the literal is already known not to
// mention the receiver, and every unsafe verdict in visit is reached through
// w.info.Uses[id] == w.recv, so descending could not find one. Recorded as
// subsumed rather than defended, which is what derived.go:22-25 and
// rootIsRecv's deref link already do with the same shape; it is kept as the
// pruning it is, not as a guard.
func (w bodyWalk) funcLitSafe(lit *ast.FuncLit) (isSafe, shouldDescend bool) {
	if w.contained[lit] {
		return true, true
	}
	return !w.mentionsRecv(lit), false
}

// mentionsRecv reports whether the receiver identifier appears anywhere in a
// subtree, at any depth and through any expression — the only question worth
// asking about a literal that outlives the call, where reading the receiver is
// as unsafe as writing through it.
func (w bodyWalk) mentionsRecv(root ast.Node) bool {
	mentioned := false
	ast.Inspect(root, func(n ast.Node) bool {
		id, isIdent := n.(*ast.Ident)
		mentioned = mentioned || (isIdent && w.info.Uses[id] == w.recv)
		return !mentioned
	})
	return mentioned
}
