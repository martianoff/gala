package transformer_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// useProgram wraps a `use` body in a minimal Closeable-providing program so the
// desugaring can be exercised end to end through the transpiler.
func useProgram(body string) string {
	return "package main\n\n" +
		"type Handle struct {\n    name string\n}\n\n" +
		"func (h Handle) Close() error {\n    return nil\n}\n\n" +
		"func open(name string) Handle = Handle(name = name)\n\n" +
		"func work() {\n" + body + "\n}\n"
}

// TestUseBindingLowersToAssignPlusDefer verifies `use x = acquire` desugars to
// `x := acquire` followed by `defer x.Close()` — the sanctioned internal Go
// `defer` lowering that replaces the (forbidden) bare `defer` on the surface.
func TestUseBindingLowersToAssignPlusDefer(t *testing.T) {
	trans := newForbiddenBuiltinTranspiler()

	out, err := trans.Transpile(useProgram("    use a = open(\"a\")\n    a.Close()"), "")
	require.NoError(t, err)
	require.Contains(t, out, "a := open(\"a\")", "use binding should emit a plain := assignment:\n%s", out)
	require.Contains(t, out, "defer a.Close()", "use binding should emit a deferred Close:\n%s", out)
}

// TestUseBindingReadsArePlain verifies a `use` binding is stored plainly, so
// reads of it are NOT rewritten to `x.Get()` the way an Immutable `val` is.
func TestUseBindingReadsArePlain(t *testing.T) {
	trans := newForbiddenBuiltinTranspiler()

	out, err := trans.Transpile(useProgram("    use a = open(\"a\")\n    Println(a.name)"), "")
	require.NoError(t, err)
	require.NotContains(t, out, "a.Get()",
		"a `use` binding is not Immutable-wrapped; reads must stay plain:\n%s", out)
}

// TestMultipleUseBindingsCloseLIFO verifies that two `use` bindings emit their
// defers in source order, so Go runs them last-in-first-out (b closes before a).
func TestMultipleUseBindingsCloseLIFO(t *testing.T) {
	trans := newForbiddenBuiltinTranspiler()

	out, err := trans.Transpile(useProgram("    use a = open(\"a\")\n    use b = open(\"b\")\n    Println(a.name)"), "")
	require.NoError(t, err)
	ia := strings.Index(out, "defer a.Close()")
	ib := strings.Index(out, "defer b.Close()")
	require.Greater(t, ia, -1, "expected defer a.Close():\n%s", out)
	require.Greater(t, ib, -1, "expected defer b.Close():\n%s", out)
	require.Less(t, ia, ib,
		"defer a.Close() must be emitted before defer b.Close() so Go closes b first (LIFO):\n%s", out)
}
