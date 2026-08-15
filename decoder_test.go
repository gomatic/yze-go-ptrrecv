package ptrrecv

// White-box tests for the decode/bind exemption. These methods legitimately
// need a pointer receiver because the interface writes INTO the receiver, so
// the exemption must key on the CONTRACT — name, parameters AND result — and
// not merely on a familiar name. The parameters were no part of the check for
// as long as this analyzer shipped, and go vet disagreed out loud: "method
// UnmarshalJSON() error should have signature UnmarshalJSON([]byte) error".

import (
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/tools/go/analysis"
)

// TestDecoderMethodRequiresTheWholeContractNotJustTheName names decoderMethod's
// claim. The exemption exists because an interface like sql.Scanner writes INTO
// the receiver, so a value receiver cannot implement it — but a name and an
// arity are free. Picking one of nine names and returning a single error must
// not silence the rule: every does-not-apply case below is written against the
// MATCHER (the parameter types it compares) rather than against the sentence
// that describes it.
func TestDecoderMethodRequiresTheWholeContractNotJustTheName(t *testing.T) {
	t.Parallel()

	pass, file := checkedPass(t, `package p
import "encoding/xml"
type Bytes []byte
type Empty interface{}
type T struct{ n int }
func (t *T) UnmarshalText(b []byte) error { return nil }
func (t *T) UnmarshalTextString(s string) error { return nil }
func (t *T) Scan(src any) error { return nil }
func (t *T) Set(s string) error { return nil }
func (t *T) UnmarshalJSON(b []byte) error { return nil }
func (t *T) UnmarshalBinary(b []byte) error { return nil }
func (t *T) GobDecode(b []byte) error { return nil }
func (t *T) UnmarshalTOML(v any) error { return nil }
func (t *T) UnmarshalYAML(unmarshal func(any) error) error { return nil }
func (t *T) UnmarshalXML(d *xml.Decoder, start xml.StartElement) error { return nil }
func (t *T) SetNoError(s string) {}
func (t *T) SetTwoErrors(s string) (error, error) { return nil, nil }
func (t *T) SetWrongResult(s string) string { return "" }
func (t *T) Ordinary(s string) error { return nil }
`)
	for _, tc := range []struct {
		method string
		why    string
		want   bool
	}{
		{method: methodUnmarshalText, want: true, why: "encoding.TextUnmarshaler"},
		{method: methodScan, want: true, why: "sql.Scanner"},
		{method: methodSet, want: true, why: "flag.Value"},
		{method: methodUnmarshalJSON, want: true, why: "json.Unmarshaler"},
		{method: methodUnmarshalBinary, want: true, why: "encoding.BinaryUnmarshaler"},
		{method: methodGobDecode, want: true, why: "gob.GobDecoder"},
		{method: methodUnmarshalTOML, want: true, why: "toml.Unmarshaler"},
		{method: methodUnmarshalYAML, want: true, why: "yaml.v2's callback"},
		{method: methodUnmarshalXML, want: true, why: "xml.Unmarshaler's two arguments"},

		{method: "SetNoError", want: false, why: "not a well-known name"},
		{method: "SetTwoErrors", want: false, why: "a multi-error signature is no decode contract"},
		{method: "SetWrongResult", want: false, why: "one result, but not an error"},
		{method: "Ordinary", want: false, why: "an error result alone exempts nothing"},
	} {
		assert.Equal(t, tc.want, decoderMethod(pass, methodNamed(t, file, tc.method)),
			"decoderMethod(%s): %s", tc.method, tc.why)
	}
}

// TestDecoderMethodRefusesTheRightNameWithTheWrongParameters is the
// does-not-apply half, and it is the whole reason the parameters are checked.
// Each method here carries a decode NAME and a single error result — everything
// the exemption used to require — and implements nothing at all. go vet says so
// for two of them; the other four fail the same way for the same reason.
func TestDecoderMethodRefusesTheRightNameWithTheWrongParameters(t *testing.T) {
	t.Parallel()

	pass, file := checkedPass(t, `package p
import "encoding/xml"
type Bytes []byte
type Empty interface{}
type T struct{ n int }
func (t *T) UnmarshalJSON() error { return nil }
func (t *T) GobDecode(a, b, c string, x int) error { return nil }
func (t *T) Scan(a, b, c int) error { return nil }
func (t *T) Set() error { return nil }
func (t *T) UnmarshalText(s string) error { return nil }
func (t *T) UnmarshalBinary(b Bytes) error { return nil }
func (t *T) UnmarshalTOML(v Empty) error { return nil }
func (t *T) UnmarshalYAML(unmarshal func(any)) error { return nil }
func (t *T) UnmarshalXML(d *xml.Decoder) error { return nil }
`)
	for _, tc := range []struct{ method, why string }{
		{method: methodUnmarshalJSON, why: "no parameter at all decodes nothing"},
		{method: methodGobDecode, why: "four parameters are not one []byte"},
		{method: methodScan, why: "sql.Scanner takes exactly one any"},
		{method: methodSet, why: "flag.Value's Set takes the string it parses"},
		{method: methodUnmarshalText, why: "TextUnmarshaler takes []byte, not string"},
		{method: methodUnmarshalBinary, why: "a NAMED []byte is a different type, and implements nothing"},
		{method: methodUnmarshalTOML, why: "a named empty interface is not any"},
		{method: methodUnmarshalYAML, why: "the callback must return an error"},
		{method: methodUnmarshalXML, why: "xml.Unmarshaler takes the start element too"},
	} {
		assert.False(t, decoderMethod(pass, methodNamed(t, file, tc.method)),
			"decoderMethod(%s) must refuse: %s", tc.method, tc.why)
	}

	varPass, varFile := checkedPass(t, `package p
type T struct{ n int }
func (t *T) GobDecode(b ...byte) error { return nil }
`)
	assert.False(t, decoderMethod(varPass, methodNamed(t, varFile, methodGobDecode)),
		"a variadic ...byte is not []byte, and no decode contract is variadic")
}

