package a

import (
	"bytes"
	"strings"
	"sync"
	"sync/atomic"
)

// Plain holds no no-copy field; its pointer-receiver method is flagged.
type Plain struct {
	count int
	err   error
}

func (p *Plain) Inc() { p.count++ } // want `pointer receiver on Plain should be a value receiver; the type holds no field that requires a pointer`

// Value receivers are always allowed.
func (p Plain) Get() int { return p.count }

// Guarded holds a sync.Mutex, so a pointer receiver is allowed.
type Guarded struct {
	mu   sync.Mutex
	data int
}

func (g *Guarded) Set(n int) {
	g.mu.Lock()
	g.data = n
	g.mu.Unlock()
}

// RWGuarded holds a sync.RWMutex, so a pointer receiver is allowed.
type RWGuarded struct{ mu sync.RWMutex }

func (g *RWGuarded) Touch() { g.mu.Lock(); g.mu.Unlock() }

// Waiter holds a sync.WaitGroup, so a pointer receiver is allowed.
type Waiter struct{ wg sync.WaitGroup }

func (w *Waiter) Touch() { w.wg.Wait() }

// Counter holds a sync/atomic.Int64, so a pointer receiver is allowed.
type Counter struct{ n atomic.Int64 }

func (c *Counter) Touch() { c.n.Add(1) }

// Buffered holds a bytes.Buffer, so a pointer receiver is allowed.
type Buffered struct{ buf bytes.Buffer }

func (b *Buffered) Touch() { b.buf.Reset() }

// Built holds a strings.Builder, so a pointer receiver is allowed.
type Built struct{ sb strings.Builder }

func (b *Built) Touch() { b.sb.Reset() }

// Embedded directly embeds a sync.Mutex (anonymous field), so a pointer receiver
// is allowed.
type Embedded struct {
	sync.Mutex
	data int
}

func (e *Embedded) Touch() { e.Lock(); e.Unlock() }

// Nested transitively contains a Mutex through Guarded, so it is allowed.
type Nested struct {
	inner Guarded
}

func (n *Nested) Touch() { n.inner.Set(1) }

// ArrGuarded holds an array of Mutex. An array stores its elements inline, so the
// struct cannot be copied (go vet copylocks agrees); the pointer receiver is
// allowed.
type ArrGuarded struct {
	mus [3]sync.Mutex
}

func (a *ArrGuarded) Touch() { a.mus[0].Lock(); a.mus[0].Unlock() }

// SliceGuarded holds a slice of Mutex. A slice is a reference: copying the struct
// copies only the slice header, not the mutexes, so the struct is copyable and
// the pointer receiver is flagged.
type SliceGuarded struct {
	mus []sync.Mutex
}

func (s *SliceGuarded) Bad() { _ = s.mus } // want `pointer receiver on SliceGuarded should be a value receiver; the type holds no field that requires a pointer`

// Scalar is a non-struct named type; its pointer-receiver method is flagged.
type Scalar int

func (s *Scalar) Bump() { *s++ } // want `pointer receiver on Scalar should be a value receiver; the type holds no field that requires a pointer`

// Box is a generic type with no no-copy field; its pointer-receiver method is
// flagged.
type Box[T any] struct{ v T }

func (b *Box[T]) Touch() { _ = b.v } // want `pointer receiver on Box should be a value receiver; the type holds no field that requires a pointer`

// GuardedBox is a generic type holding a sync.Mutex, so a pointer receiver is
// allowed.
type GuardedBox[T any] struct {
	mu sync.Mutex
	v  T
}

func (g *GuardedBox[T]) Set(x T) { g.mu.Lock(); g.v = x; g.mu.Unlock() }

// AliasInner is a plain struct with no no-copy field. AliasPlain aliases it, and
// a pointer-receiver method declared through the alias resolves to *types.Alias
// (Go 1.23+); it must be unaliased to the underlying named type rather than
// crashing, and the diagnostic names that underlying type.
type AliasInner struct{ x int }

type AliasPlain = AliasInner

