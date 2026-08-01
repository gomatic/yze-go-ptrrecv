package ptrrecv

import (
	"go/ast"
	"go/types"

	"golang.org/x/tools/go/analysis"
)

// The decode/bind exemption: methods whose pointer receiver is dictated by the
// interface they implement, because the interface writes into the receiver and
// returns only an error.

// decoderNames are the well-known decode/bind interface methods whose pointer
// receiver is dictated by the contract itself: the interface writes INTO the
// receiver and returns only an error, so a value receiver cannot implement it
// (encoding.TextUnmarshaler, json/yaml/xml Unmarshalers, gob, sql.Scanner,
// flag.Value's Set).
var decoderNames = map[string]bool{
	"UnmarshalJSON":   true,
	"UnmarshalYAML":   true,
	"UnmarshalText":   true,
	"UnmarshalBinary": true,
	"UnmarshalXML":    true,
	"UnmarshalTOML":   true,
	"GobDecode":       true,
	"Scan":            true,
	"Set":             true,
}

// decoderMethod reports whether fn is a decode/bind contract method: a
// well-known name AND the contract shape — exactly one result, of the builtin
// error interface, verified semantically. An ordinary setter that happens to be
// called Set (no error result), a multi-error signature (`(a, b error)`), or a
// result naming a package-level `type error` shadow stays reported.
func decoderMethod(pass *analysis.Pass, fn *ast.FuncDecl) bool {
	if !decoderNames[fn.Name.Name] {
		return false
	}
	sig := pass.TypesInfo.Defs[fn.Name].Type().(*types.Signature)
	return sig.Results().Len() == 1 && isBuiltinError(sig.Results().At(0).Type())
}

// isBuiltinError reports whether t is the universe's error interface itself.
func isBuiltinError(t types.Type) bool {
	return types.Identical(t, types.Universe.Lookup("error").Type())
}
