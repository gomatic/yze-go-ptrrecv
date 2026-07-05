// Package c shadows the builtin error with a package-level type. A method with
// a well-known decoder name returning the SHADOW does not satisfy the decode
// contract (whose sole result is the builtin error interface), so it stays
// reported — the shape check must be semantic, not textual.
package c

// error shadows the builtin error interface for the whole package.
type error struct{ msg string }

// Fake carries a Set method with the decoder name but the shadowed result type.
type Fake struct{ v string }

func (f *Fake) Set(v string) error { f.v = v; return error{msg: v} } // want `pointer receiver on Fake`
