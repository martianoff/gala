package transformer_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	galaparser "martianoff/gala/internal/parser"
)

// postfixShapes is a program with every postfix suffix the native walk
// lowers: member accesses (fields, vals' Immutable fields, a tuple's
// components, package members, an Immutable's own Get and value), indexes
// (a generic function's or generic method's type arguments, Ptr and Tuple
// type arguments, a Go slice's element) and calls (of functions, methods, Go
// functions and constructors, with positional, named, default and lambda
// arguments). Each line marked `// native: <shapes>` names the shapes the
// native walk must have lowered on that line, as exprShapes does.
const postfixShapes = `package main

import (
	"strconv"
	"strings"
)

struct P(x int, var y int)

struct Pair(a P, b P)

func id[T any](v T) T = v

func pick[T any, U any](v T, u U) T = v

func members(p P, q Pair) int {
	val a = p.x // native: selector, name
	val b = q.a.y + q.b.x // native: selector, operator, name
	val t = (a, "ü").V1 // native: selector, parens, name, literal
	val f = id[int] // native: index, name
	val g = pick[int, string] // native: index, name
	val s = strings.ToUpper // native: selector, name
	val m = strconv.IntSize // native: selector, name
	val words = strings.Fields("ä ö")
	val w = words[a - 1] // native: index, operator, name, literal
	Println(f(t), g(a, "ü"), s("ö"), m, w) // native: name, literal
	a + b + q.a.x // native: selector, operator, name
}

struct Box[T any](v T)

func (b Box[T]) Empty[U any]() Option[U] = None[U]()

func typeArgs(b Box[int], cell Immutable[int]) {
	val e = b.Empty[string] // native: selector, index, name
	val get = cell.Get // native: selector, name
	val raw = cell.value // native: selector, name
	val np = None[Ptr[P]] // native: index, name
	val nt = None[Tuple[int, string, bool]] // native: index, name
	Println(e(), get(), raw, np(), nt()) // native: name
}

func (p P) Sum(k int = 1) int = (p.x + p.y) * k

func dflt(a int, b int = 2) int = a + b

func mk() P = P(0, 0)

func calls(p P) int {
	val a = id(5) + dflt(1) + dflt(a = 1, b = 3) // native: call, name, literal
	val b = P(x = 1, y = 2).Sum() + p.Sum(k = 2) // native: call, selector
	val c = Some(a).Map((v) => v + b).GetOrElse(0) // native: call, selector
	val d = strconv.Atoi("ü").GetOrElse(-1) // native: call, selector
	val e = p.Copy(y = 3).y + mk().x // native: call, selector
	val w = strings.Join(strings.Fields("ä ö"), "-") // native: call, selector
	Println(w) // native: call
	a + b + c + d + e
}

func main() {
	Println(members(P(1, 2), Pair(P(1, 2), P(3, 4)))) // native: literal, call
	typeArgs(Box(1), NewImmutable(2))
	Println(calls(P(1, 2))) // native: call
}
`

