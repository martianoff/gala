package transformer_test

import (
	"os"
	"path/filepath"
	"testing"

	"martianoff/gala/galaerr"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// privateFieldsFixture is a module whose vault package declares encapsulated
// values — structs with private fields built through a checking constructor —
// with and without a Validate method, and with Validate methods of the wrong
// shape.
func privateFieldsFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	write := func(rel, content string) {
		t.Helper()
		full := filepath.Join(root, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(full), 0755))
		require.NoError(t, os.WriteFile(full, []byte(content), 0644))
	}
	write("gala.mod", "module example.com/vaultx\n\ngala dev\n")
	write("vault/vault.gala", `package vault

import "strings"

// Email can only hold an address with an "@", and checks decoded ones too.
struct Email(v string)

func ParseEmail(s string) Option[Email] = When(strings.Contains(s, "@"), Email(s))

func (e Email) Validate() Try[Email] = FromOption(ParseEmail(e.v))

// Mid has only exported fields, one of them an Email.
struct Mid(Label string, Mail Email)

// Token has private fields and no Validate method.
struct Token(v string)

func NewToken(v string) Token = Token(v)

// Holder has only exported fields, and holds a Token.
struct Holder(Name string, Tok Token)

// Pin's Validate has a pointer receiver.
struct Pin(n int)

func (p *Pin) Validate() Try[Pin] = Success(*p)

// Code's Validate reports an error rather than returning the value.
struct Code(n int)

func (c Code) Validate() error = nil

// Open has only exported fields.
struct Open(Name string)
`)
	return root
}

// TestCodecPrivateFieldsValidate: a struct with private fields is decoded
// through its Validate method, so a value its constructor would refuse is
// refused by the codec too; a struct with only exported fields is decoded as
// before.
func TestCodecPrivateFieldsValidate(t *testing.T) {
	root := privateFieldsFixture(t)
	lib, err := os.ReadFile(filepath.Join(root, "vault", "vault.gala"))
	require.NoError(t, err)
	libOut, err := transpileCrossPkgFile(t, root, string(lib), filepath.Join(root, "vault", "vault.gala"))
	require.NoError(t, err)
	for _, want := range []string{
		// The decoded value goes through Validate; so does an absent Email's
		// empty value.
		"return Email{v: std.NewImmutable(_v)}.Validate().Get()",
		"func (_ StructMeta_Email) Empty() Email {\n\treturn Email{}.Validate().Get()",
		// Mid's absent Mail is built only when it is absent.
		"if !__seen",
		"_Mail = StructMeta_Email{}.Empty()",
		// Token has no Validate: its StructMeta still encodes, but refuses to
		// decode.
		"panic(\"Token has private fields and no `func (t Token) Validate() Try[Token]` method, so it cannot be decoded\")",
		"return Open{Name: std.NewImmutable(_Name)}\n",
	} {
		assert.Contains(t, libOut, want, "generated:\n%s", libOut)
	}
	assert.NotContains(t, libOut, "Open{Name: std.NewImmutable(_Name)}.Validate()", "generated:\n%s", libOut)
	assert.NotContains(t, libOut, "var _Mail Email = StructMeta_Email{}.Empty()", "generated:\n%s", libOut)

	out, err := transpileCrossPkg(t, root, `package main

import (
    . "martianoff/gala/json"
    . "martianoff/gala/collection_immutable"
    "example.com/vaultx/vault"
)

struct Account(
    Main vault.Email,
    Backup Option[vault.Email],
    Others Array[vault.Email],
    Recent List[vault.Email],
    ByLabel HashMap[string, vault.Email],
    Nested vault.Mid,
    Plain vault.Open,
)

func main() {
    val c = Codec[Account](SnakeCase())
    Println(c.Decode("{}").IsFailure())
    Println(Codec[vault.Email](SnakeCase()).Decode("{\"v\":\"junk\"}").IsFailure())
    Println(Value[Array[vault.Email]]().Decode("[]").IsSuccess())
}`)
	require.NoError(t, err)
	assert.Contains(t, out, "vault.StructMeta_Email{}.DecodeFields(r", "generated:\n%s", out)
	assert.Contains(t, out, "_Main = vault.StructMeta_Email{}.Empty()", "generated:\n%s", out)
}

