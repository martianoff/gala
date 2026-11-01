package transformer_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// leafShapes is a program with the partial functions, composite literals,
// lambda expression bodies and self tail calls the native walk lowers on its
// own tree. Each line marked `// native: <shapes>` names the shapes the native
// walk must have lowered on that line, as exprShapes does: partial, a
// partial-function literal (transformPartialFunctionLiteral); composite, a
// composite literal (transformCompositeLiteral); tail call, a self tail call
// rewritten to a loop step (buildTailCallStep). The leafCases that name a
// sealed variant as a type need the file's types read on the native tree
// (checkVariantTypeNamesIn): variant type, a type spelled like a variant.
const leafShapes = `package main

import . "martianoff/gala/collection_immutable"

sealed type Shape {
	case Circle(r int)
	case Rect(w int, h int)
	case Empty()
}

type Box struct {
	label string
	var size int
}

type Point struct {
	X int
	Y int
}

struct Rules(pick func(Shape) Option[int])

func area(s Shape, f func(Shape) Option[int]) int = f(s).GetOrElse(-1)

func classify() func(Shape) Option[string] = { case Circle(_) => "circle" } // native: partial

func shapes() int {
	val pf func(int) Option[int] = { case 1 => 10 case 2 => 20 } // native: partial, literal
	val guarded func(int) Option[string] = { case n if n > 1 => "big" case _ => "ü" } // native: partial, operator
	val blocky func(Shape) Option[int] = { // a block arm
		case Circle(r) => {
			val d = r * 2
			d + 1
		}
		case Rect(w, h) => w * h
	}
	val typed = { case n: int => n + 1 } // native: partial
	val opt = { case Some(v) => 1 case None() => 0 } // native: partial
	val xs = ArrayOf(1, 2, 3)
	val evens = xs.Collect({ case x if x % 2 == 0 => x * 10 }) // native: partial, arg
	val r = Rules(pick = { case Rect(w, _) => w }) // native: partial
	val viaArg = area(Circle(2), { case Circle(n) => n * 10 }) // native: partial, arg
	val grouped func(int) Option[int] = ({ case 1 => 10 }) // native: partial
	pf(2).GetOrElse(0) + guarded(2).GetOrElse("").Size() + blocky(Circle(1)).GetOrElse(0) + typed(1).GetOrElse(0) + opt(Some(1)).GetOrElse(0) + evens.Size() + r.pick(Rect(4, 5)).GetOrElse(0) + viaArg + grouped(1).GetOrElse(0)
}

func composites() int {
	val b = Box{label: "ü", size: 2} // native: composite, literal
	val p = Point{X: 3} // native: composite, literal
	val q = Point{} // native: composite
	val pos = Point{1, 2} // native: composite, literal
	b.size + p.X + q.Y + pos.Y
}

func bodies() int {
	val add = (a int, b int) => a + b // native: operator, name
	val wrap = (n int) => Box{label: "w", size: n} // native: composite
	val pick = (s Shape) => s match {
		case Circle(r) => r
		case _ => 0
	}
	add(1, 2) + wrap(3).size + pick(Circle(4))
}

func fact(n int, acc int) int = if (n <= 1) acc else fact(n - 1, acc * n) // native: tail call

func steps(n int, acc int) int = if (n <= 0) acc else if (n % 2 == 0) steps(n / 2, acc + 1) else { // native: tail call
	val next = n - 1
	next + acc
}

func named(n int, acc int) int = if (n <= 0) acc else named(acc = acc + 1, n = n - 1)

func main() {
	Println(classify()(Empty()).GetOrElse("none"), shapes(), composites(), bodies(), fact(5, 1), steps(9, 0), named(3, 0))
}
`

// variantSealed starts the leafCases that name a sealed variant where a type
// is expected (GALA-E0061).
const variantSealed = "package main\n\n/* é */ sealed type Shape {\n\tcase Circle(r int)\n\tcase Square(s int)\n}\n\n"

