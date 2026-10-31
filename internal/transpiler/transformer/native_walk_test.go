package transformer_test

import (
	"go/ast"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/bazelbuild/rules_go/go/tools/bazel"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	galaparser "martianoff/gala/internal/parser"
	"martianoff/gala/internal/parser/grammar"
	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/analyzer"
	"martianoff/gala/internal/transpiler/generator"
	"martianoff/gala/internal/transpiler/transformer"
)

// walkRun is one transpile of a file: its Go, or its error, how many of its
// top-level declarations the native walk lowered, and whether the analysis
// converted the parse's native tree, which the walk reads only then.
type walkRun struct {
	out       string
	err       error
	native    int
	converted bool
}

// walkPipeline transpiles files the way the CLI does, parsing with galop, each
// with an analyzer of its own: a package that fails to load stands an
// analyzer's undefined-symbol check down for every later file (the analysis
// still converts the tree the native walk reads). antlrOnly makes its
// transformer walk ANTLR's
// tree whatever the parse came with.
type walkPipeline struct {
	searchPaths []string
	antlrOnly   bool
}

func (w *walkPipeline) transpile(path, src string) walkRun {
	tr := transformer.NewGalaASTTransformer()
	var run walkRun
	transformer.OnNativeDecl(tr, func() { run.native++ })
	if w.antlrOnly {
		tr = withoutNative{tr}
	}
	p := &handleParser{GalaParser: transpiler.NewAntlrGalaParser()}
	run.out, run.err = transpiler.NewGalaToGoTranspiler(p, analyzer.NewGalaAnalyzer(p, w.searchPaths), tr, generator.NewGoCodeGenerator()).Transpile(src, path)
	run.converted = p.compiled.Converted()
	return run
}

// handleParser parses as its parser does and keeps the native-tree handle of
// the first parse, the compiled file's: the analysis parses the package's
// other files after it.
type handleParser struct {
	transpiler.GalaParser
	compiled *galaparser.Native
	parsed   bool
}

func (p *handleParser) ParseFile(input string) *galaparser.Parsed {
	parsed := p.GalaParser.ParseFile(input)
	if !p.parsed {
		p.compiled, p.parsed = parsed.Native, true
	}
	return parsed
}

// withoutNative transforms as its transformer does, but without the file's
// native-tree handle, so its walk reads ANTLR's tree after the same parse and
// analysis.
type withoutNative struct{ transpiler.ASTTransformer }

func (w withoutNative) Transform(richAST *transpiler.RichAST) (*token.FileSet, *ast.File, error) {
	richAST.Native = nil
	return w.ASTTransformer.Transform(richAST)
}

// walkCorpus is the paths of the GALA sources staged as the test's data
// (those TestEnginesAgree parses), sorted, and the search path that resolves
// their imports.
func walkCorpus(tb testing.TB) ([]string, []string) {
	stdFile, err := bazel.Runfile("std/option.gala")
	if err != nil {
		tb.Skipf("std sources not staged: %v", err)
	}
	files, err := bazel.ListRunfiles()
	require.NoError(tb, err, "cannot list the runfiles")
	var paths []string
	for _, f := range files {
		if !strings.HasSuffix(f.ShortPath, ".gala") {
			continue
		}
		if info, err := os.Stat(f.Path); err != nil || !info.Mode().IsRegular() {
			continue // a directory named *.gala, such as a tool's .gala cache
		}
		paths = append(paths, f.Path)
	}
	sort.Strings(paths)
	return paths, []string{filepath.Dir(filepath.Dir(stdFile))}
}

// topLevelDecls counts the top-level declarations of src's ANTLR tree, or -1
// when src does not parse cleanly with galop (no native tree to walk).
func topLevelDecls(src string) int {
	parsed := transpiler.NewAntlrGalaParser().ParseFile(src)
	if parsed.CleanNative() == nil {
		return -1
	}
	return len(parsed.Tree.(*grammar.SourceFileContext).AllTopLevelDeclaration())
}

