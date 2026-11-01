package transformer_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// matchShapes is a program with the matches the native walk lowers on its own
// tree: the subject with its suffixes, and the values the patterns hold.
// Each line marked `// native: <shapes>` must have been lowered on the native
// tree with those shapes (checkMarkedShapes): `match` for a match whose
// subject starts on the line, `pattern` for a pattern's value compared by ==,
// `alternative` for one alternative of several, `extractor` for an
// extractor's or a qualified pattern's head, and `type-arg` for a pattern's
// explicit type argument.
const matchShapes = `package main

import (
	"strings"

	"martianoff/gala/std"
	. "martianoff/gala/collection_immutable"
)

struct Box[T any](v T, tag string)

struct Bag(items Array[int])

val Limit = 10

func literals(x int) string = x match { // native: match, name
	case 0 => "zéro" // native: pattern, literal
	case 1 | 2 | 3 => "small" // native: alternative, literal
	case Limit => "limit" // native: pattern, name
	case _ => "other"
}

func subjects(b Bag, s string) int {
	val n = b.items.Size() match { // native: match, selector, call
		case 0 => 0 // native: pattern
		case k => k
	}
	strings.ToUpper(s) match { // native: match, call
		case "ü" | "Ä" => Println("umlaut") // native: alternative, literal
		case _ => Println(s)
	}
	n
}

func heads(o Option[int], x any) int {
	val a = o match { // native: match, name
		case std.Some(v) => v // native: extractor
		case _ => 0
	}
	val b = x match { // native: match, name
		case Box[int](v, _) => v // native: extractor, type-arg
		case _ => 0
	}
	a + b
}

func hoisted(x int) int {
	val v = x match { // native: match, name
		case 1 => { return 10 } // native: pattern
		case _ => x + 1
	}
	v
}

func nested(o Option[Option[string]]) string = o match { // native: match
	case Some(inner) => inner match { // native: match, name
		case Some("ö") => "ö" // native: pattern, literal
		case _ => "other"
	}
	case _ => "none"
}

func main() {
	Println(literals(1), subjects(Bag(ArrayOf(1)), "ü"), heads(Some(1), Box[int](2, "t")), hoisted(2), nested(None[Option[string]]()))
}
`

// matchWalkCases cover the diagnostics about a match as a whole and about the
// values its patterns hold, after multibyte text: the line of a case's
// `/* ü */` marker must have been lowered on the native tree with the case's
// shape.
var matchWalkCases = []struct{ name, src, shape string }{
	{"arms of different types", "package main\n\nfunc f(x int) {\n\tval s = \"ü\"\n\tval v = /* ü */ x match {\n\t\tcase 1 => s\n\t\tcase _ => 2\n\t}\n\tPrintln(v)\n}\n", "match"},
	{"bare return in a value match", "package main\n\nfunc f(x int) {\n\tval s = \"ü\"\n\tPrintln(/* ü */ x match {\n\t\tcase 1 => { return }\n\t\tcase _ => s\n\t})\n}\n", "match"},
	{"sealed view with a Ptr receiver", "package main\n\nstruct M(raw int)\n\nfunc (m Ptr[M]) MatchView() Option[int] = Some(m.raw)\n\nfunc f(m *M) int {\n\tval s = \"ü\"\n\t/* ü */ m match {\n\t\tcase Some(n) => n\n\t\tcase _ => s.Size()\n\t}\n}\n", "match"},
	{"loop control in a value match", "package main\n\nfunc f(x int) {\n\tval s = \"ü\"\n\tfor {\n\t\tPrintln(/* ü */ x match {\n\t\t\tcase 1 => break\n\t\t\tcase _ => s\n\t\t})\n\t}\n}\n", "match"},
	{"subject suffix error", "package main\n\nstruct P(x int)\n\nfunc f(p P) int {\n\tval s = \"ü\"\n\t/* ü */ p.nope match {\n\t\tcase _ => s.Size()\n\t}\n}\n", "match"},
	{"pattern value compared by == that cannot compare", "package main\n\nval Fn = (x int) => x\n\nfunc f(g func(int) int) int = g match {\n\t/* ü */ case Fn => \"ü\".Size()\n\tcase _ => 0\n}\n", "pattern"},
	{"variant as a pattern's type argument", "package main\n\nsealed type S {\n\tcase A()\n\tcase B()\n}\n\nstruct Box[T any](v T)\n\nfunc f(x any) int = x match {\n\t/* ü */ case Box[A](v) => \"ü\".Size()\n\tcase _ => 0\n}\n", "extractor"},
	{"generic struct with the wrong type arguments", "package main\n\nstruct Box[T any](v T)\n\nfunc f(x any) int = x match {\n\t/* ü */ case Box[int, string](v) => \"ü\".Size()\n\tcase _ => 0\n}\n", "extractor"},
	{"function as a qualified extractor", "package main\n\nimport \"strings\"\n\nfunc f(x string) int = x match {\n\t/* ü */ case strings.ToUpper(v) => v.Size()\n\tcase _ => 0\n}\n", "extractor"},
	{"operands of an unconstrained type compared by ==", "package main\n\nfunc f[T any](a T, b T) bool {\n\tval s = \"ü\"\n\tPrintln(s)\n\t/* ü */ a == b\n}\n", "operator"},
	{"sub-pattern value compared by == that cannot compare", "package main\n\nval Fn = (x int) => x\n\nfunc f(o Option[func(int) int]) int = o match {\n\t/* ü */ case Some(Fn) => \"ü\".Size()\n\tcase _ => 0\n}\n", "pattern"},
}

// TestNativeMatchWalk holds the native walk of matchShapes and of each of
// matchWalkCases to the ANTLR walk (checkWalksAgree), and requires the native
// walk to have lowered the matches and pattern values they mark.
func TestNativeMatchWalk(t *testing.T) {
	run := exprWalkRunner(t)
	t.Run("match shapes", func(t *testing.T) {
		n := run(t, "match shapes", matchShapes)
		require.NoError(t, n.err, "the program does not transpile")
		// A match filling a slot is lowered by its shape (lowerAgainst), not
		// as an expression, so only the shapes are required.
		for i, l := range strings.Split(matchShapes, "\n") {
			_, marked, ok := strings.Cut(l, "// native: ")
			if !ok {
				continue
			}
			for _, shape := range strings.Split(marked, ", ") {
				assert.Truef(t, n.shapes[i+1][shape], "the native walk lowered no %s on line %d (it lowered %v): %s", shape, i+1, n.shapes[i+1], strings.TrimSpace(l))
			}
		}
	})
	for _, c := range matchWalkCases {
		t.Run(c.name, func(t *testing.T) {
			n := run(t, c.name, c.src)
			require.Error(t, n.err, "the case transpiles")
			line, ok := caseMarkerLine(c.src)
			require.True(t, ok, "the case has no marker")
			assert.Truef(t, n.shapes[line][c.shape], "the native walk lowered no %s on line %d (it lowered %v); error: %v", c.shape, line, n.shapes[line], n.err)
		})
	}
}
