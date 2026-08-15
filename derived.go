package ptrrecv

import "go/types"

// Reaching a type written over ANOTHER package's — `type MyBuf bytes.Buffer`.
// Such a type takes the original's representation and leaves every method
// behind, so the method set copy.go reads has nothing to say about it, and the
// analyzer told authors to copy a buffered writer one `type` keyword away from
// the field it correctly refused.

// foreignRepresentation reports whether t is another package's type under a
// local name — `type MyBuf bytes.Buffer` — that must not be copied. Such a type
// takes the original's representation and leaves every method behind, so the
// method set has nothing to read and stdlibNoCopy cannot see it.
//
// The question is answered by finding the ORIGINAL and asking it, not by
// treating a hidden field as a proxy for machinery. Nothing structural
// distinguishes `type MyBuf bytes.Buffer` from `type Timestamp time.Time` — the
// canonical idiom for custom marshalling — and the first is machinery while the
// second is a value with 49 value methods. Only a type declared HERE is asked: a
// foreign type's own struct always holds its own fields, and whether THAT one
// may be copied is the method set's question — which copy.go asks first, so no
// input discriminates this guard: it bounds the search to the only shape that
// can be a definition-over, and it is recorded as subsumed rather than defended
// by a case no corpus could write.
func (j judgement) foreignRepresentation(t types.Type) bool {
	named, ok := types.Unalias(t).(*types.Named)
	if !ok || named.Obj().Pkg() != j.own {
		return false
	}
	origin, isDefinedOver := j.originOf(named.Underlying())
	return isDefinedOver && j.stdlibNoCopy(origin)
}

// originOf returns the standard-library type a local definition was written
// over. A struct carrying an unexported field declared in another package can
// only have been copied from that package's own type, so the field names the
// package and the package's scope names the type: whichever of its types has
// this very struct as its underlying.
func (j judgement) originOf(under types.Type) (types.Type, bool) {
	st, isStruct := under.(*types.Struct)
	if !isStruct {
		return nil, false
	}
	pkg := j.foreignFieldPackage(st)
	if pkg == nil {
		return nil, false
	}
	return typeWithUnderlying(pkg, st)
}

// foreignFieldPackage returns the standard-library package a struct's fields
// were declared in, or nil when every field is this package's own. A field
// declared elsewhere is only reachable by writing a type over that package's
// type, which is the whole shape this file exists to find.
func (j judgement) foreignFieldPackage(st *types.Struct) *types.Package {
	for i := range st.NumFields() {
		if field := st.Field(i); j.isForeignField(field) {
			return field.Pkg()
		}
	}
	return nil
}

// typeWithUnderlying finds the type in pkg whose underlying type is st, which is
// the type a local definition was written over.
func typeWithUnderlying(pkg *types.Package, st *types.Struct) (types.Type, bool) {
	scope := pkg.Scope()
	for _, name := range scope.Names() {
		named, isNamed := scope.Lookup(name).Type().(*types.Named)
		if isNamed && types.Identical(named.Underlying(), st) {
			return named, true
		}
	}
	return nil, false
}

// isForeignField reports whether a struct field was declared in a
// standard-library package other than the one under analysis.
//
// It used to require the field to be UNEXPORTED, on the theory that a hidden
// field is what a local definition cannot have written itself. That decided
// nothing: measured by deleting the condition, every fixture and every probe
// reported identically, because a struct whose fields belong to another package
// AT ALL can only be a definition over that package's type, and the original —
// not the field — is what answers the copy question. go/token.Position, whose
// fields are all exported, is still judged, because token.Position has value
// methods.
func (j judgement) isForeignField(field *types.Var) bool {
	pkg := field.Pkg()
	return pkg != nil && pkg != j.own && j.isStdlib(packagePath(pkg.Path()))
}
