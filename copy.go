package ptrrecv

import (
	"go/token"
	"go/types"
	"strings"
)

// Deciding whether a type must not be copied, which is what makes a pointer
// receiver justified. The --fix path rewrites receivers this file judges
// unjustified into VALUE receivers, so a false negative here does not produce a
// wrong diagnostic — it hands the compiler permission to copy a mutex, which is
// a data race the author never wrote.
//
// The criterion is DERIVED from the types themselves, never from a table of
// names. A hand-maintained list is short by exactly the amount its author did
// not think of: the list this file used to carry named seventeen types and
// omitted bufio.Writer, bufio.Reader, bufio.Scanner and math/rand.Rand, so the
// analyzer reported a bufio.Writer holder AND rewrote it to a value receiver,
// duplicating the byte count and the error while both copies shared one backing
// array. All seventeen entries are subsumed by stdlibNoCopy below, measured by
// deleting the map and comparing output.

// packagePath is a type's declaring import path, as isNoCopy looks one up.
type packagePath string

// typeName is a type's own name, the half after the final dot.
type typeName string

// judgement is the copy-semantics decision for one package: the configured
// allow-list, and the package under analysis (which is never stdlib, whatever
// its import path looks like).
type judgement struct {
	allow allowSet
	own   *types.Package
}

// requiresPointer reports whether t must not be copied: it is itself an
// uncopyable type — allow-listed, satisfying the vet copylocks locker shape, or
// a standard-library type whose API is entirely pointer-based — a type
// parameter (whose instantiation may be any of those), or it transitively holds
// one through struct fields and array elements.
func (j judgement) requiresPointer(t types.Type) bool {
	if isNoCopy(j.allow, t) || lockerShape(t) || j.stdlibNoCopy(t) || j.foreignRepresentation(t) ||
		isTypeParam(t) {
		return true
	}
	return j.componentsRequirePointer(t)
}

// stdlibNoCopy reports whether t is a standard-library named type that presents
// itself as a thing you hold rather than a thing you copy: every method in its
// method set needs a pointer, and none of them is available on a value.
//
// That is the property, not a proxy for it. bytes.Buffer, strings.Builder,
// bufio.Writer, math/rand.Rand, every sync primitive and every sync/atomic type
// have it; time.Time (49 value methods), time.Duration and os.File do not. The
// exemption is unforgeable by construction — an author cannot add a package to
// the standard library — which is why it is restricted there; everything else
// uncopyable goes through -allow, where it is written down and auditable.
//
// It errs toward silence: net/url.URL and strings.Reader are copyable and are
// exempted anyway. That is the direction this file's header demands — a missed
// diagnostic costs a finding, a wrong one hands the compiler permission to copy
// a mutex.
func (j judgement) stdlibNoCopy(t types.Type) bool {
	named, ok := types.Unalias(t).(*types.Named)
	if !ok || named.Obj().Pkg() == nil || named.Obj().Pkg() == j.own {
		return false
	}
	if !stdlibPath(packagePath(named.Obj().Pkg().Path())) {
		return false
	}
	return types.NewMethodSet(types.NewPointer(named)).Len() > 0 &&
		types.NewMethodSet(named).Len() == 0
}

// foreignRepresentation reports whether t's underlying struct IS another
// package's representation: a field that is unexported and declared in a
// standard-library package is only reachable by DEFINING a type over that
// package's type — `type MyBuf bytes.Buffer` — which copies the machinery and
// leaves none of the methods behind for stdlibNoCopy to read.
//
// Only a type declared HERE is asked: a foreign type's own struct always holds
// its own unexported fields, and whether THAT one may be copied is the method
// set's question, not this one. A struct written here has its own package's
// fields, so nothing ordinary matches; a defined-over type whose fields are all
// EXPORTED (go/token.Position) is copyable and stays judged, because nothing
// about it was hidden in the first place.
func (j judgement) foreignRepresentation(t types.Type) bool {
	named, ok := types.Unalias(t).(*types.Named)
	if !ok || named.Obj().Pkg() != j.own {
		return false
	}
	st, ok := named.Underlying().(*types.Struct)
	if !ok {
		return false
	}
	for i := range st.NumFields() {
		if j.isForeignField(st.Field(i)) {
			return true
		}
	}
	return false
}

// isForeignField reports whether a struct field is unexported and declared in a
// standard-library package other than the one under analysis.
func (j judgement) isForeignField(field *types.Var) bool {
	pkg := field.Pkg()
	return !field.Exported() && pkg != nil && pkg != j.own && stdlibPath(packagePath(pkg.Path()))
}

// stdlibPath reports whether an import path is a standard-library one: its
// first segment carries no dot, because a domain is what every other module
// path begins with. The package under analysis is excluded before this is
// consulted, so a module whose own path happens to look like one (a dotless
// module path, which analysistest fixtures have) is still judged.
func stdlibPath(path packagePath) bool {
	first, _, _ := strings.Cut(string(path), "/")
	return !strings.Contains(first, ".")
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
func (j judgement) componentsRequirePointer(t types.Type) bool {
	switch u := t.Underlying().(type) {
	case *types.Struct:
		return j.anyFieldRequiresPointer(u)
	case *types.Array:
		return j.requiresPointer(u.Elem())
	}
	return false
}

// anyFieldRequiresPointer reports whether any field of st is a no-copy type or
// transitively holds one.
func (j judgement) anyFieldRequiresPointer(st *types.Struct) bool {
	for i := range st.NumFields() {
		if j.requiresPointer(st.Field(i).Type()) {
			return true
		}
	}
	return false
}

// isNoCopy reports whether ft names an allow-listed no-copy type. The type is
// unaliased first so an aliased type (`type MuAlias = other.Thing`) is still
// recognized instead of falling through to its plain underlying struct.
func isNoCopy(allow allowSet, ft types.Type) bool {
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
//
// FORGEABLE BY DESIGN, and the forgery acquires the property. Two empty methods
// on an ordinary struct silence it and every type holding it — and they hand
// the same struct to go vet, which then refuses every copy of it: `go vet` on a
// forged type reports "passes lock by value" at each value receiver, "assignment
// copies lock value" at each assignment, and the same transitively for any
// struct holding one. The marker is not a spelling that costs nothing; taking
// it costs copyability everywhere, enforced by another tool in the same gate.
// So the escape is sanctioned, the corpus asserts the silence, and the two
// Lock/Unlock methods are themselves justified — the value form of either is a
// vet error.
func lockerShape(t types.Type) bool {
	return types.Implements(types.NewPointer(t), lockerIface) && !types.Implements(t, lockerIface)
}
