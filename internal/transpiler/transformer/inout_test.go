package transformer_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// inoutDecls declares functions and methods with `inout` parameters and
// receivers for the tests below.
const inoutDecls = `
struct Counter(var n int, label string)

struct Point(X int, Y int)

struct Pair(var left Counter, var right Counter, fixed Counter)

func (inout c Counter) inc() int {
    c.n = c.n + 1
    c.n
}

func (inout c Counter) peek() int = c.n

func bump(inout c Counter, by int) {
    c.n = c.n + by
}

func lendOn(inout c Counter) {
    bump(&c, 1)
    c.inc()
}

func moveRight(inout p Point) {
    p = p.Copy(X = p.X + 1)
}

func swap(inout a int, inout b int) {
    val tmp = a
    a = b
    b = tmp
}

func both(inout a Counter, inout b Counter) {
    a.n = b.n
}

func each(inout c Counter, f func(inout Counter)) = f(&c)

func mk() Counter = Counter(0, "mk")
`

func inoutSource(body string) string {
	return "package main\n" + inoutDecls + "\nfunc main() {\n    " + body + "\n}\n"
}

// TestInoutLowering pins how `inout` lowers: an `inout x T` is the Go
// parameter `x *T`, an `inout` receiver a pointer receiver, a whole read of x
// is `(*x)`, a field `x.f`, `x = e` is `(*x) = e`, `&x` lends x itself, and an
// argument is `&place`.
func TestInoutLowering(t *testing.T) {
	trans := newDefaultsTranspiler()
	body := `var c = Counter(0, "c")
    bump(&c, 2)
    c.inc()
    lendOn(&c)
    var p = Point(1, 2)
    moveRight(&p)
    var x = 1
    var y = 2
    swap(&x, &y)
    var pr = Pair(Counter(1, "l"), Counter(2, "r"), Counter(3, "f"))
    both(&pr.left, &pr.right)
    pr.left.inc()
    val ptr = &Counter(5, "p")
    bump(ptr, 1)
    ptr.inc()
    each(&c, (inout k Counter) => { k.n = 0 })
    each(&c, (inout k) => k.inc())
    Println(c.n, p.X, x, y, pr.left.n, ptr.n)`
	out, err := trans.Transpile(inoutSource(body), "inout_test.gala")
	require.NoError(t, err)
	for _, want := range []string{
		"func (c *Counter) inc() int {",
		"(*c).n = (*c).n + 1",
		"func bump(c *Counter, by int) {",
		"bump(c, 1)",
		"(*c).inc()",
		"func moveRight(p *Point) {",
		"(*p) = func(",
		"func swap(a *int, b *int) {",
		"var tmp = (*a)",
		"(*a) = (*b)",
		"bump(&c, 2)",
		"swap(&x, &y)",
		"both(&pr.left, &pr.right)",
		"pr.left.inc()",
		"bump(ptr, 1)",
		"func each(c *Counter, f func(*Counter)) {",
		"func(k *Counter) {",
	} {
		assert.Contains(t, out, want)
	}
}

// TestInoutArgumentRejected pins GALA-E0111: an inout argument is `&` of a
// mutable place, or a Ptr; and an inout parameter is neither variadic nor
// defaulted.
func TestInoutArgumentRejected(t *testing.T) {
	trans := newDefaultsTranspiler()
	tests := []struct {
		name, body, want string
	}{
		{"missing &", `var c = Counter(0, "c")
    bump(c, 1)`, "the argument c of bump is passed to an inout parameter without `&`"},
		{"a call result", `bump(&mk(), 1)`, "cannot lend mk() to the inout parameter of bump: it is not a mutable place"},
		{"a named argument", `bump(by = 1, c = &mk())`, "cannot lend mk() to the inout parameter of bump"},
		{"a method argument", `var a = Counter(0, "a")
    var b = Counter(0, "b")
    both(&a, b)`, "the argument b of both is passed to an inout parameter without `&`"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := trans.Transpile(inoutSource(tc.body), "inout_test.gala")
			require.Error(t, err)
			assert.Contains(t, err.Error(), "GALA-E0111")
			assert.Contains(t, err.Error(), tc.want)
		})
	}

	// `&` of a val or a val field is GALA-E0076 before it reaches the call.
	t.Run("a val field", func(t *testing.T) {
		_, err := trans.Transpile(inoutSource(`var pr = Pair(Counter(1, "l"), Counter(2, "r"), Counter(3, "f"))
    bump(&pr.fixed, 1)`), "inout_test.gala")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "GALA-E0076")
	})

	decls := []struct{ name, decl, want string }{
		{"variadic", "func all(inout cs ...int) {}", "inout parameter cs cannot be variadic"},
		{"default", "func dflt(inout n int = 1) {}", "inout parameter n cannot be given a default"},
		{"struct field", "struct Box(inout n int)", "field n cannot be inout"},
		{"a Ptr parameter", "struct Box(var n int)\n\nfunc set(inout b Ptr[Box]) {}", "inout parameter b cannot be a pointer"},
		{"a pointer receiver", "struct Box(var n int)\n\nfunc (inout b *Box) set() {}", "inout parameter b cannot be a pointer"},
		{"an inout lambda in a by-value slot", "func each(f func(int)) = f(1)\n\nfunc run() = each((inout k) => Println(k))", "lambda parameter k is inout, but the function it fills takes a int"},
	}
	for _, tc := range decls {
		t.Run(tc.name, func(t *testing.T) {
			_, err := trans.Transpile("package main\n\n"+tc.decl+"\n\nfunc main() {}\n", "inout_test.gala")
			require.Error(t, err)
			assert.Contains(t, err.Error(), "GALA-E0111")
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}

// TestInoutReceiverRejected pins GALA-E0114: a method with an inout receiver
// is called only on a mutable place, even when it only reads it.
func TestInoutReceiverRejected(t *testing.T) {
	trans := newDefaultsTranspiler()
	rejected := []struct{ name, body string }{
		{"a val", `val c = Counter(0, "c")
    Println(c.peek())`},
		{"a call result", `Println(mk().inc())`},
		{"a val field of a var", `var pr = Pair(Counter(1, "l"), Counter(2, "r"), Counter(3, "f"))
    Println(pr.fixed.inc())`},
	}
	for _, tc := range rejected {
		t.Run(tc.name, func(t *testing.T) {
			_, err := trans.Transpile(inoutSource(tc.body), "inout_test.gala")
			require.Error(t, err)
			assert.Contains(t, err.Error(), "GALA-E0114")
			assert.Contains(t, err.Error(), "has an inout receiver, and this Counter is not a mutable place")
		})
	}

	t.Run("a parameter not declared var", func(t *testing.T) {
		src := "package main\n" + inoutDecls + "\nfunc read(c Counter) int = c.peek()\n\nfunc main() = Println(read(mk()))\n"
		_, err := trans.Transpile(src, "inout_test.gala")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "GALA-E0114")
	})
}

// TestInoutIsReserved pins that `inout` is a keyword: it cannot name a
// binding.
func TestInoutIsReserved(t *testing.T) {
	trans := newDefaultsTranspiler()
	_, err := trans.Transpile("package main\n\nfunc main() {\n    val inout = 1\n    Println(inout)\n}\n", "inout_test.gala")
	require.Error(t, err)
}