// leafCases cover each diagnostic a partial function or a composite literal
// reports, after multibyte text: the value on the line of each case's
// `/* ü */` marker must have been read on the native tree as the shape the
// case names.
var leafCases = []struct{ name, shape, src string }{
	{"partial arm with no value", "partial", "package main\n\nfunc f() {\n\tval s = \"ü\"\n\tval pf func(int) Option[int] = /* ü */ { case 1 => {\n\t\tPrintln(s)\n\t} }\n\tPrintln(pf(1))\n}\n"},
	{"partial arm assigns", "partial", "package main\n\nfunc f() {\n\tvar s = \"ü\"\n\tval pf func(int) Option[int] = /* ü */ { case 1 => s = \"ä\" }\n\tPrintln(pf(1))\n}\n"},
	{"partial arm is a void call", "partial", "package main\n\nfunc f() {\n\tval s = \"ü\"\n\tval pf func(int) Option[int] = /* ü */ { case 1 => Println(s) }\n\tPrintln(pf(1))\n}\n"},
	{"partial arm is a Go keyword", "partial", "package main\n\nfunc f() {\n\tval s = \"ü\"\n\tval pf func(int) Option[int] = /* ü */ { case 1 => defer }\n\tPrintln(pf(1), s)\n}\n"},
	{"partial arms of two types", "partial", "package main\n\nfunc f() {\n\tval s = \"ü\"\n\tval pf = /* ü */ { case 1 => \"a\" case 2 => 2 }\n\tPrintln(pf(1), s)\n}\n"},
	{"partial arm guard fails", "partial", "package main\n\nfunc f() {\n\tval s = \"ü\"\n\tval pf func(int) Option[int] = /* ü */ { case n if Some(n).Size() > 0 => n }\n\tPrintln(pf(1), s)\n}\n"},
	{"slice literal", "composite", "package main\n\nfunc f() {\n\tval s = \"ü\"\n\tval xs = /* ü */ []int{1, 2}\n\tPrintln(xs, s)\n}\n"},
	{"map literal", "composite", "package main\n\nfunc f() {\n\tval s = \"ü\"\n\tval m = /* ü */ map[string]int{\"a\": 1}\n\tPrintln(m, s)\n}\n"},
	{"nil for an immutable field", "composite", "package main\n\ntype Box struct {\n\tlabel Option[string]\n}\n\nfunc f() {\n\tval s = \"ü\"\n\tval b = /* ü */ Box{label: nil}\n\tPrintln(b, s)\n}\n"},
	{"tail call argument fails", "tail call", "package main\n\nfunc f(n int, acc int) int = if (n <= 0) acc else /* ü */ f(n - 1, Some(acc).Size())\n"},
	{"tail call passes nil for a Ptr", "tail call", "package main\n\nstruct P(x int)\n\nfunc f(n int, p Ptr[P]) int = if (n <= 0) p.x else /* ü */ f(n - 1, nil)\n"},
	{"variant as a parameter type", "variant type", variantSealed + "func f(/* ü */ c Circle) int = 1\n"},
	{"variant as a result type", "variant type", variantSealed + "/* ü */ func f() Circle = Circle(1)\n"},
	{"variant as a field type", "variant type", variantSealed + "type Holder struct {\n\t/* ü */ c Circle\n}\n"},
	{"variant as a type argument", "variant type", variantSealed + "/* ü */ val xs Option[Circle] = None[Shape]()\n"},
	{"variant in a func type", "variant type", variantSealed + "/* ü */ type F func(Circle) int\n"},
	{"variant as an alias target", "variant type", variantSealed + "/* ü */ type C Circle\n"},
	{"variant in a typed pattern", "variant type", variantSealed + "func f(s Shape) int = s match {\n\t/* ü */ case c: Circle => 1\n\tcase _ => 0\n}\n"},
	{"variant in a lambda parameter", "variant type", variantSealed + "func f() {\n\t/* ü */ val g = (c Circle) => 1\n\tPrintln(g)\n}\n"},
	{"variant shadowed by a type parameter", "variant type", variantSealed + "func g[Circle any](c Circle) Circle = c\n\n/* ü */ func f(c Circle) int = 1\n"},
	{"composite element fails", "composite", "package main\n\ntype Box struct {\n\tsize int\n}\n\nfunc f() {\n\tval s = \"ü\"\n\tval b = /* ü */ Box{size: Some(1).Size()}\n\tPrintln(b, s)\n}\n"},
}

// TestNativeLeavesWalk holds the native walk of leafShapes and of each of
// leafCases to the ANTLR walk (checkWalksAgree), and requires the native walk
// to have lowered the values they mark.
func TestNativeLeavesWalk(t *testing.T) {
	run := exprWalkRunner(t)
	t.Run("leaf shapes", func(t *testing.T) {
		n := run(t, "leaf shapes", leafShapes)
		checkMarkedShapes(t, n, leafShapes)
		assert.Positive(t, n.cases, "the native walk read no case clause")
	})
	for _, c := range leafCases {
		t.Run(c.name, func(t *testing.T) {
			n := run(t, c.name, c.src)
			require.Error(t, n.err, "the case transpiles")
			line, _ := bodyMarkerLine(c.src)
			assert.Truef(t, n.shapes[line][c.shape], "the native walk read no %s on line %d (it read %v); error: %v", c.shape, line, n.shapes[line], n.err)
		})
	}
}