func (a *AliasPlain) Touch() { _ = a.x } // want `pointer receiver on AliasInner should be a value receiver; the type holds no field that requires a pointer`

// AliasGuarded aliases a mutex-holding struct; resolving the alias must still
// find the no-copy field through the underlying type, so the pointer receiver is
// allowed.
type AliasGuarded = Guarded

func (g *AliasGuarded) Poke() { g.mu.Lock(); g.mu.Unlock() }

// Reader never writes through its receiver — it only reads fields, calls a
// value-receiver method, and mutates locals — so its diagnostics carry the
// value-receiver fix.
type Reader struct{ n int }

func (r Reader) get() int { return r.n }

func (r *Reader) Len() int { // want `pointer receiver on Reader should be a value receiver; the type holds no field that requires a pointer`
	i := 0
	i++
	local := r.n
	_ = &i
	if !(local > 0) {
		return -r.get()
	}
	return r.get() + local
}

func (r *Reader) Sum(xs []int) int { // want `pointer receiver on Reader should be a value receiver`
	total := 0
	i := 0
	for i = range xs {
		total += xs[i]
	}
	for range xs {
		total++
	}
	return total + r.n
}

// Mutator assigns through its receiver: still flagged, but the rewrite would
// silently mutate a copy, so no fix is attached.
type Mutator struct{ n int }

func (m *Mutator) Set(v int) { m.n = v } // want `pointer receiver on Mutator should be a value receiver`

// point exists to give DeepMutator a nested field chain.
type point struct{ x int }

// DeepMutator assigns through a nested field chain rooted in the receiver; no
// fix.
type DeepMutator struct{ p point }

func (d *DeepMutator) Zero() { d.p.x = 0 } // want `pointer receiver on DeepMutator should be a value receiver; the type holds no field that requires a pointer`

// Indexed assigns through an index reachable from the receiver; conservatively
// no fix.
type Indexed struct{ xs []int }

func (i *Indexed) Set() { i.xs[0] = 1 } // want `pointer receiver on Indexed should be a value receiver; the type holds no field that requires a pointer`

// RangeMutator assigns its receiver's field as the range variable; no fix.
type RangeMutator struct{ i int }

func (r *RangeMutator) Last(xs []int) { // want `pointer receiver on RangeMutator should be a value receiver`
	for r.i = range xs {
		_ = r.i
	}
}

// Escaper takes the address of a field reachable through the receiver (through
// a parenthesized chain); the pointer could outlive the call, so no fix.
type Escaper struct{ n int }

func (e *Escaper) Leak() *int { return &(e.n) } // want `pointer receiver on Escaper should be a value receiver; the type holds no field that requires a pointer`

// SelfEscaper uses its receiver bare — the identifier's pointer-typed semantics
// would change (here the body would not even compile as a value receiver), so
// no fix.
type SelfEscaper struct{ n int }

func (s *SelfEscaper) Self() *SelfEscaper { return s } // want `pointer receiver on SelfEscaper should be a value receiver; the type holds no field that requires a pointer`

// Chained calls a pointer-receiver method ON ITS OWN receiver; the callee may
// mutate, so Touch gets no fix even though its own body assigns nothing. bump
// mutates directly, so it gets no fix either.
type Chained struct{ n int }

func (c *Chained) bump() { c.n++ } // want `pointer receiver on Chained should be a value receiver; the type holds no field that requires a pointer`

func (c *Chained) Touch() { c.bump() } // want `pointer receiver on Chained should be a value receiver; the type holds no field that requires a pointer`

// Delegator calls a pointer-receiver method on a value that is NOT its
// receiver, which leaves its own receiver copy-safe; fixable.
type Delegator struct{ n int }

func (d *Delegator) Poke(c *Chained) { c.bump() } // want `pointer receiver on Delegator should be a value receiver`

// Unnamed has no receiver identifier, so nothing in the body can reach the
// receiver; fixable.
type Unnamed struct{ n int }

func (*Unnamed) Touch() {} // want `pointer receiver on Unnamed should be a value receiver; the type holds no field that requires a pointer`

