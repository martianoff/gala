package transformer_test

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/bazelbuild/rules_go/go/tools/bazel"

	galaparser "martianoff/gala/internal/parser"
	"martianoff/gala/internal/parser/grammar"
	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/analyzer"
	"martianoff/gala/internal/transpiler/generator"
	"martianoff/gala/internal/transpiler/transformer"
)

// walkRun is one transpile of a file: its Go, or its error, and how many of
// its top-level declarations the native walk lowered.
type walkRun struct {
	out    string
	err    error
	native int
}

// walkPipeline transpiles files the way the CLI does, parsing with galop, with
// one analyzer for every file; antlrOnly makes its transformer walk ANTLR's
// tree whatever the parse came with.
type walkPipeline struct {
	p         transpiler.GalaParser
	a         transpiler.Analyzer
	antlrOnly bool
}

func newWalkPipeline(searchPaths []string, antlrOnly bool) *walkPipeline {
	p := transpiler.NewAntlrGalaParser()
	return &walkPipeline{p: p, a: analyzer.NewGalaAnalyzer(p, searchPaths), antlrOnly: antlrOnly}
}

func (w *walkPipeline) transpile(path, src string) walkRun {
	tr := transformer.NewGalaASTTransformer()
	var run walkRun
	transformer.SetWalkHooks(tr, w.antlrOnly, func() { run.native++ })
	run.out, run.err = transpiler.NewGalaToGoTranspiler(w.p, w.a, tr, generator.NewGoCodeGenerator()).Transpile(src, path)
	return run
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
	if err != nil {
		tb.Fatalf("cannot list the runfiles: %v", err)
	}
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
func topLevelDecls(p transpiler.GalaParser, src string) int {
	parsed := p.ParseFile(src)
	if parsed.CleanNative() == nil {
		return -1
	}
	return len(parsed.Tree.(*grammar.SourceFileContext).AllTopLevelDeclaration())
}

// checkWalksAgree fails tb unless the native and the ANTLR walk of path give
// the same Go, or the same error, and the native walk lowered every top-level
// declaration of the file (each one up to the failing one, on an error) while
// the ANTLR walk lowered none.
func checkWalksAgree(tb testing.TB, nativeW, antlrW *walkPipeline, path, src string) (native walkRun, ok bool) {
	tb.Helper()
	want := topLevelDecls(nativeW.p, src)
	if want < 0 {
		return walkRun{}, false
	}
	n := nativeW.transpile(path, src)
	a := antlrW.transpile(path, src)
	switch {
	case a.native != 0:
		tb.Errorf("%s: the ANTLR walk lowered %d declarations on the native tree", path, a.native)
	case n.err == nil && n.native != want, n.err != nil && n.native > want:
		tb.Errorf("%s: the native walk lowered %d of %d top-level declarations (error: %v)", path, n.native, want, n.err)
	case n.out != a.out:
		tb.Errorf("%s: the native walk's Go differs from the ANTLR walk's at %s", path, firstLineDiff(n.out, a.out))
	case !reflect.DeepEqual(n.err, a.err):
		tb.Errorf("%s: the native walk's error differs from the ANTLR walk's:\n native: %#v\n  ANTLR: %#v", path, n.err, a.err)
	}
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
// byte-identical Go, or the same error, both ways. Each file's native walk
// must have lowered each of its top-level declarations (checkWalksAgree), so
// a bridge or converter that fails quietly cannot turn the comparison into
// ANTLR's against ANTLR's.
func TestNativeWalkAgrees(t *testing.T) {
	galaparser.RequestEngine(string(galaparser.EngineGalop))
	t.Cleanup(func() { galaparser.RequestEngine("") })
	paths, searchPaths := walkCorpus(t)
	nativeW, antlrW := newWalkPipeline(searchPaths, false), newWalkPipeline(searchPaths, true)
	files, clean, decls := 0, 0, 0
	for _, path := range paths {
		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if n, ok := checkWalksAgree(t, nativeW, antlrW, path, string(src)); ok {
			files++
			decls += n.native
			if n.err == nil {
				clean++
			}
		}
	}
	// Floors catch the sources not being staged or the native walk going
	// unused.
	if files < 200 || clean < files*3/4 || decls < files {
		t.Fatalf("walked %d sources, %d of them transpiled, %d top-level declarations on the native tree", files, clean, decls)
	}
	t.Logf("walked %d sources, %d of them transpiled, %d top-level declarations on the native tree", files, clean, decls)
}

// walkCases cover every top-level declaration shape the native walk reads,
// and each diagnostic it positions, after multibyte text too: a position
// read off the wrong tree, or a column counted in bytes, shows as a
// differing error.
var walkCases = []struct{ name, src string }{
	{"all shapes", `// Package main is documented.
package main

import (
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
	nativeW, antlrW := newWalkPipeline(searchPaths, false), newWalkPipeline(searchPaths, true)
	dir := t.TempDir()
	for _, c := range walkCases {
		t.Run(c.name, func(t *testing.T) {
			path := filepath.Join(dir, strings.ReplaceAll(c.name, " ", "_"), "main.gala")
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(c.src), 0o644); err != nil {
				t.Fatal(err)
			}
			n, ok := checkWalksAgree(t, nativeW, antlrW, path, c.src)
			if !ok {
				t.Fatalf("the case does not parse cleanly with galop")
			}
			// The first case is a program that transpiles, so every
			// declaration shape in it is lowered to the end.
			if c.name == walkCases[0].name && n.err != nil {
				t.Fatalf("the case does not transpile: %v", n.err)
			}
			t.Logf("the native walk lowered %d declarations; error: %v", n.native, n.err)
		})
	}
}
