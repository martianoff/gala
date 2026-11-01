package transformer_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// declShapes is a program with the parts of declarations the native walk
// reads on its own tree: signatures, parameters (variadic, Sendable, a struct
// shorthand's), Go result lists, initializers of package-level and local vals
// and vars, tuple destructurings, and expression bodies, valued and void.
// Each line marked `// native: <shapes>` names the shapes the native walk
// must have read on that line: sig, a signature (lowerSignature); param, a
// parameter (lowerParam); goresults, a Go result list (liftDeclaredGoResults);
// init, an initializer list (exprsOf); body, an expression body; tuple, a
// tuple destructuring (lowerTupleDestructure). Its error cases add valread,
// a value read checked for being used (isUnwrittenValRead), nested, a
// function declared in a body, and receiver, the receiver of a member access
// quoted by a diagnostic (memberSrc.receiver).
const declShapes = `package main

import (
	"strconv"
	"strings"
)

type Parser interface {
	Parse(s string) (int, error) // native: sig, param
	Name() string // native: sig
}

struct Radix(base int) // native: param

val greeting = "ü" + "ä" // native: init

var counter int = 0 // native: init

func (r Radix) Parse(s string) (int, error) = strconv.Atoi(s) // native: sig, param, goresults, body

func (r Radix) Name() string = strings.Repeat("ö", r.base) // native: sig, body

func run(body Sendable[func() int]) int = body() // native: sig, param, body

func forward(f Sendable[func() int]) int = run(f) // native: sig, param, body

func sum(var acc int, xs ...int) int { // native: sig, param
	val n = xs.Size() // native: init
	acc + n
}

func pair() Tuple[int, string] = (1, "ü") // native: sig, body

func destructure() int {
	val (a, b) = pair() // native: tuple, init
	var (c, d) = pair() // native: tuple, init
	val e, f = 1, 2 // native: init
	var g int = a + c // native: init
	val o Option[int] = None() // native: init
	a + e + f + g + b.Size() + d.Size() + o.GetOrElse(0)
}

func say() = Println(greeting) // native: sig, body

func main() {
	say()
	Println(Radix(10).Name(), forward(() => 1), sum(1, 2, 3), destructure(), counter)
}
`

