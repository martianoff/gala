package generator

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestParenthesizeCompositeLits exercises the helper directly, covering the
// operand shapes no current lowering produces. The end-to-end coverage lives in
// the transformer's control_clause_composite_lit_test.go; this pins the rule itself so a future
// lowering that reaches one of these shapes is already handled.
func TestParenthesizeCompositeLits(t *testing.T) {
	tests := []struct {
		name string
		expr string
		want string
	}{
		{
			name: "named type in a method call",
			expr: `d == RightToLeft{}.Apply()`,
			want: `d == (RightToLeft{}).Apply()`,
		},
		{
			name: "qualified named type",
			expr: `pkg.T{}.Field`,
			want: `(pkg.T{}).Field`,
		},
		{
			name: "instantiated named type",
			expr: `T[int]{}.Field`,
			want: `(T[int]{}).Field`,
		},
		{
			name: "index operand",
			expr: `T{1, 2}[0]`,
			want: `(T{1, 2})[0]`,
		},
		{
			name: "slice operand",
			expr: `T{1, 2}[0:1]`,
			want: `(T{1, 2})[0:1]`,
		},
		{
			name: "explicit slice type needs no parens",
			expr: `[]int{1, 2}[0]`,
			want: `[]int{1, 2}[0]`,
		},
		{
			name: "explicit map type needs no parens",
			expr: `map[string]int{}["k"]`,
			want: `map[string]int{}["k"]`,
		},
		{
			name: "index expression is inside brackets",
			expr: `xs[T{}.N]`,
			want: `xs[T{}.N]`,
		},
		{
			name: "call argument is inside parens",
			expr: `f(T{})`,
			want: `f(T{})`,
		},
		{
			name: "already parenthesized is left alone",
			expr: `(T{}).Apply()`,
			want: `(T{}).Apply()`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			expr, err := parser.ParseExpr(tt.expr)
			require.NoError(t, err)
			require.Equal(t, tt.want, printExpr(t, parenthesizeCompositeLits(expr)))
		})
	}
}

// TestParenthesizeCompositeLitsIsIdempotent guards the property the whole-file
// pass relies on: running over an expression twice must not stack parens.
func TestParenthesizeCompositeLitsIsIdempotent(t *testing.T) {
	expr, err := parser.ParseExpr(`d == RightToLeft{}.Apply()`)
	require.NoError(t, err)

	once := printExpr(t, parenthesizeCompositeLits(expr))
	twice := printExpr(t, parenthesizeCompositeLits(parenthesizeCompositeLits(expr)))
	require.Equal(t, once, twice)
}

func printExpr(t *testing.T, expr ast.Expr) string {
	t.Helper()
	var buf bytes.Buffer
	require.NoError(t, printer.Fprint(&buf, token.NewFileSet(), expr))
	return buf.String()
}
