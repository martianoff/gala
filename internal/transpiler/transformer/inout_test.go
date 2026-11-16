package transformer_test

import (
	"os"
	"path/filepath"
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

struct Holder(var c Ptr[Counter])

func (c Counter) give(inout d Counter) {
    d.n = c.n
}

func put[T any](inout x T, v T) {
    x = v
}

struct Box[T any](var v T)

func setBox[T any](inout b Box[T], v T) {
    b.v = v
}
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
		"GALA_Counter_inc(c)",
		"func moveRight(p *Point) {",
		"(*p) = func(",
		"func swap(a *int, b *int) {",
		"var tmp = (*a)",
		"(*a) = (*b)",
		"bump(&c, 2)",
		"swap(&x, &y)",
		"both(&pr.left, &pr.right)",
		"GALA_Counter_inc(&pr.left)",
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
		{"a var holding a Ptr", `var p = &Counter(0, "p")
    bump(&p, 1)`, "cannot lend &p to the inout parameter of bump: p holds a Ptr[Counter], and the parameter takes the Counter it points to"},
		{"a field holding a Ptr", `var h = Holder(&Counter(0, "h"))
    bump(&h.c, 1)`, "cannot lend &h.c to the inout parameter of bump: h.c holds a Ptr[Counter]"},
		{"a method's parameter", `val c = Counter(0, "c")
    var p = &Counter(1, "p")
    c.give(&p)`, "cannot lend &p to the inout parameter of give: p holds a Ptr[Counter]"},
		{"a generic type's parameter", `var b = &Box(1)
    setBox(&b, 2)`, "cannot lend &b to the inout parameter of setBox: b holds a Ptr[Box[int]], and the parameter takes the Box[T] it points to"},
		{"a pointer to another type", `var p = &Point(1, 2)
    bump(&p, 1)`, "cannot lend &p to the inout parameter of bump: p holds a Ptr[Point], and the parameter takes a Counter"},
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

// TestInoutLendPtrVar pins the lend of a variable holding a pointer: `&xs`
// for a `var xs` of a Ptr type (a mutable collection) would lend the
// variable, a `**T` in Go where the inout parameter takes a `*T`, and fail
// in go build. It is GALA-E0111; passing xs itself lends its pointee.
func TestInoutLendPtrVar(t *testing.T) {
	trans := newDefaultsTranspiler()
	src := func(arg string) string {
		return `package main

import "martianoff/gala/collection_mutable"

func total(inout xs collection_mutable.Array[int]) int = xs.Size()

func main() {
    var xs = collection_mutable.ArrayOf(1, 2, 3)
    Println(total(` + arg + `))
}
`
	}
	_, err := trans.Transpile(src("&xs"), "inout_ptr_var_test.gala")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "GALA-E0111")
	assert.Contains(t, err.Error(), "cannot lend &xs to the inout parameter of total: xs holds a Ptr[collection_mutable.Array[int]]")
	assert.Contains(t, err.Error(), "pass `xs` without `&`")

	out, err := trans.Transpile(src("xs"), "inout_ptr_var_test.gala")
	require.NoError(t, err)
	assert.Contains(t, out, "GALA_total(xs)")

	// A bare type parameter takes the pointer itself.
	_, err = trans.Transpile(inoutSource(`var p = &Counter(1, "p")
    put(&p, &Counter(2, "q"))
    Println(p.n)`), "inout_ptr_var_test.gala")
	require.NoError(t, err)
}

// inoutGenericDecls declares generic callees with `inout` parameters whose
// type parameters a call can fix: explicitly (`put[Counter]`) or through
// its receiver's type arguments (a Holds[Counter]).
const inoutGenericDecls = `
func putBoth[A any, B any](inout a A, inout b B, va A, vb B) {
    a = va
    b = vb
}

func (c Counter) putIn[T any](inout x T, v T) {
    x = v
}

struct Holds[T any](v T)

func (h Holds[T]) fill(inout x T) {
    x = h.v
}
`

