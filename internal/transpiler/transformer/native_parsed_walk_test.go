package transformer_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"martianoff/gala/galaerr"
	galaparser "martianoff/gala/internal/parser"
	"martianoff/gala/internal/transpiler/transformer"
)

// parsedShapes is a program whose default values and interpolated
// expressions the native walk parses on trees of their own (parseExprAt) and
// lowers there. Each line marked `// default: <kinds>` or `// interpolation:
// <kinds>` (both, split by "; ") holds a default or an interpolation whose
// nodes of those kinds (stmt, lambda, expr, case) the native walk must have
// read on such a tree; the multibyte text before each puts its columns past
// its bytes.
const parsedShapes = `package main

sealed type Shape {
	case Circle(r int)
	case Empty()
}

func apply(x int, f func(int) int = (a) => a + 1) int = f(x) // default: lambda, expr

func pick(s Shape, f func(Shape) Option[int] = { case Circle(r) => r }) int = f(s).GetOrElse(0) // default: case

func label(s string = "ü", n int = "ü".Size() * 2) string = s"${s}:${n}" // default: expr; interpolation: expr

func hook(cb func() = nil) bool = cb == nil // a nil default is read, not lowered

func block(f func(int) int = (a) => { // default: lambda
	val d = a * 2 // default: stmt
	d + 1 // default: expr
}) int = f(1)

func tried(n int = Try(() => 6 / 2).GetOrElse(0)) int = n // default: lambda, expr

struct Cfg(name string = "é", size int = 3, tags Option[string] = None[string]()) // default: expr

type Box struct {
	n int
}

func (b Box) Scaled(k int = b.n * 2) int = b.n * k // default: expr

func main() {
	val x = 41
	val c = Cfg()
	Println(apply(1), pick(Circle(2)), label(), hook(), block(), tried(), Try(() => apply(2)).GetOrElse(0), c.size, Box{n: 3}.Scaled())
	Println(s"üü ${x + 1} ${c.name}", f"é ${x}%5d") // interpolation: expr
	Println(s"ü ${x * 2} and ${(if (x > 1) "bïg" else "small")} ${Box{n: x}.n}") // interpolation: expr
}
`

// parsedMarkers is each line of src that marks what the native walk read
// there on a tree of its own (see parsedShapes): the origin and the kinds.
func parsedMarkers(src string) map[parsedLine][]string {
	origins := map[string]string{"default": parsedDefault, "interpolation": parsedInterp}
	marks := map[parsedLine][]string{}
	for i, l := range strings.Split(src, "\n") {
		_, comment, _ := strings.Cut(l, "// ")
		for _, m := range strings.Split(comment, "; ") {
			origin, kinds, ok := strings.Cut(m, ": ")
			if what := origins[origin]; ok && what != "" {
				marks[parsedLine{what: what, line: i + 1}] = strings.Split(kinds, ", ")
			}
		}
	}
	return marks
}

// parsedCases cover each diagnostic a default value or an interpolated
// expression reports: the line of each case's `/* ü */` marker is where it is
// reported, after multibyte text and at the offset its own parse starts at,
// and kind names how the native walk must have read that line: a kind of
// node read on a tree of its own of the case's origin (stmt, expr; see
// parsedShapes), or a check's shape (variant type, codec name) that reports it
// there.
var parsedCases = []struct{ name, what, kind, src string }{
	{"a default fails to lower", parsedDefault, "expr", "package main\n\n/* ü */ func f(s string = \"ü\", n int = Some(1).Size()) int = n\n\nfunc main() {\n\tPrintln(f())\n}\n"},
	{"a lambda default fails on its second line", parsedDefault, "stmt", "package main\n\nfunc f(g func(int) int = (a) => {\n/* ü */ \tval d = Some(a).Size()\n\td\n}) int = g(1)\n\nfunc main() {\n\tPrintln(f())\n}\n"},
	{"a struct field default fails", parsedDefault, "expr", "package main\n\n/* ü */ struct Cfg(s string = \"é\", n int = Some(1).Size())\n\nfunc main() {\n\tPrintln(Cfg())\n}\n"},
	{"a method default fails", parsedDefault, "expr", "package main\n\ntype Box struct {\n\tn int\n}\n\n/* ü */ func (b Box) Get(k int = Some(b.n).Size()) int = k\n\nfunc main() {\n\tPrintln(Box{n: 1}.Get())\n}\n"},
	{"an interpolation fails", parsedInterp, "expr", "package main\n\nfunc main() {\n\tval s = \"ü\"\n\t/* ü */ Println(s\"üé ${Some(s).Size()}\")\n}\n"},
	{"a format interpolation fails", parsedInterp, "expr", "package main\n\nfunc main() {\n\tval s = \"ü\"\n\t/* ü */ Println(f\"é ${Some(s).Size()}%5d\")\n}\n"},
	{"a later interpolation part fails", parsedInterp, "expr", "package main\n\nfunc main() {\n\tval s = \"ü\"\n\t/* ü */ Println(s\"é ${s} ü ${\"ä\" + s} ${Some(s).Size()}\")\n}\n"},
	{"an interpolation names a variant as a type", parsedInterp, "variant type", variantSealed + "func main() {\n\t/* ü */ Println(s\"é ${(c Circle) => 1}\")\n}\n"},
	{"an interpolation names generated codec metadata", parsedInterp, "codec name", "package main\n\nstruct P(x int)\n\nfunc main() {\n\t/* ü */ Println(s\"é ${StructMeta_P{}}\")\n}\n"},
}

