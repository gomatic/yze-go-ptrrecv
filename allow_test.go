package ptrrecv

// White-box tests for the configurable exemption. It is the only disablement
// channel this analyzer has, and the only one that leaves no trace when it
// fires: a configured -allow prints nothing, counts nothing and ratchets
// nothing. So the one thing it owes its reader is that a value it accepts does
// what the reader believes it does.

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestParseAllowListTrimsEveryEntry names the claim that made this a finding.
// "a.B, c.D" is how a person writes a list, and the untrimmed value stored
// " c.D", which no lookup — a package path, a dot and a type name — can ever
// equal. Half the list worked, the other half exempted nothing, and nothing
// said so.
func TestParseAllowListTrimsEveryEntry(t *testing.T) {
	t.Parallel()

	entries, err := parseAllowList(allowValue("example.com/x.Plain, example.com/x.Other"))
	require.NoError(t, err)
	assert.Equal(t, []allowEntry{"example.com/x.Plain", "example.com/x.Other"}, entries,
		"a space after the comma is how a list is written, not a different type")

	entries, err = parseAllowList(allowValue(""))
	require.NoError(t, err)
	assert.Nil(t, entries, "an unset flag is no entries, not one empty entry")

	entries, err = parseAllowList(allowValue("main.Pool"))
	require.NoError(t, err)
	assert.Equal(t, []allowEntry{"main.Pool"}, entries,
		"a syntactically valid path is accepted even when nothing in the run has it — "+
			"a main package's path is its module path, never \"main\", so this exempts "+
			"nothing, and catching that needs the run to report an entry nothing matched")
}

// TestParseAllowListRefusesAnEntryItCannotHonour names the refusal. An entry
// that is not pkgpath.Name exempts nothing, and an exemption that exempts
// nothing is invisible from both sides: the author believes a type is
// allow-listed, and the reviewer reading the config believes it too. go-yze's
// ApplyConfig turns this error into ErrInvalidSettingValue, so a mistyped entry
// in a .stickler.yaml fails the run instead of passing it.
func TestParseAllowListRefusesAnEntryItCannotHonour(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct{ value, why string }{
		{value: "not-a-type", why: "no dot, so no package path"},
		{value: "example.com/x.Plain,,example.com/x.Other", why: "a stray comma is a typo"},
		{value: ",,,   ,not-a-type,,,", why: "and so is whatever this was"},
		{value: "example.com/x.", why: "a path with no type name"},
		{value: ".Plain", why: "a type name with no path"},
		{value: "example.com/x.Not A Name", why: "a type name is an identifier"},
		{value: "example com/x.Plain", why: "and an import path holds no spaces"},
		{value: "example.com/x.a/b", why: "the last dot is the separator, so this names no identifier"},
		{value: "..Pool", why: "a path segment made only of dots is no package"},
		{value: ".....Pool", why: "however many dots it is made of"},
		{value: "/.Pool", why: "and an empty segment is no package either"},
		{value: "example.com//x.Pool", why: "wherever the empty segment falls"},
		{value: "exam\nple.com/x.Pool", why: "an import path holds no newline"},
	} {
		_, err := parseAllowList(allowValue(tc.value))
		require.Error(t, err, "%q must be refused: %s", tc.value, tc.why)
		assert.True(t, errors.Is(err, ErrInvalidAllowEntry),
			"%q must be refused with the sentinel a caller can match, got %v", tc.value, err)
	}
}

// TestIsImportPathRefusesASegmentNoPackageCanHave names the path half of the
// check. The name half is an identifier and go/token decides it; the path half
// has no such decider, and before this every one of these was accepted and
// exempted nothing. It is deliberately not a claim that the path EXISTS —
// "main.Pool" passes and matches nothing, which needs the run to report an
// entry nothing matched, and nothing here does that.
func TestIsImportPathRefusesASegmentNoPackageCanHave(t *testing.T) {
	t.Parallel()

	assert.True(t, isImportPath("example.com/x"), "a domain and a segment")
	assert.True(t, isImportPath("bufio"), "a standard-library path is one segment")
	assert.True(t, isImportPath("main"), "and this is syntactically one too, though nothing has it")

	assert.False(t, isImportPath(""), "nothing is not a path")
	assert.False(t, isImportPath(".."), "nor is a segment made only of dots")
	assert.False(t, isImportPath("...."), "however many of them")
	assert.False(t, isImportPath("/"), "nor one that is empty")
	assert.False(t, isImportPath("example.com//x"), "wherever the empty segment falls")
	assert.False(t, isImportPath("example.com/x y"), "and a path holds no whitespace")
	assert.False(t, isImportPath("exam\nple.com/x"), "of any kind")
}

// TestSplitQualifiedCutsAtTheFinalDotAndRefusesAnEmptyHalf names the split's own
// contract, which parseAllowEntry's later checks hide: isImportPath and
// token.IsIdentifier refuse the same two inputs the boundary conditions do, so
// no -allow value can tell a sound split from one that returns an empty path or
// an empty name. The contract is still the split's to keep, and it is asserted
// where it is decided rather than left to a caller that happens to agree.
func TestSplitQualifiedCutsAtTheFinalDotAndRefusesAnEmptyHalf(t *testing.T) {
	t.Parallel()

	path, name, isQualified := splitQualified("example.com/pkg.Type")
	assert.True(t, isQualified)
	assert.Equal(t, packagePath("example.com/pkg"), path, "the FINAL dot is the separator")
	assert.Equal(t, typeName("Type"), name)

	_, _, isQualified = splitQualified(".Pool")
	assert.False(t, isQualified, "a leading dot leaves no path")

	_, _, isQualified = splitQualified("example.com/pkg.")
	assert.False(t, isQualified, "a trailing dot leaves no name")

	_, _, isQualified = splitQualified("Pool")
	assert.False(t, isQualified, "and a name with no dot is not qualified at all")
}

// TestAllowListSetIsTheFlagAndTheLookup names what the flag.Value does with a
// value it accepts: it keeps the raw text for the flag package to print, and it
// yields the set isNoCopy looks up. flag.StringVar's Set cannot fail, which is
// why this is a flag.Value at all.
func TestAllowListSetIsTheFlagAndTheLookup(t *testing.T) {
	t.Parallel()

	var list allowList
	assert.Empty(t, list.String(), "an unset flag prints as nothing")
	assert.Empty(t, list.set(), "and allows nothing")

	require.NoError(t, list.Set("example.com/x.Plain, example.com/x.Other"))
	assert.Equal(t, "example.com/x.Plain, example.com/x.Other", list.String(),
		"the flag prints what was configured, not what it parsed to")
	assert.Equal(t, allowSet{"example.com/x.Plain": true, "example.com/x.Other": true}, list.set())

	require.Error(t, list.Set("not-a-type"))
	assert.Equal(t, allowSet{"example.com/x.Plain": true, "example.com/x.Other": true}, list.set(),
		"a refused value leaves the previous one standing, rather than half-applying")
}