// TestInoutLendPtrVarExplicitTypeArgs pins that a type argument fixed at
// the call does not exempt the lend of a variable holding a pointer from
// GALA-E0111: with T fixed to Counter, an inout x T takes a *Counter, and
// `&p` for a p holding a Ptr[Counter] is a **Counter that fails go build.
// Only a type parameter the call leaves to inference may take the pointer.
func TestInoutLendPtrVarExplicitTypeArgs(t *testing.T) {
	trans := newDefaultsTranspiler()
	src := func(body string) string {
		return "package main\n" + inoutDecls + inoutGenericDecls + "\nfunc main() {\n    " + body + "\n}\n"
	}
	rejected := []struct{ name, body, want, hint string }{
		{"a function", `var p = &Counter(1, "p")
    put[Counter](&p, Counter(2, "q"))`,
			"cannot lend &p to the inout parameter of put: p holds a Ptr[Counter], and the parameter takes the Counter it points to", "pass `p` without `&`"},
		{"a generic type's parameter", `var b = &Box(1)
    setBox[int](&b, 2)`,
			"cannot lend &b to the inout parameter of setBox: b holds a Ptr[Box[int]], and the parameter takes the Box[int] it points to", "pass `b` without `&`"},
		{"several type arguments", `var c = Counter(0, "c")
    var p = &Point(1, 2)
    putBoth[Counter, Point](&c, &p, Counter(1, "c"), Point(3, 4))`,
			"cannot lend &p to the inout parameter of putBoth: p holds a Ptr[Point], and the parameter takes the Point it points to", "pass `p` without `&`"},
		{"a method", `val c = Counter(0, "c")
    var p = &Counter(1, "p")
    c.putIn[Counter](&p, Counter(2, "q"))`,
			"cannot lend &p to the inout parameter of putIn: p holds a Ptr[Counter], and the parameter takes the Counter it points to", "pass `p` without `&`"},
		{"a receiver's type argument", `val h = Holds(Counter(2, "h"))
    var p = &Counter(1, "p")
    h.fill(&p)`,
			"cannot lend &p to the inout parameter of fill: p holds a Ptr[Counter], and the parameter takes the Counter it points to", "pass `p` without `&`"},
	}
	for _, tc := range rejected {
		t.Run(tc.name, func(t *testing.T) {
			_, err := trans.Transpile(src(tc.body), "inout_explicit_test.gala")
			require.Error(t, err)
			assert.Contains(t, err.Error(), "GALA-E0111")
			assert.Contains(t, err.Error(), tc.want)
			assert.Contains(t, err.Error(), tc.hint)
		})
	}

	accepted := []struct{ name, body, want string }{
		{"the pointer itself", `var p = &Counter(1, "p")
    put[Counter](p, Counter(2, "q"))
    Println(p.n)`, "put[Counter](p, "},
		{"a pointer type argument", `var p = &Counter(1, "p")
    put[Ptr[Counter]](&p, &Counter(2, "q"))
    Println(p.n)`, "put[*Counter](&p, "},
		{"a method, the pointer itself", `val c = Counter(0, "c")
    var p = &Counter(1, "p")
    c.putIn[Counter](p, Counter(2, "q"))
    Println(p.n)`, "](c, p, "},
		{"a receiver's type argument, the pointer itself", `val h = Holds(Counter(2, "h"))
    var p = &Counter(1, "p")
    h.fill(p)
    Println(p.n)`, "(h, p)"},
		{"a value", `var c = Counter(1, "c")
    put[Counter](&c, Counter(2, "q"))
    Println(c.n)`, "put[Counter](&c, "},
	}
	for _, tc := range accepted {
		t.Run(tc.name, func(t *testing.T) {
			out, err := trans.Transpile(src(tc.body), "inout_explicit_test.gala")
			require.NoError(t, err)
			assert.Contains(t, out, tc.want)
		})
	}
}

// TestInoutLendPtrVarQualifiedGeneric pins the same check for a generic
// function of another package called with explicit type arguments:
// `lend.Put[lend.Counter](&p)` is GALA-E0111, `lend.Put[lend.Counter](p)`
// lends p's pointee.
func TestInoutLendPtrVarQualifiedGeneric(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "gala.mod"), []byte("module example.com/lendx\n\ngala dev\n"), 0644))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "lend"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "lend", "lend.gala"), []byte(`package lend

struct Counter(var N int)

func Put[T any](inout x T, v T) {
    x = v
}
`), 0644))
	src := func(arg string) string {
		return `package main

import "example.com/lendx/lend"

func main() {
    var p = &lend.Counter(1)
    lend.Put[lend.Counter](` + arg + `, lend.Counter(2))
    Println(p.N)
}
`
	}
	_, err := transpileCrossPkg(t, root, src("&p"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "GALA-E0111")
	assert.Contains(t, err.Error(), "cannot lend &p to the inout parameter of Put: p holds a Ptr[lend.Counter], and the parameter takes the lend.Counter it points to")
	assert.Contains(t, err.Error(), "pass `p` without `&`")

	out, err := transpileCrossPkg(t, root, src("p"))
	require.NoError(t, err)
	assert.Contains(t, out, "lend.GALA_Put[lend.Counter](p, ")
}
