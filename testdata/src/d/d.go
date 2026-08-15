// Package d is run with the same -allow=b.special as package b, and is the
// DOES-NOT-APPLY half of that exemption, written against its matcher rather
// than against its description. The matcher is a lookup of the package path,
// a dot and the type name, so the near-miss that leaks through a sloppy one is
// a type with the SAME NAME in a different package: widening the lookup to the
// bare name silences everything here, and an added disjunct inside an existing
// condition adds no statement, so no coverage number can see it.
package d

// special has the same name as b.special and a different package path, so
// -allow=b.special exempts nothing here.
type special struct{ _ int }

type Holder struct {
	s special
	n int
}

func (h *Holder) N() int { return h.n } // want `pointer receiver on Holder should be a value receiver`

// Buffer, Value, Mutex and Builder are ordinary copyable types whose BARE names
// collide with the standard-library types whose copy is a defect. Nothing here
// is uncopyable, so all of it is reported.
type Buffer struct{ n int }

func (b *Buffer) N() int { return b.n } // want `pointer receiver on Buffer should be a value receiver`

type Value struct{ n int }

func (v *Value) N() int { return v.n } // want `pointer receiver on Value should be a value receiver`

type Mutex struct{ n int }

func (m *Mutex) N() int { return m.n } // want `pointer receiver on Mutex should be a value receiver`

type Builder struct{ n int }

func (b *Builder) N() int { return b.n } // want `pointer receiver on Builder should be a value receiver`

type Collides struct {
	a Buffer
	b Value
	c Mutex
	d Builder
	n int
}

func (c *Collides) N() int { return c.n } // want `pointer receiver on Collides should be a value receiver`
