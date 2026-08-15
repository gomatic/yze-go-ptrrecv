package ptrrecv

import (
	"go/ast"
	"go/types"
	"strings"

	"golang.org/x/tools/go/analysis"
)

// The decode/bind exemption: methods whose pointer receiver is dictated by the
// interface they implement, because the interface writes into the receiver and
// returns only an error.
//
// The PARAMETERS are the contract, as much as the name and the result. A method
// called UnmarshalJSON taking no arguments implements nothing — go vet says so
// out loud ("method UnmarshalJSON() error should have signature
// UnmarshalJSON([]byte) error") — and exempting it would let any author silence
// this rule by picking one of nine names and returning a single error, which
// costs nothing and acquires none of the property the exemption exists for.

// paramCheck reports whether one parameter has the type its contract dictates.
type paramCheck func(types.Type) bool

// contract reports whether a parameter list is the one its interface dictates.
type contract func(*types.Tuple) bool

// decoderContracts maps each well-known decode/bind method name to the
// parameter list its interface dictates:
//
//	UnmarshalJSON([]byte) error                            encoding/json.Unmarshaler
//	UnmarshalText([]byte) error                            encoding.TextUnmarshaler
//	UnmarshalBinary([]byte) error                          encoding.BinaryUnmarshaler
//	GobDecode([]byte) error                                encoding/gob.GobDecoder
//	UnmarshalTOML(any) error                               BurntSushi/toml.Unmarshaler
//	Scan(any) error                                        database/sql.Scanner
//	Set(string) error                                      flag.Value
//	UnmarshalXML(*xml.Decoder, xml.StartElement) error     encoding/xml.Unmarshaler
//	UnmarshalYAML(func(any) error) error                   gopkg.in/yaml.v2
//	UnmarshalYAML(*yaml.Node) error                        gopkg.in/yaml.v3
var decoderContracts = map[string]contract{
	methodUnmarshalJSON:   params(isByteSlice),
	methodUnmarshalText:   params(isByteSlice),
	methodUnmarshalBinary: params(isByteSlice),
	methodGobDecode:       params(isByteSlice),
	methodUnmarshalTOML:   params(isAny),
	methodScan:            params(isAny),
	methodSet:             params(isString),
	methodUnmarshalXML:    params(isXMLDecoder, isXMLStartElement),
	methodUnmarshalYAML:   either(params(isUnmarshalFunc), params(isYAMLNode)),
}

// The decode/bind method names, defined once so the map above and the tests
// that judge it read the same literals.
const (
	methodUnmarshalJSON   = "UnmarshalJSON"
	methodUnmarshalText   = "UnmarshalText"
	methodUnmarshalBinary = "UnmarshalBinary"
	methodUnmarshalXML    = "UnmarshalXML"
	methodUnmarshalYAML   = "UnmarshalYAML"
	methodUnmarshalTOML   = "UnmarshalTOML"
	methodGobDecode       = "GobDecode"
	methodScan            = "Scan"
	methodSet             = "Set"
)

// decoderMethod reports whether fn is a decode/bind contract method: a
// well-known name AND the contract shape — the parameter list its interface
// dictates, and exactly one result, of the builtin error interface, verified
// semantically. An ordinary setter that happens to be called Set (no error
// result), a multi-error signature (`(a, b error)`), a result naming a
// package-level `type error` shadow, or a right-named method whose parameters
// implement no interface at all stays reported.
func decoderMethod(pass *analysis.Pass, fn *ast.FuncDecl) bool {
	shape, ok := decoderContracts[fn.Name.Name]
	if !ok {
		return false
	}
	sig := pass.TypesInfo.Defs[fn.Name].Type().(*types.Signature)
	if sig.Variadic() || sig.Results().Len() != 1 || !isBuiltinError(sig.Results().At(0).Type()) {
		return false
	}
	return shape(sig.Params())
}

