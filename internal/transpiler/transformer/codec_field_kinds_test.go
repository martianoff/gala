package transformer_test

import (
	"testing"

	"martianoff/gala/galaerr"
	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/analyzer"
	"martianoff/gala/internal/transpiler/generator"
	"martianoff/gala/internal/transpiler/transformer"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newCodecTestTranspiler() *checkedTranspiler {
	p := transpiler.NewAntlrGalaParser()
	a := analyzer.NewGalaAnalyzer(p, getStdSearchPath())
	return newCheckedTranspiler(p, a, transformer.NewGalaASTTransformer(), generator.NewGoCodeGenerator())
}

// TestCodecFieldKinds_Emission pins how each scalar kind crosses the
// FieldEncoder/FieldDecoder boundary. Every kind used to fall through to
// `w.WriteNull()` / `r.Skip()` unless it was exactly string, int, int64,
// float64, bool or rune, so an int32 field encoded as null and decoded as 0.
func TestCodecFieldKinds_Emission(t *testing.T) {
	src := `package main

import (
    "time"
    . "martianoff/gala/std"
    . "martianoff/gala/collection_immutable"
)

type Millis int64
type Label string
type Wait time.Duration
type Tags Array[Label]
type Point Inner

struct Inner(X int16)

struct Rec(
    A int8,
    B int32,
    C uint8,
    D uint,
    E uint64,
    F float32,
    G Millis,
    H time.Duration,
    I byte,
    J Option[Inner],
    K HashMap[Label, uint16],
    L Array[Option[int32]],
    M uintptr,
    N Wait,
    O Tags,
    P Point,
)

func main() {
    val m = StructMeta[Rec]()
    Println(m.NumFields())
}`
	out, err := newCodecTestTranspiler().Transpile(src, "codec_kinds.gala")
	require.NoError(t, err)

	for _, want := range []string{
		"w.WriteInt64(int64(t.A.Get()))",
		"_A = int8(r.ReadIntN(8))",
		"w.WriteInt64(int64(t.B.Get()))",
		"_B = int32(r.ReadIntN(32))",
		"w.WriteUint64(uint64(t.C.Get()))",
		"_C = uint8(r.ReadUintN(8))",
		"_D = uint(r.ReadUintN(0))",
		"w.WriteUint64(t.E.Get())",
		"_E = r.ReadUintN(64)",
		"w.WriteFloat32(t.F.Get())",
		"_F = r.ReadFloat32()",
		// A GALA alias is a Go alias, so it is encoded as the type it names.
		"w.WriteInt64(t.G.Get())",
		"_G = r.ReadInt64()",
		// A Go named type over a scalar converts through its underlying type.
		"w.WriteInt64(int64(t.H.Get()))",
		"_H = time.Duration(r.ReadInt64())",
		// Aliases of a Go named type, of a container and of a struct resolve
		// to their targets.
		"w.WriteInt64(int64(t.N.Get()))",
		"_N = time.Duration(r.ReadInt64())",
		"_O = ArrayFromSlice(",
		"_StructMeta_Inner{}.EncodeFields(w, t.P.Get()",
		"_I = byte(r.ReadUintN(8))",
		"_M = uintptr(r.ReadUintN(0))",
		// Option of a struct dispatches to the nested meta instead of null.
		"_StructMeta_Inner{}.EncodeFields(w, t.J.Get().Get()",
		"Some[Inner]{}.Apply(",
		"_X = int16(r.ReadIntN(16))",
	} {
		assert.Contains(t, out, want)
	}
	// The only null a struct field may produce is an Option's None branch;
	// nothing may be skipped except keys the struct does not declare.
	assert.NotContains(t, out, "\t\tr.Skip()\n\t\tcase", "a declared field fell back to Skip")
}

// TestCodecFieldKinds_Unsupported checks that a field shape with no encoding
// is a compile-time GALA-E0050 at the codec use site, never a silent null.
func TestCodecFieldKinds_Unsupported(t *testing.T) {
	cases := []struct {
		name     string
		decls    string
		use      string
		contains string
	}{
		{
			name:     "function field",
			decls:    "struct Job(Name string, Run func() int)",
			use:      "Job",
			contains: "field Job.Run has type func() int: functions have no serialized form",
		},
		{
			name:     "pointer field",
			decls:    "struct Node(Next *int)",
			use:      "Node",
			contains: "field Node.Next has type *int: pointers are not encoded",
		},
		{
			name:     "Go slice field",
			decls:    "struct Bag(Items []int)",
			use:      "Bag",
			contains: "field Bag.Items has type []int: Go slices and maps are not encoded",
		},
		{
			name: "sealed field",
			decls: `sealed type Shape {
    case Circle(R float64)
    case Square(S float64)
}
struct Drawing(S Shape)`,
			use:      "Drawing",
			contains: "field Drawing.S has type Shape: Shape is a sealed type",
		},
		{
			name: "sealed type requested directly",
			decls: `sealed type Shape {
    case Circle(R float64)
    case Square(S float64)
}`,
			use:      "Shape",
			contains: "cannot generate a codec for Shape: Shape is a sealed type",
		},
		{
			name:     "non-string map key",
			decls:    "struct Scores(ByID HashMap[int, string])",
			use:      "Scores",
			contains: "HashMap keys must be strings",
		},
		{
			name:     "option of option",
			decls:    "struct Maybe(V Option[Option[int]])",
			use:      "Maybe",
			contains: "None and Some(None) would both be null",
		},
		{
			name:     "option of an immutable option",
			decls:    "struct Maybe(V Option[Immutable[Option[int]]])",
			use:      "Maybe",
			contains: "None and Some(None) would both be null",
		},
		{
			name: "struct with no fields",
			decls: `struct Marker()
struct Evt(Name string, M Marker)`,
			use:      "Evt",
			contains: "field Evt.M has type Marker: Marker has no fields",
		},
		{
			name:     "complex number",
			decls:    "struct Wave(Z complex128)",
			use:      "Wave",
			contains: "field Wave.Z has type complex128",
		},
		{
			name: "unsupported field in a nested struct",
			decls: `struct Inner(F func(int) int)
struct Outer(Name string, In Array[Inner])`,
			use:      "Outer",
			contains: "cannot generate a codec for Outer: field Inner.F has type func(int) int",
		},
	}
	trans := newCodecTestTranspiler()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := "package main\n\nimport (\n    . \"martianoff/gala/std\"\n    . \"martianoff/gala/collection_immutable\"\n)\n\n" + tc.decls +
				"\n\nfunc main() {\n    val m = StructMeta[" + tc.use + "]()\n    Println(m.NumFields())\n}\n"
			_, err := trans.Transpile(src, "codec_unsupported.gala")
			require.Error(t, err)
			assert.Contains(t, err.Error(), string(galaerr.CodeUnsupportedCodecField))
			assert.Contains(t, err.Error(), tc.contains)
		})
	}
}

