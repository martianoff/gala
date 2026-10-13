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

// TestUnresolvedInventorySkipsGenericFunctionName pins that the name of a
// generic function written with its type arguments (`F[T](...)`, read as an
// index before it is a call) is not probed for an Immutable wrapper, so it is
// not reported unresolved: GALA functions and Go functions the file reaches
// unqualified alike (see the function-name early-out in unwrapImmutable).
func TestUnresolvedInventorySkipsGenericFunctionName(t *testing.T) {
	t.Setenv("GALA_WARN_TYPES", "1")

	tests := []struct {
		name, src, fn string
	}{
		{"a GALA generic function", `package main

func Make[T any](n int) Option[T] = None[T]()

func main() { Println(Make[int](2)) }
`, "Make"},
		{"a dot-imported Go generic function", `package main

import . "martianoff/gala/go_interop"

func main() { Println(SliceWithSize[int](2)) }
`, "SliceWithSize"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			trans, tr := newTranspilerWithTransformer()
			_, err := trans.Transpile(tt.src, "main.gala")
			require.NoError(t, err)
			for _, u := range transformer.UnresolvedTypes(tr) {
				assert.NotEqual(t, tt.fn, u.Expr, "%s reported as unresolved at line %d", tt.fn, u.Line)
			}
		})
	}
}
