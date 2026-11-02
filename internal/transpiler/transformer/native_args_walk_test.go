package transformer_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// argShapes is a program with the argument values and slot-typed values the
// native walk reads on its own tree: lambdas, placeholder lambdas, by-name
// thunks and plain values passed to GALA functions, methods, Go functions,
// Copy and a Sendable boundary, and values lowered against the slot they fill
// (results, tuple elements, if-expression branches, a hoisted match). Each
// line marked `// native: <shapes>` names the shapes the native walk must
// have lowered on that line, as exprShapes does: arg, an argument's value read
// on the native tree (argContent); slot, a value classified against its slot
// there (lowerAgainst); placeholder, a placeholder lambda's `_` counted there.
const argShapes = `package main

import (
	"strconv"
	"strings"
)

struct P(x int, var y int)

func apply(f func(int) int, v int) int = f(v)

func run(body Sendable[func() int]) int = body()

func pair() Tuple[int64, string] = (1, "ü") // native: slot, parens

func pick(b bool) int = if (b) 1 else 2 // native: slot, if

func parse[T any]() Option[T] = None[T]()

// A hint pushed for parse() through its ANTLR context is the one its call
// takes on the native tree (checkWalksAgree).
func parsed() Option[int] = parse() // native: slot

func args(p P) int {
	val a = apply((v) => v + 1, 2) // native: arg, slot
	val b = apply(_ * 2, 3) // native: arg, slot, placeholder
	val c = apply((v) => v, p.x) // native: arg, slot
	val d = strconv.Itoa(a) + strings.Repeat("ü", 2) // native: arg
	val e = p.Copy(y = b).y // native: arg, slot
	val f = run(() => c + 1) + run(c * 2) // native: arg, slot
	val g = Some(a).Map(_ + 1).GetOrElse(0) // native: arg, placeholder
	val h = p.x match { // hoisted
		case 0 => {
			return 0
		}
		case n => n
	}
	val t = Try(() => strconv.Atoi("ü")).GetOrElse(0) // native: arg
	a + b + c + d.Size() + e + f + g + h + t
}

func loops(n int) int {
	for i := 0; i < n; i++ {
		val v = if (i > 2) { // a break of its own loop
			for j := 0; j < i; j++ {
				break
			}
			i
		} else {
			0
		}
		Println(v)
	}
	n
}

func main() {
	Println(pair(), pick(true), parsed(), args(P(1, 2)), loops(4)) // native: arg
}
`

// argCases cover each diagnostic an argument value or a slot-typed value
// reports, after multibyte text: the value on the line of each case's
// `/* ü */` marker must have been read on the native tree as the shape the
// case names.
var argCases = []struct{ name, shape, src string }{
	{"placeholders past the function's parameters", "placeholder", "package main\n\nfunc apply(f func(int) int, v int) int = f(v)\n\nfunc f() int {\n\tval s = \"ü\"\n\t/* ü */ apply(_ + _, s.Size())\n}\n"},
	{"lambda that cannot make Go's results", "arg", "package main\n\nimport \"sync\"\n\nfunc f() int {\n\tval get = /* ü */ sync.OnceValues(() => Some(\"ä\"))\n\t0\n}\n"},
	{"opaque value as an argument", "slot", "package main\n\nopaque type A int64\n\nfunc g(x int64) int64 = x\n\nfunc f(a A) int64 = /* ü */ g(a)\n"},
	{"opaque value as a tuple element", "slot", "package main\n\nopaque type A int64\n\nfunc f(a A) Tuple[int64, string] = /* ü */ (a, \"ö\")\n"},
	{"opaque value as an if-expression branch", "slot", "package main\n\nopaque type A int64\n\nfunc f(c bool, a A) int64 = /* ü */ if (c) a else 1\n"},
	{"nil for a Ptr argument", "slot", "package main\n\nstruct P(x int)\n\nfunc g(p Ptr[P]) int = p.x\n\nfunc f() int = /* ü */ g(nil)\n"},
	{"Go call's Try as an argument", "slot", "package main\n\nimport \"strconv\"\n\nfunc g(n int) int = n\n\nfunc f() int = /* ü */ g(strconv.Atoi(\"ä\"))\n"},
	{"Go call's Try passed to a Go function", "arg", "package main\n\nimport (\n\t\"strconv\"\n\t\"strings\"\n)\n\nfunc f() string = /* ü */ strings.Repeat(\"ü\", strconv.Atoi(\"2\"))\n"},
	{"var captured by a Sendable lambda", "arg", "package main\n\nfunc run(body Sendable[func() int]) int = body()\n\nfunc f() int {\n\tvar counter = 0\n\t/* ü */ run(() => counter + 1)\n}\n"},
	{"var captured by a Sendable thunk", "arg", "package main\n\nfunc run(body Sendable[func() int]) int = body()\n\nfunc f() int {\n\tvar counter = 0\n\t/* ü */ run(counter + 1)\n}\n"},
	{"typed pattern as an argument", "arg", "package main\n\nfunc g(n int) int = n\n\nfunc f() int {\n\tval s = \"ü\"\n\t/* ü */ g(x: int)\n}\n"},
}

// TestNativeArgsWalk holds the native walk of argShapes and of each of
// argCases to its golden (checkWalk), and requires the native walk
// to have read the values they mark.
func TestNativeArgsWalk(t *testing.T) {
	run := exprWalkRunner(t)
	t.Run("argument shapes", func(t *testing.T) {
		n := run(t, "argument shapes", argShapes)
		checkMarkedShapes(t, n, argShapes)
		// The hoisted match is lowered on ANTLR's tree, but it is classified
		// against its slot, and its arms checked for escapes, on the native one.
		for i, l := range strings.Split(argShapes, "\n") {
			if strings.HasSuffix(l, "// hoisted") {
				assert.Truef(t, n.shapes[i+1]["slot"], "the native walk classified no value on line %d (it lowered %v)", i+1, n.shapes[i+1])
			}
		}
	})
	for _, c := range argCases {
		t.Run(c.name, func(t *testing.T) {
			n := run(t, c.name, c.src)
			require.Error(t, n.err, "the case transpiles")
			line, _ := bodyMarkerLine(c.src)
			assert.Truef(t, n.shapes[line][c.shape], "the native walk read no %s on line %d (it read %v); error: %v", c.shape, line, n.shapes[line], n.err)
		})
	}
}