// TestCodecPrivateFieldsWithoutValidate: a codec that would decode a struct
// with private fields and no Validate method is GALA-E0050 at the codec use,
// wherever the struct sits: as the root, a nested field, inside an Option or a
// collection, or under a struct of exported fields.
func TestCodecPrivateFieldsWithoutValidate(t *testing.T) {
	root := privateFieldsFixture(t)
	const tokenReason = "vault.Token has private fields and no Validate method, so decoding it would bypass its constructor"
	for _, tc := range []struct{ name, decl, use, root string }{
		{name: "root", use: `Codec[vault.Token](SnakeCase())`, root: "vault.Token"},
		{name: "nested field", decl: `struct W(T vault.Token)`, use: `Codec[W](SnakeCase())`, root: "W"},
		{name: "option", decl: `struct W(T Option[vault.Token])`, use: `Codec[W](SnakeCase())`, root: "W"},
		{name: "array", decl: `struct W(T Array[vault.Token])`, use: `Codec[W](SnakeCase())`, root: "W"},
		{name: "list", decl: `struct W(T List[vault.Token])`, use: `Codec[W](SnakeCase())`, root: "W"},
		{name: "hashmap", decl: `struct W(T HashMap[string, vault.Token])`, use: `Codec[W](SnakeCase())`, root: "W"},
		{name: "under an exported-field struct", use: `Codec[vault.Holder](SnakeCase())`, root: "vault.Holder"},
		{name: "array root", use: `Codec[vault.Token](SnakeCase()).Array()`, root: "vault.Token"},
		{name: "value root", use: `Value[List[vault.Token]]()`, root: "List[vault.Token]"},
		{name: "StructMeta intrinsic", use: `StructMeta[vault.Token]()`, root: "vault.Token"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, err := transpileCrossPkg(t, root, `package main

import (
    . "martianoff/gala/json"
    . "martianoff/gala/collection_immutable"
    "example.com/vaultx/vault"
)

`+tc.decl+`

func main() {
    val c = `+tc.use+`
    Println(c)
}`)
			require.Error(t, err, out)
			msg := err.Error()
			assert.Contains(t, msg, string(galaerr.CodeUnsupportedCodecField))
			assert.Contains(t, msg, "cannot generate a codec for "+tc.root+": "+tokenReason)
			assert.Contains(t, msg, "make it decodable with a Validate method; declare `func (t Token) Validate() Try[Token]`")
		})
	}
}

// TestCodecPrivateFieldsInMain: the rule holds for a struct of the main
// package too.
func TestCodecPrivateFieldsInMain(t *testing.T) {
	root := privateFieldsFixture(t)
	_, err := transpileCrossPkg(t, root, `package main

import . "martianoff/gala/json"

struct Secret(token string)

func main() {
    Println(Codec[Secret](SnakeCase()).Encode(Secret("x")).Get())
}`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), string(galaerr.CodeUnsupportedCodecField))
	assert.Contains(t, err.Error(), "Secret has private fields and no Validate method")

	out, err := transpileCrossPkg(t, root, `package main

import . "martianoff/gala/json"

struct Secret(token string)

func (s Secret) Validate() Try[Secret] = Success(s)

func main() {
    Println(Codec[Secret](SnakeCase()).Encode(Secret("x")).Get())
}`)
	require.NoError(t, err)
	assert.Contains(t, out, "return Secret{token: std.NewImmutable(_token)}.Validate().Get()", "generated:\n%s", out)
}

// TestCodecValidateWrongSignature: a Validate method decoding cannot call is
// GALA-E0057, naming the signature it needs.
func TestCodecValidateWrongSignature(t *testing.T) {
	root := privateFieldsFixture(t)
	for _, tc := range []struct{ typ, declared string }{
		{"Pin", "func (p *Pin) Validate() Try[Pin]"},
		{"Code", "func (c Code) Validate() error"},
	} {
		t.Run(tc.typ, func(t *testing.T) {
			_, err := transpileCrossPkg(t, root, `package main

import (
    . "martianoff/gala/json"
    "example.com/vaultx/vault"
)

struct W(X vault.`+tc.typ+`)

func main() {
    Println(Codec[W](SnakeCase()))
}`)
			require.Error(t, err)
			msg := err.Error()
			assert.Contains(t, msg, string(galaerr.CodeInvalidValidateSignature))
			assert.Contains(t, msg, "cannot generate a codec for W: vault."+tc.typ+
				" has private fields, and its Validate method is `"+tc.declared+"`")
		})
	}
}

// TestGeneratedCodecNamesRejected: GALA code cannot name the codec metadata
// the transpiler generates, qualified or not, as a use or a declaration.
func TestGeneratedCodecNamesRejected(t *testing.T) {
	root := privateFieldsFixture(t)
	for _, tc := range []struct{ name, body, ident string }{
		{
			name:  "qualified DecodeFields call",
			body:  `func main() { Println(vault.StructMeta_Email{}.NumFields()) }`,
			ident: "StructMeta_Email",
		},
		{
			name:  "qualified type in a val",
			body:  `func main() { val m vault.StructMeta_Email = vault.StructMeta_Email{}; Println(m) }`,
			ident: "StructMeta_Email",
		},
		{
			name:  "main-package metadata",
			body:  "struct P(Name string)\n\nfunc main() { Println(_StructMeta_P{}) }",
			ident: "_StructMeta_P",
		},
		{
			name:  "inside an interpolated string",
			body:  `func main() { Println(s"${vault.StructMeta_Email{}.NumFields()}") }`,
			ident: "StructMeta_Email",
		},
		{
			name:  "declaration",
			body:  "struct StructMeta_X(Name string)\n\nfunc main() { Println(StructMeta_X(\"a\")) }",
			ident: "StructMeta_X",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := transpileCrossPkg(t, root, `package main

import "example.com/vaultx/vault"

`+tc.body+`
`)
			require.Error(t, err)
			assert.Contains(t, err.Error(), string(galaerr.CodeGeneratedCodecName))
			assert.Contains(t, err.Error(), tc.ident+" is codec metadata the transpiler generates")
		})
	}

	// StructMeta[T]() remains the way to reach the metadata, an imported
	// struct's included.
	out, err := transpileCrossPkg(t, root, `package main

import "example.com/vaultx/vault"

func main() {
    val m = StructMeta[vault.Open]()
    Println(m.NumFields())
}`)
	require.NoError(t, err)
	assert.Contains(t, out, "vault.StructMeta_Open{}", "generated:\n%s", out)
}
