package transformer

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"martianoff/gala/internal/transpiler"
)

// TestInferFuncTypeRoundTrip pins the Hindley-Milner encoding of function
// types. A zero-parameter function must stay a function (`unit -> R`): it was
// encoded as its bare result, so `func() int` came back as `int`.
func TestInferFuncTypeRoundTrip(t *testing.T) {
	tr, ok := NewGalaASTTransformer().(*galaASTTransformer)
	require.True(t, ok)

	intT := transpiler.BasicType{Name: "int"}
	strT := transpiler.BasicType{Name: "string"}
	cases := []struct {
		name string
		typ  transpiler.FuncType
	}{
		{"zero parameters", transpiler.FuncType{Results: []transpiler.Type{intT}}},
		{"one parameter", transpiler.FuncType{Params: []transpiler.Type{intT}, Results: []transpiler.Type{strT}}},
		{"two parameters", transpiler.FuncType{Params: []transpiler.Type{intT, strT}, Results: []transpiler.Type{intT}}},
		{"zero parameters returning a function", transpiler.FuncType{Results: []transpiler.Type{
			transpiler.FuncType{Params: []transpiler.Type{intT}, Results: []transpiler.Type{intT}},
		}}},
		{"one parameter returning a thunk", transpiler.FuncType{Params: []transpiler.Type{intT}, Results: []transpiler.Type{
			transpiler.FuncType{Results: []transpiler.Type{intT}},
		}}},
		{"two parameters returning a thunk", transpiler.FuncType{Params: []transpiler.Type{intT, strT}, Results: []transpiler.Type{
			transpiler.FuncType{Results: []transpiler.Type{strT}},
		}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			back := tr.fromInferType(tr.toInferType(tc.typ))
			assert.Equal(t, tc.typ.String(), back.String())
		})
	}
}
