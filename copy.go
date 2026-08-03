package ptrrecv

import (
	"go/token"
	"go/types"
)

// Deciding whether a type must not be copied, which is what makes a pointer
// receiver justified. The --fix path rewrites receivers this file judges
// unjustified into VALUE receivers, so a false negative here does not produce a
// wrong diagnostic — it hands the compiler permission to copy a mutex, which is
// a data race the author never wrote.

// noCopyTypes are the standard-library types whose presence in a struct makes a
// pointer receiver legitimate, because they must not be copied after first use.
var noCopyTypes = map[string]bool{
	"sync.Mutex":          true,
	"sync.RWMutex":        true,
	"sync.WaitGroup":      true,
	"sync.Once":           true,
	"sync.Pool":           true,
	"sync.Map":            true,
	"sync.Cond":           true,
	"sync/atomic.Int32":   true,
	"sync/atomic.Int64":   true,
	"sync/atomic.Uint32":  true,
	"sync/atomic.Uint64":  true,
	"sync/atomic.Bool":    true,
	"sync/atomic.Uintptr": true,
	"sync/atomic.Pointer": true,
	"sync/atomic.Value":   true,
	"bytes.Buffer":        true,
	"strings.Builder":     true,
}

// allowExtra is the configurable allow-list of additional fully-qualified
// no-copy types (pkgpath.Name), set via the -allow flag or analyzer config.
var allowExtra string

// requiresPointer reports whether t must not be copied: it is itself a no-copy
// type — allow-listed or satisfying the vet copylocks locker shape — a type
// parameter (whose instantiation may be any of those), or it transitively
// holds one through struct fields and array elements.
func requiresPointer(allow map[string]bool, t types.Type) bool {
	if isNoCopy(allow, t) || lockerShape(t) || isTypeParam(t) {
		return true
	}
	return componentsRequirePointer(allow, t)
}

// isTypeParam reports whether t is a type parameter. An unconstrained (or
// merely unknowable) type parameter may be instantiated with no-copy machinery
// — Box[sync.Mutex] — and the --fix path would rewrite the receiver into one
// the compiler happily copies, a data race the author never wrote. The pointer
// receiver conservatively stands, the same line valuector draws for
// type-parameter fields in constructed types.
func isTypeParam(t types.Type) bool {
	_, ok := types.Unalias(t).(*types.TypeParam)
	return ok
}

// componentsRequirePointer descends into the component types whose copy copies
// the component itself: struct fields and array elements (an array stores its
// elements inline, so a no-copy element makes the whole array — and its
// enclosing struct — uncopyable, as go vet copylocks treats it). Slices, maps,
// channels, and pointers are deliberately not walked: they are references whose
// copy duplicates only the header/pointer, never the pointee, so they leave the
// type copyable.
func componentsRequirePointer(allow map[string]bool, t types.Type) bool {
	switch u := t.Underlying().(type) {
	case *types.Struct:
		return anyFieldRequiresPointer(allow, u)
	case *types.Array:
		return requiresPointer(allow, u.Elem())
	}
	return false
}

// anyFieldRequiresPointer reports whether any field of st is a no-copy type or
// transitively holds one.
func anyFieldRequiresPointer(allow map[string]bool, st *types.Struct) bool {
	for i := range st.NumFields() {
		if requiresPointer(allow, st.Field(i).Type()) {
			return true
		}
	}
	return false
}

// isNoCopy reports whether ft names an allow-listed no-copy type. The type is
// unaliased first so an aliased primitive (`type MuAlias = sync.Mutex`) is
// still recognized instead of falling through to its plain underlying struct.
func isNoCopy(allow map[string]bool, ft types.Type) bool {
	named, ok := types.Unalias(ft).(*types.Named)
	if !ok || named.Obj().Pkg() == nil {
		return false
	}
	return allow[named.Obj().Pkg().Path()+"."+named.Obj().Name()]
}

// lockerIface is the sync.Locker shape — nullary Lock and Unlock — built
// structurally so the check needs no import of sync's export data.
var lockerIface = newLockerIface()

func newLockerIface() *types.Interface {
	nullary := types.NewSignatureType(nil, nil, nil, nil, nil, false)
	iface := types.NewInterfaceType([]*types.Func{
		types.NewFunc(token.NoPos, nil, "Lock", nullary),
		types.NewFunc(token.NoPos, nil, "Unlock", nullary),
	}, nil)
	iface.Complete()
	return iface
}

// lockerShape reports whether t is uncopyable by the vet copylocks criterion:
// its POINTER method set satisfies the Locker shape while its value method set
// does not — the `type noCopy struct{}` marker idiom and the sync primitives
// themselves. Satisfying the shape requires the pointer receivers, so such a
// type's methods (Lock/Unlock included) are legitimate. A type whose VALUE is
// already a Locker is freely copyable and stays flagged.
func lockerShape(t types.Type) bool {
	return types.Implements(types.NewPointer(t), lockerIface) && !types.Implements(t, lockerIface)
}
