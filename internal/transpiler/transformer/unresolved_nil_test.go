package transformer_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"martianoff/gala/internal/transpiler/transformer"
)

// TestUnresolvedInventorySkipsNilOperand pins that a `nil` operand of a
// comparison is not reported as an expression whose type could not be
// determined. `nil` is a keyword, never an Immutable wrapper, so reading
// through one does not need its type; asking for it only produced a NilType
// that the inventory then counted.
func TestUnresolvedInventorySkipsNilOperand(t *testing.T) {
	t.Setenv("GALA_WARN_TYPES", "1")

	tests := []struct {
		name string
		src  string
	}{
		{"error compared to nil", `package main

func ok(err error) bool = err == nil
`},
		{"nil on the left", `package main

func missing(p *int) bool = nil == p
`},
		{"func-typed param compared to nil", `package main

func runOrZero(f func() int) int = if (f != nil) f() else 0
`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			trans, tr := newTranspilerWithTransformer()
			_, err := trans.Transpile(tt.src, "main.gala")
			require.NoError(t, err)
			for _, u := range transformer.UnresolvedTypes(tr) {
				assert.NotEqual(t, "nil", u.Expr, "nil reported as unresolved at line %d", u.Line)
			}
		})
	}
}
