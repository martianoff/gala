package transformer_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stmtShapes is a program with the statements the native walk reads on its
// own tree: returns, `use`, assignments, increments, short variable
// declarations, loop conditions, if-expressions run as statements, the
// blocks of if-expression branches, and `bind` / `also` chains with the
// statements after them. Each line marked `// stmt` starts a statement the
// native walk must have lowered.
const stmtShapes = `package main

import "strings"

struct P(x int, var y int)

struct Res(name string)

func (r Res) Close() {
	Println("close ü " + r.name)
}

func lookup(k string) Option[int] = if (k == "ä") Some(1) else None[int]()

func chain(a string, b string) Option[int] {
	val pre = a + "ü" // stmt
	bind x = lookup(a) // stmt
	also y int = lookup(b) // stmt
	val sum = x + y // stmt
	Println(pre, sum) // stmt
	bind z = Some(sum * 2) // stmt
	if (z > 2) { // stmt
		Some(z) // stmt
	} else {
		None[int]() // stmt
	}
}

func branches(c bool, n int) int {
	val v = if (c) { // stmt
		val m = n * 2 // stmt
		Println("ü", m) // stmt
		m + 1 // stmt
	} else {
		var k = n // stmt
		k += 3 // stmt
		if (k > 4) { // stmt
			k // stmt
		} else {
			0 // stmt
		}
	}
	if (c) Println("a") else { // stmt
		Println("b") // stmt
	}
	val o = if (c) { // stmt
		bind q = lookup("ä") // stmt
		Some(q + n) // stmt
	} else None[int]()
	v + o.GetOrElse(0) // stmt
}

func loops(n int) int {
	var p = P(1, 2) // stmt
	p.y = p.y + 1 // stmt
	p.y++ // stmt
	var i = 0 // stmt
	for i < n { // stmt
		i = i + 1 // stmt
		if (i == 3) { // stmt
			continue // stmt
		}
	}
	for j := 0; j < n; j++ { // stmt
		i -= j // stmt
	}
	a, b := 1, "ü" // stmt
	Println(a, b, strings.ToUpper(b)) // stmt
	if (i < 0) { // stmt
		return -i // stmt
	}
	i + p.y // stmt
}

func resource() {
	use r = Res("ü") // stmt
	Println(r.name) // stmt
}

func main() {
	Println(chain("ä", "ä"), branches(true, 3), loops(4)) // stmt
	resource() // stmt
}
`