// postfixCases cover each diagnostic a member access, an index or a call reports,
// after multibyte text: the suffix on the line of each case's `/* ü */`
// marker must be lowered on the native tree as the shape the case names.
var postfixCases = []struct{ name, shape, src string }{
	{"member of a Go call's Try", "selector", "package main\n\nimport \"strconv\"\n\nfunc f() int {\n\tval n = strconv.Atoi(\"1\")\n\tval x = /* ü */ n.Foo\n\tx\n}\n"},
	{"member of a Go call's Tuple", "selector", "package main\n\nimport \"strings\"\n\nfunc f() string {\n\tval c = strings.Cut(\"a=b\", \"=\")\n\tval x = /* ü */ c.Before\n\tx\n}\n"},
	{"Ptr method value on a nullable pointer", "selector", "package main\n\nstruct Counter(var N int)\n\nfunc (c Ptr[Counter]) Count() int = c.N\n\nfunc lookup(found bool) *Counter = if (found) &Counter(1) else nil\n\nfunc f() {\n\tval c = lookup(true)\n\tval g = /* ü */ c.Count\n\tPrintln(g)\n}\n"},
	{"index of a Go call's Try", "index", "package main\n\nimport \"os\"\n\nfunc f() {\n\tval data = os.ReadFile(\"f\")\n\tval x = /* ü */ data[0]\n\tPrintln(x)\n}\n"},
	{"type arguments of a type parameter", "index", "package main\n\nfunc g[Left any](x Left) int {\n\tval f = /* ü */ Left[int]\n\t1\n}\n"},
	{"sealed variant as a type argument", "index", "package main\n\nsealed type Shape {\n\tcase Circle(r float64)\n\tcase Square(s float64)\n}\n\nfunc id[T any](v T) T = v\n\nfunc f() {\n\tval g = /* ü */ id[Circle]\n\tPrintln(g)\n}\n"},
	{"forbidden builtin", "call", "package main\n\nfunc f() int {\n\tval n = /* ü */ len(\"äö\")\n\tn\n}\n"},
	{"forbidden builtin with no arguments", "call", "package main\n\nfunc f() {\n\tval r = /* ü */ recover()\n\tPrintln(r)\n}\n"},
	{"sealed parent called", "call", "package main\n\nsealed type Shape {\n\tcase Circle(r float64)\n\tcase Square(s float64)\n}\n\nfunc f() {\n\tval s = /* ü */ Shape()\n\tPrintln(s)\n}\n"},
	{"sealed parent called with a named argument", "call", "package main\n\nsealed type Shape {\n\tcase Circle(r float64)\n\tcase Square(s float64)\n}\n\nfunc f() {\n\tval s = /* ü */ Shape(r = 1.0)\n\tPrintln(s)\n}\n"},
	{"Go call's Try converted", "call", "package main\n\nimport \"os\"\n\nfunc f() string {\n\tval s = /* ü */ string(os.ReadFile(\"f\"))\n\ts\n}\n"},
	{"too many arguments", "call", "package main\n\nfunc g(a int) int = a\n\nfunc f() int {\n\tval x = /* ü */ g(1, 2)\n\tx\n}\n"},
	{"too many method arguments", "call", "package main\n\nstruct P(x int)\n\nfunc (p P) Get(k int) int = p.x + k\n\nfunc f(p P) int {\n\tval x = /* ü */ p.Get(1, 2)\n\tx\n}\n"},
	{"missing argument", "call", "package main\n\nfunc g(a int, b int) int = a + b\n\nfunc f() int {\n\tval x = /* ü */ g()\n\tx\n}\n"},
	{"unknown named parameter", "call", "package main\n\nstruct P(x int)\n\nfunc (p P) Get(k int = 1) int = p.x + k\n\nfunc f(p P) int {\n\tval x = /* ü */ p.Get(z = 2)\n\tx\n}\n"},
	{"positional argument after a named one", "call", "package main\n\nstruct P(x int, y int)\n\nfunc f() {\n\tval p = /* ü */ P(x = 1, 2)\n\tPrintln(p)\n}\n"},
	{"unknown field", "call", "package main\n\nstruct P(x int, y int)\n\nfunc f() {\n\tval p = /* ü */ P(x = 1, z = 2)\n\tPrintln(p)\n}\n"},
	{"field given twice", "call", "package main\n\nstruct P(x int, y int)\n\nfunc f() {\n\tval p = /* ü */ P(1, x = 2)\n\tPrintln(p)\n}\n"},
	{"field given more than once", "call", "package main\n\nstruct P(x int, y int)\n\nfunc f() {\n\tval p = /* ü */ P(x = 1, x = 2)\n\tPrintln(p)\n}\n"},
	{"spread argument for a field", "call", "package main\n\nstruct P(x int, y int)\n\nfunc f(xs []int) {\n\tval p = /* ü */ P(xs..., y = 2)\n\tPrintln(p)\n}\n"},
	{"Ptr method call on a nullable pointer", "selector", "package main\n\nstruct Counter(var N int)\n\nfunc (c Ptr[Counter]) Count() int = c.N\n\nfunc lookup(found bool) *Counter = if (found) &Counter(1) else nil\n\nfunc f() int {\n\tval c = lookup(true)\n\t/* ü */ c.Count()\n}\n"},
	{"val called as a function", "call", "package main\n\nfunc f() {\n\tval n = 1\n\tval x = /* ü */ n()\n\tPrintln(x)\n}\n"},
	{"val called with arguments", "call", "package main\n\nfunc f() {\n\tval n = 1\n\tval x = /* ü */ n(2)\n\tPrintln(x)\n}\n"},
	{"val field called", "call", "package main\n\nstruct P(n int)\n\nfunc f(p P) {\n\tval x = /* ü */ p.n()\n\tPrintln(x)\n}\n"},
	{"unknown method", "call", "package main\n\nstruct P(x int)\n\nfunc f(p P) {\n\tval x = /* ü */ p.Nope()\n\tPrintln(x)\n}\n"},
	{"unknown method with arguments", "call", "package main\n\nstruct P(x int)\n\nfunc f(p P) {\n\tval x = /* ü */ p.Nope(1)\n\tPrintln(x)\n}\n"},
	{"type parameter called", "call", "package main\n\nfunc g[T any]() int {\n\tval x = /* ü */ T(() => 1)\n\t1\n}\n"},
	{"void call used as a value", "call", "package main\n\nfunc v() {\n\tPrintln(\"ö\")\n}\n\nfunc f() {\n\tval x = /* ü */ v()\n\tPrintln(x)\n}\n"},
	{"interface literal argument without a name", "call", "package main\n\ntype Greeter interface {\n\tGreet() string\n}\n\nfunc f() {\n\tval g = /* ü */ Greeter(() => \"ä\")\n\tPrintln(g)\n}\n"},
	{"Copy of an unknown field", "call", "package main\n\nstruct P(x int)\n\nfunc f(p P) {\n\tval q = /* ü */ p.Copy(z = 1)\n\tPrintln(q)\n}\n"},
	{"opaque conversion", "call", "package main\n\nopaque type A int64\n\nopaque type B int64\n\nfunc f(a A) B = /* ü */ B(a)\n"},
	// A callee of 60 characters is quoted, one of 61 is not: counted in
	// characters on both trees, not in bytes.
	{"void call with a callee of 60 characters", "call", voidCalleeOf(50)},
	{"void call with a callee of 61 characters", "call", voidCalleeOf(51)},
}

