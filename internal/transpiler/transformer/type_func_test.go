package transformer

import (
	"bytes"
	"go/ast"
	"go/printer"
	"go/token"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"martianoff/gala/internal/transpiler"
)

// TestTypeToExprFuncType covers function types with no parameters: their Go
// node must carry an empty, non-nil parameter list, because go/ast's
// FuncType.End reads it when there are no results too, and the printer calls
// End while laying out a parameter list of such types.
func TestTypeToExprFuncType(t *testing.T) {
	unit := transpiler.FuncType{}
	cases := []struct {
		name string
		typ  transpiler.FuncType
		want string
	}{
		{"func()", unit, "func()"},
		{"func(func())", transpiler.FuncType{Params: []transpiler.Type{unit}}, "func(func())"},
		{"func() func()", transpiler.FuncType{Results: []transpiler.Type{unit}}, "func() func()"},
		{"func(int) int", transpiler.FuncType{
			Params:  []transpiler.Type{transpiler.BasicType{Name: "int"}},
			Results: []transpiler.Type{transpiler.BasicType{Name: "int"}},
		}, "func(int) int"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tr := NewGalaASTTransformer().(*galaASTTransformer)
			tr.importManager = NewImportManager()
			tr.richAST = &transpiler.RichAST{}
			ft, ok := tr.typeToExpr(tc.typ).(*ast.FuncType)
			require.True(t, ok, "want a function type")
			ast.Inspect(ft, func(n ast.Node) bool {
				if f, ok := n.(*ast.FuncType); ok {
					assert.NotNil(t, f.Params, "a function type's parameter list must not be nil")
				}
				return true
			})
			var buf bytes.Buffer
			require.NotPanics(t, func() { _ = ft.End() })
			require.NoError(t, printer.Fprint(&buf, token.NewFileSet(), ft))
			assert.Equal(t, tc.want, buf.String())
		})
	}
}
