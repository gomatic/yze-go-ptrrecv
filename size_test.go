package ptrrecv

// White-box tests for the copy-cost criterion. The bound is a POLICY and the
// default is what the fleet sees, so the default is pinned here as well as by
// the pair of fixtures sitting either side of it. The LAYOUT the bound is
// measured on is a policy too, and it is pinned against a second one so the
// case can tell "one answer everywhere" from "the host's answer".

import (
	"go/types"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/tools/go/analysis"
)

// sized reports copyIsCostly for the type declared as `T` in src, measured with
// the layout the analyzer states rather than any the host or driver supplies.
func sized(t *testing.T, src string) bool {
	t.Helper()
	return sizedWith(t, judgedSizes, src)
}

// sizedWith reports copyIsCostly for the type declared as `T` in src under an
// arbitrary layout, so a case can measure what the answer WOULD be on another
// platform and assert the analyzer does not give it.
func sizedWith(t *testing.T, sizes types.Sizes, src string) bool {
	t.Helper()
	pkg := checked(t, src)
	obj := pkg.Scope().Lookup("T")
	require.NotNil(t, obj, "the fixture must declare a type T")
	return judgement{own: pkg, sizes: sizes}.copyIsCostly(obj.Type())
}

// TestReceiverBytesDefaultMaxCopyIsTheBoundTheFleetSees pins the shipped default, and
// receiverBytes is both the bound and the measurement so the two cannot be
// compared in different units. Every other
// case in this file and both fixtures either side of the bound are measured
// against it, so a silent change here would move the whole rule.
func TestReceiverBytesDefaultMaxCopyIsTheBoundTheFleetSees(t *testing.T) {
	t.Parallel()

	assert.Equal(t, receiverBytes(128), defaultMaxCopy)
	assert.Equal(t, defaultMaxCopy, configuredMaxCopy, "the flag starts at the default")
	assert.Equal(t, "128", defaultMaxCopy.String())
	assert.GreaterOrEqual(t, defaultMaxCopy, pointerBytes,
		"the shipped default must itself be a bound Set would accept")
}

// TestJudgedArchAndJudgedSizesAreTheStatedLayoutNotTheHostS pins the layout the verdict is
// measured on. A misspelt GOARCH makes types.SizesFor return nil and every
// measurement panic, and a right-looking substitution (386, arm) would move the
// verdict on every receiver whose width is made of words — so the layout is
// asserted by its word size, not merely by being present.
func TestJudgedArchAndJudgedSizesAreTheStatedLayoutNotTheHostS(t *testing.T) {
	t.Parallel()

	require.NotNil(t, judgedSizes, "judgedArch %q must be a GOARCH types.SizesFor knows", judgedArch)
	assert.Equal(t, "amd64", judgedArch)
	assert.Equal(t, int64(8), judgedSizes.Sizeof(types.Typ[types.UnsafePointer]))
	assert.Equal(t, receiverBytes(8), pointerBytes, "the floor is one pointer wide")
}

// TestCopyIsCostlyGivesOneVerdictWhateverThePlatformUnderAnalysisIs is the
// two-platform case. `type Twenty struct{ w [20]Word; n Count }` over
// `type Word uintptr` is 168 bytes on a 64-bit layout and 84 on a 32-bit one, so
// the 128-byte bound falls between them: measured with the driver's own sizes
// the SAME source file is withheld on amd64 and reported on 386, which is a
// repo green on a developer's machine and red in a cross-compiled CI with no
// fix available. The first two assertions prove the dimension really moves —
// without them the third could pass because nothing varies — and the third is
// the contract: the analyzer's answer is the stated layout's, on both.
func TestCopyIsCostlyGivesOneVerdictWhateverThePlatformUnderAnalysisIs(t *testing.T) {
	t.Parallel()

	const src = "type Word uintptr\ntype Count int\ntype T struct{ w [20]Word; n Count }"

	assert.True(t, sizedWith(t, types.SizesFor("gc", "amd64"), src),
		"168 bytes on a 64-bit layout is over the bound")
	assert.False(t, sizedWith(t, types.SizesFor("gc", "386"), src),
		"84 bytes on a 32-bit layout is under it, which is the verdict this rule must not give")
	assert.True(t, sized(t, src), "the analyzer answers with the stated layout on every platform")
}

