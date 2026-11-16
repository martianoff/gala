package transformer_test

import (
	"os"
	"path/filepath"
	"strings"
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

type PC Ptr[Counter]
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
		{"a std type argument", `var p = &Tuple(1, 2)
    put[Tuple[int, int]](&p, Tuple(3, 4))`,
			"cannot lend &p to the inout parameter of put: p holds a Ptr[Tuple[int, int]], and the parameter takes the Tuple[int, int] it points to", "pass `p` without `&`"},
		{"a method's std type argument", `val c = Counter(0, "c")
    var p = &Tuple(1, 2)
    c.putIn[Tuple[int, int]](&p, Tuple(3, 4))`,
			"cannot lend &p to the inout parameter of putIn: p holds a Ptr[Tuple[int, int]], and the parameter takes the Tuple[int, int] it points to", "pass `p` without `&`"},
		{"a receiver's type argument", `val h = Holds(Counter(2, "h"))
    var p = &Counter(1, "p")
    h.fill(&p)`,
			"cannot lend &p to the inout parameter of fill: p holds a Ptr[Counter], and the parameter takes the Counter it points to", "pass `p` without `&`"},
		{"an Option type argument", `var p = &Some(1)
    put[Option[int]](&p, None[int]())`,
			"cannot lend &p to the inout parameter of put: p holds a Ptr[Option[int]], and the parameter takes the Option[int] it points to", "pass `p` without `&`"},
		{"a Ptr without & for a pointer type argument", `var p = &Counter(1, "p")
    put[Ptr[Counter]](p, &Counter(2, "q"))`,
			"cannot lend p to the inout parameter of put: p holds a Ptr[Counter], and the parameter takes a Ptr[Counter]", "lend the variable holding the Ptr with `&p`"},
		{"an alias of a Ptr", `var p PC = &Counter(1, "p")
    bump(&p, 1)`,
			"cannot lend &p to the inout parameter of bump: p holds a Ptr[Counter], and the parameter takes the Counter it points to", "pass `p` without `&`"},
		{"a val Ptr without & for a pointer type argument", `val p = &Counter(1, "p")
    put[Ptr[Counter]](p, &Counter(2, "q"))`,
			"cannot lend p to the inout parameter of put: p holds a Ptr[Counter], and the parameter takes a Ptr[Counter]", "lend a `var` holding the Ptr: `var q = p`, then pass `&q`"},
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
		{"an argument binding the type parameter, the pointer itself", `var p = &Counter(1, "p")
    put(p, Counter(2, "q"))
    Println(p.n)`, "put(p, "},
		{"an alias of a Ptr, the pointer itself", `var p PC = &Counter(1, "p")
    bump(p, 1)
    put[Counter](p, Counter(2, "q"))
    Println(p.n)`, "put[Counter](p, "},
	}
	imp := newOracleImporter()
	for _, tc := range accepted {
		t.Run(tc.name, func(t *testing.T) {
			out, err := trans.Transpile(src(tc.body), "inout_explicit_test.gala")
			require.NoError(t, err)
			assert.Contains(t, out, tc.want)
			res := imp.TypeCheck("main.go", out)
			require.Empty(t, res.Skipped)
			assert.Empty(t, res.Errors, "generated Go:\n%s", out)
		})
	}

	// Valid programs: only the pointer depth is compared, and a type
	// parameter the call leaves to Go is not. A program whose imports are
	// all Go's is built and run (runs is its output); the others, which
	// import GALA packages, are transpiled.
	acceptedPrograms := []struct{ name, src, runs string }{
		{"a literal for an inferred type parameter", `package main

type Named interface {
    Name() string
}

struct Counter(var n int)

func (c Counter) Name() string = s"c${c.n}"

func set[T any](inout x T, v T) {
    x = v
}

func main() {
    var n int64 = 1
    val p = &n
    set(p, 0)
    var f float64 = 1.5
    val q = &f
    set(q, 2)
    var s Named = Counter(1)
    val r = &s
    val c2 Named = Counter(2)
    set(r, c2)
    Println(n, f, s.Name())
}
`, ""},
		{"a byte for a uint8 and a rune for an int32", `package main

func setU8(inout x uint8) {
    x = 2
}

func setI32(inout x int32) {
    x = 3
}

func main() {
    var b byte = 1
    val p = &b
    setU8(p)
    var r rune = 1
    val q = &r
    setI32(q)
    Println(b, r)
}
`, "2 3"},
		{"a function value of a generic field", `package main

struct Counter(var n int)

func bumpAll[T any](inout x T) {
    Println("lent")
}

struct Box[T any](var f func(inout T))

func main() {
    val b = Box[Ptr[Counter]](bumpAll[Ptr[Counter]])
    var p = &Counter(1)
    b.f(&p)
}
`, ""},
		{"a slice of an inferred type parameter", `package main

import . "martianoff/gala/go_interop"

func appendTo[T any](inout xs []T, v T) {
    xs = SliceAppend(xs, v)
}

func main() {
    var s = SliceOf(1, 2)
    val p = &s
    appendTo(p, 3)
    Println(s)
}
`, ""},
		{"a type argument named like a free type parameter", `package main

struct U(var n int)

func put[T any, U any](inout x T, v U) {
    Println(v)
}

func main() {
    var p = &U(1)
    put[U](p, 5)
    Println(p.n)
}
`, ""},
		{"a receiver's type argument named like a free type parameter", `package main

struct U(var n int)

struct Holds[T any](var v T)

func (h Holds[T]) fillWith[U any](inout x T, u U) {
    x = h.v
}

func main() {
    val h = Holds(U(2))
    var p = &U(1)
    h.fillWith(p, 5)
    Println(p.n)
}
`, ""},
		{"an alias of a Ptr, the pointer itself", `package main

type PI Ptr[int]

func incr(inout n int) {
    n = n + 1
}

func put[T any](inout x T, v T) {
    x = v
}

func main() {
    var x = 1
    var p PI = &x
    incr(p)
    put[int](p, 5)
    incr(p)
    Println(x)
}
`, "6"},
		{"a field of a val holding an alias of a Ptr", `package main

struct Counter(var n int)

type PC Ptr[Counter]

func tick(inout n int) {
    n = n + 1
}

func main() {
    val p PC = &Counter(1)
    tick(&p.n)
    Println(p.n)
}
`, ""},
	}
	for _, tc := range acceptedPrograms {
		t.Run(tc.name, func(t *testing.T) {
			out, err := trans.Transpile(tc.src, "inout_explicit_test.gala")
			require.NoError(t, err)
			if tc.runs == "" {
				// GALA's std, which a struct imports, is type-checked from source.
				res := imp.TypeCheck("main.go", out)
				require.Empty(t, res.Skipped)
				assert.Empty(t, res.Errors, "generated Go:\n%s", out)
				return
			}
			got, runErr := buildAndRunGeneratedGo(t, out)
			require.NoError(t, runErr, got)
			assert.Equal(t, tc.runs, strings.TrimSpace(got))
		})
	}

	// A type argument is the type it names, even when a type parameter of
	// the callee has the same name.
	t.Run("a type argument named like the type parameter", func(t *testing.T) {
		_, err := trans.Transpile(`package main

struct T(var n int)

func put[T any](inout x T, v T) {
    x = v
}

func main() {
    var p = &T(1)
    put[T](&p, T(2))
}
`, "inout_explicit_test.gala")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "GALA-E0111")
		assert.Contains(t, err.Error(), "cannot lend &p to the inout parameter of put: p holds a Ptr[T], and the parameter takes the T it points to")
	})

	// A pointer as deep as the parameter's type is certain is rejected
	// whatever names the types; a hint suggests the place only when it fits.
	rejectedPrograms := []struct{ name, src, want, hint, notHint string }{
		{"a pointer to a type GALA does not resolve", `package main

import "os"

func put[T any](inout x T, v T) {
    x = v
}

func main() {
    var m = os.FileMode(1)
    var pm = &m
    put[os.FileMode](&pm, m)
}
`, "cannot lend &pm to the inout parameter of put: pm holds a pointer", "", ""},
		{"a Ptr to a type parameter Go infers, not deep enough", `package main

struct Counter(var n int)

type PT[T any] Ptr[T]

func putp[T any](inout x PT[T], v PT[T]) {
    x = v
}

func main() {
    var p = &Counter(1)
    putp(p, p)
}
`, "cannot lend p to the inout parameter of putp", "", ""},
		{"a type parameter in scope", `package main

func put[T any](inout x T, v T) {
    x = v
}

func g[T any](var p Ptr[T], v T) {
    put[T](&p, v)
}

func main() {
    var n = 1
    g(&n, 2)
}
`, "cannot lend &p to the inout parameter of put: p holds a Ptr[T], and the parameter takes the T it points to", "pass `p` without `&`", ""},
		{"a pointer too deep to another type argument", `package main

struct Box[T any](var v T)

func put[T any](inout x T, v T) {
    x = v
}

func main() {
    var inner = &Box(1)
    var pp = &inner
    put[Ptr[Box[string]]](&pp, &Box("s"))
}
`, "cannot lend &pp to the inout parameter of put: pp holds a Ptr[Ptr[Box[int]]], and the parameter takes a Ptr[Box[string]]", "lend a place of type Ptr[Box[string]]", "without `&`"},
		{"a Ptr not deep enough for another type argument", `package main

struct Box[T any](var v T)

func put[T any](inout x T, v T) {
    x = v
}

func main() {
    var p = &Box(1)
    put[Ptr[Box[string]]](p, &Box("s"))
}
`, "cannot lend p to the inout parameter of put: p holds a Ptr[Box[int]], and the parameter takes a Ptr[Box[string]]", "lend a place of type Ptr[Box[string]]", "`&p`"},
	}
	for _, tc := range rejectedPrograms {
		t.Run(tc.name, func(t *testing.T) {
			_, err := trans.Transpile(tc.src, "inout_explicit_test.gala")
			require.Error(t, err)
			assert.Contains(t, err.Error(), "GALA-E0111")
			assert.Contains(t, err.Error(), tc.want)
			assert.Contains(t, err.Error(), tc.hint)
			if tc.notHint != "" {
				assert.NotContains(t, err.Error(), tc.notHint)
			}
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

type PC Ptr[Counter]

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

	// A type argument naming, under an import alias, an alias of the
	// pointer's type takes the pointer itself.
	_, err = transpileCrossPkg(t, root, `package main

import z "example.com/lendx/lend"

func main() {
    var p z.PC = &z.Counter(1)
    z.Put[z.PC](&p, &z.Counter(2))
    Println(p.N)
}
`)
	require.NoError(t, err)

	// A qualified parameter type is not a type parameter of the same name:
	// lend.Counter stays lend's type, never the type argument.
	_, err = transpileCrossPkg(t, root, `package main

import "example.com/lendx/lend"

func keep[Counter any](inout x lend.Counter, v Counter) {
    Println(v)
}

func main() {
    var p = &lend.Counter(1)
    keep[int](p, 5)
    Println(p.N)
}
`)
	require.NoError(t, err)

	// A Go type the parameter is an alias of is no pointer here: &p lends a
	// pointer where the parameter takes the GoCounter itself. (This harness
	// has no Go type information for lend's .go file, so Same counts as a
	// named type, depth 0; a Go alias of a pointer, `type P = *GoCounter`,
	// is checked end to end with the CLI, which has it.)
	require.NoError(t, os.WriteFile(filepath.Join(root, "lend", "lend_go.go"), []byte(`package lend

type GoCounter struct{ N int }

type Same = GoCounter
`), 0644))
	_, err = transpileCrossPkg(t, root, `package main

import "example.com/lendx/lend"

func main() {
    var p = &lend.GoCounter(N = 1)
    lend.Put[lend.Same](&p, lend.GoCounter(N = 2))
}
`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "GALA-E0111")
	assert.Contains(t, err.Error(), "cannot lend &p to the inout parameter of Put: p holds a Ptr[lend.GoCounter]")
}
// TestInoutLendGoAliases pins the lend check through Go aliases, which a
// package's own hand-written .go file declares: a Ptr is seen through
// `type GP = *GoCounter`, an alias chain and a GALA alias of one, on
// either side of the lend; `type Same = GoCounter` is no pointer; a generic
// `type A[T any] = *T` given type arguments is at least one pointer deep,
// so it is never reported on; and a type parameter named like a Go alias
// (`type T = *GoCounter`) is the type parameter. Every accepted program is
// type-checked as Go, with the Go file's declarations.
func TestInoutLendGoAliases(t *testing.T) {
	const goSide = `
type GoCounter struct{ N int }

type GP = *GoCounter

type GP2 = GP

type Same = GoCounter

type A[T any] = *T

type B[T any] = T

type T = *GoCounter

func NewP() *GoCounter { return &GoCounter{N: 1} }
`
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "gala.mod"), []byte("module example.com/goalias\n\ngala dev\n"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "goside.go"), []byte("package main\n"+goSide), 0644))
	src := func(body string) string {
		return `package main

struct Counter(var n int)

type MyGP GP

struct HG(g GP)

func put[T any](inout x T, v T) {
    x = v
}

func bumpG(inout c GoCounter) {
    c.N = c.N + 1
}

func bumpGP(inout x GP) {
    x.N = x.N + 1
}

func viaVar(var g GP) {
    bumpGP(&g)
}

func viaInout(inout x GP) {
    bumpGP(&x)
}

func tick(inout n int) {
    n = n + 1
}

` + body + `
`
	}
	transpile := func(body string) (string, error) {
		return transpileCrossPkgFile(t, root, src(body), filepath.Join(root, "main.gala"))
	}

	rejected := []struct{ name, body, want, hint string }{
		{"&g for a Go alias of a pointer", `func main() {
    var g GP = NewP()
    bumpG(&g)
}`, "cannot lend &g to the inout parameter of bumpG: g holds a *GoCounter, and the parameter takes the GoCounter it points to",
			"pass `RequirePtr(g)` without `&`"},
		{"g, a Go alias of a pointer that may be nil", `func main() {
    var g GP = NewP()
    bumpG(g)
}`, "the argument g of bumpG is a pointer that may be nil", "pass `RequirePtr(g)`"},
		{"&p for a Go alias of the pointee", `func main() {
    var p = &GoCounter(N = 1)
    put[Same](&p, GoCounter(N = 2))
}`, "cannot lend &p to the inout parameter of put", "pass `p` without `&`"},
		{"a type parameter named like a Go alias", `func g2[T any](var p Ptr[T], v T) {
    put[T](&p, v)
}

func main() {
    var n = 1
    g2(&n, 2)
}`, "cannot lend &p to the inout parameter of put: p holds a Ptr[T], and the parameter takes the T it points to", "pass `p` without `&`"},
		{"&p for a generic Go alias of a Go pointee", `func main() {
    var p = &GoCounter(N = 1)
    put[B[GoCounter]](&p, GoCounter(N = 2))
}`, "cannot lend &p to the inout parameter of put: p holds a Ptr[GoCounter]", "pass `p` without `&`"},
		{"&p for a generic Go alias of a GALA pointee", `func main() {
    var p = &Counter(1)
    put[B[Counter]](&p, Counter(2))
}`, "cannot lend &p to the inout parameter of put: p holds a Ptr[Counter]", "pass `p` without `&`"},
		{"&p for a generic Go alias of a pointer too deep", `func main() {
    var p = &GoCounter(N = 1)
    var q = &GoCounter(N = 2)
    put[A[Ptr[GoCounter]]](&p, &q)
}`, "cannot lend &p to the inout parameter of put: p holds a Ptr[GoCounter]", ""},
		{"&pp for a generic Go alias of a pointer", `func main() {
    var c = &Counter(1)
    var pp = &c
    put[A[Counter]](&pp, &Counter(2))
}`, "cannot lend &pp to the inout parameter of put: pp holds a Ptr[Ptr[Counter]]", ""},
		{"g for a parameter of its own Go pointer type", `func main() {
    var g GP = NewP()
    bumpGP(g)
}`, "the argument g of bumpGP is passed to an inout parameter without `&`", "lend the place with `&g`"},
		{"an inout parameter of a Go pointer type passed on without &", `func f(inout x GP) {
    bumpGP(x)
}

func main() {
    var g GP = NewP()
    f(&g)
}`, "the argument x of bumpGP is passed to an inout parameter without `&`", "lend the place with `&x`"},
	}
	for _, tc := range rejected {
		t.Run(tc.name, func(t *testing.T) {
			_, err := transpile(tc.body)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "GALA-E0111")
			assert.Contains(t, err.Error(), tc.want)
			assert.Contains(t, err.Error(), tc.hint)
		})
	}

	imp := newOracleImporter()
	accepted := []struct{ name, body string }{
		{"a field of a val of a Go alias of a pointer", `func main() {
    val g GP = NewP()
    tick(&g.N)
    Println(g.N)
}`},
		{"a field of a Go alias of a pointer in a val field", `func main() {
    val h = HG(NewP())
    tick(&h.g.N)
    Println(h.g.N)
}`},
		{"a Go alias of a pointer and an alias chain as type arguments", `func main() {
    var p = &GoCounter(N = 1)
    var q = &GoCounter(N = 7)
    put[GP](&p, q)
    put[GP2](&p, q)
    Println(p.N)
}`},
		{"a generic Go alias of a pointer", `func main() {
    var c = &Counter(1)
    var pp = &c
    var q = &Counter(2)
    put[A[Ptr[Counter]]](&pp, &q)
    Println(c.n)
}`},
		{"a parameter of a Go alias of a pointer", `func main() {
    var g GP = NewP()
    bumpGP(&g)
    viaVar(g)
    viaInout(&g)
    Println(g.N)
}`},
		{"a GALA alias of a Go alias of a pointer", `func main() {
    var m MyGP = NewP()
    bumpGP(&m)
    Println(m.N)
}`},
		{"a Ptr to a Go alias of a pointer", `func main() {
    var g GP = NewP()
    var q = &g
    bumpGP(q)
    Println(g.N)
}`},
		{"a Ptr and a Ptr to it for a Go alias of a pointer", `func main() {
    var p = &GoCounter(N = 1)
    bumpGP(&p)
    var pp = &p
    bumpGP(pp)
    Println(p.N)
}`},
		{"generic Go aliases as deep as the place", `func main() {
    var p = &GoCounter(N = 1)
    var q = &GoCounter(N = 2)
    put[B[Ptr[GoCounter]]](&p, q)
    put[A[GoCounter]](&p, q)
    Println(p.N)
}`},
		{"a field of a val of a generic Go alias of a pointer", `func main() {
    val a A[GoCounter] = NewP()
    tick(&a.N)
    Println(a.N)
}`},
		{"a type parameter Go infers named like a Go alias", `func main() {
    var p = &Counter(1)
    var q = &Counter(2)
    put(&p, q)
    Println(p.n)
}`},
	}
	for _, tc := range accepted {
		t.Run(tc.name, func(t *testing.T) {
			out, err := transpile(tc.body)
			require.NoError(t, err)
			res := imp.TypeCheck("main.go", out+goSide)
			require.Empty(t, res.Skipped)
			assert.Empty(t, res.Errors, "generated Go:\n%s", out)
		})
	}
}
