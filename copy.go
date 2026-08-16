package ptrrecv

import (
	"go/token"
	"go/types"
	"strings"

	"golang.org/x/tools/go/analysis"
)

// Deciding whether a type must not be copied, which is what makes a pointer
// receiver justified. A false negative here does not produce a merely noisy
// diagnostic — it tells the author to copy a mutex, which is a data race the
// tool asked for and the author never wrote. So this file errs toward silence,
// and every criterion in it is written in that direction.
//
// The criterion is DERIVED from the types themselves, never from a table of
// names. A hand-maintained list is short by exactly the amount its author did
// not think of: the list this file used to carry named seventeen types and
// omitted bufio.Writer, bufio.Reader, bufio.Scanner and math/rand.Rand, so the
// analyzer reported a bufio.Writer holder and prescribed a value receiver,
// duplicating the byte count and the error while both copies shared one backing
// array. All seventeen entries are subsumed by stdlibNoCopy below, measured by
// deleting the map and comparing output.

// packagePath is a type's declaring import path, as isNoCopy looks one up.
type packagePath string

// typeName is a type's own name, the half after the final dot.
type typeName string

// judgement is the copy-semantics decision for one package: the configured
// allow-list, the package under analysis (which is never stdlib, whatever its
// import path looks like), its module, and the type layout the copy cost in
// size.go is measured against — judgedSizes, never the driver's, so the verdict
// does not move with the platform under analysis.
type judgement struct {
	allow  allowSet
	own    *types.Package
	sizes  types.Sizes
	module packagePath
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
//
// IT DOES NOT APPLY TO A TYPE THE PACKAGE UNDER ANALYSIS DECLARES, and cannot.
// The criterion is "every method takes a pointer", which is the very shape this
// rule reports, so asking it of the package's own types would exempt every type
// all of whose methods are pointer methods — the rule would answer itself.
// Measured by deleting `named.Obj().Pkg() == j.own`: 24 of the fixture's
// expectations go silent, Timed, Scalar, SliceGuarded, Honest, Setting and
// NoErr among them, and the finding count over the Go 1.26.6 standard library
// falls from 1709 locations to 161.
//
// The exclusion has a visible cost and it is stated rather than hidden: when
// the standard library IS the tree under analysis, bytes.Buffer's and
// strings.Builder's own read-only methods are reported — nine locations, on
// the two types the package doc names as exempt. Nothing else reaches it,
// because every other module's own packages are excluded by isStdlib below
// before this criterion is consulted. localAPI/LocalHolder in the fixture is
// the declared-here side of that boundary and Buffered/bytes.Buffer the
// foreign side.
func (j judgement) stdlibNoCopy(t types.Type) bool {
	named, ok := types.Unalias(t).(*types.Named)
	if !ok || named.Obj().Pkg() == nil || named.Obj().Pkg() == j.own {
		return false
	}
	if !j.isStdlib(packagePath(named.Obj().Pkg().Path())) {
		return false
	}
	return types.NewMethodSet(types.NewPointer(named)).Len() > 0 &&
		types.NewMethodSet(named).Len() == 0
}

// isStdlib reports whether a path is the standard library's. It is not enough to
// ask stdlibPath: a module path is a domain by convention and not by rule, and
// `go mod init myapp` produces one that reads exactly like the standard library
// to every package in it but the one under analysis. Excluding the module closes
// a silent, undocumented exemption an author reaches by naming their module.
func (j judgement) isStdlib(path packagePath) bool {
	if j.module != "" && underModule(path, j.module) {
		return false
	}
	return stdlibPath(path)
}

// underModule reports whether an import path is the module's own or inside it.
func underModule(path, module packagePath) bool {
	return path == module || strings.HasPrefix(string(path), string(module)+"/")
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

// modulePath is the path of the module under analysis, or empty when the driver
// reports none — a GOPATH-style fixture has no module at all. The yze driver
// asks for it (go-yze's driver_checker.go requests NeedModule) precisely so an
// analyzer can tell the module's own packages from the standard library's.
func modulePath(pass *analysis.Pass) packagePath {
	if pass.Module == nil {
		return ""
	}
	return packagePath(pass.Module.Path)
}

// isTypeParam reports whether t is a type parameter. An unconstrained (or
// merely unknowable) type parameter may be instantiated with no-copy machinery
// — Box[sync.Mutex] — and the value receiver this rule would prescribe is one
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
// FORGEABLE, AND THE ACQUISITION ARGUMENT DOES NOT COVER IT. Two empty methods
// on an ordinary struct — or one `_ noCopy` field — silence the rule for the
// type and for everything holding it. The defence offered for that was that
// forging the marker hands the type to go vet, which then refuses every copy of
// it, so the marker costs copyability rather than nothing. Measured, that is
// true only where a copy exists: on a struct with a pointer-based API — which is
// exactly the population this rule reports — `go vet` has nothing to say either
// way, and the marker silences three findings at no cost at all.
//
// The criterion stays because it is go vet's own and dropping it would report
// every type carrying the canonical `noCopy` marker, which is a real uncopyable
// idiom. The forgery is an open hole with no discriminator, recorded as a
// finding rather than sanctioned as a design.
func lockerShape(t types.Type) bool {
	return types.Implements(types.NewPointer(t), lockerIface) && !types.Implements(t, lockerIface)
}
