package lsp

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"martianoff/gala/internal/transpiler"
)

// TestSignatureShowsInout covers `inout` parameters and receivers: hover,
// completion and signature help show them as declared, `inout c Counter`,
// not as the Ptr[Counter] they lower to.
func TestSignatureShowsInout(t *testing.T) {
	counter := transpiler.NamedType{Name: "Counter"}
	ptr := transpiler.PointerType{Elem: counter, NonNull: true}
	intT := transpiler.BasicType{Name: "int"}
	fm := &transpiler.FunctionMetadata{
		Name:        "bump",
		ParamNames:  []string{"c", "by"},
		ParamTypes:  []transpiler.Type{ptr, intT},
		InoutParams: []bool{true},
		ReturnType:  transpiler.VoidType{},
	}
	owner := &transpiler.TypeMetadata{Name: "Counter"}
	mm := &transpiler.MethodMetadata{
		Name:          "transfer",
		ParamNames:    []string{"to", "p"},
		ParamTypes:    []transpiler.Type{ptr, ptr},
		InoutParams:   []bool{true, false},
		InoutReceiver: true,
		ReturnType:    transpiler.VoidType{},
	}

	assert.Contains(t, formatFuncMeta(fm), "func bump(inout c Counter, by int)")
	assert.Contains(t, formatMethodMeta(owner, mm), "func (inout Counter) transfer(inout to Counter, p *Counter)")
	assert.Equal(t, "func bump(inout c Counter, by int)", functionSignature("bump", fm).Label)
	assert.Equal(t, "transfer(inout to Counter, p *Counter)", methodSignature("transfer", mm).Label)
	assert.Contains(t, formatFuncSig(fm), "inout c Counter")
	assert.Contains(t, formatMethodSig(mm), "inout to Counter")
}
