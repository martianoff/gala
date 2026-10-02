package transformer_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCodecFieldIsEmpty_Emission pins the per-field emptiness test behind a
// codec's OmitEmpty, and the empty value an absent field decodes to.
func TestCodecFieldIsEmpty_Emission(t *testing.T) {
	src := `package main

import (
    "time"
    . "martianoff/gala/std"
    . "martianoff/gala/collection_immutable"
)

type Label string

struct Inner(X int)

struct Rec(
    S string,
    I int8,
    F float64,
    B bool,
    R rune,
    L Label,
    D time.Duration,
    O Option[string],
    A Array[int],
    Li List[int],
    H HashMap[string, int],
    In Inner,
)

func main() {
    val m = StructMeta[Rec]()
    Println(m.FieldIsEmpty(Rec("", 0, 0.0, false, 0, "", 0, None[string](), EmptyArray[int](), EmptyList[int](), EmptyHashMap[string, int](), Inner(0)), 0))
}`
	out, err := newCodecTestTranspiler().Transpile(src, "codec_omit_empty.gala")
	require.NoError(t, err)

	for _, want := range []string{
		"FieldIsEmpty(t Rec, i int) bool",
		"case 0:\n\t\treturn t.S.Get() == \"\"",
		"case 1:\n\t\treturn t.I.Get() == 0",
		"case 2:\n\t\treturn t.F.Get() == 0",
		"case 3:\n\t\treturn !t.B.Get()",
		"case 4:\n\t\treturn t.R.Get() == 0",
		"case 5:\n\t\treturn t.L.Get() == \"\"",
		"case 6:\n\t\treturn t.D.Get() == 0",
		"case 7:\n\t\treturn t.O.Get().IsEmpty()",
		"case 8:\n\t\treturn t.A.Get().IsEmpty()",
		"case 9:\n\t\treturn t.Li.Get().IsEmpty()",
		"case 10:\n\t\treturn t.H.Get().IsEmpty()",
		// An absent Option decodes to None and an absent List to an empty
		// List; Go's zero values of both are not.
		"var _O Option[string] = None[string]{}.Apply()",
		"var _Li List[int] = EmptyList[int]()",
	} {
		assert.Contains(t, out, want)
	}
	// A nested struct is never empty, so it has no case.
	assert.NotContains(t, out, "return t.In.")
}

// TestCodecEmpty_NestedStruct pins the empty value an absent nested struct
// decodes to: built by the nested StructMeta's Empty, with its Option and List
// fields empty rather than Go's zero values. A struct whose zero value is
// already empty needs no Empty call.
func TestCodecEmpty_NestedStruct(t *testing.T) {
	src := `package main

import (
    . "martianoff/gala/std"
    . "martianoff/gala/collection_immutable"
)

struct Plain(N int, S string)
struct Pouch(Coins List[int], Tag Option[string])
struct Outer(P Pouch, Q Plain, Deep Array[Pouch])

func main() {
    val m = StructMeta[Outer]()
    Println(m.NumFields())
}`
	out, err := newCodecTestTranspiler().Transpile(src, "codec_empty.gala")
	require.NoError(t, err)

	pouch := "_StructMeta_Pouch" + metaSuffix("codec_empty.gala")
	for _, want := range []string{
		"var _P Pouch = " + pouch + "{}.Empty()",
		"var _Q Plain\n",
		"Empty() Pouch {\n\treturn Pouch{Coins: NewImmutable(EmptyList[int]()), Tag: NewImmutable(None[string]{}.Apply())}",
	} {
		assert.Contains(t, out, want)
	}
}
