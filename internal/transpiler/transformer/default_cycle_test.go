package transformer_test

import (
	"testing"

	"martianoff/gala/galaerr"

	"github.com/stretchr/testify/require"
)

// TestDefaultCycle pins GALA-E0135: a default is expanded at every call that
// omits its argument, so a default whose expansion needs itself again —
// directly, or through the defaults of other parameters, functions, methods or
// fields — is reported, where it used to overflow the compiler's stack. A
// default that calls its own function but passes the argument is no cycle.
func TestDefaultCycle(t *testing.T) {
	rejected := []struct{ name, src, want string }{
		{
			"a default calling its own function without the parameter",
			"package main\n\nfunc f(a int, b int = f(1)) int = a + b\n\nval x = f(0, 2)\n\nval y = f(0)\n\nfunc main() {\n    Println(x)\n}\n",
			"the default value of parameter b of f depends on itself: f.b → f.b",
		},
		{
			"through a named-argument call",
			"package main\n\nfunc f(a int, b int = f(a = 1)) int = a + b\n\nfunc main() {\n    Println(f(a = 0))\n}\n",
			"parameter b of f depends on itself: f.b → f.b",
		},
		{
			"through another function's default",
			"package main\n\nfunc g(a int = h()) int = a\n\nfunc h(b int = g()) int = b\n\nfunc main() {\n    Println(g())\n}\n",
			"the default value of parameter a of g depends on itself, through the default of h.b: g.a → h.b → g.a",
		},
		{
			"through another parameter's default of the same function",
			"package main\n\nfunc f(a int, b int = f(a, 0), c int = f(a)) int = a + b + c\n\nfunc main() {\n    Println(f(1))\n}\n",
			"parameter b of f depends on itself, through the default of f.c: f.b → f.c → f.b",
		},
		{
			"through two other functions",
			"package main\n\nfunc a(x int = b()) int = x\n\nfunc b(y int = c()) int = y\n\nfunc c(z int = a()) int = z\n\nfunc main() {\n    Println(a())\n}\n",
			"through the default of b.y, then c.z: a.x → b.y → c.z → a.x",
		},
		{
			"a method default calling the method",
			"package main\n\nstruct Box(n int)\n\nfunc (bx Box) Get(k int = bx.Get()) int = bx.n + k\n\nfunc main() {\n    Println(Box(1).Get())\n}\n",
			"parameter k of Box.Get depends on itself: Box.Get.k → Box.Get.k",
		},
		{
			"a field default constructing its own struct",
			"package main\n\nstruct Node(v int = Node().v)\n\nfunc main() {\n    Println(Node().v)\n}\n",
			"field v of Node depends on itself: Node.v → Node.v",
		},
	}
	for _, tc := range rejected {
		t.Run(tc.name, func(t *testing.T) {
			_, err := newForbiddenBuiltinTranspiler().Transpile(tc.src, "")
			require.Error(t, err)
			require.Contains(t, err.Error(), string(galaerr.CodeDefaultCycle))
			require.Contains(t, err.Error(), tc.want)
		})
	}

	accepted := []struct{ name, src string }{
		{"a default calling its own function with every argument", "package main\n\nfunc f(a int, b int = f(1, 2)) int = a + b\n\nfunc main() {\n    Println(f(0))\n}\n"},
		{"a default passing the parameter by name", "package main\n\nfunc f(a int, b int = f(a = 1, b = 2)) int = a + b\n\nfunc main() {\n    Println(f(0))\n}\n"},
		{"a chain of defaults that ends", "package main\n\nfunc g(a int = h()) int = a\n\nfunc h(b int = 1) int = b\n\nfunc main() {\n    Println(g() + g())\n}\n"},
		{"one default used twice in another", "package main\n\nfunc one(n int = 1) int = n\n\nfunc two(m int = one() + one()) int = m\n\nfunc main() {\n    Println(two())\n}\n"},
		{"a default calling its own function without a parameter whose default ends", "package main\n\nfunc f(a int, b int = f(a, 0), c int = 3) int = a + b + c\n\nfunc main() {\n    Println(f(1))\n}\n"},
	}
	for _, tc := range accepted {
		t.Run(tc.name, func(t *testing.T) {
			_, err := newForbiddenBuiltinTranspiler().Transpile(tc.src, "")
			require.NoError(t, err)
		})
	}
}
