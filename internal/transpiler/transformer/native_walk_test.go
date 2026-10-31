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

// TestNativeWalkAgrees holds the transformer's walk of the compiled file's
// top-level declarations, function-body statements and expressions on its
// native tree to its walk on ANTLR's: every GALA source staged as data,
// transpiled as the CLI transpiles a file, gives byte-identical Go, or the
// same error, both ways. Each file's walk must have taken the tree it
// should (checkWalksAgree), and most must have taken the native one, so a
// bridge or converter that fails quietly cannot turn the comparison into
// ANTLR's against ANTLR's.
func TestNativeWalkAgrees(t *testing.T) {
	galaparser.RequestEngine(string(galaparser.EngineGalop))
	t.Cleanup(func() { galaparser.RequestEngine("") })
	paths, searchPaths := walkCorpus(t)
	nativeW, antlrW := &walkPipeline{searchPaths: searchPaths}, &walkPipeline{searchPaths: searchPaths, antlrOnly: true}
	files, clean, walked, decls, stmts, lambdas, exprs := 0, 0, 0, 0, 0, 0, 0
	for _, path := range paths {
		src, err := os.ReadFile(path)
		require.NoError(t, err)
		n, _, ok := checkWalksAgree(t, nativeW, antlrW, path, string(src))
		if !ok {
			continue
		}
		files++
		decls += n.native
		stmts += n.stmts
		lambdas += n.lambdas
		exprs += n.exprs
		if n.err == nil {
			clean++
		}
		if n.converted {
			walked++
		}
	}
	// Floors catch the sources not being staged or the native walk going
	// unused.
	require.Truef(t, files >= 200 && clean >= files*3/4 && walked >= files*3/4 && decls >= files && stmts >= files && lambdas >= files && exprs >= stmts,
		"%d sources, %d of them transpiled, %d walked on the native tree, %d top-level declarations, %d body statements, %d lambdas and %d expressions lowered there", files, clean, walked, decls, stmts, lambdas, exprs)
	t.Logf("%d sources, %d of them transpiled, %d walked on the native tree, %d top-level declarations, %d body statements, %d lambdas and %d expressions lowered there", files, clean, walked, decls, stmts, lambdas, exprs)
}