// declCases cover each diagnostic a declaration's signature, parameters,
// initializers or body reports, after multibyte text: the line of each case's
// `/* ü */` marker must have been read on the native tree as the shape the
// case names.
var declCases = []struct{ name, shape, src string }{
	{"untyped parameter", "param", "package main\n\nval s = \"ü\"\n\n/* ü */ func f(a, b int) int = b\n"},
	{"parameter with no name", "param", "package main\n\nval s = \"ü\"\n\n/* ü */ func f(a int, ...int) int = a\n"},
	{"untyped method spec parameter", "param", "package main\n\nval s = \"ü\"\n\ntype I interface {\n\t/* ü */ M(a, b int) int\n}\n"},
	{"Go result list of too many values", "goresults", "package main\n\nval s = \"ü\"\n\n/* ü */ func f() (int, int, int, int, int, int, int, int, int, int, int) = 0\n"},
	{"void expression body with an unused value", "body", "package main\n\nval s = \"ü\"\n\n/* ü */ func f() = 1\n"},
	{"unused field read", "valread", "package main\n\nstruct B(n int)\n\nfunc f(b B) int {\n\tval s = \"ü\"\n\t/* ü */ b.n\n\t0\n}\n"},
	{"unused parenthesized field read", "valread", "package main\n\nstruct B(n int)\n\nfunc f(b B) int {\n\tval s = \"ü\"\n\t/* ü */ (b.n)\n\t0\n}\n"},
	{"Try destructured as a tuple", "init", "package main\n\nimport \"strconv\"\n\nfunc f() int {\n\tval s = \"ü\"\n\t/* ü */ val (a, b) = strconv.Atoi(s)\n\t0\n}\n"},
	{"tuple destructuring with a type", "tuple", "package main\n\nfunc pair() Tuple[int, int] = (1, 2)\n\nfunc f() int {\n\tval s = \"ü\"\n\t/* ü */ val (a, b) Tuple[int, int] = pair()\n\t0\n}\n"},
	{"tuple destructuring of two values", "tuple", "package main\n\nfunc f() int {\n\tval s = \"ü\"\n\t/* ü */ val (a, b) = 1, 2\n\t0\n}\n"},
	{"tuple destructuring with no initializer", "tuple", "package main\n\nfunc f() int {\n\tval s = \"ü\"\n\t/* ü */ var (a, b)\n\t0\n}\n"},
	{"nested function", "nested", "package main\n\nfunc f() int {\n\tval s = \"ü\"\n\t/* ü */ func g(x int, y string) int = x\n\t0\n}\n"},
	{"nested function with a default", "nested", "package main\n\nfunc f() int {\n\tval s = \"ü\"\n\t/* ü */ func g(x int = 1) int = x\n\t0\n}\n"},
	{"nested function with a Go result list", "nested", "package main\n\nfunc f() int {\n\tval s = \"ü\"\n\t/* ü */ func g() (int, error) = (1, nil)\n\t0\n}\n"},
	{"nested function with an untyped parameter", "nested", "package main\n\nfunc f() int {\n\tval s = \"ü\"\n\t/* ü */ func g(x) int = 1\n\t0\n}\n"},
	{"nested method", "nested", "package main\n\nstruct P(x int)\n\nfunc f() int {\n\tval s = \"ü\"\n\t/* ü */ func (p P) g() int = 1\n\t0\n}\n"},
	{"Ptr receiver on a pointer that may be nil", "receiver", "package main\n\nstruct Counter(var N int)\n\nfunc (c Ptr[Counter]) Count() int = c.N\n\nstruct Holder(c *Counter)\n\nfunc f(h Holder) int {\n\tval s = \"ü\"\n\t/* ü */ h.c.Count()\n}\n"},
	{"forwarded parameter that is not Sendable", "param", "package main\n\nfunc run(body Sendable[func() int]) int = body()\n\nval s = \"ü\"\n\n/* ü */ func forward(f func() int) int = run(f)\n"},
}

// TestNativeDeclsWalk holds the native walk of declShapes and of each of
// declCases to the ANTLR walk (checkWalksAgree), and requires the native walk
// to have read the declaration parts they mark.
func TestNativeDeclsWalk(t *testing.T) {
	run := exprWalkRunner(t)
	t.Run("declaration shapes", func(t *testing.T) {
		n := run(t, "declaration shapes", declShapes)
		require.NoError(t, n.err, "the program does not transpile")
		// checkWalksAgree held the hint stream to the ANTLR walk's; it must
		// have found a value's own hint for that to show anything.
		assert.Truef(t, strings.ContainsAny(string(n.hints), "TW"), "no look at the hint stack found the asking value's hint: %q", n.hints)
		for i, l := range strings.Split(declShapes, "\n") {
			_, marked, ok := strings.Cut(l, "// native: ")
			if !ok {
				continue
			}
			for _, shape := range strings.Split(marked, ", ") {
				assert.Truef(t, n.shapes[i+1][shape], "the native walk read no %s on line %d (it read %v): %s", shape, i+1, n.shapes[i+1], strings.TrimSpace(l))
			}
		}
	})
	for _, c := range declCases {
		t.Run(c.name, func(t *testing.T) {
			n := run(t, c.name, c.src)
			require.Error(t, n.err, "the case transpiles")
			line, _ := bodyMarkerLine(c.src)
			assert.Truef(t, n.shapes[line][c.shape], "the native walk read no %s on line %d (it read %v); error: %v", c.shape, line, n.shapes[line], n.err)
		})
	}
}
