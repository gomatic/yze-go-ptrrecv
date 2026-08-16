package ptrrecv

import (
	"go/ast"
	"go/token"
)

// Whether the value-receiver rewrite preserves what the method observes about
// the OBJECT, which is not the question rewrite.go asks.
//
// rewrite.go asks what the BODY does to the receiver — assigns through it, takes
// its address, mentions it bare — and treats every other read as
// behaviour-preserving. It is not, because the copy is made at CALL time: any
// read of the receiver after a point where something else could have written
// through another alias observes a DIFFERENT value once the receiver is a value.
//
// REPRODUCED four times, each built and run end to end with `go vet ./...`
// silent before and after; the last two were found while fixing the first two,
// which is why barrier.go enumerates what can change the object rather than
// reasoning about it. A CALLBACK, with no package state anywhere:
//
//	func (c *C) WithStep(step Step) Count { step(); return c.n }
//	c.WithStep(func() { c.n = 99 })
//
// was reported, and deleting the star — the remedy the diagnostic prescribes —
// took the program from "with step sees: 99" to "with step sees: 1". An ALIAS,
// with no function literal at all:
//
//	var registered *C
//	func bump()              { registered.n = 99 }
//	func (c *C) Late() Count { bump(); return c.n }
//
// was reported, and the same edit took "late sees: 99" to "late sees: 1". A
// RANGE OVER AN ITERATOR, whose call appears nowhere in the AST because the
// compiler synthesises the yield, took "sum: 100" to "sum: 2". And a WRITE
// THROUGH AN ALIAS with no call, no function literal and no defer anywhere in
// the method — `live.n = 99; sink = c.n` — took "sink: 99" to "sink: 1".
//
// No function literal appears in the second, and in the first the literal is at
// the CALL SITE in another function, so the escaping-literal clause reaches
// neither. Whether anything else holds an alias is undecidable in one pass —
// the same undecidability that withdrew the SuggestedFix — but here it falls on
// the REPORT, because rewriteSafe IS the reporting condition. So the body is
// judged on what it can see: a read the method makes after anything barrier.go
// calls a barrier is withheld. A method taking a callback (ForEach,
// Walk, WithLock) or reading a field after any call is an ordinary shape, and
// the cost of this is findings, which is the direction this analyzer is
// conservative in everywhere else — 273 of the 1431 source-only locations the
// rule reported across the Go 1.26.6 standard library, measured as a sorted
// projection of `-json std` on both binaries, with nothing added and zero
// packages failing to load in either run.

// aliasSafe reports whether every read of the receiver in the body happens
// before anything beyond this walk's reach could have written to the object.
func (w bodyWalk) aliasSafe(body *ast.BlockStmt) bool {
	barrier := w.firstBarrier(body)
	if !barrier.IsValid() {
		return true
	}
	if containsGoto(body) {
		return !w.mentionsRecv(body)
	}
	return !w.observesAfter(body, barrier) && !w.observesOnALaterTurn(body)
}

// observesAfter reports whether the body reads the receiver at a point the
// barrier has already passed. A read inside a `defer` counts however early it is
// written, because a deferred call runs after every barrier in the method — the
// one place where source order is not run order.
func (w bodyWalk) observesAfter(body *ast.BlockStmt, barrier token.Pos) bool {
	observed := false
	ast.Inspect(body, func(n ast.Node) bool {
		observed = observed || w.observationFollows(n, barrier)
		return !observed
	})
	return observed
}

// observationFollows reports whether one node is a read of the receiver that
// runs after the barrier.
func (w bodyWalk) observationFollows(n ast.Node, barrier token.Pos) bool {
	if deferred, isDefer := n.(*ast.DeferStmt); isDefer {
		return w.mentionsRecv(deferred)
	}
	id, isIdent := n.(*ast.Ident)
	return isIdent && w.info.Uses[id] == w.recv && id.Pos() > barrier
}

// observesOnALaterTurn reports whether a loop reads the receiver in a region it
// can re-enter after a barrier. The read is written before the barrier and runs
// after it on the next turn, so source order alone would call it safe.
func (w bodyWalk) observesOnALaterTurn(root ast.Node) bool {
	repeats := false
	ast.Inspect(root, func(n ast.Node) bool {
		repeats = repeats || w.loopRepeatsAnObservation(n)
		return !repeats
	})
	return repeats
}

// loopRepeatsAnObservation reports whether a loop statement runs both a barrier
// and a read of the receiver on every turn. The two need not be in the same part
// of the loop: a condition reading the receiver and a body calling out is the
// same hazard as the reverse.
//
// The loop statement's OWN barrier — a range over a channel or an iterator — is
// deliberately not consulted here, and measured to be subsumed: its position is
// the end of
// the ranged expression, which every part that repeats sits after, so
// observesAfter has already answered. Adding the disjunct back changes no
// fixture's verdict.
func (w bodyWalk) loopRepeatsAnObservation(n ast.Node) bool {
	parts := repeatingParts(n)
	if len(parts) == 0 {
		return false
	}
	return anyNode(parts, w.hasBarrier) && anyNode(parts, w.mentionsRecv)
}

// repeatingParts is the parts of a loop statement that run once per turn. A
// for statement's init and a range statement's ranged expression are evaluated
// once, before the loop, so a receiver read in either is not repeated.
func repeatingParts(n ast.Node) []ast.Node {
	switch x := n.(type) {
	case *ast.ForStmt:
		return presentNodes(x.Cond, x.Post, x.Body)
	case *ast.RangeStmt:
		return presentNodes(x.Key, x.Value, x.Body)
	}
	return nil
}

// presentNodes drops the parts a loop statement leaves out — `for { … }` has no
// condition and no post statement, and `for range xs` has neither variable.
func presentNodes(parts ...ast.Node) []ast.Node {
	present := make([]ast.Node, 0, len(parts))
	for _, part := range parts {
		if part != nil {
			present = append(present, part)
		}
	}
	return present
}

// anyNode reports whether any node satisfies a predicate.
func anyNode(nodes []ast.Node, holds func(ast.Node) bool) bool {
	for _, n := range nodes {
		if holds(n) {
			return true
		}
	}
	return false
}

// containsGoto reports whether a body can jump backwards. A `goto` makes any
// region of the body repeatable, so the loop rule above has nothing to
// enumerate and every read of the receiver is treated as following the barrier.
func containsGoto(root ast.Node) bool {
	found := false
	ast.Inspect(root, func(n ast.Node) bool {
		branch, isBranch := n.(*ast.BranchStmt)
		found = found || (isBranch && branch.Tok == token.GOTO)
		return !found
	})
	return found
}