// walkCases cover every top-level declaration shape the native walk reads,
// and each diagnostic it positions, after multibyte text too: a position
// read off the wrong tree, or a column counted in bytes, shows as a
// differing error.
var walkCases = []struct{ name, src string }{
	{"all shapes", `// Package main is documented.
package main

import (
	"go/ast"
	"go/token"
	"fmt"
	s "strings"
	. "martianoff/gala/collection_immutable"
)

// Greeting is documented.
val Greeting = "héllo"

val a, b = 1, "zwei"

val (p, q) = (1, "x")

var counter int

var (m, n) = (2.5, true)

val typed Option[int] = Some(1)

// Point is a struct shorthand.
struct Point(
	// X is documented.
	x int,
	var y int,
	val z string,
)

// Pair is generic.
struct Pair[A any, B any](first A, second B)

// Shape is a struct.
type Shape struct {
	// Name is documented.
	Name string
	var Sides int
}

// Named is an interface.
type Named interface {
	// Name is documented.
	Name() string
}

type Celsius float64

type Label string

type Handler func(int) int

type Alias Point

// Tree is sealed.
sealed type Tree {
	// Leaf is documented.
	case Leaf(v int)
	case Node(l Tree, r Tree)
}

opaque type Meters float64

// Area is a method.
func (s Shape) Area() int = s.Sides * 2

func (p Pair[A, B]) Swap() Pair[B, A] = Pair[B, A](p.second, p.first)

func Generic[T any](x T) T = x

func Block(x int) int {
	val y = x + 1
	y * 2
}

func Show() = Println(s.ToUpper(Greeting))

func Fact(n int, acc int) int = if (n <= 1) acc else Fact(n - 1, acc * n)

func main() {
	Show()
	fmt.Println(Point(1, 2, "z"), Generic(3), Block(4), ArrayOf(1, 2), Fact(5, 1), a, b, p, q, m, n, counter, typed)
}
`},
	{"blank val", "package main\n\n/* ünï */ val _ = \"äöü\"\n"},
	{"blank typed var", "package main\n\n/* ü */ var _ int = 1 /* ü */ + 2\n"},
	{"blank val in a multiline span", "package main\n\n/* ü */ val _ = 1 +\n\t2\n"},
	{"reserved name", "package main\n\n/* ü */ val __gala_line_7 = 1\n"},
	{"reserved name in a tuple", "package main\n\n/* ü */ val (a, __gala_line_7) = (1, 2)\n"},
	{"tuple with a type", "package main\n\n/* ü */ val (a, b) Tuple[int, int] = (1, 2)\n"},
	{"var tuple with a type", "package main\n\n/* ü */ var (a, b) int\n"},
	{"tuple of two expressions", "package main\n\n/* ü */ val (a, b) = (1, \"ü\"), (3, 4)\n"},
	{"tuple arity", "package main\n\n/* ü */ val (a, b, c) = (1, \"ü\")\n"},
	{"tuple arity, too few names", "package main\n\n/* ü */ var (a) = (1, \"ü\")\n"},
	{"var tuple without initializer", "package main\n\n/* ü */ var (a, b)\n\nval c = 1\n"},
	{"tuple of a failing Go call", "package main\n\nimport \"strconv\"\n\n/* ü */ val (n, err) = strconv.Atoi(\"1\")\n"},
	{"var receiver", "package main\n\nstruct P(x int)\n\n/* ü */ func (var p P) Get() int = p.x\n"},
	{"alias redeclared", "package main\n\ntype A int\n\n/* ü */ type A string\n"},
	{"untyped parameter", "package main\n\n/* ü */ struct S(a, b int)\n"},
	{"None without a type", "package main\n\n/* ü */ val v = None()\n"},
	{"var None without a type", "package main\n\n/* ü */ var v = None()\n"},
	{"assignment mismatch", "package main\n\n/* ü */ val u, v = 1, 2, 3\n"},
	{"missing return", "package main\n\nfunc f(x int) int {\n\tif x > 0 {\n\t\treturn 1\n\t}\n\t/* ü */ Println(x)\n}\n"},
	{"self-containing struct", "package main\n\n/* ü */ type S struct {\n\tnext S\n}\n"},
	{"self-containing shorthand", "package main\n\n/* ü */ struct S(next S)\n"},
	{"receiver alias", "package main\n\ntype Millis int64\n\n/* ü */ func (m Millis) Double() Millis = m * 2\n"},
	{"multi-return val", "package main\n\nimport \"os\"\n\nval entries, err = os.ReadDir(\".\")\n\nvar names, err2 = os.ReadDir(\"..\")\n\nfunc main() = Println(entries, err, names, err2)\n"},
	{"var default", "package main\n\nvar o Option[int]\n\nvar n, m int\n\nfunc main() = Println(o, n, m)\n"},
	{"embed", "package main\n\nembed val text = \"x.txt\"\n\nembed val files = \"a/*.txt\"\n\nembed val two = \"a.txt\", \"b.txt\"\n\nembed val typed string = \"c.txt\"\n"},
	{"dot import", "package main\n\nimport . \"strings\"\n\nfunc main() = Println(ToUpper(\"a\"))\n"},
	{"no declarations", "package main\n"},
	{"exported pointer params", "package main\n\nstruct P(x int)\n\nfunc (p *P) Get(q *P) int = p.x + q.x\n\nfunc Use(p Ptr[P]) int = 1\n"},
	{statementShapes, `package main

import "strconv"

type Handle struct {
	name string
}

func (h Handle) Close() error {
	return nil
}

func open(name string) Handle = Handle(name = name)

struct Cell(var n int, label string)

func look(x int) Option[int] = if (x > 0) Some(x) else None[int]()

// Monad binds in a body: the statements after the bind are its chain's.
func both(x int, y int) Option[int] {
	val base = 1
	bind a = look(x)
	also b = look(y)
	Some(a + b + base)
}

func sign(x int) string {
	if x > 0 {
		return "plus"
	} else if x < 0 {
		"minus"
	} else {
		"zéro"
	}
}

func early(x int) int {
	if x > 9 {
		return 9
	}
	x * 2
}

func loops() int {
	var total = 0
	for i := 0; i < 3; i++ {
		total += i
	}
	var j = 0
	for ; j < 3; j = j + 1 {
		total -= j
	}
	for total < 100 {
		total *= 2
	}
	for {
		total /= 2
		if total < 10 {
			break
		}
		continue
	}
	for k, v := range "ünï" {
		total = total + k + int(v)
	}
	var key = 0
	for key = range "ab" {
		total++
	}
	total--
	total
}

func locals() {
	val (p, q) = (1, "ü")
	var (m, n) = (2, 3)
	var cell = Cell(1, "c")
	cell.n = 2
	m, n = n, m
	val a, b = 1, 2
	val parsed, err = strconv.Atoi("12")
	use h = open("h")
	Println(p, q, m, n, cell, a, b, parsed, err, h.name, key())
	val x = 3
	x match {
		case 3 => Println("three")
		case _ => Println("other")
	}
	if (x > 2) Println("big") else Println("small")
	return
}

func key() int = 1

func main() {
	Println(both(1, 2), sign(1), early(3), loops())
	locals()
}
`},
	{"also without a bind", "package main\n\nfunc f() Option[int] {\n\t/* ü */ also x = Some(1)\n\tSome(x)\n}\n"},
	{"bind in a loop body", "package main\n\nfunc f() Option[int] {\n\tfor {\n\t\t/* ü */ bind x = Some(1)\n\t}\n\tSome(1)\n}\n"},
	{"bind without a monad", "package main\n\nfunc f() {\n\t/* ü */ bind x = Some(1)\n\tSome(x)\n}\n"},
	{"nested function", "package main\n\nfunc f() {\n\t/* ü */ func gee(x int) int = x\n}\n"},
	{"nested method", "package main\n\nstruct P(x int)\n\nfunc f() {\n\t/* ü */ func (p P) G() int = 1\n}\n"},
	{"if initializer", "package main\n\nfunc f(x int) {\n\t/* ü */ if y := x; y > 0 {\n\t\tPrintln(y)\n\t}\n}\n"},
	{"assign to a val", "package main\n\nfunc f() {\n\tval x = 1\n\t/* ü */ x = 2\n}\n"},
	{"assign to a parameter", "package main\n\nfunc f(x int) {\n\t/* ü */ x += 2\n}\n"},
	{"assign to a receiver", "package main\n\nstruct P(x int)\n\nfunc (p P) M() {\n\t/* ü */ p = P(1)\n}\n"},
	{"assign to an immutable field", "package main\n\nstruct P(x int)\n\nfunc f() {\n\tvar p = P(1)\n\t/* ü */ p.x = 2\n}\n"},
	{"assign to an if-expression", "package main\n\nfunc f(c bool) {\n\tvar x = 1\n\t/* ü */ if (c) Println(x) else x = 2\n}\n"},
	{"increment a val", "package main\n\nfunc f() {\n\tval x = 1\n\t/* ü */ x++\n}\n"},
	{"range assigns a val", "package main\n\nfunc f() {\n\tval k = 0\n\t/* ü */ for k = range \"ab\" {\n\t\tPrintln(k)\n\t}\n}\n"},
	{"for post binds two names", "package main\n\nimport \"strconv\"\n\nfunc f() {\n\tfor i := 0; i < 3; /* ü */ a, b := strconv.Atoi(\"1\") {\n\t\tPrintln(i)\n\t}\n}\n"},
	{"blank short var", "package main\n\nfunc f() {\n\t/* ü */ _ := 1 /* ü */ + 2\n}\n"},
	{"blank local val", "package main\n\nfunc f() {\n\t/* ü */ val _ = \"äöü\"\n}\n"},
	{"blank local var in a lambda body", "package main\n\nfunc f() {\n\tval g = () => {\n\t\t/* ü */ var _ int = 1\n\t}\n\tg()\n}\n"},
	{"short var mismatch", "package main\n\nfunc f() {\n\t/* ü */ a, b := 1, 2, 3\n}\n"},
	{"short var of one value", "package main\n\nfunc f() {\n\t/* ü */ a, b := 1\n}\n"},
	{"short var None", "package main\n\nfunc f() {\n\t/* ü */ x := None()\n}\n"},
	{"forbidden keyword", "package main\n\nfunc f() {\n\t/* ü */ defer\n}\n"},
	{"value not used", "package main\n\nfunc f() {\n\tval x = 1\n\t/* ü */ x\n\tPrintln(x)\n}\n"},
	{"tail with no value", "package main\n\nfunc f() int {\n\t/* ü */ Println(\"ü\")\n}\n"},
	{"missing return in a branch", "package main\n\nfunc f(x int) int {\n\tif x > 0 {\n\t\t/* ü */ Println(x)\n\t} else {\n\t\treturn 1\n\t}\n}\n"},
	{"body ends in an if without else", "package main\n\nfunc f(x int) int {\n\tPrintln(\"ü\")\n\t/* ü */ if x > 0 {\n\t\treturn 1\n\t}\n}\n"},
	{"empty body with a result", "package main\n\nfunc f() int {\n/* ü */ }\n"},
	{lambdaShapes, `package main

import (
	"os"
	"strconv"
	. "martianoff/gala/concurrent"
	. "martianoff/gala/collection_immutable"
)

type Handler func(int) int

val inc = (x int) => x + 1

val twice Handler = (x) => x * 2

func apply(f func(int) int, x int) int = f(x)

func main() {
	val add = (a int, b int) int => a + b
	val block = (x int) int => {
		val y = x + 1
		if y > 2 {
			return y
		}
		y * 2
	}
	val say = (s string) => {
		Println(s)
	}
	val nested = (x int) => (y int) => x + y
	val xs = ArrayOf(1, 2, 3)
	val doubled = xs.Map((x) => x * 2)
	val sum = xs.FoldLeft(0, (acc, x) => acc + x)
	xs.ForEach((x) => {
		val label = "ünï"
		Println(label, x)
	})
	val bump = (var n int) int => {
		n = n + 1
		n
	}
	val bumped = xs.Map((var x) => {
		x = x + 1
		x
	})
	val shared = 5
	val f = Future(() => shared + 1)
	val g = Future(shared * 2)
	val tried = Try(() => os.Getenv("HOME"))
	val parsed = Try(() => {
		val digits = "1" + "2"
		strconv.Atoi(digits)
	})
	val noop = () => {}
	Println(inc(1), twice(2), apply((x) => x - 1, 3), add(1, 2), block(1), nested(1)(2), doubled, sum, bump(1), bumped, f.Get(), g.Get(), tried.IsSuccess(), parsed.IsSuccess())
	say("ü")
	noop()
}
`},
	{"untyped lambda parameter", "package main\n\nfunc f() {\n\t/* ü */ val g = (x) => x\n\tg(1)\n}\n"},
	{"wildcard and explicit parameter types", "package main\n\nfunc f() {\n\t/* ü */ val g = (x _, y int) => y\n\tg(1, 2)\n}\n"},
	{"lambda of too many parameters", "package main\n\nimport . \"martianoff/gala/collection_immutable\"\n\nfunc f() {\n\t/* ü */ Println(ArrayOf(1).Map((x, y) => x))\n}\n"},
	{"missing return in a lambda", "package main\n\nfunc f() {\n\tval g = (x int) int => {\n\t\tif x > 0 {\n\t\t\treturn 1\n\t\t}\n\t\t/* ü */ Println(\"ü\")\n\t}\n\tg(1)\n}\n"},
	{"void block lambda discards an error", "package main\n\nimport (\n\t\"os\"\n\t. \"martianoff/gala/collection_immutable\"\n)\n\nfunc f() {\n\t/* ü */ ArrayOf(\"ü\").ForEach((p) => {\n\t\tos.Remove(p)\n\t})\n}\n"},
	{"void expression lambda discards an error", "package main\n\nimport (\n\t\"os\"\n\t. \"martianoff/gala/collection_immutable\"\n)\n\nfunc f() {\n\t/* ü */ ArrayOf(\"ü\").ForEach((p) => os.Remove(p))\n}\n"},
	{"blank local val in a lambda body", "package main\n\nfunc f() {\n\tval g = (x int) => {\n\t\t/* ü */ val _ = x\n\t\tx\n\t}\n\tg(1)\n}\n"},
	{"var captured by a Future lambda", "package main\n\nimport . \"martianoff/gala/concurrent\"\n\nfunc f() {\n\tvar n = 1\n\t/* ü */ val g = Future(() => n + 1)\n\tPrintln(g.Get())\n}\n"},
	{"var captured by a Future thunk", "package main\n\nimport . \"martianoff/gala/concurrent\"\n\nfunc f() {\n\tvar n = 1\n\t/* ü */ val g = Future(n + 1)\n\tPrintln(g.Get())\n}\n"},
}

