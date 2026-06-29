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

// plainFunc is not a method.
func plainFunc() {}
