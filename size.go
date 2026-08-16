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

// judgedArch is the GOARCH whose type layout every verdict is measured on,
// whatever platform the analysis is running for.
//
// It is stated rather than taken from the driver because a width made of words
// moves with GOARCH and the bound is in BYTES, so the driver's own sizes make
// the VERDICT move too. REPRODUCED on one file with no build tag: `type Word
// uintptr`, `type Twenty struct{ w [20]Word; n Count }` and a read-only
// `func (t *Twenty) First() Word` is 168 bytes on a 64-bit platform and 84 on a
// 32-bit one, so the 128-byte bound falls between — the same binary on the same
// file was silent on darwin/arm64 and under GOOS=linux GOARCH=amd64 and
// reported under GOOS=linux GOARCH=386.
//
// That is a rule green on the developer's machine and red in a cross-compiled
// CI, whose only available answer is a disablement, and a remedy taken to
// satisfy one platform's verdict is a value receiver the other never asked for.
// So the layout is the analyzer's and is named here. 64-bit is the layout the
// fleet builds and ships on; a 32-bit target reads a verdict about the 64-bit
// copy, which is the wider of the two.
//
// WHAT THIS DOES NOT FIX, because nothing in an analyzer can: a type whose own
// DIMENSIONS come from a platform-dependent constant is a different type on each
// platform before this package sees it. `const words = 20 * unsafe.Sizeof(
// uintptr(0)); type Buf struct{ b [words]byte; n int }` is `[160]byte` on a
// 64-bit target and `[80]byte` on a 32-bit one — the type-checker folds the
// constant with the DRIVER's sizes, and the array length is baked into the
// types.Array handed to the pass. MEASURED on one untagged file: silent under
// GOOS=linux GOARCH=amd64, reported under GOARCH=386 and GOARCH=arm, on this
// build and on the one before it alike. Normalising the layout makes the same
// type measure the same everywhere; it cannot make two types one. Recorded
// rather than hidden, because the fix's own claim would otherwise read as
// covering it.
const judgedArch = "amd64"

// judgedSizes is judgedArch's type layout, the only sizes any verdict is
// measured with. gc is the toolchain the fleet builds with.
var judgedSizes = types.SizesFor("gc", judgedArch)

// ErrInvalidMaxCopy reports a -max value that is not a byte count this rule can
// honour. go-yze's ApplyConfig turns a Set failure into its own
// ErrInvalidSettingValue, so a mistyped bound in a .stickler.yaml fails the run
// instead of silently judging every receiver or none.
const ErrInvalidMaxCopy errs.Const = "-max is not a receiver size in bytes at least as wide as the pointer a value receiver replaces"

// receiverBytes is a receiver type's width in bytes, as judgedSizes computes it.
// It is also the -max flag's value, so the bound and the measurement are the
// same type and cannot be compared in different units.
type receiverBytes int64

// pointerBytes is the width of the pointer receiver a value receiver replaces,
// and so the FLOOR of the -max bound. Below it the bound withholds a finding on
// a receiver whose copy is narrower than the pointer it replaces — a copy that
// cannot cost more than the pointer does — which is not a bound on cost but a
// refusal to judge.
//
// The value this exists for is 0, which the guard used to accept while refusing
// -1 for a reason that is equally true of it: REPRODUCED end to end through the
// real runner, three lines of .stickler.yaml (`analyzers: {ptrrecv: {max:
// ["0"]}}`) took a module reporting two findings to complete silence and exit
// 0, and `-max=0 -json std` over Go 1.26.6 reported 65 locations against the
// default's 1431 — 95.5% of the rule, off, with nothing printed, nothing
// counted and nothing ratcheted. 0 is not a smaller bound but a different rule:
// copyIsCostly is `> max`, so at 0 the survivors are exactly the receivers that
// occupy no memory at all.
//
// The floor bounds the incoherent values, not the disabling ones, and the
// difference is measured: `-max=8`, the narrowest bound this guard accepts, takes
// the standard library from 1158 source-only locations to 93. Lowering the bound
// inside the permitted range is a disablement channel like every other setting
// here, and how far it may move is a quota question the owner sets — recorded as
// k1n8213w, not decided here. What the floor buys is that a bound can no longer
// mean "judge nothing that occupies memory", which is not a smaller bound but a
// different rule.
var pointerBytes = receiverBytes(judgedSizes.Sizeof(types.Typ[types.UnsafePointer]))

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
// requires. A value that is not an integer of at least pointerBytes is refused
// with ErrInvalidMaxCopy rather than accepted as a bound nothing can honour: a
// negative one, and 0, and anything under one pointer width, exempt receivers
// whose copy is no wider than the pointer they replace, which prints nothing
// and charges nothing.
func (b *receiverBytes) Set(value string) error {
	size, err := strconv.ParseInt(value, 10, 64)
	if err != nil || receiverBytes(size) < pointerBytes {
		return ErrInvalidMaxCopy.With(err, "max", value, "minimum", pointerBytes)
	}
	*b = receiverBytes(size)
	return nil
}