// TestNativeParsedWalk holds the native walk of parsedShapes and of each of
// parsedCases to the ANTLR walk (checkWalksAgree), which also holds the two
// to the same diagnostic, at the same line and column, and requires the
// native walk to have read the lines they mark on the native tree.
func TestNativeParsedWalk(t *testing.T) {
	run := exprWalkRunner(t)
	t.Run("parsed shapes", func(t *testing.T) {
		n := run(t, "parsed shapes", parsedShapes)
		require.NoError(t, n.err, "the program does not transpile")
		marks := parsedMarkers(parsedShapes)
		require.NotEmpty(t, marks)
		for at, kinds := range marks {
			read := n.parsedOn(at.what, n.path, at.line)
			for _, kind := range kinds {
				assert.Truef(t, read[kind], "the native walk read no %s on a tree of its own of a %s on line %d (it read %v)", kind, at.what, at.line, read)
			}
		}
	})
	for _, c := range parsedCases {
		t.Run(c.name, func(t *testing.T) {
			n := run(t, c.name, c.src)
			require.Error(t, n.err, "the case transpiles")
			line, _ := bodyMarkerLine(c.src)
			var se *galaerr.SemanticError
			require.Truef(t, errors.As(n.err, &se), "not a semantic error: %v", n.err)
			assert.Equalf(t, line, se.Line, "reported on another line: %v", n.err)
			read := n.parsedOn(c.what, n.path, line)
			assert.Truef(t, read[c.kind] || n.shapes[line][c.kind], "the native walk read no %s on line %d (on a tree of its own of a %s %v, shapes %v); error: %v", c.kind, line, c.what, read, n.shapes[line], n.err)
		})
	}
}

// TestNativeParsedWalkFallback holds a native walk whose defaults and
// interpolations all fall back to ANTLR's parser (RefuseNativeParse), as when
// galop's parser does not settle them, to the ANTLR walk on parsedShapes and
// on each of parsedCases: the same Go, or the same diagnostic, with nothing
// parsed natively. Its defaults' lambdas, a Try thunk among them, are then
// lowered from their contexts alone, in a file whose own lambdas, a Try thunk
// calling a lambda default among them, are lowered from their nodes alone
// (lambdaSrc.is).
func TestNativeParsedWalkFallback(t *testing.T) {
	run := walkRunner(t, walkPipeline{refuseNative: true})
	check := func(t *testing.T, name, src string) walkRun {
		n := run(t, name, src)
		assert.Emptyf(t, n.parsedAt, "the native walk parsed with galop's native parser, which it was refused")
		return n
	}
	t.Run("parsed shapes", func(t *testing.T) {
		require.NoError(t, check(t, "parsed shapes", parsedShapes).err, "the program does not transpile")
	})
	for _, c := range parsedCases {
		t.Run(c.name, func(t *testing.T) {
			require.Error(t, check(t, c.name, c.src).err, "the case transpiles")
		})
	}
}

