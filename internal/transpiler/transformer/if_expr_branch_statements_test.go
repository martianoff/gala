package transformer_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The statements before the value of an if-expression's block branch, and
// those of a `bind` continuation, are lowered as in any other block, so an
// unused value there is a GALA error, not a Go one. The runtime example
// if_expr_branch_statements covers the statement-position match and the
// discarded if-expression.
func TestUnusedValueInIfExpressionBranchIsRejected(t *testing.T) {
	for _, tc := range []struct{ name, src string }{
		{"if-expression branch", `package main

func run(c bool, n int) int {
    val v = if (c) { n + 1; 5 } else 2
    v
}
`},
		{"bind continuation", `package main

func run(n int) int = Some(n).FlatMap((m) => {
    bind x = Some(m)
    n + 1
    Some(x)
}).GetOrElse(0)
`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := newBindTranspiler().Transpile(tc.src, "")
			require.Error(t, err)
			assert.Contains(t, err.Error(), "`n+1` is evaluated but not used")
		})
	}
}
