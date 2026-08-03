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

// typeOf type-checks src and returns the named type declared as `T`.
func typeOf(t *testing.T, src string) types.Type {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "p.go", "package p\n"+src, 0)
	require.NoError(t, err)
	conf := types.Config{Importer: importer.Default()}
	pkg, err := conf.Check("example.test/p", fset, []*ast.File{file}, nil)
	require.NoError(t, err)
	obj := pkg.Scope().Lookup("T")
	require.NotNil(t, obj, "the fixture must declare a type T")
	return obj.Type()
}

// TestNoCopyTypesNamesEveryStdlibTypeThatMustNotBeCopied names noCopyTypes'
// claim. Each entry is a type whose copy is a bug the compiler will not catch:
// copying a sync.Mutex duplicates its state, so two goroutines can hold what
// they each believe is the same lock. Entries are matched by their qualified
// name, so a typo silently removes a type from the allow-list and the analyzer
// starts telling authors to make those receivers values.
func TestNoCopyTypesNamesEveryStdlibTypeThatMustNotBeCopied(t *testing.T) {
	t.Parallel()

	for _, name := range []string{
		"sync.Mutex", "sync.RWMutex", "sync.WaitGroup", "sync.Once",
		"sync.Pool", "sync.Map", "sync.Cond",
		"sync/atomic.Int32", "sync/atomic.Int64", "sync/atomic.Uint32",
		"sync/atomic.Uint64", "sync/atomic.Bool", "sync/atomic.Uintptr",
		"sync/atomic.Pointer",
	} {
		assert.True(t, noCopyTypes[name], "%s must be allow-listed as no-copy", name)
	}
	assert.False(t, noCopyTypes["sync.Locker"], "an interface is copyable; only concrete state is not")
	assert.False(t, noCopyTypes["time.Time"], "an ordinary copyable value must not be allow-listed")
}

// TestRequiresPointerAndComponentsRequirePointerAgreeOnInlineComponents names
// both claims together, because they are one rule: a copy of the outer value copies its INLINE components, so a
// mutex reached through struct fields or array elements — at any depth — makes
// the whole type uncopyable.
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
		{src: `import "sync"; type T sync.Mutex`, want: true, why: "the no-copy type itself"},
		{src: `import "sync"; type T struct{ mu sync.Mutex }`, want: true, why: "a direct field"},
		{src: `import "sync"; type inner struct{ mu sync.Mutex }
type T struct{ in inner }`, want: true, why: "a nested struct field"},
		{src: `import "sync"; type T struct{ locks [3]sync.Mutex }`, want: true, why: "an array stores elements inline"},
		{src: `import "sync"; type T struct{ locks [2][2]sync.Mutex }`, want: true, why: "and does so at any depth"},

		{src: `import "sync"; type T struct{ locks []sync.Mutex }`, want: false, why: "a slice copy duplicates only the header"},
		{src: `import "sync"; type T struct{ locks map[string]sync.Mutex }`, want: false, why: "a map copy duplicates only the reference"},
		{src: `import "sync"; type T struct{ locks chan sync.Mutex }`, want: false, why: "a channel copy duplicates only the reference"},
		{src: `import "sync"; type T struct{ mu *sync.Mutex }`, want: false, why: "both copies point at the same mutex"},
		{src: `type T struct{ n int }`, want: false, why: "an ordinary struct is copyable"},
		{src: `type T struct{}`, want: false, why: "and so is an empty one"},
	} {
		assert.Equal(t, tc.want, requiresPointer(nil, typeOf(t, tc.src)),
			"requiresPointer(%s): %s", tc.src, tc.why)
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

	assert.True(t, requiresPointer(nil, fieldType(t, "type T[P any] struct{ v P }")),
		"a type-parameter field may be instantiated no-copy")
	assert.False(t, requiresPointer(nil, typeOf(t, "type box[P any] struct{ v P }\ntype T = box[int]")),
		"an INSTANTIATED generic is judged by its instantiation, not conservatively")
	assert.False(t, requiresPointer(nil, fieldType(t, "type T[P any] struct{ v int }")),
		"a generic type with no type-parameter field stays copyable")
}

// fieldType type-checks src, which must declare a struct type `T`, and returns
// the type of T's sole field — the shape under judgement.
func fieldType(t *testing.T, src string) types.Type {
	t.Helper()
	st, ok := typeOf(t, src).Underlying().(*types.Struct)
	require.True(t, ok, "the fixture's T must be a struct")
	require.Equal(t, 1, st.NumFields(), "the fixture's T must have one field")
	return st.Field(0).Type()
}