// TestCopyIsCostlyMeasuresTheWholeArrayAndNotItsElement names the defect this
// criterion exists for: componentsRequirePointer walks an array's ELEMENTS and
// never its LENGTH, so a [1<<15]uint32 field multiplies the copy by 32768 and
// every criterion in copy.go answers "copyable". Applied to the standard
// library, that rewrote compress/flate's (*compressor).findMatch — a 656616-byte
// receiver — into a value receiver, and its own TestMaxStackSize died of a stack
// overflow.
func TestCopyIsCostlyMeasuresTheWholeArrayAndNotItsElement(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		src    string
		why    string
		costly bool
	}{
		{"type T struct{ n int }", "a word-sized receiver is free", false},
		{"type T struct{ a [128]byte }", "exactly the bound is still free", false},
		{"type T struct{ a [129]byte }", "one byte over the bound is not", true},
		{"type T struct{ a [1 << 15]uint32; n int }", "the flate shape: 128 KiB per call", true},
		{"type T [1 << 15]uint32", "an array type is its own receiver", true},
		{"type T struct{ a [1 << 15]struct{} }", "an empty element makes a wide array free", false},
		{"type T int", "a scalar is free", false},
	} {
		assert.Equal(t, tc.costly, sized(t, tc.src), tc.why)
	}
}

// TestCopyIsCostlyMeasuresAGenericReceiverOnItsNarrowestInstance names what
// declining to measure one cost: `type GenericMatcher[T any] struct{ table
// [1<<15]uint32; n Count }` was REPORTED where the byte-identical non-generic
// type was withheld, and the remedy took two million calls of its read-only Find
// from 977.167µs to 26.779913458s. The parameter is unused and decides nothing
// about the width, so "such a receiver has no single width" was false for it.
//
// Substituting the empty struct measures the width no instantiation can go
// below, which is the conservative direction: a receiver withheld on its
// narrowest instance is withheld on every one.
func TestCopyIsCostlyMeasuresAGenericReceiverOnItsNarrowestInstance(t *testing.T) {
	t.Parallel()

	assert.False(t, sized(t, "type T[E any] struct{ v E }"),
		"a type parameter is measured at zero width, not refused")
	assert.True(t, sized(t, "type T[E any] struct{ a [1 << 15]uint32; v E }"),
		"the non-generic part alone is far over the bound")
	assert.False(t, sized(t, "type T[E any] struct{ a [16]byte; v E }"),
		"a narrow generic is still judged, so the substitution is not a blanket exemption")
	assert.True(t, sized(t, "type Inner[E any] struct{ v E }\ntype T struct{ a [1 << 15]uint32; i Inner[byte] }"),
		"an INSTANTIATED generic field has a width and is measured")
}

// TestNarrowestInstanceKeepsAConcreteTypeArgument pins the half of
// narrowestArgument no receiver reaches: a type already instantiated over a
// concrete type is measured as instantiated. Substituting there would read
// Box[Table] — 256 KiB — as an empty struct, which is the same hole in the
// opposite direction from the one this criterion was fixed for.
func TestNarrowestInstanceKeepsAConcreteTypeArgument(t *testing.T) {
	t.Parallel()

	pkg := checked(t, "type Table [1 << 15]uint32\ntype Box[E any] struct{ v E }\ntype T struct{ b Box[Table] }")
	field := pkg.Scope().Lookup("T").Type().Underlying().(*types.Struct).Field(0)

	narrowest, instantiable := narrowestInstance(field.Type())
	require.True(t, instantiable)
	assert.Equal(t, int64(131072), judgedSizes.Sizeof(narrowest),
		"Box[Table] is measured over Table, not over the empty struct")
	assert.True(t, sized(t, "type Table [1 << 15]uint32\ntype Box[E any] struct{ v E }\ntype T struct{ b Box[Table] }"))
}

// TestNarrowestBytesRefusesAWidthGoTypesWillNotState names a shape that only a
// generic declaration reaches. go/types answers a type it considers larger than
// the address space with -1 rather than an error, and `struct{ a [1<<40]Inner }`
// written WITHOUT a type parameter is rejected at compile time — with one it is
// accepted, because the size of an uninstantiated generic is never computed. A
// -1 read as a byte count is under every bound, so the widest receiver there is
// would be judged free and its copy prescribed.
func TestNarrowestBytesRefusesAWidthGoTypesWillNotState(t *testing.T) {
	t.Parallel()

	const src = "type Inner [1 << 40]byte\ntype T[E any] struct{ a [1 << 40]Inner; v E }"

	pkg := checked(t, src)
	narrowest, instantiable := narrowestInstance(pkg.Scope().Lookup("T").Type())
	require.True(t, instantiable)
	require.Equal(t, int64(-1), judgedSizes.Sizeof(narrowest),
		"the case is only about the -1 answer, so it must actually get one")

	assert.True(t, sized(t, src), "a width go/types will not state is costly, never free")
}

