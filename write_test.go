package ptrrecv

// White-box tests for what a write can reach. The fixtures in testdata drive the
// clause end to end on the shapes that occur; the table here is the contract for
// the type walk itself, which a fixture can only exercise one composite at a
// time and only where a package-level variable of that composite is plausible.

import (
	"go/types"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// reaching type-checks src and reports whether a write to storage of the type
// named `W` could disturb a receiver pointing at the type named `R`.
func reaching(t *testing.T, src string) bool {
	t.Helper()
	pkg := checked(t, src)
	base := pkg.Scope().Lookup("R")
	require.NotNil(t, base, "the fixture must declare a receiver type R")
	written := pkg.Scope().Lookup("W")
	require.NotNil(t, written, "the fixture must declare a written type W")
	return bodyWalk{base: base.Type()}.reachesTheReceiver(written.Type())
}

// TestReachesTheReceiverAsksWhatTheStorageCanHold is the contract for the walk
// the write barrier turns on. It decides whether a write to a variable's OWN
// storage could be a write to the receiver, so every way one value can sit
// inside another is a case, and a type that cannot hold the receiver is the
// other side: without it the clause withholds a finding for `calls++` on a
// package-level int, which no receiver can ever be.
func TestReachesTheReceiverAsksWhatTheStorageCanHold(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		src     string
		why     string
		reaches bool
	}{
		{"type R struct{ n int }\ntype W = R", "the receiver's own type is the storage", true},
		{"type R struct{ n int }\ntype W *R", "a pointer leads to one", true},
		{"type R struct{ n int }\ntype W []R", "a slice element is one", true},
		{"type R struct{ n int }\ntype W [4]R", "an array element is one, stored inline", true},
		{"type R struct{ n int }\ntype W chan R", "a channel carries one", true},
		{"type R struct{ n int }\ntype W map[string]R", "a map value is one", true},
		{"type R struct{ n int }\ntype W map[R]string", "and so is a map key", true},
		{"type R struct{ n int }\ntype W struct{ a int; r *R }", "a field holds one", true},
		{"type R struct{ n int }\ntype W struct{ a any }", "an interface could be holding one", true},
		{"type R struct{ n int }\ntype W struct{ f func() R }", "a closure's result is not its storage", false},
		{"type R struct{ n int }\ntype W int", "a bare int is not a receiver and holds none", false},
		{"type R struct{ n int }\ntype W struct{ a, b string }", "nor is an unrelated struct", false},
		{"type R struct{ n int }\ntype W map[string]int", "nor an unrelated map", false},
	} {
		assert.Equal(t, tc.reaches, reaching(t, tc.src), tc.why)
	}
}

// TestReachesTheReceiverTerminatesOnASelfReferentialType names the guard, which
// no documented clause would ask for: `type Node struct{ next *Node }` is the
// ordinary shape of a tree, and without the seen set the walk follows next
// forever and the analyzer never returns. The pair is what makes the case
// discriminate — the cycle must be walked far enough to find a receiver that IS
// behind it, so the guard cannot be "stop at the first named type".
func TestReachesTheReceiverTerminatesOnASelfReferentialType(t *testing.T) {
	t.Parallel()

	assert.False(t, reaching(t, "type R struct{ n int }\ntype W struct{ next *W; label string }"),
		"a cycle that never reaches the receiver terminates and answers no")
	assert.True(t, reaching(t, "type R struct{ n int }\ntype W struct{ next *W; held *R }"),
		"a cycle with the receiver behind it terminates and answers yes")
}

// TestReachesTheReceiverTreatsATypeParameterAsAnything pins what a generic
// declaration reaches: an uninstantiated parameter could be instantiated as the
// receiver, so storage written over one counts. It arrives at the walk as its
// CONSTRAINT — go/types answers TypeParam.Underlying() with the interface — which
// is why the interface case answers for both and a parameter case beside it
// would be dead.
func TestReachesTheReceiverTreatsATypeParameterAsAnything(t *testing.T) {
	t.Parallel()

	pkg := checked(t, "type R struct{ n int }\ntype G[T any] struct{ v T }")
	parameter := pkg.Scope().Lookup("G").Type().Underlying().(*types.Struct).Field(0).Type()
	require.IsType(t, &types.TypeParam{}, parameter)

	assert.True(t, bodyWalk{base: pkg.Scope().Lookup("R").Type()}.reachesTheReceiver(parameter))
}

// TestReachesTheReceiverCountsWhatItCannotMeasure pins the two readings that
// answer before the walk starts. A pass with no type for the written storage,
// and a walk with no receiver type, both mean "this could be anything", and the
// cost of answering otherwise is a finding whose remedy changes the program.
func TestReachesTheReceiverCountsWhatItCannotMeasure(t *testing.T) {
	t.Parallel()

	pkg := checked(t, "type R struct{ n int }\ntype W int")
	receiver := pkg.Scope().Lookup("R").Type()
	unrelated := pkg.Scope().Lookup("W").Type()

	assert.True(t, bodyWalk{base: receiver}.reachesTheReceiver(nil),
		"a type the pass could not resolve could be anything")
	assert.True(t, bodyWalk{}.reachesTheReceiver(unrelated),
		"a receiver whose type could not be resolved is reachable from anything")
}

// TestPointedAtDeclinesAReceiverThatIsNotAPointer pins the branch no method
// declaration reaches: check() calls rewriteSafe only for a receiver written
// `*T`. The branch decides what the write barrier is measured against, and a nil
// there is read as "anything reaches it", which is the conservative answer and
// the one a value receiver — which this analyzer never judges — would deserve.
func TestPointedAtDeclinesAReceiverThatIsNotAPointer(t *testing.T) {
	t.Parallel()

	pkg := checked(t, "type R struct{ n int }")
	value := types.NewVar(0, pkg, "r", pkg.Scope().Lookup("R").Type())

	assert.Nil(t, pointedAt(value))
	assert.True(t, bodyWalk{base: pointedAt(value)}.reachesTheReceiver(types.Typ[types.Int]))
}
