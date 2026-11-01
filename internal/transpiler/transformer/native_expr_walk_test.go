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

// exprShapes is a program with every expression shape the native walk
// lowers. Each line marked `// native: <shapes>` holds an expression the
// native walk must lower itself, not hand to ANTLR's tree, and the shapes
// it must have lowered on that line (nativeShape).
const exprShapes = `package main

import (
	"fmt"
	"strconv"
)

struct P(x int, var y int)

type Celsius float64

val limit = 10 // native: literal

var hits = 0

func id[T any](v T) T = v // native: name

func ops(a int, b int, c bool, d bool) bool {
	val sum = a + b - a * b / 2 % 3 // native: operator, name, literal
	val bits = a | b ^ a & b &^ 1 << 2 >> 1 // native: operator, name, literal
	val rel = sum < bits || sum <= bits && sum > 0 || sum >= 0 // native: operator, name, literal
	val eq = a == b != c // native: operator, name
	val neg = -a + +b // native: operator, unary, name
	val flip = ^a // native: unary, name
	val not = !c && !(d || c) // native: operator, unary, parens, name
	Println(sum, bits, rel, eq, neg, flip, not) // native: name
	rel || d // native: operator, name
}

func literals() {
	val i = 42 // native: literal
	val o = 0644 // native: literal
	val h = 0xFF // native: literal
	val f = 1.5e3 // native: literal
	val r = 'ü' // native: literal
	val s = "ä\tö\n" // native: literal
	val raw = ` + "`raw ü \\d`" + ` // native: literal
	val t, u, n = true, false, nil // native: literal
	val interp = s"i=$i ü ${i + 1} ${s.Size()}" // native: literal
	val format = f"${f}%.2f ${i}%05d ü" // native: literal
	Println(i, o, h, f, r, s, raw, t, u, n, interp, format) // native: name
}

func names(p P) int {
	var local = 1 // native: literal
	local = local + limit // native: operator, name
	hits = hits + 1 // native: operator, name, literal
	val pair = (local, "ü") // native: parens, name, literal
	val grouped = (local + 1) * 2 // native: operator, parens, name, literal
	val ptr = &local // native: unary, name
	val deref = *ptr // native: unary, name
	Println(pair, grouped, deref, Celsius(1.5), fmt.Sprint(local)) // native: name, literal
	id(local) + p.x // native: operator, name
}

func branches(c bool, n int) int {
	val a = if (c) 1 else 2 // native: if, name, literal
	val b = if (n > 0) { // native: if, operator, name, literal
		val m = n * 2
		m + 1
	} else {
		0
	}
	val o = if (c) Some(n) else None[int]() // native: if, name
	if (c) Println("ü") else Println("ö") // native: name, literal
	for i := 0; i < n; i++ { // native: literal
		if i % 2 == 0 { // native: operator, name, literal
			continue
		}
		val v = if (i > 3) i else { // native: if, operator, name, literal
			return i
		}
		Println(v)
	}
	for c && n > 100 { // native: operator, name, literal
		break
	}
	for k, ch := range "äö" { // native: literal
		Println(k, ch)
	}
	val parsed = strconv.Itoa(n) // native: name
	Println(parsed, o) // native: name
	a + b // native: operator, name
}

func main() {
	Println(ops(1, 2, true, false), names(P(1, 2)), branches(true, 5)) // native: literal
	literals()
}
`

// exprCases cover each diagnostic the native expression walk positions,
// after multibyte text: a position read off the wrong tree, or a column
// counted in bytes, shows as a differing error. The expression on the line
// of each case's `/* ü */` marker must be lowered on the native tree.
var exprCases = []struct{ name, src string }{
	{"octal literal", "package main\n\nfunc f() int {\n\tval x = /* ü */ 0829\n\tx\n}\n"},
	{"string escape", "package main\n\nfunc f() string {\n\tval x = /* ü */ \"äö\\d\"\n\tx\n}\n"},
	{"rune escape", "package main\n\nfunc f() rune {\n\tval x = /* ü */ '\\d'\n\tx\n}\n"},
	{"interpolated escape", "package main\n\nfunc f() string {\n\tval x = /* ü */ s\"ä${1}\\d\"\n\tx\n}\n"},
	{"format escape", "package main\n\nfunc f() string {\n\tval x = /* ü */ f\"ä${1}%d\\q\"\n\tx\n}\n"},
	{"interpolated syntax error", "package main\n\nfunc f() string {\n\tval x = /* ü */ s\"ä ${1 +}\"\n\tx\n}\n"},
	{"break as a value", "package main\n\nfunc f() {\n\tfor {\n\t\tval x = /* ü */ 1 + break\n\t\tPrintln(x)\n\t}\n}\n"},
	{"empty parentheses", "package main\n\nfunc f() {\n\tval x = /* ü */ ()\n\tPrintln(x)\n}\n"},
	{"Go call as an operand", "package main\n\nimport \"strconv\"\n\nfunc f() int {\n\t/* ü */ strconv.Atoi(\"1\") * 2\n}\n"},
	{"Ptr compared with nil", "package main\n\nstruct P(x int)\n\nfunc f(p Ptr[P]) bool {\n\t/* ü */ p == nil\n}\n"},
	{"incomparable operands", "package main\n\nfunc f() bool {\n\tval g = (x int) => x\n\t/* ü */ g == g\n}\n"},
	{"address of a val", "package main\n\nfunc f() {\n\tval x = 1\n\tval p = /* ü */ &x\n\tPrintln(p)\n}\n"},
	{"branch with no value", "package main\n\nfunc f(c bool) int {\n\tval x = /* ü */ if (c) 1 else { Println(\"ö\") }\n\tx\n}\n"},
	{"untyped if-expression", "package main\n\nfunc f(c bool) {\n\tval x = /* ü */ if (c) nil else nil\n\tPrintln(x)\n}\n"},
	{"loop control in an if-expression", "package main\n\nfunc f(c bool) {\n\tfor {\n\t\tPrintln(/* ü */ if (c) 1 else break)\n\t}\n}\n"},
}