// TestDecoderContractsCoversTheStandardDecodeInterfaces names decoderContracts'
// claim: the exemption applies exactly to the interfaces whose contract dictates
// a pointer receiver. A name missing here means the analyzer tells an author to
// break encoding/json; a name wrongly present exempts a method that should have
// been flagged.
func TestDecoderContractsCoversTheStandardDecodeInterfaces(t *testing.T) {
	t.Parallel()

	want := []string{
		methodGobDecode, methodScan, methodSet, methodUnmarshalBinary, methodUnmarshalJSON,
		methodUnmarshalText, methodUnmarshalTOML, methodUnmarshalXML, methodUnmarshalYAML,
	}
	names := make([]string, 0, len(decoderContracts))
	for name := range decoderContracts {
		names = append(names, name)
	}
	assert.ElementsMatch(t, want, names,
		"the exemption must name exactly the standard decode/bind interfaces — "+
			"a missing name tells an author to break encoding/json, an extra one "+
			"exempts a method that should have been flagged")

	for _, reader := range []string{"MarshalJSON", "String", "Error"} {
		assert.NotContains(t, decoderContracts, reader,
			"%s READS the receiver, so it needs no pointer and earns no exemption", reader)
	}
}

// TestIsYAMLNodeMatchesTheDecoderThatIsNotInTheStandardLibrary names the one
// parameter check that cannot be written against a real import: yaml.v3's
// UnmarshalYAML takes *yaml.Node, and the YAML decoder is a module away.
func TestIsYAMLNodeMatchesTheDecoderThatIsNotInTheStandardLibrary(t *testing.T) {
	t.Parallel()

	node := checkedAt(t, "gopkg.in/yaml.v3", "type Node struct{ Kind int }").Scope().Lookup("Node")
	assert.True(t, isYAMLNode(types.NewPointer(node.Type())), "*yaml.Node is the v3 contract")
	assert.False(t, isYAMLNode(node.Type()), "the contract takes a POINTER to it")

	other := checkedAt(t, "example.com/notyaml", "type Node struct{ Kind int }").Scope().Lookup("Node")
	assert.False(t, isYAMLNode(types.NewPointer(other.Type())),
		"a Node from a package that is not a yaml one decodes nothing")

	renamed := checkedAt(t, "gopkg.in/yaml.v3", "type Tree struct{ Kind int }").Scope().Lookup("Tree")
	assert.False(t, isYAMLNode(types.NewPointer(renamed.Type())), "and the type is called Node")
}

// TestParamsAndIsByteSliceCheckTheArgumentThemselves names the two smallest
// pieces of the contract, because each claims something exact that the table
// tests above would still pass without. params requires one argument PER CHECK
// — an arity mismatch is a refusal before any type is compared — and
// isByteSlice requires the unnamed []byte, so a named `type Bytes []byte`,
// which implements no decode interface, is not mistaken for it.
func TestParamsAndIsByteSliceCheckTheArgumentThemselves(t *testing.T) {
	t.Parallel()

	pkg := checkedAt(t, "p", "type Bytes []byte")
	bytes := pkg.Scope().Lookup("Bytes").Type()
	assert.True(t, isByteSlice(types.NewSlice(types.Typ[types.Byte])), "the unnamed []byte is the contract")
	assert.False(t, isByteSlice(bytes), "a NAMED byte slice is a different type")
	assert.False(t, isByteSlice(types.Typ[types.String]), "and a string is not one at all")

	none := types.NewTuple()
	one := types.NewTuple(types.NewParam(0, nil, "b", types.NewSlice(types.Typ[types.Byte])))
	assert.True(t, params(isByteSlice)(one), "one argument, and it is the right one")
	assert.False(t, params(isByteSlice)(none), "no argument is not one argument")
	assert.False(t, params(isByteSlice, isByteSlice)(one), "and one is not two")
	assert.True(t, params()(none), "a contract taking nothing is satisfied by nothing")
}

// checkedPass type-checks src and returns a pass carrying its types.
func checkedPass(t *testing.T, src string) (*analysis.Pass, *ast.File) {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "p.go", src, 0)
	require.NoError(t, err)
	info := &types.Info{Defs: map[*ast.Ident]types.Object{}}
	conf := types.Config{Importer: importer.Default()}
	_, err = conf.Check("example.test/p", fset, []*ast.File{file}, info)
	require.NoError(t, err)
	return &analysis.Pass{TypesInfo: info}, file
}

// methodNamed returns the declaration of the method called name.
func methodNamed(t *testing.T, file *ast.File, name string) *ast.FuncDecl {
	t.Helper()
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if ok && fn.Name.Name == name {
			return fn
		}
	}
	t.Fatalf("no method %s", name)
	return nil
}
