package a

import (
	"bufio"
	"bytes"
	"fmt"
	"go/token"
	"math/rand"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Plain holds no no-copy field and its method only reads, so the pointer is
// unjustified and the rewrite is safe.
type Plain struct {
	count int
	err   error
}

func (p *Plain) Get() int { return p.count } // want `pointer receiver on Plain should be a value receiver; the type holds no field that requires a pointer and the method never writes through it`

// Inc writes through its receiver: the pointer is what makes the write visible,
// which the immutability standard sanctions as mutation-as-contract, so it is
// not reported. Rewriting it would compile and silently mutate a copy.
func (p *Plain) Inc() { p.count++ }

// Value receivers are always allowed.
func (p Plain) Err() error { return p.err }

// Guarded holds a sync.Mutex, whose method set is entirely pointer-based, so a
// pointer receiver is allowed even for a method that only reads.
type Guarded struct {
	mu   sync.Mutex
	data int
}

func (g *Guarded) Get() int { return g.data }

// RWGuarded holds a sync.RWMutex.
type RWGuarded struct{ mu sync.RWMutex }

func (g *RWGuarded) Peek() bool { return g.mu.TryLock() }

// Waiter holds a sync.WaitGroup.
type Waiter struct {
	wg sync.WaitGroup
	n  int
}

func (w *Waiter) N() int { return w.n }

// Counter holds a sync/atomic.Int64.
type Counter struct {
	n atomic.Int64
	k int
}

func (c *Counter) K() int { return c.k }

// Cache holds a sync/atomic.Value, which carries no noCopy marker of its own —
// its underlying struct is a bare `v any` — so nothing but the pointer-only
// method set makes it uncopyable. Deleting that criterion reports Cache.
type Cache struct {
	v atomic.Value
	k int
}

func (c *Cache) K() int { return c.k }

// Buffered holds a bytes.Buffer.
type Buffered struct {
	buf bytes.Buffer
	n   int
}

func (b *Buffered) N() int { return b.n }

// Built holds a strings.Builder.
type Built struct {
	sb strings.Builder
	n  int
}

func (b *Built) N() int { return b.n }

// Writing holds a bufio.Writer. No hand-maintained list ever named it, and the
// analyzer reported it AND rewrote it to a value receiver, so every call copied
// the byte count and the error while both copies shared one backing array.
type Writing struct {
	w bufio.Writer
	n int
}

func (w *Writing) N() int { return w.n }

// Reading holds a bufio.Reader.
type Reading struct {
	r bufio.Reader
	n int
}

func (r *Reading) N() int { return r.n }

// Scanning holds a bufio.Scanner.
type Scanning struct {
	s bufio.Scanner
	n int
}

func (s *Scanning) N() int { return s.n }

// Random holds a math/rand.Rand.
type Random struct {
	r rand.Rand
	n int
}

func (r *Random) N() int { return r.n }

// OverBuf is DEFINED OVER bytes.Buffer: it has the Buffer's representation and
// none of its methods, so the pointer-only-API criterion cannot see it and only
// the foreign representation can. Copying it copies a buffered writer, which is
// the same data loss under another name.
type OverBuf bytes.Buffer

func (o *OverBuf) Zero() int { return 0 }

// OverHolder holds one inline.
type OverHolder struct {
	b OverBuf
	n int
}

func (o *OverHolder) N() int { return o.n }

// OverPosition is defined over a standard-library struct whose fields are all
// EXPORTED, so nothing was hidden and nothing is uncopyable: it stays reported.
type OverPosition token.Position

func (o *OverPosition) N() int { return o.Line } // want `pointer receiver on OverPosition should be a value receiver`

// Keyed slices an ARRAY reached through its receiver, which is an implicit
// address-of: the slice aliases the array's own storage, so a value receiver
// would alias a COPY and every write through it would land nowhere. Both
// methods below become silent no-ops after the rewrite, and go vet says nothing
// — measured on image/gif's own decoder, whose test suite fails once
// (*decoder).readBlock takes a value receiver.
type Keyed struct {
	bytes [4]byte
	n     int
}

func (k *Keyed) Fill(src []byte) { copy(k.bytes[:], src) }

func (k *Keyed) Zero() {
	s := k.bytes[:]
	for i := range s {
		s[i] = 0
	}
}

// Held slices a SLICE field, whose header is copied either way and whose
// backing array both copies share, so the rewrite is safe and it is reported.
// Slicing something that is not the receiver is safe whatever its type.
type Held struct {
	xs   []byte
	arr  [4]byte
	name string
}

func (h *Held) Head(other [4]byte) []byte { // want `pointer receiver on Held should be a value receiver`
	_ = other[:]
	return h.xs[:1]
}

// Stamp is DEFINED OVER time.Time — the canonical idiom for custom marshalling
// — so a hidden field is no proof of machinery: the original has 49 value
// methods and is copied everywhere, and Stamp stays reported.
type Stamp time.Time

func (s *Stamp) Zero() int { return 0 } // want `pointer receiver on Stamp should be a value receiver`

// Verb implements fmt.Scanner, whose Scan writes into the receiver exactly as
// sql.Scanner's does and shares nothing with it but the name.
type Verb struct{ v string }

func (v *Verb) Scan(state fmt.ScanState, verb rune) error { return nil }

// Timed holds a time.Time, which is the boundary of the pointer-only-API
// criterion: time.Time has 49 value methods, so it is an ordinary copyable
// value and Timed's read-only pointer receiver is reported.
type Timed struct {
	at time.Time
	n  int
}

func (t *Timed) N() int { return t.n } // want `pointer receiver on Timed should be a value receiver`

// localAPI is declared in the package under analysis, whose import path in a
// GOPATH-style fixture ("a") carries no dot and therefore looks exactly like a
// standard-library one. It has a pointer-only method set, so only the
// package-under-analysis exclusion keeps LocalHolder judged.
type localAPI struct{ n int }

func (l *localAPI) Bump() { l.n++ }

type LocalHolder struct {
	l localAPI
	n int
}

func (h *LocalHolder) N() int { return h.n } // want `pointer receiver on LocalHolder should be a value receiver`

// Embedded directly embeds a sync.Mutex (anonymous field).
type Embedded struct {
	sync.Mutex
	data int
}

func (e *Embedded) Data() int { return e.data }

// Nested transitively contains a Mutex through Guarded.
type Nested struct {
	inner Guarded
	n     int
}

func (n *Nested) N() int { return n.n }

// ArrGuarded holds an array of Mutex. An array stores its elements inline, so
// the struct cannot be copied (go vet copylocks agrees).
type ArrGuarded struct {
	mus [3]sync.Mutex
	n   int
}

func (a *ArrGuarded) N() int { return a.n }

// SliceGuarded holds a slice of Mutex. A slice is a reference: copying the
// struct copies only the slice header, not the mutexes, so the struct is
// copyable and the pointer receiver is reported. Maps, channels and pointers
// are the same shape and are not walked either.
type SliceGuarded struct {
	mus  []sync.Mutex
	m    map[string]sync.Mutex
	ch   chan sync.Mutex
	ptr  *sync.Mutex
	name string
}

func (s *SliceGuarded) Name() string { return s.name } // want `pointer receiver on SliceGuarded should be a value receiver`

// Scalar is a non-struct named type.
type Scalar int

func (s *Scalar) Zero() int { return 0 } // want `pointer receiver on Scalar should be a value receiver`

// Box holds a type-parameter field, which may be instantiated with a no-copy
// type (Box[sync.Mutex] must not be copied), so its pointer receiver
// conservatively stands.
type Box[T any] struct{ v T }

func (b *Box[T]) Len() int { return 0 }

// GenericFree is generic but stores no type-parameter field, so every
// instantiation is copyable and its pointer-receiver method stays reported.
type GenericFree[T any] struct{ n int }

func (g *GenericFree[T]) Peek() int { return g.n } // want `pointer receiver on GenericFree should be a value receiver`

// Wrapped reaches a type parameter TRANSITIVELY through a generic field.
type Wrapped[T any] struct{ b Box[T] }

func (w *Wrapped[T]) Len() int { return 0 }

// GuardedBox is a generic type holding a sync.Mutex.
type GuardedBox[T any] struct {
	mu sync.Mutex
	v  T
	n  int
}

func (g *GuardedBox[T]) N() int { return g.n }

// AliasInner is a plain struct with no no-copy field. AliasPlain aliases it, and
// a pointer-receiver method declared through the alias resolves to *types.Alias
// (Go 1.23+); it must be unaliased to the underlying named type rather than
// crashing, and the diagnostic names that underlying type.
type AliasInner struct{ x int }

type AliasPlain = AliasInner

func (a *AliasPlain) X() int { return a.x } // want `pointer receiver on AliasInner should be a value receiver`

// BufAlias aliases a no-copy type that carries NO Locker shape and no noCopy
// marker field, so nothing but the alias resolution can see through it: with
// the Unalias removed, AliasField is reported.
type BufAlias = bytes.Buffer

type AliasField struct {
	buf BufAlias
	n   int
}

func (a *AliasField) N() int { return a.n }

// Reader never writes through its receiver — it only reads fields, calls a
// value-receiver method, and mutates locals — so it is reported and fixed.
type Reader struct{ n int }

func (r Reader) get() int { return r.n }

func (r *Reader) Len() int { // want `pointer receiver on Reader should be a value receiver`
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

// Mutator assigns through its receiver, which is the standard's second
// justification: the pointer is what the method's contract needs, so nothing is
// reported and no rewrite is offered.
type Mutator struct{ n int }

func (m *Mutator) Set(v int) { m.n = v }

// point exists to give DeepMutator a nested field chain.
type point struct{ x int }

// DeepMutator assigns through a nested field chain rooted in the receiver.
type DeepMutator struct{ p point }

func (d *DeepMutator) Zero() { d.p.x = 0 }

// Indexed assigns through an index reachable from the receiver.
type Indexed struct{ xs []int }

func (i *Indexed) Set() { i.xs[0] = 1 }

// Bumper increments through its receiver.
type Bumper struct{ n int }

func (b *Bumper) Bump() { b.n++ }

// RangeMutator assigns its receiver's field as the range variable.
type RangeMutator struct{ i int }

func (r *RangeMutator) Last(xs []int) {
	for r.i = range xs {
		_ = r.i
	}
}

// Escaper takes the address of a field reachable through the receiver (through
// a parenthesized chain); the pointer could outlive the call and observe
// mutation, so the rewrite is not behaviour-preserving.
type Escaper struct{ n int }

func (e *Escaper) Leak() *int { return &(e.n) }

// SelfEscaper uses its receiver bare — the identifier's pointer-typed semantics
// would change, and here the body would not even compile as a value receiver.
type SelfEscaper struct{ n int }

func (s *SelfEscaper) Self() *SelfEscaper { return s }

// Chained calls a pointer-receiver method ON ITS OWN receiver; the callee may
// mutate, so the rewrite is unsafe for Touch as well as for bump.
type Chained struct{ n int }

func (c *Chained) bump() { c.n++ }

func (c *Chained) Touch() { c.bump() }

// Delegator calls a pointer-receiver method on a value that is NOT its
// receiver, which leaves its own receiver copy-safe.
type Delegator struct{ n int }

func (d *Delegator) Poke(c *Chained) { c.bump() } // want `pointer receiver on Delegator should be a value receiver`

// Unnamed has no receiver identifier, so nothing in the body can reach the
// receiver.
type Unnamed struct{ n int }

func (*Unnamed) Touch() {} // want `pointer receiver on Unnamed should be a value receiver`

// plainFunc is not a method.
func plainFunc() {}

// Setting implements decode contracts with the parameter lists their interfaces
// dictate, so their pointer receivers are not reported.
type Setting struct{ v string }

func (s *Setting) UnmarshalYAML(unmarshal func(any) error) error { return unmarshal(&s.v) }

func (s *Setting) UnmarshalText(b []byte) error { return nil }

func (s *Setting) UnmarshalJSON(b []byte) error { return nil }

func (s *Setting) UnmarshalBinary(b []byte) error { return nil }

func (s *Setting) GobDecode(b []byte) error { return nil }

func (s *Setting) UnmarshalTOML(v any) error { return nil }

func (s *Setting) Scan(src any) error { return nil }

func (s *Setting) Set(v string) error { return nil }

// Get is an ordinary pointer-receiver method on a lock-free type: reported.
func (s *Setting) Get() string { return s.v } // want `pointer receiver on Setting should be a value receiver`

// Forged carries the decode NAMES with parameter lists no decode interface
// declares. go vet says so out loud for two of them — "method UnmarshalJSON()
// error should have signature UnmarshalJSON([]byte) error" — so exempting them
// would let any author silence this rule by picking a name and returning one
// error, which costs nothing and acquires none of the property the exemption
// exists for.
type Forged struct{ v string }

func (f *Forged) Set() error { return nil } // want `pointer receiver on Forged should be a value receiver`

func (f *Forged) Scan(a, b, c int) error { return nil } // want `pointer receiver on Forged should be a value receiver`

func (f *Forged) UnmarshalText(s string) error { return nil } // want `pointer receiver on Forged should be a value receiver`

func (f *Forged) UnmarshalJSON(b []int) error { return nil } // want `pointer receiver on Forged should be a value receiver`

func (f *Forged) UnmarshalTOML(v any, extra int) error { return nil } // want `pointer receiver on Forged should be a value receiver`

func (f *Forged) GobDecode(b ...byte) error { return nil } // want `pointer receiver on Forged should be a value receiver`

// TwoErr's Set has a well-known decoder NAME and the right parameter, but one
// result group declaring TWO error results is not "the sole result is error".
type TwoErr struct{ v string }

func (t *TwoErr) Set(v string) (a, b error) { return nil, nil } // want `pointer receiver on TwoErr`

// NoErr's Set is an ordinary setter with the decode name and no error result.
type NoErr struct{ v string }

func (n *NoErr) Set(v string) string { return n.v } // want `pointer receiver on NoErr`

// ErrAlias aliases the BUILTIN error. A decode method returning it satisfies
// the contract semantically although the result type prints as "a.E", which is
// what makes the check semantic rather than textual.
type E = error

type ErrAlias struct{ v string }

func (e *ErrAlias) Set(v string) E { return nil }

// UintptrCounter holds a sync/atomic.Uintptr.
type UintptrCounter struct {
	n atomic.Uintptr
	k int
}

func (u *UintptrCounter) K() int { return u.k }

// noCopy is the vet copylocks marker idiom: a zero-size type whose pointer
// method set has nullary Lock and Unlock. Satisfying that shape requires the
// pointer receivers, so its own methods are exempt.
type noCopy struct{}

func (n *noCopy) Lock() {}

func (n *noCopy) Unlock() {}

// Marked holds a noCopy marker field.
type Marked struct {
	_ noCopy
	n int
}

func (m *Marked) N() int { return m.n }

// FakeLock is the FORGERY of the Locker shape, and the exemption is declared
// forgeable by design: two empty methods on an ordinary struct silence this
// rule for the type and for everything holding it. The silence is asserted
// rather than fixed because the forgery acquires the property — go vet then
// refuses every copy of FakeLock and of Holding, at every value receiver and
// every assignment, so the marker costs copyability rather than nothing.
type FakeLock struct{ n int }

func (f *FakeLock) Lock() {}

func (f *FakeLock) Unlock() {}

func (f *FakeLock) N() int { return f.n }

// Holding merely holds a forged lock and is silenced transitively.
type Holding struct {
	f FakeLock
	n int
}

func (h *Holding) N() int { return h.n }

// Honest is Holding without the two decoy methods on its field's type, and is
// reported at the same shape.
type honestInner struct{ n int }

type Honest struct {
	f honestInner
	n int
}

func (h *Honest) N() int { return h.n } // want `pointer receiver on Honest should be a value receiver`

// ValueLocker's Lock/Unlock take VALUE receivers, so the value itself is a
// Locker and copying it is fine (vet copylocks agrees); its read-only
// pointer-receiver method stays reported.
type ValueLocker struct{ n int }

func (ValueLocker) Lock() {}

func (ValueLocker) Unlock() {}

func (v *ValueLocker) N() int { return v.n } // want `pointer receiver on ValueLocker should be a value receiver`

// Outer calls a pointer-receiver method on its FIELD, one selector level deep;
// the rewrite would make bump mutate a copy of the field.
type Outer struct{ c Chained }

func (o *Outer) Bump() { o.c.bump() }

// Slots reaches a pointer-receiver method through an index expression.
type Slots struct{ cs []Chained }

func (s *Slots) Poke() { s.cs[0].bump() }

// Cell gives Grid, IdxEsc and Parens a value-receiver method at chain depth.
type Cell struct{ n int }

func (c Cell) Get() int { return c.n }

// Grid chains through a safe index expression to a value-receiver method;
// nothing can mutate the receiver.
type Grid struct {
	cells []Cell
	k     int
}

func (g *Grid) First() int { return g.cells[g.k].Get() } // want `pointer receiver on Grid should be a value receiver`

// Parens reaches a value-receiver method through a PARENTHESIZED
// receiver-rooted chain. Without the parenthesis link the chain is judged
// unsafe, the rewrite is withheld, and — because this analyzer reports exactly
// what it can rewrite — Parens is not reported at all.
type Parens struct{ c Cell }

func (p *Parens) First() int { return (p.c).Get() } // want `pointer receiver on Parens should be a value receiver`

// deref lets IdxEsc leak an address from inside an index expression.
func deref(p *int) int { return *p }

// IdxEsc's chain links are safe, but the INDEX expression takes the address of
// a receiver field; the pointer could observe mutation.
type IdxEsc struct {
	cells []Cell
	k     int
}

func (e *IdxEsc) Grab() int { return e.cells[deref(&e.k)].Get() }

// Deref reads a field through an explicit receiver deref, which would not
// compile after the rewrite.
type Deref struct{ n int }

func (d *Deref) Val() int { return (*d).n }
