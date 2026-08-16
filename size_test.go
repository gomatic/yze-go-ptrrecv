package ptrrecv

// White-box tests for the copy-cost criterion. The bound is a POLICY and the
// default is what the fleet sees, so the default is pinned here as well as by
// the pair of fixtures sitting either side of it.

import (
	"go/types"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// sized reports copyIsCostly for the type declared as `T` in src, measured with
// the same platform sizes a driver hands the pass.
func sized(t *testing.T, src string) bool {
	t.Helper()
	pkg := checked(t, src)
	obj := pkg.Scope().Lookup("T")
	require.NotNil(t, obj, "the fixture must declare a type T")
	return judgement{own: pkg, sizes: types.SizesFor("gc", "arm64")}.copyIsCostly(obj.Type())
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

// TestIsGenericNamedMakesCopyIsCostlyDeclineToMeasureAGenericReceiver names the guard on
// go/types: Alignof asserts !isTypeParam and panics on a type parameter
// (go/types/gcsizes.go:54), and `func (b *Box[T]) M()` has one in every field
// written over T. Such a receiver has no single width — Box[byte] and
// Box[[1<<20]byte] are the same declaration — so the bound says nothing about
// it, and the fixture's Box, Wrapped, GenericFree and GuardedBox are what drive
// this through the analyzer rather than around it.
func TestIsGenericNamedMakesCopyIsCostlyDeclineToMeasureAGenericReceiver(t *testing.T) {
	t.Parallel()

	assert.False(t, sized(t, "type T[E any] struct{ v E }"), "a type parameter has no width here")
	assert.False(t, sized(t, "type T[E any] struct{ a [1 << 15]uint32; v E }"),
		"not even when the non-generic part is far over the bound")
	assert.True(t, sized(t, "type Inner[E any] struct{ v E }\ntype T struct{ a [1 << 15]uint32; i Inner[byte] }"),
		"an INSTANTIATED generic field has a width and is measured")
}

// TestMaxCopyRefusesAValueItCannotHonour pins the flag's refusal at the surface
// the yze framework uses: ApplyConfig calls Flags.Set and turns a failure into
// ErrInvalidSettingValue. A negative bound would exempt every receiver in the
// run and print nothing, which is the silent disablement allow.go was written to
// stop and this flag must not reintroduce.
func TestMaxCopyRefusesAValueItCannotHonour(t *testing.T) {
	original := configuredMaxCopy
	t.Cleanup(func() { configuredMaxCopy = original })

	for _, value := range []string{"", "-1", "128.0", "one", " 128", "0x80", "9223372036854775808"} {
		err := configuredMaxCopy.Set(value)
		require.Error(t, err, "value %q", value)
		assert.ErrorIs(t, err, ErrInvalidMaxCopy)
		assert.Equal(t, original, configuredMaxCopy, "a refused value leaves the bound standing")
	}

	require.NoError(t, configuredMaxCopy.Set("0"))
	assert.Equal(t, receiverBytes(0), configuredMaxCopy, "zero is a bound, not a refusal")

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