// stmtCases cover each diagnostic a statement the native walk reads
// reports, after multibyte text: the statement on the line of each case's
// `/* ü */` marker must have been lowered on the native tree.
var stmtCases = []struct{ name, src string }{
	{"assignment to a val", "package main\n\nfunc f() int {\n\tval x = \"ü\".Size()\n\t/* ü */ x = 2\n\tx\n}\n"},
	{"assignment to a parameter", "package main\n\nfunc f(n int) int {\n\t/* ü */ n = 2\n\tn\n}\n"},
	{"increment of an immutable field", "package main\n\nstruct P(x int)\n\nfunc f() int {\n\tvar p = P(\"ü\".Size())\n\t/* ü */ p.x++\n\tp.x\n}\n"},
	{"assignment to an immutable field", "package main\n\nstruct P(x int)\n\nfunc f() int {\n\tvar p = P(\"ü\".Size())\n\t/* ü */ p.x = 3\n\tp.x\n}\n"},
	{"assignment to an if-expression", "package main\n\nfunc g() {}\n\nfunc f(c bool) {\n\tvar x = \"ü\".Size()\n\t/* ü */ if (c) g() else x = 1\n\tPrintln(x)\n}\n"},
	{"range assignment to a val", "package main\n\nimport . \"martianoff/gala/go_interop\"\n\nfunc f() {\n\tval k = 0\n\tval xs = SliceOf(\"ü\")\n\t/* ü */ for k = range xs {\n\t}\n}\n"},
	{"value not used", "package main\n\nfunc f(n int) {\n\tval s = \"ü\"\n\t/* ü */ n + s.Size()\n\tPrintln(n)\n}\n"},
	{"value not used in an if-expression branch", "package main\n\nfunc f(c bool, n int) {\n\tval s = \"ü\"\n\t/* ü */ if (c) n + 1 else Println(s)\n}\n"},
	{"function ends in no value", "package main\n\nfunc f() int {\n\tval s = \"ü\"\n\t/* ü */ Println(s)\n}\n"},
	{"if-expression branch values of different types", "package main\n\nfunc f(c bool, d bool) int {\n\tval s = \"ü\"\n\t/* ü */ val v = if (c) {\n\t\tif (d) { 1 } else { s }\n\t} else 2\n\t0\n}\n"},
	{"bind over a value that is no monad", "package main\n\nfunc f() Option[int] {\n\tval s = \"ü\"\n\t/* ü */ bind a = s.Size()\n\tSome(a)\n}\n"},
	{"bind annotated with another type", "package main\n\nfunc f() Option[int] {\n\tval s = \"ü\"\n\t/* ü */ bind a string = Some(s.Size())\n\tSome(1)\n}\n"},
	{"bind block with no value", "package main\n\nfunc f() Option[int] {\n\tval s = \"ü\"\n\t/* ü */ bind a = Some(s.Size())\n}\n"},
	{"also referencing its sibling", "package main\n\nfunc f() Option[int] {\n\tval s = \"ü\"\n\tbind a = Some(s.Size())\n\t/* ü */ also b = Some(a)\n\tSome(a + b)\n}\n"},
	{"also with no bind", "package main\n\nfunc f() Option[int] {\n\tval s = \"ü\"\n\t/* ü */ also b = Some(s.Size())\n\tSome(b)\n}\n"},
	{"bind ending in a value of another monad", "package main\n\nfunc f() Option[int] {\n\tval s = \"ü\"\n\tbind a = Some(s.Size())\n\t/* ü */ Success(a)\n}\n"},
	{"bind ending in an if with no else", "package main\n\nfunc f() Option[int] {\n\tval s = \"ü\"\n\tbind a = Some(s.Size())\n\t/* ü */ if (a > 0) {\n\t\tSome(a)\n\t}\n}\n"},
	{"return after a bind in an if-expression branch", "package main\n\nfunc f(c bool) Option[int] {\n\tval s = \"ü\"\n\tval v = if (c) {\n\t\t/* ü */ val k = s.Size()\n\t\tbind a = Some(k)\n\t\treturn None[int]()\n\t\tSome(a)\n\t} else None[int]()\n\tv\n}\n"},
	{"bind in an if statement", "package main\n\nfunc f(c bool) Option[int] {\n\tval s = \"ü\"\n\tif (c) {\n\t\t/* ü */ bind a = Some(s.Size())\n\t\tSome(a)\n\t}\n\tNone[int]()\n}\n"},
	{"forbidden keyword as a statement", "package main\n\nfunc f() {\n\tval s = \"ü\"\n\t/* ü */ defer\n\tPrintln(s)\n}\n"},
}

// TestNativeStmtWalk holds the native walk of stmtShapes and of each of
// stmtCases to its golden (checkWalk), and requires the native
// walk to have lowered the statements they mark.
func TestNativeStmtWalk(t *testing.T) {
	run := exprWalkRunner(t)
	t.Run("statement shapes", func(t *testing.T) {
		n := run(t, "statement shapes", stmtShapes)
		require.NoError(t, n.err, "the program does not transpile")
		for i, l := range strings.Split(stmtShapes, "\n") {
			if strings.HasSuffix(l, "// stmt") {
				assert.Truef(t, n.loweredStmtOn(i+1), "the native walk did not lower the statement on line %d: %s", i+1, strings.TrimSpace(l))
			}
		}
	})
	for _, c := range stmtCases {
		t.Run(c.name, func(t *testing.T) {
			n := run(t, c.name, c.src)
			require.Error(t, n.err, "the case transpiles")
			line, _ := bodyMarkerLine(c.src)
			assert.Truef(t, n.loweredStmtOn(line), "the native walk did not lower the statement on line %d; error: %v", line, n.err)
		})
	}
}
