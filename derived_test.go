package ptrrecv

// White-box tests for the criterion that reaches a type written over ANOTHER
// package's, which the method set cannot see: such a type takes the original's
// representation and leaves every method behind. Split from copy_test.go, whose
// subject is the copy walk itself.

import (
	"go/types"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestForeignRepresentationAndIsForeignFieldAskTheTypeItWasDefinedOver names the criterion the
// method set cannot reach. `type MyBuf bytes.Buffer` takes the Buffer's
// representation and leaves every method behind, so stdlibNoCopy sees a type
// with no methods at all — while copying it copies a buffered writer.
//
// The sharp half is that a hidden field is NOT a proxy for machinery. Nothing
// structural separates that from `type Timestamp time.Time`, the canonical idiom
// for custom marshalling, whose original has 49 value methods and is copied
// everywhere. So the original is found and asked, rather than guessed at.
func TestForeignRepresentationAndIsForeignFieldAskTheTypeItWasDefinedOver(t *testing.T) {
	t.Parallel()

	assert.True(t, judged(t, `import "bytes"; type T bytes.Buffer`),
		"a type defined over bytes.Buffer holds the machinery and shows none of it")
	assert.True(t, judged(t, `import "bytes"; type over bytes.Buffer
type T struct{ b over }`), "and a struct holding one holds it inline")

	assert.False(t, judged(t, `import "time"; type T time.Time`),
		"a type defined over time.Time is a VALUE, and its original says so")
	assert.False(t, judged(t, `import "net/netip"; type T netip.Addr`),
		"and so is one over netip.Addr, whose whole purpose is value semantics")
	assert.False(t, judged(t, `import "go/token"; type T token.Position`),
		"a defined-over type whose fields are all EXPORTED is found the same way and "+
			"answered by its original, which has value methods — exportedness decides nothing")
	assert.False(t, judged(t, "type T struct{ n int }"),
		"a struct written here has its own package's fields")

	foreign := checked(t, `import "time"; type T struct{ at time.Time }`).Scope().Lookup("T")
	assert.False(t, judgement{}.foreignRepresentation(foreign.Type()),
		"a type declared elsewhere is never asked, or every foreign struct answers yes")

	_, isDefinedOver := judgement{}.originOf(types.Typ[types.Int])
	assert.False(t, isDefinedOver, "only a struct can have been defined over another package's")

	own := checkedAt(t, "mypkg", "type Thing struct{ n int }")
	field := own.Scope().Lookup("Thing").Type().Underlying().(*types.Struct).Field(0)
	assert.False(t, judgement{own: own}.isForeignField(field),
		"a field of the package under analysis is not foreign, however standard-library its dotless path reads")
	assert.True(t, judgement{}.isForeignField(field),
		"and the SAME field is foreign to any other package, which is what that comparison decides "+
			"and what no -allow value or fixture can tell apart, because the type it finds is then "+
			"answered by stdlibNoCopy, which stands down on the package under analysis too")
}

// TestTypeWithUnderlyingAnswersWhenThereIsNothingToFind names the search's empty
// answer, which the fixtures cannot produce: every struct
// carrying a package's hidden fields IS one of that package's types, and every
// driver this analyzer runs under reports a module. Both are asked anyway,
// because "not found" and "found" take different paths and only one of them is
// exercised by a corpus.
func TestTypeWithUnderlyingAnswersWhenThereIsNothingToFind(t *testing.T) {
	t.Parallel()

	pkg := checkedAt(t, "example.com/x", "type Thing struct{ n int }")
	stranger := types.NewStruct([]*types.Var{
		types.NewField(0, pkg, "nothing", types.Typ[types.Bool], false),
	}, nil)
	_, isFound := typeWithUnderlying(pkg, stranger)
	assert.False(t, isFound, "a struct no type in the package has is a type nothing was defined over")

	found, isFound := typeWithUnderlying(pkg, pkg.Scope().Lookup("Thing").Type().Underlying().(*types.Struct))
	assert.True(t, isFound, "and the package's own struct finds the type that has it")
	assert.Equal(t, "example.com/x.Thing", found.String())

	twin := types.NewStruct([]*types.Var{
		types.NewField(0, pkg, "n", types.Typ[types.Int], false),
	}, nil)
	found, isFound = typeWithUnderlying(pkg, twin)
	assert.True(t, isFound,
		"the search matches by STRUCTURE, not by object identity: a struct rebuilt with the "+
			"same fields is the same type, and a pointer comparison would answer no to it — "+
			"which is the distinction this analyzer already had to fix once for the error result")
	assert.Equal(t, "example.com/x.Thing", found.String())
}
