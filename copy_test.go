package ptrrecv

// White-box tests for the copy-semantics rules. Every judgement here decides
// whether a pointer receiver is JUSTIFIED, and the --fix path rewrites the ones
// it judges unjustified into value receivers. Getting that wrong does not
// produce a wrong diagnostic — it rewrites a type that must not be copied into
// one the compiler will happily copy, which is a data race the author never
// wrote and the tool introduced.

import (
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// checked type-checks src as a package whose import path is a bare word — the
// shape every analysistest fixture has, and the one that makes the
// package-under-analysis exclusion load-bearing.
func checked(t *testing.T, src string) *types.Package {
	t.Helper()
	return checkedAt(t, "p", src)
}

// checkedAt type-checks src as the package at the given import path.
func checkedAt(t *testing.T, path, src string) *types.Package {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "p.go", "package p\n"+src, 0)
	require.NoError(t, err)
	conf := types.Config{Importer: importer.Default()}
	pkg, err := conf.Check(path, fset, []*ast.File{file}, nil)
	require.NoError(t, err)
	return pkg
}

// judged type-checks src and reports requiresPointer for the type declared as
// `T`, judged as though src were the package under analysis.
func judged(t *testing.T, src string) bool {
	t.Helper()
	pkg := checked(t, src)
	obj := pkg.Scope().Lookup("T")
	require.NotNil(t, obj, "the fixture must declare a type T")
	return judgement{own: pkg}.requiresPointer(obj.Type())
}

// TestJudgementRequiresPointerDerivesUncopyabilityFromTheType names the criterion
// that replaced a hand-maintained list of seventeen names. A list is short by
// exactly the amount its author did not think of: the one this analyzer shipped
// omitted bufio.Writer — the exemplar the receiver standard itself names for
// "stateful machinery that cannot be copied" — so a bufio.Writer holder was
// reported AND rewritten to a value receiver, duplicating the byte count and
// the error while both copies shared one backing array.
//
// The property is that the type's whole method set needs a pointer and none of
// it is available on a value. The negative half is the sharper one: time.Time
// and time.Duration have value methods, are ordinary copyable values, and must
// stay judged, or the criterion swallows the rule.
func TestJudgementRequiresPointerDerivesUncopyabilityFromTheType(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		src  string
		why  string
		want bool
	}{
		{
			src: `import "bufio"; type T struct{ w bufio.Writer }`, want: true,
			why: "a bufio.Writer shares one backing array with its copy",
		},
		{src: `import "bufio"; type T struct{ r bufio.Reader }`, want: true, why: "a bufio.Reader copy re-reads its buffer"},
		{src: `import "bufio"; type T struct{ s bufio.Scanner }`, want: true, why: "a bufio.Scanner copy repeats a token"},
		{src: `import "math/rand"; type T struct{ r rand.Rand }`, want: true, why: "a rand.Rand copy repeats its sequence"},
		{src: `import "bytes"; type T struct{ b bytes.Buffer }`, want: true, why: "bytes.Buffer"},
		{
			src: `import "strings"; type T struct{ b strings.Builder }`, want: true,
			why: "strings.Builder, which panics on a copy",
		},
		{
			src: `import "sync/atomic"; type T struct{ v atomic.Value }`, want: true,
			why: "atomic.Value carries no marker of its own — only the method set says so",
		},
		{src: `import "sync"; type T struct{ mu sync.Mutex }`, want: true, why: "a sync primitive"},
		{
			src: `import "encoding/json"; type T struct{ d json.Decoder }`, want: true,
			why: "no list would ever have named this one",
		},

		{
			src: `import "time"; type T struct{ at time.Time }`, want: false,
			why: "time.Time has 49 value methods and is copied everywhere",
		},
		{src: `import "time"; type T struct{ d time.Duration }`, want: false, why: "and a Duration is a number"},
		{
			src: `import "os"; type T struct{ f os.File }`, want: false,
			why: "os.File has a value method, so the criterion does not claim it",
		},
		{src: `import "io"; type T struct{ w io.Writer }`, want: false, why: "an interface value is a pair of words"},
		{
			src: `import "unicode"; type T struct{ r unicode.RangeTable }`, want: false,
			why: "a standard-library struct with NO methods at all presents no pointer API to hold",
		},
		{src: `type T struct{ n int }`, want: false, why: "an ordinary struct is copyable"},
		{src: `type T struct{}`, want: false, why: "and so is an empty one"},
	} {
		assert.Equal(t, tc.want, judged(t, tc.src), "requiresPointer(%s): %s", tc.src, tc.why)
	}
}

