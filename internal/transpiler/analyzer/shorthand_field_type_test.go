package analyzer

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"martianoff/gala/internal/transpiler"
)

// TestShorthandImmutableFieldRecordedAsValueType pins the recorded type of a
// shorthand field declared Immutable[T]. With no keyword it is a val field
// stored as that Immutable[T], the same field as one declared T, so it is
// recorded as T. With `var`, or with an explicit `val` (which wraps it once
// more), it holds the Immutable[T] it names.
func TestShorthandImmutableFieldRecordedAsValueType(t *testing.T) {
	rich := analyzeSrc(t, `package shpkg

struct Box(A Immutable[int64], B int64, var C Immutable[int], val D Immutable[int])
`)

	box := rich.Types["shpkg.Box"]
	require.NotNil(t, box)
	assert.Equal(t, transpiler.BasicType{Name: "int64"}, box.Fields["A"])
	assert.Equal(t, box.Fields["B"], box.Fields["A"])
	assert.Equal(t, "std.Immutable[int]", box.Fields["C"].String())
	assert.Equal(t, "std.Immutable[int]", box.Fields["D"].String())
	assert.Equal(t, []bool{true, true, false, true}, box.ImmutFlags)
}

// TestSynthesizedValFieldRecordedAsValueType pins the same rule for metadata
// synthesized from generated Go: a field whose Go type is std.Immutable[T] is
// a val field holding a T, so it is flagged and recorded as T. The Go type
// info itself keeps the Go type.
func TestSynthesizedValFieldRecordedAsValueType(t *testing.T) {
	immutableInt := transpiler.GenericType{
		Base:   transpiler.NamedType{Package: "std", Name: transpiler.TypeImmutable},
		Params: []transpiler.Type{transpiler.BasicType{Name: "int"}},
	}
	goInfo := transpiler.NewGoTypeInfo()
	goInfo.Types["gen.Box"] = &transpiler.GoTypeData{
		Kind:       "struct",
		Fields:     map[string]transpiler.Type{"X": immutableInt, "Y": transpiler.BasicType{Name: "string"}},
		FieldOrder: []string{"X", "Y"},
		Methods:    map[string]*transpiler.GoFuncSignature{},
	}
	pkgAST := &transpiler.RichAST{}
	synthesizeTypeMetadataFromGo(pkgAST, goInfo)

	box := pkgAST.Types["gen.Box"]
	require.NotNil(t, box)
	assert.Equal(t, transpiler.BasicType{Name: "int"}, box.Fields["X"])
	assert.Equal(t, transpiler.BasicType{Name: "string"}, box.Fields["Y"])
	assert.Equal(t, []bool{true, false}, box.ImmutFlags)
	assert.Equal(t, immutableInt, goInfo.Types["gen.Box"].Fields["X"])
}
