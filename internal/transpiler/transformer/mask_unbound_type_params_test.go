package transformer

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"martianoff/gala/internal/transpiler"
)

// TestMaskUnboundMethodTypeParams pins the backstop for a method argument's
// expected type: a type parameter the call left unbound, and that is not in
// scope, never reaches a lambda as a type.
func TestMaskUnboundMethodTypeParams(t *testing.T) {
	tParam, intT := transpiler.BasicType{Name: "T"}, transpiler.BasicType{Name: "int"}
	meta := &transpiler.MethodMetadata{}
	cases := []struct {
		name   string
		typ    transpiler.Type
		subst  map[string]string
		active []string
		want   transpiler.Type
	}{
		{"unbound parameter type", transpiler.FuncType{Params: []transpiler.Type{tParam}}, nil, nil, transpiler.NilType{}},
		{"unbound result only", transpiler.FuncType{Params: []transpiler.Type{intT}, Results: []transpiler.Type{tParam}}, nil, nil,
			transpiler.FuncType{Params: []transpiler.Type{intT}, Results: []transpiler.Type{transpiler.NilType{}}}},
		{"bound: a user type called T", transpiler.FuncType{Params: []transpiler.Type{tParam}}, map[string]string{"T": "T"}, nil,
			transpiler.FuncType{Params: []transpiler.Type{tParam}}},
		{"in scope", transpiler.FuncType{Params: []transpiler.Type{tParam}}, nil, []string{"T"},
			transpiler.FuncType{Params: []transpiler.Type{tParam}}},
		{"concrete", transpiler.FuncType{Params: []transpiler.Type{intT}}, nil, nil, transpiler.FuncType{Params: []transpiler.Type{intT}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tr := NewGalaASTTransformer().(*galaASTTransformer)
			for _, a := range tc.active {
				tr.activeTypeParams[a] = true
			}
			ctx := callContext{methodMeta: meta, typeSubst: tc.subst, recvTypeParams: []string{"T"}}
			assert.Equal(t, tc.want, tr.maskUnboundMethodTypeParams(tc.typ, ctx))
		})
	}
}