// TestStdlibNoCopyNeverClaimsThePackageUnderAnalysis names the exclusion that
// keeps the criterion from swallowing local code. An import path is judged
// standard-library by having no dot in its first segment, and a package under
// analysis can look exactly like that — every analysistest fixture does, and so
// does any module whose path is a bare word. Without the exclusion, a local
// type with a pointer-only method set would exempt everything holding it.
func TestStdlibNoCopyNeverClaimsThePackageUnderAnalysis(t *testing.T) {
	t.Parallel()

	local := `type inner struct{ n int }
func (i *inner) Bump() { i.n++ }
type T struct{ in inner }`
	assert.False(t, judged(t, local), "a local pointer-only type is not standard-library machinery")

	pkg := checked(t, local)
	inner := pkg.Scope().Lookup("inner").Type()
	assert.True(
		t,
		judgement{own: nil}.stdlibNoCopy(inner),
		"the same type IS claimed once it is not the package under analysis — the exclusion is the only thing between them",
	)

	foreign := checkedAt(t, "example.com/x", `type Thing struct{ n int }
func (t *Thing) Bump() { t.n++ }`).Scope().Lookup("Thing")
	assert.False(t, judgement{own: nil}.stdlibNoCopy(foreign.Type()),
		"a THIRD-PARTY pointer-only type is not claimed either: an author can write one, "+
			"so it goes through -allow where it is written down, not through a derivation")

	assert.True(t, stdlibPath("bufio"), "a first segment with no dot reads as standard-library")
	assert.True(t, stdlibPath("math/rand"), "and so does a nested one")
	assert.False(t, stdlibPath("example.com/x"), "a domain does not")
	assert.False(t, stdlibPath("gopkg.in/yaml.v3"), "wherever its dots fall")
}

// TestForeignRepresentationSeesATypeDefinedOverAnotherPackage names the
// criterion the method set cannot reach. `type MyBuf bytes.Buffer` takes the
// Buffer's representation and leaves every method behind, so stdlibNoCopy sees
// a type with no methods at all — while copying it copies a buffered writer,
// which is the same data loss under another name. The question is asked only of
// a type declared HERE: a foreign type's own struct always holds its own
// unexported fields, and whether that one may be copied is the method set's
// question.
func TestForeignRepresentationSeesATypeDefinedOverAnotherPackage(t *testing.T) {
	t.Parallel()

	assert.True(t, judged(t, `import "bytes"; type T bytes.Buffer`),
		"a type defined over bytes.Buffer holds the machinery and shows none of it")
	assert.True(t, judged(t, `import "bytes"; type over bytes.Buffer
type T struct{ b over }`), "and a struct holding one holds it inline")

	assert.False(t, judged(t, `import "go/token"; type T token.Position`),
		"a defined-over type whose fields are all EXPORTED hid nothing and is copyable")
	assert.False(t, judged(t, "type T struct{ n int }"),
		"a struct written here has its own package's fields")

	foreign := checked(t, `import "time"; type T struct{ at time.Time }`).Scope().Lookup("T")
	assert.False(t, judgement{own: nil}.foreignRepresentation(foreign.Type()),
		"a type declared elsewhere is never asked, or every foreign struct answers yes")
}

