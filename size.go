package ptrrecv

import (
	"go/types"
	"strconv"

	errs "github.com/gomatic/go-error"
)

// The COST of the copy this rule prescribes, which is the one dimension every
// criterion in copy.go leaves out. copy.go decides a type may be copied by
// asking what it HOLDS — no sync primitive, no Locker shape, no pointer-only
// standard-library API, no type parameter — and never how BIG it is:
// componentsRequirePointer walks an array's ELEMENTS and never its LENGTH, so a
// [1<<15]uint32 field multiplies the copy by 32768 and the walk never sees it.
//
// Measured on unmodified Go 1.26.6 source: `ptrrecv -test=false -fix
// compress/flate` inside a copy of GOROOT rewrote compress/flate/deflate.go:233
// `(d *compressor) findMatch` to a value receiver on a 656616-byte struct
// (hashHead [1<<15]uint32 and hashPrev [1<<15]uint32 inline), after which
// compress/flate's own TestMaxStackSize dies with `runtime: goroutine stack
// exceeds 65536-byte limit / fatal error: stack overflow`. The minimal shape —
// `type Matcher struct{ table [1<<15]uint32; n int }` with a read-only Find —
// went from 2.09ms to 18.1s over two million calls, and `go vet` was silent
// either way.
//
// So a receiver wider than the bound is not judged AT ALL, rather than judged
// and left unfixed. The remedy is untakable there at any price — no edit to the
// method makes a 640 KiB copy per call acceptable — and a diagnostic naming a
// remedy nobody can take is answered with a baseline, which is the population
// this suite exists to remove.

// ErrInvalidMaxCopy reports a -max value that is not a byte count this rule can
// honour. go-yze's ApplyConfig turns a Set failure into its own
// ErrInvalidSettingValue, so a mistyped bound in a .stickler.yaml fails the run
// instead of silently judging every receiver or none.
const ErrInvalidMaxCopy errs.Const = "-max is not a receiver size in bytes (a non-negative integer)"

// receiverBytes is a receiver type's width in bytes, as the driver's own
// types.Sizes computes it for the platform under analysis. It is also the -max
// flag's value, so the bound and the measurement are the same type and cannot
// be compared in different units.
type receiverBytes int64

// defaultMaxCopy is the widest receiver whose copy this rule still calls free.
//
// Measured on darwin/arm64, two million calls of a read-only method on
// `struct{ a [n]uint64; n int }`, value receiver against pointer receiver:
// 40 bytes 2.9% slower, 72 bytes 4.7%, 136 bytes 20%, 264 bytes 98%, 520 bytes
// 292%, 1032 bytes 5400%. The copy is inside the noise up to about 72 bytes and
// is the dominant cost from about 264. 128 is the round number between them,
// and it is two cache lines.
//
// It is a policy, not a fact, which is why it is configurable — but it is the
// default that decides what the fleet sees, so it is pinned by a case on each
// side of it running at this value.
const defaultMaxCopy receiverBytes = 128

// configuredMaxCopy is the -max flag's value. It is package state because a
// go/analysis flag set has nowhere else to write.
var configuredMaxCopy = defaultMaxCopy

// String renders the bound as the flag package prints it in usage and defaults.
func (b receiverBytes) String() string { return strconv.FormatInt(int64(b), 10) }

// Set parses and validates the bound. A pointer receiver is what flag.Value
// requires. A value that is not a non-negative integer is refused with
// ErrInvalidMaxCopy rather than accepted as a bound nothing can honour: a
// negative one would exempt every receiver in the run and print nothing.
func (b *receiverBytes) Set(value string) error {
	size, err := strconv.ParseInt(value, 10, 64)
	if err != nil || size < 0 {
		return ErrInvalidMaxCopy.With(err, "max", value)
	}
	*b = receiverBytes(size)
	return nil
}

// copyIsCostly reports whether copying t on every call costs materially more
// than passing the pointer it would replace — which makes the value receiver a
// remedy the author cannot take whatever else the method does.
func (j judgement) copyIsCostly(t types.Type) bool {
	if isGenericNamed(t) {
		return false
	}
	return receiverBytes(j.sizes.Sizeof(t)) > configuredMaxCopy
}

// isGenericNamed reports whether t is an uninstantiated generic named type,
// whose width go/types refuses to compute: Alignof asserts !isTypeParam and
// panics on a type parameter (go/types/gcsizes.go:54), and the receiver of
// `func (b *Box[T]) M()` carries one in every field written over T. Such a
// receiver has no single width — Box[byte] and Box[[1 << 20]byte] are the same
// declaration — so the bound cannot speak about it and does not.
func isGenericNamed(t types.Type) bool {
	named, isNamed := types.Unalias(t).(*types.Named)
	return isNamed && named.TypeParams().Len() > 0
}