// copyIsCostly reports whether copying t on every call costs materially more
// than passing the pointer it would replace — which makes the value receiver a
// remedy the author cannot take whatever else the method does. A width this
// package cannot measure is costly, because a missed finding costs a finding
// and a finding whose remedy breaks the program manufactures a baseline.
func (j judgement) copyIsCostly(t types.Type) bool {
	narrowest, measurable := j.narrowestBytes(t)
	return !measurable || narrowest > configuredMaxCopy
}

// narrowestBytes is the width in bytes of the narrowest value t can hold, and
// whether that width could be measured at all.
//
// go/types answers a type it considers larger than the address space with -1
// rather than an error, and a generic declaration reaches that answer while
// still compiling: `type Big[T any] struct{ a [1<<40][1<<40]byte; v T }` is
// rejected outright when it is written without the parameter and accepted with
// it, because the size of an uninstantiated generic is never computed. A -1 read
// as a byte count is smaller than every bound, so the widest receiver there is
// would be judged free.
func (j judgement) narrowestBytes(t types.Type) (receiverBytes, bool) {
	narrowest, instantiable := narrowestInstance(t)
	if !instantiable {
		return 0, false
	}
	size := j.sizes.Sizeof(narrowest)
	if size < 0 {
		return 0, false
	}
	return receiverBytes(size), true
}

// narrowestInstance is t with the empty struct — the narrowest type there is —
// substituted for every type parameter it still stands on, so a generic
// receiver is measured against the smallest width any instantiation of it can
// have.
//
// The alternative, declining to measure a generic receiver at all, is what this
// replaces, and it turned criterion 4 off for anything carrying a parameter:
// REPRODUCED with two types in one file over byte-identical fields, where
// `type PlainMatcher struct{ table [1<<15]uint32; n Count }` was withheld at
// 131080 bytes and `type GenericMatcher[T any]` with the same two fields and an
// unused T was REPORTED — and the remedy, built and run, took two million calls
// of its read-only Find from 977.167µs to 26.779913458s.
//
// Substituting is sound because it can only make the type NARROWER: the bound
// is `wider than max`, so a receiver withheld on its narrowest instance is
// withheld on every instance, and one reported on its narrowest may still be
// wide once instantiated — which is the direction this rule is conservative in
// everywhere else.
//
// WHAT IT IS WORTH, measured rather than argued, because the number is smaller
// than the prose above would suggest. Replacing the substitution with the bare
// type changes NO verdict the analyzer reaches: over Go 1.26.6 the sorted
// location sets are identical, and every fixture reports the same. The reason is
// a coupling with copy.go — componentsRequirePointer withholds every receiver
// with a by-value type parameter component, which is precisely the shape
// go/types refuses to size, so reportable never asks copyIsCostly a question the
// substitution would answer differently. The load-bearing half of this fix is
// that a generic receiver is MEASURED AT ALL rather than declined.
//
// It is kept because it makes copyIsCostly total: a function whose only
// precondition is enforced by a different file, with nothing asserting the
// coupling, is one refactor in copy.go away from a panic in a rule the whole
// fleet runs. The test that pins it calls copyIsCostly directly, which is the
// only caller that can reach the branch, and that is stated rather than dressed
// up as an end-to-end case.
func narrowestInstance(t types.Type) (types.Type, bool) {
	named, isNamed := types.Unalias(t).(*types.Named)
	if !isNamed || named.TypeParams().Len() == 0 {
		return t, true
	}
	instance, err := instantiate(nil, named.Origin(), narrowestArguments(named), false)
	if err != nil {
		return nil, false
	}
	return instance, true
}

// instantiate is go/types' instantiation, indirected because its failure is
// unreachable from any receiver this analyzer sees — the argument count is
// derived from the parameter list it is checked against, and constraints are
// not validated — while the branch that answers it decides a verdict, so the
// only way to test that branch is to make the call fail.
var instantiate = types.Instantiate

// typeArgumentIndex is a position in a generic type's argument list.
type typeArgumentIndex int

// narrowestArguments is one type argument per type parameter of named's origin:
// the empty struct wherever the declaration still stands on a parameter of its
// own, and the argument itself wherever it is already instantiated over a
// concrete type. `Box[Table]` reached through a field is 262144 bytes and is
// measured as such; the receiver of `func (b *Box[T]) M()`, whose one argument
// IS the parameter T, is measured with T at zero width.
func narrowestArguments(named *types.Named) []types.Type {
	args := named.TypeArgs()
	narrowest := make([]types.Type, named.Origin().TypeParams().Len())
	for i := range narrowest {
		narrowest[i] = narrowestArgument(args, typeArgumentIndex(i))
	}
	return narrowest
}

// narrowestArgument is args[i] when the type is instantiated over a concrete
// type there, and the empty struct when it stands on a type parameter or has no
// arguments at all — the bare origin `Box`, which is what a package scope holds.
func narrowestArgument(args *types.TypeList, at typeArgumentIndex) types.Type {
	if int(at) >= args.Len() {
		return types.NewStruct(nil, nil)
	}
	argument := args.At(int(at))
	if _, stillAParameter := types.Unalias(argument).(*types.TypeParam); stillAParameter {
		return types.NewStruct(nil, nil)
	}
	return argument
}