// TestCodecFieldKinds_UnsupportedRoot checks that a Codec[T] whose T itself has
// no encoding is GALA-E0050, not a Go compile error about a StructMeta that was
// never generated.
func TestCodecFieldKinds_UnsupportedRoot(t *testing.T) {
	cases := []struct {
		name     string
		decls    string
		use      string
		contains string
	}{
		{
			name:     "generic struct",
			decls:    "struct Box[T any](V T)",
			use:      "Box[int]",
			contains: "cannot generate a codec for Box[int]: generic type Box[int] has no codec encoding",
		},
		{
			name:     "struct with no fields",
			decls:    "struct Marker()",
			use:      "Marker",
			contains: "cannot generate a codec for Marker: Marker has no fields",
		},
		{
			// Used to reference a _StructMeta_int that was never generated
			// and fail in the Go compiler.
			name:     "scalar",
			use:      "int",
			contains: "cannot generate a codec for int: int is not a struct",
		},
		{
			name:     "collection",
			use:      "Array[int]",
			contains: "Array[int] is not a struct: StructMeta[T] describes the fields of a struct",
		},
	}
	trans := newCodecTestTranspiler()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := "package main\n\nimport (\n    . \"martianoff/gala/collection_immutable\"\n    \"martianoff/gala/json\"\n)\n\n" + tc.decls +
				"\n\nfunc main() {\n    val c = json.Codec[" + tc.use + "](json.AsIs())\n    Println(c)\n}\n"
			_, err := trans.Transpile(src, "codec_unsupported_root.gala")
			require.Error(t, err)
			assert.Contains(t, err.Error(), string(galaerr.CodeUnsupportedCodecField))
			assert.Contains(t, err.Error(), tc.contains)
		})
	}
}
