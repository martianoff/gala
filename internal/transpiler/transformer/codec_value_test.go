package transformer_test

import (
	"strings"
	"testing"

	"martianoff/gala/galaerr"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestValueMeta_Emission pins the ValueMeta[T] intrinsic: a generic Apply whose
// only parameter is ValueMeta[T] is called with no arguments (`Value[T]()`),
// gets a generated _ValueMeta_X{} injected, and the generated methods carry
// the same typed dispatch a field of type T gets — including the StructMeta of
// a struct reached inside the value.
func TestValueMeta_Emission(t *testing.T) {
	src := `package main

import (
    . "martianoff/gala/collection_immutable"
    "martianoff/gala/json"
)

type Millis int64

struct User(Id int, Name string)

func main() {
    Println(json.Value[int8]().Decode("1"))
    Println(json.Value[Millis]().Decode("1"))
    Println(json.Value[Array[User]]().Encode(EmptyArray[User]()))
    Println(json.Value[Option[string]]().Decode("null"))
}
`
	got, err := newCodecTestTranspiler().Transpile(src, "value_meta.gala")
	require.NoError(t, err)

	for _, want := range []string{
		// Zero-argument call routed through Apply with the meta injected.
		"json.Value[int8]{}.Apply(_ValueMeta_int8{})",
		"json.Value[Array[User]]{}.Apply(_ValueMeta_Array_User{})",
		// Sized ints are range-checked on decode, exactly as fields are.
		"func (_ _ValueMeta_int8) DecodeValue(r std.FieldDecoder, naming func(string) string) int8",
		"v = int8(r.ReadIntN(8))",
		"w.WriteInt64(int64(v))",
		// An alias classifies as the type it names.
		"func (_ _ValueMeta_Millis) EncodeValue(w std.FieldEncoder, v int64, naming func(string) string)",
		// A struct inside the value gets its StructMeta, and the element
		// dispatch threads the naming function into it.
		"type _StructMeta_User struct",
		"_StructMeta_User{}.EncodeFields(w,",
		"_StructMeta_User{}.DecodeFields(r,",
		"r.StartArray()",
		// Option is null-aware at the root too.
		"if r.IsNull() {",
	} {
		assert.Contains(t, got, want)
	}
	metas := got[strings.Index(got, "type _ValueMeta_"):]
	assert.NotContains(t, metas, "any", "ValueMeta codegen must stay fully typed")
}

// TestValueMeta_Unsupported checks that a Value[T] whose T has no encoding is
// GALA-E0050 at the use site, with the same reasons a field gets.
func TestValueMeta_Unsupported(t *testing.T) {
	cases := []struct {
		name     string
		decls    string
		use      string
		contains string
	}{
		{
			name:     "nested option",
			use:      "Option[Option[int]]",
			contains: "Option[int]]: an Option nested directly in an Option has no encoding",
		},
		{
			name:     "generic struct",
			decls:    "struct Box[T any](V T)",
			use:      "Box[int]",
			contains: "generic type Box[int] has no codec encoding",
		},
		{
			name:     "unsupported field of a struct element",
			decls:    "struct Wave(Z complex128)",
			use:      "Array[Wave]",
			contains: "field Wave.Z has type complex128",
		},
	}
	trans := newCodecTestTranspiler()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := "package main\n\nimport (\n    . \"martianoff/gala/collection_immutable\"\n    \"martianoff/gala/json\"\n)\n\n" + tc.decls +
				"\n\nfunc main() {\n    val c = json.Value[" + tc.use + "]()\n    Println(c)\n}\n"
			_, err := trans.Transpile(src, "value_meta_unsupported.gala")
			require.Error(t, err)
			assert.Contains(t, err.Error(), string(galaerr.CodeUnsupportedCodecField))
			assert.Contains(t, err.Error(), tc.contains)
		})
	}
}