// checkWalksAgree fails tb unless the native and the ANTLR walk of path give
// the same Go, or the same error, and each walk took the tree it should: the
// native walk lowered every top-level declaration of the file (each one up to
// the failing one, on an error) when the analysis converted its native tree,
// and none when it did not, and the ANTLR walk none.
func checkWalksAgree(tb testing.TB, nativeW, antlrW *walkPipeline, path, src string) (native walkRun, ok bool) {
	tb.Helper()
	want := topLevelDecls(src)
	if want < 0 {
		return walkRun{}, false
	}
	n := nativeW.transpile(path, src)
	a := antlrW.transpile(path, src)
	assert.Zerof(tb, a.native, "%s: the ANTLR walk lowered declarations on the native tree", path)
	switch {
	case !n.converted:
		assert.Zerof(tb, n.native, "%s: the native walk lowered declarations on a tree the analysis did not convert", path)
	case n.err == nil:
		assert.Equalf(tb, want, n.native, "%s: top-level declarations the native walk lowered", path)
	default:
		assert.LessOrEqualf(tb, n.native, want, "%s: the native walk lowered more declarations than there are (error: %v)", path, n.err)
	}
	if n.out != a.out {
		assert.Failf(tb, "the walks' Go differs", "%s: the native walk's Go differs from the ANTLR walk's at %s", path, firstLineDiff(n.out, a.out))
	}
	assert.Equalf(tb, a.err, n.err, "%s: the native walk's error differs from the ANTLR walk's", path)
	return n, true
}

// firstLineDiff describes the first line where x and y differ.
func firstLineDiff(x, y string) string {
	xl, yl := strings.Split(x, "\n"), strings.Split(y, "\n")
	for i := 0; i < len(xl) || i < len(yl); i++ {
		var a, b string
		if i < len(xl) {
			a = xl[i]
		}
		if i < len(yl) {
			b = yl[i]
		}
		if a != b {
			return "line " + strconv.Itoa(i+1) + ":\n native: " + a + "\n  ANTLR: " + b
		}
	}
	return "nowhere"
}

// TestNativeWalkAgrees holds the transformer's walk of the compiled file's
// top-level declarations on its native tree to its walk on ANTLR's: every
// GALA source staged as data, transpiled as the CLI transpiles a file, gives
// byte-identical Go, or the same error, both ways. Each file's walk must have
// taken the tree it should (checkWalksAgree), and most must have taken the
// native one, so a bridge or converter that fails quietly cannot turn the
// comparison into ANTLR's against ANTLR's.
func TestNativeWalkAgrees(t *testing.T) {
	galaparser.RequestEngine(string(galaparser.EngineGalop))
	t.Cleanup(func() { galaparser.RequestEngine("") })
	paths, searchPaths := walkCorpus(t)
	nativeW, antlrW := &walkPipeline{searchPaths: searchPaths}, &walkPipeline{searchPaths: searchPaths, antlrOnly: true}
	files, clean, walked, decls := 0, 0, 0, 0
	for _, path := range paths {
		src, err := os.ReadFile(path)
		require.NoError(t, err)
		n, ok := checkWalksAgree(t, nativeW, antlrW, path, string(src))
		if !ok {
			continue
		}
		files++
		decls += n.native
		if n.err == nil {
			clean++
		}
		if n.converted {
			walked++
		}
	}
	// Floors catch the sources not being staged or the native walk going
	// unused.
	require.Truef(t, files >= 200 && clean >= files*3/4 && walked >= files*3/4 && decls >= files,
		"%d sources, %d of them transpiled, %d walked on the native tree, %d top-level declarations lowered there", files, clean, walked, decls)
	t.Logf("%d sources, %d of them transpiled, %d walked on the native tree, %d top-level declarations lowered there", files, clean, walked, decls)
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
}

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
			n, ok := checkWalksAgree(t, nativeW, antlrW, path, c.src)
			require.True(t, ok, "the case does not parse cleanly with galop")
			// The first case is a program that transpiles, so every
			// declaration shape in it is lowered to the end.
			if c.name == walkCases[0].name {
				require.NoError(t, n.err, "the case does not transpile")
			}
			require.Truef(t, n.converted, "the analysis did not convert the native tree, so the walk read ANTLR's; error: %v", n.err)
			t.Logf("the native walk lowered %d declarations; error: %v", n.native, n.err)
		})
	}
}