// TestRequiresPointerAndComponentsRequirePointerAgreeOnInlineComponents names
// both claims together, because they are one rule: a copy of the outer value
// copies its INLINE components, so a mutex reached through struct fields or
// array elements — at any depth — makes the whole type uncopyable.
//
// The negative half is the sharper one. A slice, map, channel or pointer holding
// a mutex leaves the outer type copyable, because copying it duplicates only the
// header or the pointer and both copies still refer to the SAME mutex. Walking
// into those would justify a pointer receiver that nothing requires, which is
// the false-negative that makes the rule toothless.
func TestRequiresPointerAndComponentsRequirePointerAgreeOnInlineComponents(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		src  string
		why  string
		want bool
	}{
		{
			src: `import "sync"; type T sync.Mutex`, want: true,
			why: "the no-copy type itself, through its noCopy marker field",
		},
		{src: `import "sync"; type T struct{ mu sync.Mutex }`, want: true, why: "a direct field"},
		{src: `import "sync"; type inner struct{ mu sync.Mutex }
type T struct{ in inner }`, want: true, why: "a nested struct field"},
		{src: `import "sync"; type T struct{ locks [3]sync.Mutex }`, want: true, why: "an array stores elements inline"},
		{src: `import "sync"; type T struct{ locks [2][2]sync.Mutex }`, want: true, why: "and does so at any depth"},

		{
			src: `import "sync"; type T struct{ locks []sync.Mutex }`, want: false,
			why: "a slice copy duplicates only the header",
		},
		{
			src: `import "sync"; type T struct{ locks map[string]sync.Mutex }`, want: false,
			why: "a map copy duplicates only the reference",
		},
		{
			src: `import "sync"; type T struct{ locks chan sync.Mutex }`, want: false,
			why: "a channel copy duplicates only the reference",
		},
		{src: `import "sync"; type T struct{ mu *sync.Mutex }`, want: false, why: "both copies point at the same mutex"},
	} {
		assert.Equal(t, tc.want, judged(t, tc.src), "requiresPointer(%s): %s", tc.src, tc.why)
	}
}

// TestIsTypeParamMakesRequiresPointerConservative names the type-parameter
// claim: a type parameter must be treated as uncopyable, because its
// instantiation may be no-copy machinery — Box[sync.Mutex] — and rewriting the
// receiver to a value would copy a mutex, a data race the author never wrote.
// The negative half keeps the rule honest: a GENERIC type whose fields never
// store a type parameter is copyable at every instantiation and stays judged.
func TestIsTypeParamMakesRequiresPointerConservative(t *testing.T) {
	t.Parallel()

	assert.True(t, judged(t, "type T[P any] struct{ v P }"),
		"a type-parameter field may be instantiated no-copy")
	assert.False(t, judged(t, "type box[P any] struct{ v P }\ntype T = box[int]"),
		"an INSTANTIATED generic is judged by its instantiation, not conservatively")
	assert.False(t, judged(t, "type T[P any] struct{ v int }"),
		"a generic type with no type-parameter field stays copyable")
}

// TestLockerShapeIsTheVetCopylocksCriterionAndIsForgeableByDesign names both
// halves of the Locker exemption. A type whose POINTER method set has nullary
// Lock and Unlock is uncopyable by go vet's copylocks criterion; a type whose
// VALUE method set has them is freely copyable and stays judged.
//
// The forgery is sanctioned, and the reason is acquisition rather than the doc
// comment sanctioning it. Two empty methods on an ordinary struct silence this
// rule for the type and for everything holding it — and they hand the same type
// to go vet, which then reports "passes lock by value" at every value receiver
// and "assignment copies lock value" at every assignment, transitively through
// any struct holding one. Forging the marker costs copyability everywhere,
// enforced by another tool in the same gate, so the forgery IS the property.
func TestLockerShapeIsTheVetCopylocksCriterionAndIsForgeableByDesign(t *testing.T) {
	t.Parallel()

	forged := `type T struct{ n int }
func (t *T) Lock() {}
func (t *T) Unlock() {}`
	assert.True(t, judged(t, forged), "the forged marker is honoured, because go vet honours it too")
	assert.True(t, judged(t, forged+"\ntype U struct{ t T }\n"), "and is honoured transitively")

	assert.False(t, judged(t, `type T struct{ n int }
func (T) Lock() {}
func (T) Unlock() {}`), "a VALUE Locker is freely copyable and stays judged")

	assert.False(t, judged(t, `type T struct{ n int }
func (t *T) Lock(n int) {}
func (t *T) Unlock() {}`), "the shape is nullary Lock and Unlock, not the two names")
}