// TestCopyIsCostlyIsTrueWhenTheNarrowestInstanceCannotBeBuilt pins the failure
// branch of the instantiation. No receiver reaches it — the argument count is
// derived from the parameter list it is checked against, and constraints are not
// validated — but the branch decides a verdict, and the verdict it must give is
// the conservative one: an unmeasurable receiver is costly, so nothing is
// reported on it.
func TestCopyIsCostlyIsTrueWhenTheNarrowestInstanceCannotBeBuilt(t *testing.T) {
	original := instantiate
	t.Cleanup(func() { instantiate = original })
	instantiate = func(*types.Context, types.Type, []types.Type, bool) (types.Type, error) {
		return nil, ErrInvalidMaxCopy
	}

	assert.True(t, sized(t, "type T[E any] struct{ v E }"),
		"a receiver whose width cannot be reached is never reported")
}

// TestReceiverBytesSetRefusesEveryBoundUnderPointerBytes pins the flag's refusal at the surface
// the yze framework uses: ApplyConfig calls Flags.Set and turns a failure into
// ErrInvalidSettingValue.
//
// 0 is the value this case exists for. The guard refused -1 because "a negative
// one would exempt every receiver in the run and print nothing" and accepted 0,
// which does exactly that: through the real runner, three lines of
// .stickler.yaml took a module reporting two findings to complete silence and
// exit 0, and `-max=0 -json std` reported 65 locations against the default's
// 1431. The floor is one pointer width, because below it the bound withholds
// receivers whose copy is narrower than the pointer it replaces — the silent
// disablement allow.go was written to stop and this flag must not reintroduce.
func TestReceiverBytesSetRefusesEveryBoundUnderPointerBytes(t *testing.T) {
	original := configuredMaxCopy
	t.Cleanup(func() { configuredMaxCopy = original })

	for _, value := range []string{"", "-1", "0", "1", "7", "128.0", "one", " 128", "0x80", "9223372036854775808"} {
		err := configuredMaxCopy.Set(value)
		require.Error(t, err, "value %q", value)
		assert.ErrorIs(t, err, ErrInvalidMaxCopy)
		assert.Equal(t, original, configuredMaxCopy, "a refused value leaves the bound standing")
	}

	require.NoError(t, configuredMaxCopy.Set("8"))
	assert.Equal(t, pointerBytes, configuredMaxCopy, "one pointer width is the narrowest bound there is")

	require.NoError(t, configuredMaxCopy.Set("4096"))
	assert.Equal(t, receiverBytes(4096), configuredMaxCopy)
}

// TestMaxCopySettingIsRegisteredUnderTheNameTheDocStates checks the flag is
// reachable by the name the package doc and any .stickler.yaml use, which no
// fixture can see: a setting registered under another spelling is accepted by
// nothing and reported by nobody.
func TestMaxCopySettingIsRegisteredUnderTheNameTheDocStates(t *testing.T) {
	original := configuredMaxCopy
	t.Cleanup(func() { configuredMaxCopy = original })

	flag := Analyzer.Flags.Lookup("max")
	require.NotNil(t, flag, "the -max flag must exist under that name")
	assert.Equal(t, "128", flag.DefValue)

	require.NoError(t, Analyzer.Flags.Set("max", "64"))
	assert.Equal(t, receiverBytes(64), configuredMaxCopy)
}

// TestNewJudgementTakesTheStatedLayoutAndNotThePassS is the half of the platform
// clause no fixture and no other unit test can see: on a 64-bit host the
// driver's own sizes and the stated ones agree, so wiring pass.TypesSizes back
// in changes nothing this suite runs. The case hands the pass a 32-bit layout —
// which measures the same receiver at 84 bytes rather than 168 — and asserts the
// judgement still measures 168.
func TestNewJudgementTakesTheStatedLayoutAndNotThePassS(t *testing.T) {
	t.Parallel()

	pkg := checked(t, "type Word uintptr\ntype Count int\ntype T struct{ w [20]Word; n Count }")
	measured := pkg.Scope().Lookup("T").Type()
	thirtyTwoBit := types.SizesFor("gc", "386")
	require.Equal(t, int64(84), thirtyTwoBit.Sizeof(measured),
		"the case is only about the disagreement, so the two layouts must disagree")

	judge := newJudgement(&analysis.Pass{Pkg: pkg, TypesSizes: thirtyTwoBit})

	assert.Equal(t, int64(168), judge.sizes.Sizeof(measured))
	assert.True(t, judge.copyIsCostly(measured), "168 bytes is over the bound on every platform")
}