// plainFunc is not a method.
func plainFunc() {}

// Setting implements decode contracts: their pointer receivers are dictated
// by the interfaces (they write INTO the receiver), so they are not reported.
type Setting struct{ v string }

func (s *Setting) UnmarshalYAML(unmarshal func(any) error) error { return unmarshal(&s.v) }

func (s *Setting) UnmarshalText(b []byte) error { s.v = string(b); return nil }

func (s *Setting) Set(v string) error { s.v = v; return nil }

// Get is an ordinary pointer-receiver method on a lock-free type: reported.
func (s *Setting) Get() string { return s.v } // want `pointer receiver`

// MuAlias aliases sync.Mutex; the no-copy check must resolve the alias, so
// AliasField's pointer receiver is allowed.
type MuAlias = sync.Mutex

type AliasField struct{ mu MuAlias }

func (a *AliasField) Touch() { a.mu.Lock(); a.mu.Unlock() }

// UintptrCounter holds a sync/atomic.Uintptr, so a pointer receiver is allowed.
type UintptrCounter struct{ n atomic.Uintptr }

func (u *UintptrCounter) Touch() { u.n.Add(1) }

// noCopy is the vet copylocks marker idiom: a zero-size type whose pointer
// method set has nullary Lock and Unlock. Satisfying that shape requires the
// pointer receivers, so its own methods are exempt.
type noCopy struct{}

func (n *noCopy) Lock() {}

func (n *noCopy) Unlock() {}

// Marked holds a noCopy marker field, so its pointer receiver is allowed.
type Marked struct {
	_ noCopy
	n int
}

func (m *Marked) Bump() { m.n++ }

// ValueLocker's Lock/Unlock take VALUE receivers, so the value itself is a
// Locker and copying it is fine (vet copylocks agrees); its pointer-receiver
// method stays flagged and fixable.
type ValueLocker struct{}

func (ValueLocker) Lock() {}

func (ValueLocker) Unlock() {}

func (v *ValueLocker) Touch() {} // want `pointer receiver on ValueLocker should be a value receiver`

// Outer calls a pointer-receiver method on its FIELD, one selector level deep;
// the rewrite would make bump mutate a copy of the field, so no fix.
type Outer struct{ c Chained }

func (o *Outer) Bump() { o.c.bump() } // want `pointer receiver on Outer should be a value receiver`

// Slots reaches a pointer-receiver method through an index expression; no fix.
type Slots struct{ cs []Chained }

func (s *Slots) Poke() { s.cs[0].bump() } // want `pointer receiver on Slots should be a value receiver`

// Cell gives Grid and IdxEsc a value-receiver method at chain depth.
type Cell struct{ n int }

func (c Cell) Get() int { return c.n }

// Grid chains through a safe index expression to a value-receiver method;
// nothing can mutate the receiver, so the fix is attached.
type Grid struct {
	cells []Cell
	k     int
}

func (g *Grid) First() int { return g.cells[g.k].Get() } // want `pointer receiver on Grid should be a value receiver`

// deref lets IdxEsc leak an address from inside an index expression.
func deref(p *int) int { return *p }

// IdxEsc's chain links are safe, but the INDEX expression takes the address of
// a receiver field; the pointer could observe mutation, so no fix.
type IdxEsc struct {
	cells []Cell
	k     int
}

func (e *IdxEsc) Grab() int { return e.cells[deref(&e.k)].Get() } // want `pointer receiver on IdxEsc should be a value receiver`

// Deref reads a field through an explicit receiver deref, which would not
// compile after the rewrite; no fix.
type Deref struct{ n int }

func (d *Deref) Val() int { return (*d).n } // want `pointer receiver on Deref should be a value receiver`

// TwoErr's Set has a well-known decoder NAME but not the contract shape: one
// result group declaring TWO error results is not "the sole result is error",
// so it stays reported.
type TwoErr struct{ v string }

func (t *TwoErr) Set(v string) (a, b error) { t.v = v; return nil, nil } // want `pointer receiver on TwoErr`