// TestNativeParsedWalkForeign holds the two walks to the same Go, or the same
// diagnostic, for defaults declared in another package, whose names resolve
// where they are written (outOfLexicalScope): one that lowers, and two whose
// names only the using file's imports declare, reported at their own token in
// the default's file.
func TestNativeParsedWalkForeign(t *testing.T) {
	galaparser.RequestEngine(string(galaparser.EngineGalop))
	t.Cleanup(func() { galaparser.RequestEngine("") })
	_, searchPaths := walkCorpus(t)
	root := t.TempDir()
	write := func(rel, content string) string {
		t.Helper()
		full := filepath.Join(root, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o755))
		require.NoError(t, os.WriteFile(full, []byte(content), 0o644))
		return full
	}
	write("gala.mod", "module example.com/parsed\n\ngala dev\n")
	write("pa/pa.gala", "package pa\n\nsealed type KindA {\n    case Same()\n    case OnlyA()\n}\n")
	write("pb/pb.gala", "package pb\n\nsealed type KindB {\n    case Same()\n    case OnlyB()\n}\n")
	own := write("lib/own.gala", `package lib

import . "example.com/parsed/pa"

func Matched(s string = Some(Same()) match {
    case Some(Same()) => "ü"
    case _ => "other"
}) string = s
`)
	// The constructor and the qualifier these defaults name are pb's, which
	// this file does not import.
	borrowed := write("lib/borrowed.gala", `package lib

val Thing any = 1

func Bare(s string = Thing match {
    case /* ü */ OnlyB() => "b"
    case _ => "other"
}) string = s

func Qualified(s string = Thing match {
    /* é */ case pb.OnlyB() => "b"
    case _ => "other"
}) string = s
`)
	nativeW := &walkPipeline{searchPaths: append([]string{root}, searchPaths...), root: root}
	antlrW := &walkPipeline{searchPaths: nativeW.searchPaths, root: root, antlrOnly: true}
	cases := []struct {
		name, using, call string
		line, col         int // where the diagnostic is, in lib/borrowed.gala; 0 for none
	}{
		{"a default that lowers", `"example.com/parsed/pb"`, "lib.Matched()", 0, 0},
		{"a bare constructor only the using file's dot import declares", `. "example.com/parsed/pb"`, "lib.Bare()", 6, 17},
		{"a qualifier only the using file binds", `"example.com/parsed/pb"`, "lib.Qualified()", 11, 17},
	}
	for i, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			src := "package main\n\nimport (\n    " + c.using + "\n    \"example.com/parsed/lib\"\n)\n\nfunc main() {\n    Println(" + c.call + ")\n}\n"
			path := write("main"+string(rune('a'+i))+"/main.gala", src)
			n := agreedWalk(t, nativeW, antlrW, path, src)
			// The first case clause of each default is on line 6 of its file, and
			// the rejected one's is where the diagnostic is.
			read, file := 6, own
			if c.line != 0 {
				read, file = c.line, borrowed
			}
			assert.Truef(t, n.parsedOn(parsedDefault, file, read)["case"], "the native walk read no case clause of the default on line %d of %s on a tree of its own (it read %v); error: %v", read, file, n.parsedAt, n.err)
			if c.line == 0 {
				require.NoError(t, n.err)
				return
			}
			var se *galaerr.SemanticError
			require.Truef(t, errors.As(n.err, &se), "not a semantic error: %v", n.err)
			assert.Equal(t, galaerr.CodeUndefinedVariable, se.Code, "%v", n.err)
			assert.True(t, strings.HasSuffix(filepath.ToSlash(se.FilePath), "lib/borrowed.gala"), "reported in the default's file, got %q", se.FilePath)
			assert.Equal(t, c.line, se.Line, "%v", n.err)
			assert.Equal(t, c.col, se.Column, "%v", n.err)
		})
	}
}

// TestEscapeCacheKeysNodesByTree: whether a construct's subtree escapes is
// cached per native node, and a default's or an interpolated expression's
// tree numbers its nodes from 0 like the file's, so the cache must key a node
// with its tree: the answer for one tree's node is not another's.
func TestEscapeCacheKeysNodesByTree(t *testing.T) {
	ret, err := galaparser.ParseExpressionNativeAt("if (true) { return 1 } else 2", 1, 0)
	require.NoError(t, err)
	plain, err := galaparser.ParseExpressionNativeAt("if (true) { 1 + 2 + 3 + 4 } else 5 + 6 + 7 + 8 + 9", 1, 0)
	require.NoError(t, err)
	tree := plain.Tree()
	require.Less(t, ret.Index(), len(tree.Nodes), "the plain tree must have a node at the returning root's index")
	same := tree.Node(ret.Index())
	tr := transformer.NewGalaASTTransformer()
	assert.True(t, transformer.EscapesAt(tr, ret.GalaNodeRef), "a branch that returns escapes")
	assert.False(t, transformer.EscapesAt(tr, same), "the other tree's node at the same index took the first one's cached answer")
}
