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
// through a pointer, a nested field or a generic struct. The call used to go
// untyped, since only a method's result was looked up for a selector callee.
func TestUnresolvedInventoryTypesFuncFieldCall(t *testing.T) {
	t.Setenv("GALA_WARN_TYPES", "1")

	const src = `package main

struct Box(var fn func(int) int)

struct Outer(var inner Box)

struct Gen[T any](var make func() T)

struct Hook(var cb func())

func main() {
    var b = Box((x) => x + 1)
    val bp = &b
    var o = Outer(Box((x) => x * 2))
    val op = &o
    var g = Gen[string](() => "s")
    var h = Hook(() => Println("hook"))
    val n = if (b.fn(0) > 0) bp.fn(2) else op.inner.fn(3)
    val s = if (n > 0) g.make() else "t"
    h.cb()
    Println(s"${n} ${s}")
}
`
	trans, tr := newTranspilerWithTransformer()
	out, err := trans.Transpile(src, "main.gala")
	require.NoError(t, err)
	for _, u := range transformer.UnresolvedTypes(tr) {
		assert.False(t, strings.Contains(u.Expr, "fn(") || strings.Contains(u.Expr, "make(") || strings.Contains(u.Expr, "cb("),
			"func field call reported as unresolved at line %d: %s", u.Line, u.Expr)
	}
	// The if-expressions' results take the calls' types.
	assert.Contains(t, out, "func() int {")
	assert.Contains(t, out, "func() string {")
}