// keywordCases are expression statements that are a Go-only keyword: the
// name is read on the native tree (exprSrc.bareName), and the statement is
// rejected before any expression of it is lowered. (A keyword matched on,
// `select match {...}`, is the analyzer's GALA-E0055 before it gets here.)
var keywordCases = []struct{ name, src string }{
	{"forbidden keyword", "package main\n\nfunc f() {\n\tPrintln(\"ü\")\n\t/* ü */ goto\n}\n"},
}

// exprWalkRunner selects galop for the test and returns the function that
// writes a program to a file of its own, holds the walk of it to its golden
// (checkWalk), and returns the walk's run.
func exprWalkRunner(t *testing.T) func(t *testing.T, name, src string) walkRun {
	return walkRunner(t, galaparser.EngineGalop, walkPipeline{})
}

// walkRunner is exprWalkRunner with engine parsing and w's options for the
// walk.
func walkRunner(t *testing.T, engine galaparser.Engine, w walkPipeline) func(t *testing.T, name, src string) walkRun {
	galaparser.RequestEngine(string(engine))
	t.Cleanup(func() { galaparser.RequestEngine("") })
	_, searchPaths := walkCorpus(t)
	w.searchPaths = searchPaths
	dir := t.TempDir()
	return func(t *testing.T, name, src string) walkRun {
		path := filepath.Join(dir, strings.ReplaceAll(name, " ", "_"), "main.gala")
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(src), 0o644))
		return goldenWalk(t, &w, path, src)
	}
}

// goldenWalk holds the walk of src, written at path, to its golden
// (checkWalk), requires the walk to have read the native tree, and returns
// its run.
func goldenWalk(t *testing.T, w *walkPipeline, path, src string) walkRun {
	n, _, ok := checkWalk(t, w, path, src, true)
	require.True(t, ok, "the case does not parse cleanly with galop")
	require.Truef(t, n.converted, "the analysis did not convert the native tree, so the walk read ANTLR's; error: %v", n.err)
	t.Logf("the native walk lowered %d expressions; error: %v", n.exprs, n.err)
	return n
}

// checkMarkedShapes requires n, the native walk of src, to have transpiled
// src and, on each line marked `// native: <shapes>`, to have lowered an
// expression and each of the shapes the marker names.
func checkMarkedShapes(t *testing.T, n walkRun, src string) {
	require.NoError(t, n.err, "the program does not transpile")
	for i, l := range strings.Split(src, "\n") {
		_, marked, ok := strings.Cut(l, "// native: ")
		if !ok {
			continue
		}
		assert.Truef(t, n.exprLines[i+1], "the native walk did not lower the expression on line %d: %s", i+1, strings.TrimSpace(l))
		for _, shape := range strings.Split(marked, ", ") {
			assert.Truef(t, n.shapes[i+1][shape], "the native walk lowered no %s on line %d (it lowered %v): %s", shape, i+1, n.shapes[i+1], strings.TrimSpace(l))
		}
	}
}

// TestNativeExprWalk holds the native walk of exprShapes and of each of
// exprCases to its golden (checkWalk), and requires the native
// walk to have lowered the expressions they mark.
func TestNativeExprWalk(t *testing.T) {
	run := exprWalkRunner(t)
	t.Run("expression shapes", func(t *testing.T) {
		checkMarkedShapes(t, run(t, "expression shapes", exprShapes), exprShapes)
	})
	for _, c := range exprCases {
		t.Run(c.name, func(t *testing.T) {
			n := run(t, c.name, c.src)
			require.Error(t, n.err, "the case transpiles")
			line, _ := bodyMarkerLine(c.src)
			assert.Truef(t, n.exprLines[line], "the native walk did not lower the expression on line %d; error: %v", line, n.err)
		})
	}
	for _, c := range keywordCases {
		t.Run(c.name, func(t *testing.T) {
			n := run(t, c.name, c.src)
			require.Error(t, n.err, "the case transpiles")
			assert.Contains(t, n.err.Error(), "GALA-E0036")
			line, _ := bodyMarkerLine(c.src)
			assert.Truef(t, n.stmtLines[line], "the native walk did not read the statement on line %d; error: %v", line, n.err)
		})
	}
}