// lambdaShapes is the walkCases program that has every lambda shape the
// native walk lowers, and must transpile.
const lambdaShapes = "lambda shapes"

// captureCases are the walkCases that pass a lambda, or a by-name argument,
// to a Sendable parameter, so their captures must be checked on the native
// tree.
var captureCases = map[string]bool{
	lambdaShapes:                      true,
	"var captured by a Future lambda": true,
	"var captured by a Future thunk":  true,
}

// captureFailsFirst are the captureCases whose diagnostic is the capture
// check, which runs before the lambda is lowered, so no lambda is.
var captureFailsFirst = map[string]bool{
	"var captured by a Future lambda": true,
}

// statementShapes is the walkCases program that has every statement shape
// the native walk lowers, and must transpile.
const statementShapes = "statement shapes"

// TestNativeWalkCases is TestNativeWalkAgrees over walkCases.
func TestNativeWalkCases(t *testing.T) {
	galaparser.RequestEngine(string(galaparser.EngineGalop))
	t.Cleanup(func() { galaparser.RequestEngine("") })
	_, searchPaths := walkCorpus(t)
	nativeW, antlrW := &walkPipeline{searchPaths: searchPaths}, &walkPipeline{searchPaths: searchPaths, antlrOnly: true}
	dir := t.TempDir()
	for _, c := range walkCases {
		t.Run(c.name, func(t *testing.T) {
			path := filepath.Join(dir, strings.ReplaceAll(c.name, " ", "_"), "main.gala")
			require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
			require.NoError(t, os.WriteFile(path, []byte(c.src), 0o644))
			n, want, ok := checkWalksAgree(t, nativeW, antlrW, path, c.src)
			require.True(t, ok, "the case does not parse cleanly with galop")
			// The first case and statementShapes are programs that
			// transpile, so every shape in them is lowered to the end.
			if c.name == walkCases[0].name || c.name == statementShapes || c.name == lambdaShapes {
				require.NoError(t, n.err, "the case does not transpile")
			}
			require.Truef(t, n.converted, "the analysis did not convert the native tree, so the walk read ANTLR's; error: %v", n.err)
			// A diagnostic marked in a function body must come from the
			// native walk: the statement on the marked line was lowered on
			// the native tree, so the case compares native with ANTLR, not
			// ANTLR with itself.
			if line, ok := bodyMarkerLine(c.src); ok && n.err != nil {
				assert.Truef(t, n.stmtLines[line], "the native walk did not reach the statement on line %d; error: %v", line, n.err)
			}
			// A case with a lambda must have lowered it on the native tree,
			// and a case passing to a Sendable parameter must have checked
			// its captures there, so its diagnostic, or its Go, compares the
			// native lambda lowering and capture check with ANTLR's.
			if want.lambdas > 0 && !captureFailsFirst[c.name] {
				assert.Positivef(t, n.lambdas, "the native walk lowered no lambda; error: %v", n.err)
			}
			if captureCases[c.name] {
				assert.Positivef(t, n.captures, "the native walk checked no captures; error: %v", n.err)
			}
			t.Logf("the native walk lowered %d declarations, %d statements and %d lambdas, and checked %d captures; error: %v", n.native, n.stmts, n.lambdas, n.captures, n.err)
		})
	}
}
