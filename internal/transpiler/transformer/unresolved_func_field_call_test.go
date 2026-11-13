package transformer_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"martianoff/gala/internal/transpiler/transformer"
)

// TestUnresolvedInventoryTypesFuncFieldCall pins that a call of a func-typed
// field, `b.fn(x)`, has the field's result type, also when the field is read
// through a pointer or a nested field. The call used to go untyped, since
// only a method's result was looked up for a selector callee.
func TestUnresolvedInventoryTypesFuncFieldCall(t *testing.T) {
	t.Setenv("GALA_WARN_TYPES", "1")

	const src = `package main

struct Box(var fn func(int) int)

struct Outer(var inner Box)

struct Lazy(compute func() string)

func main() {
    var b = Box((x) => x + 1)
    val bp = &b
    var o = Outer(Box((x) => x * 2))
    val op = &o
    val l = Lazy(() => "s")
    Println(b.fn(1) + bp.fn(2) + op.inner.fn(3))
    Println(l.compute())
}
`
	trans, tr := newTranspilerWithTransformer()
	_, err := trans.Transpile(src, "main.gala")
	require.NoError(t, err)
	for _, u := range transformer.UnresolvedTypes(tr) {
		assert.False(t, strings.Contains(u.Expr, "fn(") || strings.Contains(u.Expr, "compute("),
			"func field call reported as unresolved at line %d: %s", u.Line, u.Expr)
	}
}