// voidCalleeOf is a program using the value of a void method call whose
// callee, `P("ää...").Show`, is n+10 characters of multibyte text, and the
// call too long to quote whole.
func voidCalleeOf(n int) string {
	return "package main\n\nstruct P(s string)\n\nfunc (p P) Show() {\n\tPrintln(p.s)\n}\n\nfunc f() {\n\tval x = /* ü */ P(\"" +
		strings.Repeat("ä", n) + "\").Show()\n\tPrintln(x)\n}\n"
}

// TestNativePostfixWalk holds the native walk of postfixShapes and of each of
// postfixCases to the ANTLR walk (checkWalksAgree), and requires the native
// walk to have lowered the suffixes they mark.
func TestNativePostfixWalk(t *testing.T) {
	run := exprWalkRunner(t)
	t.Run("postfix shapes", func(t *testing.T) {
		checkMarkedShapes(t, run(t, "postfix shapes", postfixShapes), postfixShapes)
	})
	for _, c := range postfixCases {
		t.Run(c.name, func(t *testing.T) {
			n := run(t, c.name, c.src)
			require.Error(t, n.err, "the case transpiles")
			line, _ := bodyMarkerLine(c.src)
			assert.Truef(t, n.shapes[line][c.shape], "the native walk lowered no %s on line %d (it lowered %v); error: %v", c.shape, line, n.shapes[line], n.err)
		})
	}
	t.Run("imported package val", func(t *testing.T) {
		n := runPackageValWalk(t)
		checkMarkedShapes(t, n, packageValMain)
		assert.Contains(t, n.out, "lib.Limit.Get()", "the val is not read at once")
	})
}

// packageValMain reads `lib.Limit`, a val of an imported GALA package, which
// is read at once as an Immutable (importedPackageVal).
const packageValMain = `package main

import "example.com/pfx/lib"

func main() {
	val l = lib.Limit // native: selector, name
	Println(l, lib.Greeting) // native: selector, name
}
`

// runPackageValWalk walks packageValMain in a module whose lib package
// declares the vals it reads, on both trees (checkWalksAgree).
func runPackageValWalk(t *testing.T) walkRun {
	galaparser.RequestEngine(string(galaparser.EngineGalop))
	t.Cleanup(func() { galaparser.RequestEngine("") })
	_, std := walkCorpus(t)
	root := t.TempDir()
	write := func(rel, content string) {
		full := filepath.Join(root, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o755))
		require.NoError(t, os.WriteFile(full, []byte(content), 0o644))
	}
	write("gala.mod", "module example.com/pfx\n\ngala dev\n")
	write("lib/lib.gala", "package lib\n\nval Limit = 50\nval Greeting = \"ü\"\n")
	write("main.gala", packageValMain)
	searchPaths := append([]string{root}, std...)
	nativeW := &walkPipeline{searchPaths: searchPaths, root: root}
	antlrW := &walkPipeline{searchPaths: searchPaths, root: root, antlrOnly: true}
	return agreedWalk(t, nativeW, antlrW, filepath.Join(root, "main.gala"), packageValMain)
}
