package commands

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"martianoff/gala/internal/transpiler"
)

// TestDocShowsNonEscapingParameters covers ownership phase 3's non-escaping
// function parameters (docs/OWNERSHIP_DESIGN.MD, Q20): `gala doc` names
// them after the signature and in --json, for functions and methods.
func TestDocShowsNonEscapingParameters(t *testing.T) {
	fn := transpiler.FuncType{Params: []transpiler.Type{transpiler.BasicType{Name: "int"}}}
	intT := transpiler.BasicType{Name: "int"}
	tests := []struct {
		name     string
		params   []string
		types    []transpiler.Type
		bits     []bool
		wantLine string   // the rendered line of the function
		wantJSON []string // NonEscaping
	}{
		{"one of two", []string{"f", "n"}, []transpiler.Type{fn, intT}, []bool{true, false},
			"Apply(f func(int), n int) int  (non-escaping: f)", []string{"f"}},
		{"two", []string{"f", "g"}, []transpiler.Type{fn, fn}, []bool{true, true},
			"Apply(f func(int), g func(int)) int  (non-escaping: f, g)", []string{"f", "g"}},
		{"none", []string{"f"}, []transpiler.Type{fn}, []bool{false},
			"Apply(f func(int)) int\n", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			borrow := &transpiler.BorrowSummary{NonEscapingParams: tt.bits}
			rich := &transpiler.RichAST{
				PackageName: "hof",
				Types: map[string]*transpiler.TypeMetadata{
					"hof.Box": {Name: "Box", Package: "hof", Methods: map[string]*transpiler.MethodMetadata{
						"Each": {Name: "Each", ParamNames: tt.params, ParamTypes: tt.types, Borrow: borrow},
					}},
				},
				Functions: map[string]*transpiler.FunctionMetadata{
					"hof.Apply": {Name: "Apply", Package: "hof", ParamNames: tt.params, ParamTypes: tt.types,
						ReturnType: intT, Borrow: borrow},
				},
			}
			pkg := collectPackageDoc("hof", rich)
			require.Len(t, pkg.Functions, 1)
			require.Len(t, pkg.Types, 1)
			assert.Equal(t, tt.wantJSON, pkg.Functions[0].NonEscaping)
			assert.Equal(t, tt.wantJSON, pkg.Types[0].Methods[0].NonEscaping)

			var out bytes.Buffer
			renderPackageDoc(&out, pkg)
			assert.Contains(t, out.String(), tt.wantLine)

			data, err := json.Marshal(pkg.Functions[0])
			require.NoError(t, err)
			if tt.wantJSON == nil {
				assert.NotContains(t, string(data), "nonEscaping")
			} else {
				assert.Contains(t, string(data), `"nonEscaping":`)
			}
		})
	}
}
