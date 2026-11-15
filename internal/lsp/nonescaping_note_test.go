package lsp

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"martianoff/gala/internal/transpiler"
)

// TestNonEscapingNote covers the hover note for non-escaping function
// parameters, on functions and methods.
func TestNonEscapingNote(t *testing.T) {
	fn := transpiler.FuncType{Params: []transpiler.Type{transpiler.BasicType{Name: "int"}}}
	intT := transpiler.BasicType{Name: "int"}
	tests := []struct {
		name   string
		borrow *transpiler.BorrowSummary
		want   string // "" for no note
	}{
		{"several", &transpiler.BorrowSummary{NonEscapingParams: []bool{true, false, true}},
			"*Non-escaping: `f`, `g` are only called during the call.*"},
		{"one", &transpiler.BorrowSummary{NonEscapingParams: []bool{false, false, true}},
			"*Non-escaping: `g` is only called during the call.*"},
		{"none", &transpiler.BorrowSummary{NonEscapingParams: []bool{false, false, false}}, ""},
		{"no summary", nil, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			names := []string{"f", "n", "g"}
			fm := &transpiler.FunctionMetadata{Name: "both", ParamNames: names,
				ParamTypes: []transpiler.Type{fn, intT, fn}, ReturnType: transpiler.VoidType{}, Borrow: tt.borrow}
			mm := &transpiler.MethodMetadata{Name: "both", ParamNames: names,
				ParamTypes: []transpiler.Type{fn, intT, fn}, ReturnType: transpiler.VoidType{}, Borrow: tt.borrow}
			for _, got := range []string{formatFuncMeta(fm), formatMethodMeta(&transpiler.TypeMetadata{Name: "Box"}, mm)} {
				if tt.want == "" {
					assert.NotContains(t, got, "Non-escaping")
				} else {
					assert.Contains(t, got, tt.want)
				}
			}
		})
	}
}
