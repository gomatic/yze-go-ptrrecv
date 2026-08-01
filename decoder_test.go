package ptrrecv

// White-box tests for the decode/bind exemption. These methods legitimately
// need a pointer receiver because the interface writes INTO the receiver, so
// the exemption must key on the CONTRACT and not merely on a familiar name.

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

// TestDecoderMethodRequiresTheContractShapeNotJustTheName names decoderMethod's
// claim. The exemption exists because an interface like sql.Scanner writes INTO
// the receiver, so a value receiver cannot implement it — but the name alone is
// not the contract. An ordinary setter called Set, or a method returning two
// errors, is not implementing any decode interface and must stay reported;
// exempting it would leave a genuinely unjustified pointer receiver in place
// forever because it happened to pick a well-known name.
func TestDecoderMethodRequiresTheContractShapeNotJustTheName(t *testing.T) {
	t.Parallel()

	pass, file := checkedPass(t, `package p
type T struct{ n int }
func (t *T) UnmarshalText(b []byte) error { return nil }
func (t *T) Scan(src any) error { return nil }
func (t *T) Set(s string) error { return nil }
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
		{method: methodUnmarshalText, want: true, why: "well-known name and exactly one error result"},
		{method: methodScan, want: true, why: "sql.Scanner"},
		{method: methodSet, want: true, why: "flag.Value"},
		{method: "SetNoError", want: false, why: "not a well-known name"},
		{method: "SetTwoErrors", want: false, why: "a multi-error signature is no decode contract"},
		{method: "SetWrongResult", want: false, why: "one result, but not an error"},
		{method: "Ordinary", want: false, why: "an error result alone exempts nothing"},
	} {
		assert.Equal(t, tc.want, decoderMethod(pass, methodNamed(t, file, tc.method)),
			"decoderMethod(%s): %s", tc.method, tc.why)
	}
}

// TestDecoderNamesCoversTheStandardDecodeInterfaces names decoderNames' claim:
// the exemption applies exactly to the interfaces whose contract dictates a
// pointer receiver. A name missing here means the analyzer tells an author to
// break encoding/json; a name wrongly present exempts a method that should have
// been flagged.
func TestDecoderNamesCoversTheStandardDecodeInterfaces(t *testing.T) {
	t.Parallel()

	want := map[string]bool{
		"UnmarshalJSON": true, "UnmarshalYAML": true, methodUnmarshalText: true,
		"UnmarshalBinary": true, "UnmarshalXML": true, "UnmarshalTOML": true,
		"GobDecode": true, methodScan: true, methodSet: true,
	}
	assert.Equal(t, want, decoderNames,
		"the exemption must name exactly the standard decode/bind interfaces — "+
			"a missing name tells an author to break encoding/json, an extra one "+
			"exempts a method that should have been flagged")

	for _, reader := range []string{"MarshalJSON", "String", "Error"} {
		assert.False(t, decoderNames[reader],
			"%s READS the receiver, so it needs no pointer and earns no exemption", reader)
	}
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

// The decode-contract method names this file exercises twice — once as an
// expected map entry and once as a table case. Named so the literal has one
// definition in the tests, matching the single definition in decoder.go.
const (
	methodUnmarshalText = "UnmarshalText"
	methodScan          = "Scan"
	methodSet           = "Set"
)
