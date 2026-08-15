package ptrrecv

// The configurable allow-list: the one exemption that is set rather than
// computed, and the only one that leaves no trace when it fires. Everything
// here exists so that a value which cannot do what the author believes it does
// is refused at the moment it is configured, rather than silently exempting
// nothing (or something else) for the life of the repository.

import (
	"go/token"
	"strings"

	errs "github.com/gomatic/go-error"
)

// ErrInvalidAllowEntry reports an -allow entry that is not a fully-qualified
// type name. go-yze's ApplyConfig turns a Set failure into its own
// ErrInvalidSettingValue, so a mistyped entry in a .stickler.yaml fails the run
// instead of exempting nothing in silence.
const ErrInvalidAllowEntry errs.Const = "-allow entry is not a fully-qualified type name (pkgpath.Name)"

// allowSet is the set of fully-qualified type names (pkgpath.Name) treated as
// uncopyable in addition to the ones derived from the types themselves.
type allowSet map[string]bool

// allowValue is the raw -allow flag value: a comma-separated list.
type allowValue string

// allowField is one comma-separated field of that value, before it is trimmed.
type allowField string

// allowEntry is one fully-qualified type name, trimmed and validated.
type allowEntry string

// allowList is the -allow flag's value. It implements flag.Value so that a
// malformed entry is an error at configuration time: flag.StringVar's Set
// cannot fail, which is what made every bad value acceptable.
type allowList struct {
	raw     allowValue
	entries []allowEntry
}

// configuredAllow holds the parsed -allow flag. It is package state because a
// go/analysis flag set has nowhere else to write.
var configuredAllow allowList

// String returns the flag value as configured, which is what the flag package
// prints in usage and defaults.
func (a allowList) String() string {
	return string(a.raw)
}

// Set parses and validates a comma-separated list of fully-qualified type
// names. Entries are trimmed, so "a.B, c.D" — the way a person writes a list —
// means what it looks like; an entry that is not pkgpath.Name is refused with
// ErrInvalidAllowEntry rather than stored as a key nothing can match. A refused
// value leaves the previous one standing rather than half-applying.
func (a *allowList) Set(value string) error {
	entries, err := parseAllowList(allowValue(value))
	if err != nil {
		return err
	}
	a.raw = allowValue(value)
	a.entries = entries
	return nil
}

// set is the allow-list as a lookup set.
func (a allowList) set() allowSet {
	allow := make(allowSet, len(a.entries))
	for _, entry := range a.entries {
		allow[string(entry)] = true
	}
	return allow
}

// parseAllowList splits and validates the whole flag value. An unset flag is no
// entries at all; every other entry must be a fully-qualified type name, the
// empty one included — a stray comma is a typo, and a typo that exempts nothing
// is what nobody ever notices.
func parseAllowList(value allowValue) ([]allowEntry, error) {
	if value == "" {
		return nil, nil
	}
	fields := strings.Split(string(value), ",")
	entries := make([]allowEntry, 0, len(fields))
	for _, field := range fields {
		entry, err := parseAllowEntry(allowField(field))
		if err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

// parseAllowEntry trims one field and reports whether it names a type the way
// isNoCopy looks one up: a package path, a dot, and a Go identifier.
func parseAllowEntry(field allowField) (allowEntry, error) {
	entry := allowEntry(strings.TrimSpace(string(field)))
	path, name, isQualified := splitQualified(entry)
	if !isQualified || !token.IsIdentifier(string(name)) || strings.ContainsAny(string(path), " \t") {
		return "", ErrInvalidAllowEntry.With(nil, "entry", string(field))
	}
	return allowEntry(string(path) + "." + string(name)), nil
}

// splitQualified splits a fully-qualified type name at its final dot, which is
// the only dot that separates the package path from the type name (an import
// path holds dots of its own: "example.com/pkg.Type").
func splitQualified(entry allowEntry) (path packagePath, name typeName, isQualified bool) {
	i := strings.LastIndex(string(entry), ".")
	if i <= 0 || i == len(entry)-1 {
		return "", "", false
	}
	return packagePath(entry[:i]), typeName(entry[i+1:]), true
}