// params builds a contract requiring exactly one parameter per check, each
// satisfying its own.
func params(checks ...paramCheck) contract {
	return func(list *types.Tuple) bool {
		if list.Len() != len(checks) {
			return false
		}
		for i, check := range checks {
			if !check(list.At(i).Type()) {
				return false
			}
		}
		return true
	}
}

// either builds a contract satisfied by either shape, which one interface name
// serving two published signatures (UnmarshalYAML) needs.
func either(first, second contract) contract {
	return func(list *types.Tuple) bool {
		return first(list) || second(list)
	}
}

// byteSlice is []byte, the argument every Unmarshal*/GobDecode contract takes.
var byteSlice = types.NewSlice(types.Typ[types.Byte])

// isByteSlice reports whether t is []byte exactly. A named `type Bytes []byte`
// is not, and a method taking one implements no decode interface.
func isByteSlice(t types.Type) bool {
	return types.Identical(t, byteSlice)
}

// isString reports whether t is the builtin string, which flag.Value's Set takes.
func isString(t types.Type) bool {
	return types.Identical(t, types.Typ[types.String])
}

// emptyIface is the unnamed empty interface, which is what `any` and
// `interface{}` both denote — and what sql.Scanner's src parameter is.
var emptyIface = newEmptyIface()

func newEmptyIface() *types.Interface {
	iface := types.NewInterfaceType(nil, nil)
	iface.Complete()
	return iface
}

// isAny reports whether t is the unnamed empty interface. A NAMED empty
// interface is a different type, and a method taking one implements no decode
// interface.
func isAny(t types.Type) bool {
	return types.Identical(types.Unalias(t), emptyIface)
}

// isUnmarshalFunc reports whether t is `func(any) error`, the callback
// gopkg.in/yaml.v2's UnmarshalYAML receives.
func isUnmarshalFunc(t types.Type) bool {
	sig, ok := types.Unalias(t).(*types.Signature)
	if !ok || sig.Variadic() || sig.Params().Len() != 1 || sig.Results().Len() != 1 {
		return false
	}
	return isAny(sig.Params().At(0).Type()) && isBuiltinError(sig.Results().At(0).Type())
}

// isYAMLNode reports whether t is a pointer to a yaml package's Node, the
// argument gopkg.in/yaml.v3's UnmarshalYAML receives. The package is matched by
// its final path segment because the YAML decoder is not in the standard
// library and its module path is a version away from stable.
func isYAMLNode(t types.Type) bool {
	named, ok := pointee(t)
	if !ok || named.Obj().Name() != "Node" || named.Obj().Pkg() == nil {
		return false
	}
	path := named.Obj().Pkg().Path()
	return strings.HasPrefix(path[strings.LastIndex(path, "/")+1:], "yaml")
}

// isXMLDecoder reports whether t is *encoding/xml.Decoder.
func isXMLDecoder(t types.Type) bool {
	named, ok := pointee(t)
	return ok && isNamedFrom(named, "encoding/xml", "Decoder")
}

// isXMLStartElement reports whether t is encoding/xml.StartElement.
func isXMLStartElement(t types.Type) bool {
	named, ok := types.Unalias(t).(*types.Named)
	return ok && isNamedFrom(named, "encoding/xml", "StartElement")
}

// pointee returns the named type a pointer parameter points at.
func pointee(t types.Type) (*types.Named, bool) {
	ptr, ok := types.Unalias(t).(*types.Pointer)
	if !ok {
		return nil, false
	}
	named, ok := types.Unalias(ptr.Elem()).(*types.Named)
	return named, ok
}

// isNamedFrom reports whether named is the type called name declared in path.
func isNamedFrom(named *types.Named, path packagePath, name typeName) bool {
	pkg := named.Obj().Pkg()
	return pkg != nil && packagePath(pkg.Path()) == path && typeName(named.Obj().Name()) == name
}

// isBuiltinError reports whether t is the universe's error interface itself.
func isBuiltinError(t types.Type) bool {
	return types.Identical(t, types.Universe.Lookup("error").Type())
}
