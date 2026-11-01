package transformer_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// postfixShapes is a program with every postfix suffix the native walk
// lowers: member accesses (fields, vals' Immutable fields, a tuple's
// components, package members) and indexes (a generic function's type
// arguments, a Go slice's element). Each line marked `// native: <shapes>` names the shapes the
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

func main() {
	Println(members(P(1, 2), Pair(P(1, 2), P(3, 4)))) // native: literal
}
`

// postfixCases cover each diagnostic a member access or an index reports,
// after multibyte text: the suffix on the line of each case's `/* ü */`
// marker must be lowered on the native tree as the shape the case names.
var postfixCases = []struct{ name, shape, src string }{
	{"member of a Go call's Try", "selector", "package main\n\nimport \"strconv\"\n\nfunc f() int {\n\tval n = strconv.Atoi(\"1\")\n\tval x = /* ü */ n.Foo\n\tx\n}\n"},
	{"member of a Go call's Tuple", "selector", "package main\n\nimport \"strings\"\n\nfunc f() string {\n\tval c = strings.Cut(\"a=b\", \"=\")\n\tval x = /* ü */ c.Before\n\tx\n}\n"},
	{"Ptr method value on a nullable pointer", "selector", "package main\n\nstruct Counter(var N int)\n\nfunc (c Ptr[Counter]) Count() int = c.N\n\nfunc lookup(found bool) *Counter = if (found) &Counter(1) else nil\n\nfunc f() {\n\tval c = lookup(true)\n\tval g = /* ü */ c.Count\n\tPrintln(g)\n}\n"},
	{"index of a Go call's Try", "index", "package main\n\nimport \"os\"\n\nfunc f() {\n\tval data = os.ReadFile(\"f\")\n\tval x = /* ü */ data[0]\n\tPrintln(x)\n}\n"},
	{"type arguments of a type parameter", "index", "package main\n\nfunc g[Left any](x Left) int {\n\tval f = /* ü */ Left[int]\n\t1\n}\n"},
	{"sealed variant as a type argument", "index", "package main\n\nsealed type Shape {\n\tcase Circle(r float64)\n\tcase Square(s float64)\n}\n\nfunc id[T any](v T) T = v\n\nfunc f() {\n\tval g = /* ü */ id[Circle]\n\tPrintln(g)\n}\n"},
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
}
