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
	stmts     int // the statements the native walk lowered, each counted once
	converted bool
}

// walkPipeline transpiles files the way the CLI does, parsing with galop, each
// with an analyzer of its own, so no package-load failure carries over from
// one file to the next. antlrOnly makes its transformer walk ANTLR's tree
// whatever the parse came with.
type walkPipeline struct {
	searchPaths []string
	antlrOnly   bool
}

func (w *walkPipeline) transpile(path, src string) walkRun {
	tr := transformer.NewGalaASTTransformer()
	var run walkRun
	transformer.OnNativeDecl(tr, func() { run.native++ })
	stmts := map[int]bool{}
	transformer.OnNativeStmt(tr, func(index int) { stmts[index] = true })
	if w.antlrOnly {
		tr = withoutNative{tr}
	}
	p := &handleParser{GalaParser: transpiler.NewAntlrGalaParser()}
	run.out, run.err = transpiler.NewGalaToGoTranspiler(p, analyzer.NewGalaAnalyzer(p, w.searchPaths), tr, generator.NewGoCodeGenerator()).Transpile(src, path)
	run.converted = p.compiled.Converted()
	run.stmts = len(stmts)
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

// treeCounts is what the native walk of a file must lower, counted on its
// ANTLR tree: its top-level declarations, and the statements of its
// top-level functions' bodies (bodyStatements).
type treeCounts struct{ decls, stmts int }

// countTree counts src's ANTLR tree, and false when src does not parse
// cleanly with galop (no native tree to walk).
func countTree(src string) (treeCounts, bool) {
	parsed := transpiler.NewAntlrGalaParser().ParseFile(src)
	if parsed.CleanNative() == nil {
		return treeCounts{}, false
	}
	sf := parsed.Tree.(*grammar.SourceFileContext)
	return treeCounts{decls: len(sf.AllTopLevelDeclaration()), stmts: bodyStatements(sf)}, true
}

// bodyStatements counts the statements the native walk lowers in sf's
// top-level function bodies: each statement of a body, and of the if and for
// blocks in it, up to and including a block's first `bind`, whose chain
// lowers the statements after it on ANTLR's tree. A block below an
// expression (a lambda's, a match arm's) is lowered on ANTLR's tree too.
func bodyStatements(sf *grammar.SourceFileContext) int {
	n := 0
	var block func(grammar.IBlockContext)
	var ifBlocks func(grammar.IIfStatementContext)
	block = func(b grammar.IBlockContext) {
		if b == nil {
			return
		}
		for _, s := range b.AllStatement() {
			n++
			d := s.Declaration()
			switch {
			case d == nil:
			case d.BindDeclaration() != nil:
				return
			case d.IfStatement() != nil:
				ifBlocks(d.IfStatement())
			case d.ForStatement() != nil:
				block(d.ForStatement().Block())
			}
		}
	}
	ifBlocks = func(s grammar.IIfStatementContext) {
		for _, b := range s.AllBlock() {
			block(b)
		}
		if elseIf := s.IfStatement(); elseIf != nil {
			ifBlocks(elseIf)
		}
	}
	for _, td := range sf.AllTopLevelDeclaration() {
		if fd := td.FunctionDeclaration(); fd != nil {
			block(fd.Block())
		}
	}
	return n
}

// checkWalksAgree fails tb unless the native and the ANTLR walk of path give
// the same Go, or the same error, and each walk took the tree it should: the
// native walk lowered every top-level declaration of the file and every
// statement of its function bodies (each one up to the failing one, on an
// error) when the analysis converted its native tree, and none when it did
// not, and the ANTLR walk none.
func checkWalksAgree(tb testing.TB, nativeW, antlrW *walkPipeline, path, src string) (native walkRun, ok bool) {
	tb.Helper()
	want, ok := countTree(src)
	if !ok {
		return walkRun{}, false
	}
	n := nativeW.transpile(path, src)
	a := antlrW.transpile(path, src)
	assert.Zerof(tb, a.native, "%s: the ANTLR walk lowered declarations on the native tree", path)
	assert.Zerof(tb, a.stmts, "%s: the ANTLR walk lowered statements on the native tree", path)
	switch {
	case !n.converted:
		assert.Zerof(tb, n.native, "%s: the native walk lowered declarations on a tree the analysis did not convert", path)
		assert.Zerof(tb, n.stmts, "%s: the native walk lowered statements on a tree the analysis did not convert", path)
	case n.err == nil:
		assert.Equalf(tb, want.decls, n.native, "%s: top-level declarations the native walk lowered", path)
		assert.Equalf(tb, want.stmts, n.stmts, "%s: body statements the native walk lowered", path)
	default:
		assert.LessOrEqualf(tb, n.native, want.decls, "%s: the native walk lowered more declarations than there are (error: %v)", path, n.err)
		assert.LessOrEqualf(tb, n.stmts, want.stmts, "%s: the native walk lowered more statements than there are (error: %v)", path, n.err)
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
// top-level declarations and function-body statements on its native tree to
// its walk on ANTLR's: every GALA source staged as data, transpiled as the
// CLI transpiles a file, gives byte-identical Go, or the same error, both
// ways. Each file's walk must have
// taken the tree it should (checkWalksAgree), and most must have taken the
// native one, so a bridge or converter that fails quietly cannot turn the
// comparison into ANTLR's against ANTLR's.
func TestNativeWalkAgrees(t *testing.T) {
	galaparser.RequestEngine(string(galaparser.EngineGalop))
	t.Cleanup(func() { galaparser.RequestEngine("") })
	paths, searchPaths := walkCorpus(t)
	nativeW, antlrW := &walkPipeline{searchPaths: searchPaths}, &walkPipeline{searchPaths: searchPaths, antlrOnly: true}
	files, clean, walked, decls, stmts := 0, 0, 0, 0, 0
	for _, path := range paths {
		src, err := os.ReadFile(path)
		require.NoError(t, err)
		n, ok := checkWalksAgree(t, nativeW, antlrW, path, string(src))
		if !ok {
			continue
		}
		files++
		decls += n.native
		stmts += n.stmts
		if n.err == nil {
			clean++
		}
		if n.converted {
			walked++
		}
	}
	// Floors catch the sources not being staged or the native walk going
	// unused.
	require.Truef(t, files >= 200 && clean >= files*3/4 && walked >= files*3/4 && decls >= files && stmts >= files,
		"%d sources, %d of them transpiled, %d walked on the native tree, %d top-level declarations and %d body statements lowered there", files, clean, walked, decls, stmts)
	t.Logf("%d sources, %d of them transpiled, %d walked on the native tree, %d top-level declarations and %d body statements lowered there", files, clean, walked, decls, stmts)
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
	{statementShapes, `package main

import "strconv"

type Handle struct {
	name string
}

func (h Handle) Close() error {
	return nil
}

func open(name string) Handle = Handle(name = name)

struct Cell(var n int, label string)

func look(x int) Option[int] = if (x > 0) Some(x) else None[int]()

// Monad binds in a body: the statements after the bind are its chain's.
func both(x int, y int) Option[int] {
	val base = 1
	bind a = look(x)
	also b = look(y)
	Some(a + b + base)
}

func sign(x int) string {
	if x > 0 {
		return "plus"
	} else if x < 0 {
		"minus"
	} else {
		"zéro"
	}
}

func early(x int) int {
	if x > 9 {
		return 9
	}
	x * 2
}

func loops() int {
	var total = 0
	for i := 0; i < 3; i++ {
		total += i
	}
	var j = 0
	for ; j < 3; j = j + 1 {
		total -= j
	}
	for total < 100 {
		total *= 2
	}
	for {
		total /= 2
		if total < 10 {
			break
		}
		continue
	}
	for k, v := range "ünï" {
		total = total + k + int(v)
	}
	var key = 0
	for key = range "ab" {
		total++
	}
	total--
	total
}

func locals() {
	val (p, q) = (1, "ü")
	var (m, n) = (2, 3)
	var cell = Cell(1, "c")
	cell.n = 2
	m, n = n, m
	val a, b = 1, 2
	val parsed, err = strconv.Atoi("12")
	use h = open("h")
	Println(p, q, m, n, cell, a, b, parsed, err, h.name, key())
	val x = 3
	x match {
		case 3 => Println("three")
		case _ => Println("other")
	}
	if (x > 2) Println("big") else Println("small")
	return
}

func key() int = 1

func main() {
	Println(both(1, 2), sign(1), early(3), loops())
	locals()
}
`},
	{"also without a bind", "package main\n\nfunc f() Option[int] {\n\t/* ü */ also x = Some(1)\n\tSome(x)\n}\n"},
	{"bind in a loop body", "package main\n\nfunc f() Option[int] {\n\tfor {\n\t\t/* ü */ bind x = Some(1)\n\t}\n\tSome(1)\n}\n"},
	{"bind without a monad", "package main\n\nfunc f() {\n\t/* ü */ bind x = Some(1)\n\tSome(x)\n}\n"},
	{"nested function", "package main\n\nfunc f() {\n\t/* ü */ func gee(x int) int = x\n}\n"},
	{"nested method", "package main\n\nstruct P(x int)\n\nfunc f() {\n\t/* ü */ func (p P) G() int = 1\n}\n"},
	{"if initializer", "package main\n\nfunc f(x int) {\n\t/* ü */ if y := x; y > 0 {\n\t\tPrintln(y)\n\t}\n}\n"},
	{"assign to a val", "package main\n\nfunc f() {\n\tval x = 1\n\t/* ü */ x = 2\n}\n"},
	{"assign to a parameter", "package main\n\nfunc f(x int) {\n\t/* ü */ x += 2\n}\n"},
	{"assign to a receiver", "package main\n\nstruct P(x int)\n\nfunc (p P) M() {\n\t/* ü */ p = P(1)\n}\n"},
	{"assign to an immutable field", "package main\n\nstruct P(x int)\n\nfunc f() {\n\tvar p = P(1)\n\t/* ü */ p.x = 2\n}\n"},
	{"assign to an if-expression", "package main\n\nfunc f(c bool) {\n\tvar x = 1\n\t/* ü */ if (c) Println(x) else x = 2\n}\n"},
	{"increment a val", "package main\n\nfunc f() {\n\tval x = 1\n\t/* ü */ x++\n}\n"},
	{"range assigns a val", "package main\n\nfunc f() {\n\tval k = 0\n\t/* ü */ for k = range \"ab\" {\n\t\tPrintln(k)\n\t}\n}\n"},
	{"for post binds two names", "package main\n\nimport \"strconv\"\n\nfunc f() {\n\tfor i := 0; i < 3; /* ü */ a, b := strconv.Atoi(\"1\") {\n\t\tPrintln(i)\n\t}\n}\n"},
	{"blank short var", "package main\n\nfunc f() {\n\t/* ü */ _ := 1 /* ü */ + 2\n}\n"},
	{"blank local val", "package main\n\nfunc f() {\n\t/* ü */ val _ = \"äöü\"\n}\n"},
	{"blank local var in a lambda body", "package main\n\nfunc f() {\n\tval g = () => {\n\t\t/* ü */ var _ int = 1\n\t}\n\tg()\n}\n"},
	{"short var mismatch", "package main\n\nfunc f() {\n\t/* ü */ a, b := 1, 2, 3\n}\n"},
	{"short var of one value", "package main\n\nfunc f() {\n\t/* ü */ a, b := 1\n}\n"},
	{"short var None", "package main\n\nfunc f() {\n\t/* ü */ x := None()\n}\n"},
	{"forbidden keyword", "package main\n\nfunc f() {\n\t/* ü */ defer\n}\n"},
	{"value not used", "package main\n\nfunc f() {\n\tval x = 1\n\t/* ü */ x\n\tPrintln(x)\n}\n"},
	{"tail with no value", "package main\n\nfunc f() int {\n\t/* ü */ Println(\"ü\")\n}\n"},
	{"missing return in a branch", "package main\n\nfunc f(x int) int {\n\tif x > 0 {\n\t\t/* ü */ Println(x)\n\t} else {\n\t\treturn 1\n\t}\n}\n"},
	{"body ends in an if without else", "package main\n\nfunc f(x int) int {\n\tPrintln(\"ü\")\n\t/* ü */ if x > 0 {\n\t\treturn 1\n\t}\n}\n"},
	{"empty body with a result", "package main\n\nfunc f() int {\n/* ü */ }\n"},
}

// statementShapes is the walkCases program that has every statement shape
// the native walk lowers, and must transpile.
const statementShapes = "statement shapes"

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
			// The first case and statementShapes are programs that
			// transpile, so every shape in them is lowered to the end.
			if c.name == walkCases[0].name || c.name == statementShapes {
				require.NoError(t, n.err, "the case does not transpile")
			}
			require.Truef(t, n.converted, "the analysis did not convert the native tree, so the walk read ANTLR's; error: %v", n.err)
			t.Logf("the native walk lowered %d declarations and %d statements; error: %v", n.native, n.stmts, n.err)
		})
	}
}
