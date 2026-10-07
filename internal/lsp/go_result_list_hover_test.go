package lsp

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"martianoff/gala/internal/transpiler"
)

// TestSignatureShowsDeclaredGoResults covers a function or method declaring a
// Go result list: hover, completion and signature help show the results as
// declared, `(int, error)`, not the Try a GALA call of it gives.
func TestSignatureShowsDeclaredGoResults(t *testing.T) {
	intT, errT := transpiler.BasicType{Name: "int"}, transpiler.BasicType{Name: "error"}
	try := transpiler.GenericType{
		Base:   transpiler.NamedType{Package: "std", Name: "Try"},
		Params: []transpiler.Type{intT},
	}
	fm := &transpiler.FunctionMetadata{
		Name:       "parse",
		ParamNames: []string{"s"},
		ParamTypes: []transpiler.Type{transpiler.BasicType{Name: "string"}},
		ReturnType: try,
		GoResults:  []transpiler.Type{intT, errT},
	}
	owner := &transpiler.TypeMetadata{Name: "Counter"}
	mm := &transpiler.MethodMetadata{
		Name:       "Write",
		ParamNames: []string{"p"},
		ParamTypes: []transpiler.Type{transpiler.ArrayType{Elem: transpiler.BasicType{Name: "byte"}}},
		ReturnType: try,
		GoResults:  []transpiler.Type{intT, errT},
	}

	assert.Contains(t, formatFuncMeta(fm), "func parse(s string) (int, error)")
	assert.Contains(t, formatMethodMeta(owner, mm), "Write(p []byte) (int, error)")
	assert.Equal(t, "func parse(s string) (int, error)", functionSignature("parse", fm).Label)
	assert.Equal(t, "Write(p []byte) (int, error)", methodSignature("Write", mm).Label)
	assert.Contains(t, formatFuncSig(fm), "(int, error)")
	assert.Contains(t, formatMethodSig(mm), "(int, error)")

	// A single result type is shown as it is.
	single := &transpiler.FunctionMetadata{Name: "n", ReturnType: intT}
	assert.Equal(t, "func n() int", functionSignature("n", single).Label)
}
