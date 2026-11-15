package transformer_test

import (
	"strings"
	"testing"

	"martianoff/gala/galaerr"

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

// TestUseBindingNeedsClose pins GALA-E0118: a `use` whose resource is a GALA
// type with no Close method is rejected in GALA, rather than lowered to a
// `defer x.Close()` that fails go build against the generated Go. A resource
// whose method set is not GALA's to judge — a Go type, an interface — is left
// to Go, and every resource with a Close method, by value or pointer receiver,
// generic or not, is accepted.
func TestUseBindingNeedsClose(t *testing.T) {
	accepted := []struct{ name, src string }{
		{"a Go resource taken out of its Try", "package main\n\nimport \"os\"\n\nfunc name(p string) Try[string] = Try(() => {\n    use f = os.Open(p).Get()\n    f.Name()\n})\n"},
		{"a pointer-receiver Close", "package main\n\ntype H struct {\n    var n int\n}\n\nfunc (h *H) Close() error {\n    return nil\n}\n\nfunc work() {\n    use h = &H(n = 1)\n    Println(h.n)\n}\n"},
		{"a generic resource", "package main\n\nstruct Box[T any](v T)\n\nfunc (b Box[T]) Close() error = nil\n\nfunc work() {\n    use b = Box(1)\n    Println(b.v)\n}\n"},
		{"an interface resource", "package main\n\nimport \"io\"\n\nfunc work(c io.Closer) {\n    use r = c\n    Println(r)\n}\n"},
	}
	for _, tc := range accepted {
		t.Run(tc.name, func(t *testing.T) {
			out, err := newForbiddenBuiltinTranspiler().Transpile(tc.src, "")
			require.NoError(t, err)
			require.Contains(t, out, ".Close()")
		})
	}

	rejected := []struct{ name, src, want string }{
		{"a Try", "package main\n\nimport \"os\"\n\nfunc work() {\n    use f = os.Open(\"x\")\n    Println(f)\n}\n", "`os.Open(\"x\")` is a Try[*os.File], which has no Close method"},
		{"an Option", "package main\n\nfunc work(o Option[int]) {\n    use v = o\n    Println(v)\n}\n", "take the resource out of the Option; here, `use v = o.Get()`"},
		{"a struct with no Close", "package main\n\nstruct Conn(addr string)\n\nfunc work() {\n    use c = Conn(\"a\")\n    Println(c.addr)\n}\n", "give Conn a `Close() error` method"},
	}
	for _, tc := range rejected {
		t.Run(tc.name, func(t *testing.T) {
			_, err := newForbiddenBuiltinTranspiler().Transpile(tc.src, "")
			require.Error(t, err)
			require.Contains(t, err.Error(), string(galaerr.CodeUseWithoutClose))
			require.Contains(t, err.Error(), tc.want)
		})
	}
}
