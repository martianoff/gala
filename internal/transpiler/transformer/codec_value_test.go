package transformer_test

import (
	"fmt"
	"hash/fnv"
	"regexp"
	"strings"
	"testing"

	"martianoff/gala/galaerr"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// metaSuffix is the per-file suffix the transpiler appends to the names of the
// metadata types it generates for the file named file.
func metaSuffix(file string) string {
	h := fnv.New32a()
	h.Write([]byte(file))
	return fmt.Sprintf("_%08x", h.Sum32())
}

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

	sfx := metaSuffix("value_meta.gala")
	for _, want := range []string{
		// Zero-argument call routed through Apply with the meta injected.
		"json.Value[int8]{}.Apply(_ValueMeta_int8" + sfx + "{})",
		"json.Value[Array[User]]{}.Apply(_ValueMeta_Array_User" + sfx + "{})",
		// Sized ints are range-checked on decode, exactly as fields are.
		"func (_ _ValueMeta_int8" + sfx + ") DecodeValue(r std.FieldDecoder, naming func(string) string) int8",
		"v = int8(r.ReadIntN(8))",
		"w.WriteInt64(int64(v))",
		// An alias classifies as the type it names.
		"func (_ _ValueMeta_Millis" + sfx + ") EncodeValue(w std.FieldEncoder, v int64, naming func(string) string)",
		// A struct inside the value gets its StructMeta, and the element
		// dispatch threads the naming function into it.
		"type _StructMeta_User" + sfx + " struct",
		"_StructMeta_User" + sfx + "{}.EncodeFields(w,",
		"_StructMeta_User" + sfx + "{}.DecodeFields(r,",
		"r.StartArray()",
		// Option is null-aware at the root too.
		"if r.IsNull() {",
	} {
		assert.Contains(t, got, want)
	}
	metas := got[strings.Index(got, "type _ValueMeta_"):]
	assert.NotContains(t, metas, "any", "ValueMeta codegen must stay fully typed")
}

// TestCodecMeta_NamesArePerFile checks that two files of one package that use
// the same codec declare differently named metadata types. Each file is
// transpiled on its own and declares what it uses; with shared names, Go
// rejects the package (`_ValueMeta_int redeclared`). The names must also be
// stable from one build to the next.
func TestCodecMeta_NamesArePerFile(t *testing.T) {
	src := `package main

import (
    . "martianoff/gala/collection_immutable"
    "martianoff/gala/json"
)

struct User(Id int)

func encodeBoth() {
    Println(json.Value[int]().Decode("1"))
    Println(json.Codec[User](json.AsIs()).Decode("{}"))
}
`
	decl := regexp.MustCompile(`(?m)^type (_(?:Struct|Value)Meta_\w+) struct`)
	names := func(file string) []string {
		out, err := newCodecTestTranspiler().Transpile(src, file)
		require.NoError(t, err)
		var got []string
		for _, m := range decl.FindAllStringSubmatch(out, -1) {
			got = append(got, m[1])
		}
		return got
	}
	a, b := names("a.gala"), names("b.gala")
	require.Equal(t, []string{"_StructMeta_User" + metaSuffix("a.gala"), "_ValueMeta_int" + metaSuffix("a.gala")}, a)
	for _, n := range b {
		assert.NotContains(t, a, n, "files a.gala and b.gala of one package both declare %s", n)
	}
	assert.Equal(t, a, names("a.gala"), "generated names must be stable across builds")
}

// TestCodecMeta_InjectionMatchesShapeNotName checks that a parameter is
// injected because its type is the generated metadata's interface — whatever
// it is called and wherever it is declared — and that a parameter of another
// interface is an ordinary one, passed through untouched.
func TestCodecMeta_InjectionMatchesShapeNotName(t *testing.T) {
	src := `package main

type Encodes[T any] interface {
    EncodeValue(w FieldEncoder, v T, naming func(string) string)
    DecodeValue(r FieldDecoder, naming func(string) string) T
}

type Named[T any] interface {
    Name() string
}

struct Label(N string)

func (l Label) Name() string = l.N

type ByShape[T any] struct {}

func (h ByShape[T]) Apply(m Encodes[T]) Encodes[T] = m

type ByName[T any] struct {}

func (h ByName[T]) Apply(m Named[T]) string = m.Name()

func main() {
    Println(ByShape[int8]())
    Println(ByName[int](Label("x")))
}
`
	got, err := newCodecTestTranspiler().Transpile(src, "user_meta.gala")
	require.NoError(t, err)
	assert.Contains(t, got, "ByShape[int8]{}.Apply(_ValueMeta_int8"+metaSuffix("user_meta.gala")+"{})")
	assert.NotRegexp(t, `ByName\[int\]\{\}\.Apply\(_`, got)
}

// TestValueMeta_MetaTypeArgFollowsTypeParam checks that the injected metadata
// describes the type argument in the position of the type parameter the
// metadata parameter names, not always the first one.
func TestValueMeta_MetaTypeArgFollowsTypeParam(t *testing.T) {
	src := `package main

type Pair[K any, V any] struct {}

func (p Pair[K, V]) Apply(meta ValueMeta[V]) ValueMeta[V] = meta

func main() {
    Println(Pair[string, int8]())
}
`
	got, err := newCodecTestTranspiler().Transpile(src, "pair_meta.gala")
	require.NoError(t, err)
	sfx := metaSuffix("pair_meta.gala")
	assert.Contains(t, got, "_ValueMeta_int8"+sfx+"{}")
	assert.Contains(t, got, "func (_ _ValueMeta_int8"+sfx+") DecodeValue(r std.FieldDecoder, naming func(string) string) int8")
	assert.NotContains(t, got, "_ValueMeta_string")
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
			use:      "json.Value[Option[Option[int]]]()",
			contains: "cannot generate a codec for Option[Option[int]]: an Option nested directly in an Option has no encoding",
		},
		{
			name:     "generic struct",
			decls:    "struct Box[T any](V T)",
			use:      "json.Value[Box[int]]()",
			contains: "generic type Box[int] has no codec encoding",
		},
		{
			name:     "unsupported field of a struct element",
			decls:    "struct Wave(Z complex128)",
			use:      "json.Value[Array[Wave]]()",
			contains: "field Wave.Z has type complex128",
		},
		{
			// Used to reach Go as `json.Value{}`, a generic type without
			// instantiation.
			name:     "no type argument",
			use:      "json.Value()",
			contains: "the type argument of json.Value is not given",
		},
	}
	trans := newCodecTestTranspiler()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := "package main\n\nimport (\n    . \"martianoff/gala/collection_immutable\"\n    \"martianoff/gala/json\"\n)\n\n" + tc.decls +
				"\n\nfunc main() {\n    val c = " + tc.use + "\n    Println(c)\n}\n"
			_, err := trans.Transpile(src, "value_meta_unsupported.gala")
			require.Error(t, err)
			assert.Contains(t, err.Error(), string(galaerr.CodeUnsupportedCodecField))
			assert.Contains(t, err.Error(), tc.contains)
		})
	}
}
