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

	"github.com/antlr4-go/antlr/v4"
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
	stmts     int                     // the statements the native walk lowered, each counted once
	stmtLines map[int]bool            // the lines those statements start on
	lambdas   int                     // the lambdas lowered on the native tree, each counted once
	captures  int                     // the Sendable capture checks run on the native tree
	exprs     int                     // the expressions the native walk lowered, each counted once
	exprLines map[int]bool            // the lines those expressions start on
	shapes    map[int]map[string]bool // the expression shapes it lowered, by line
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
	run := walkRun{stmtLines: map[int]bool{}, exprLines: map[int]bool{}, shapes: map[int]map[string]bool{}}
	transformer.OnNativeDecl(tr, func() { run.native++ })
	stmts, lambdas, exprs := map[int]bool{}, map[int]bool{}, map[int]bool{}
	transformer.OnNativeStmt(tr, func(index, line int) { stmts[index], run.stmtLines[line] = true, true })
	transformer.OnNativeLambda(tr, func(index int) { lambdas[index] = true })
	transformer.OnNativeCapture(tr, func() { run.captures++ })
	transformer.OnNativeExpr(tr, func(index, line int) { exprs[index], run.exprLines[line] = true, true })
	transformer.OnNativeShape(tr, func(line int, shape string) {
		if run.shapes[line] == nil {
			run.shapes[line] = map[string]bool{}
		}
		run.shapes[line][shape] = true
	})
	if w.antlrOnly {
		tr = withoutNative{tr}
	}
	p := &handleParser{GalaParser: transpiler.NewAntlrGalaParser()}
	run.out, run.err = transpiler.NewGalaToGoTranspiler(p, analyzer.NewGalaAnalyzer(p, w.searchPaths), tr, generator.NewGoCodeGenerator()).Transpile(src, path)
	run.converted = p.compiled.Converted()
	run.stmts, run.lambdas, run.exprs = len(stmts), len(lambdas), len(exprs)
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
// top-level functions' and its lambdas' bodies, and its lambdas
// (bodyStatements).
type treeCounts struct{ decls, stmts, lambdas int }

// countTree counts src's ANTLR tree, and false when src does not parse
// cleanly with galop (no native tree to walk).
func countTree(src string) (treeCounts, bool) {
	parsed := transpiler.NewAntlrGalaParser().ParseFile(src)
	if parsed.CleanNative() == nil {
		return treeCounts{}, false
	}
	sf := parsed.Tree.(*grammar.SourceFileContext)
	stmts, lambdas := bodyStatements(sf)
	return treeCounts{decls: len(sf.AllTopLevelDeclaration()), stmts: stmts, lambdas: lambdas}, true
}

// bodyStatements counts the statements the native walk lowers in sf's
// top-level function bodies and lambda bodies: each statement of a body, and
// of the if and for blocks in it, up to and including a block's first
// `bind`, whose chain lowers the statements after it on ANTLR's tree. A match
// arm's or an if-expression branch's block is lowered on ANTLR's tree.
func bodyStatements(sf *grammar.SourceFileContext) (stmts, lambdaCount int) {
	var block func(grammar.IBlockContext)
	var ifBlocks func(grammar.IIfStatementContext)
	block = func(b grammar.IBlockContext) {
		if b == nil {
			return
		}
		for _, s := range b.AllStatement() {
			stmts++
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
	// A lambda is lowered on the native tree wherever it is written (the
	// expression it is in crosses back to it, lambdas_source.go), but in a
	// parameter's default value: a default is lowered at each call, from the
	// declaration the analysis recorded, on ANTLR's tree.
	var lambdas func(antlr.Tree)
	lambdas = func(node antlr.Tree) {
		switch l := node.(type) {
		case *grammar.ParamDefaultContext:
			return
		case *grammar.LambdaExpressionContext:
			lambdaCount++
			block(l.Block())
		}
		for _, c := range node.GetChildren() {
			lambdas(c)
		}
	}
	lambdas(sf)
	return stmts, lambdaCount
}

// checkWalksAgree fails tb unless the native and the ANTLR walk of path give
// the same Go, or the same error, and each walk took the tree it should: the
// native walk lowered every top-level declaration of the file and every
// statement of its function bodies (each one up to the failing one, on an
// error) when the analysis converted its native tree, and none when it did
// not, and the ANTLR walk none.
func checkWalksAgree(tb testing.TB, nativeW, antlrW *walkPipeline, path, src string) (native walkRun, want treeCounts, ok bool) {
	tb.Helper()
	want, ok = countTree(src)
	if !ok {
		return walkRun{}, want, false
	}
	n := nativeW.transpile(path, src)
	a := antlrW.transpile(path, src)
	assert.Zerof(tb, a.native, "%s: the ANTLR walk lowered declarations on the native tree", path)
	assert.Zerof(tb, a.stmts, "%s: the ANTLR walk lowered statements on the native tree", path)
	assert.Zerof(tb, a.lambdas, "%s: the ANTLR walk lowered lambdas on the native tree", path)
	assert.Zerof(tb, a.captures, "%s: the ANTLR walk checked captures on the native tree", path)
	assert.Zerof(tb, a.exprs, "%s: the ANTLR walk lowered expressions on the native tree", path)
	switch {
	case !n.converted:
		assert.Zerof(tb, n.native, "%s: the native walk lowered declarations on a tree the analysis did not convert", path)
		assert.Zerof(tb, n.stmts, "%s: the native walk lowered statements on a tree the analysis did not convert", path)
		assert.Zerof(tb, n.lambdas, "%s: the native walk lowered lambdas on a tree the analysis did not convert", path)
		assert.Zerof(tb, n.exprs, "%s: the native walk lowered expressions on a tree the analysis did not convert", path)
	case n.err == nil:
		assert.Equalf(tb, want.decls, n.native, "%s: top-level declarations the native walk lowered", path)
		assert.Equalf(tb, want.stmts, n.stmts, "%s: body statements the native walk lowered", path)
		assert.Equalf(tb, want.lambdas, n.lambdas, "%s: lambdas the native walk lowered", path)
		// A function body's statements hold expressions, and those are
		// lowered on the native tree too.
		if want.stmts > 0 {
			assert.Positivef(tb, n.exprs, "%s: the native walk lowered no expression", path)
		}
	default:
		assert.LessOrEqualf(tb, n.native, want.decls, "%s: the native walk lowered more declarations than there are (error: %v)", path, n.err)
		assert.LessOrEqualf(tb, n.stmts, want.stmts, "%s: the native walk lowered more statements than there are (error: %v)", path, n.err)
		assert.LessOrEqualf(tb, n.lambdas, want.lambdas, "%s: the native walk lowered more lambdas than there are (error: %v)", path, n.err)
	}
	if n.out != a.out {
		assert.Failf(tb, "the walks' Go differs", "%s: the native walk's Go differs from the ANTLR walk's at %s", path, firstLineDiff(n.out, a.out))
	}
	assert.Equalf(tb, a.err, n.err, "%s: the native walk's error differs from the ANTLR walk's", path)
	return n, want, true
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

// bodyMarkerLine is the line of src's first `/* ü */` marker, when the line
// is indented, so the marker is on a statement in a body.
func bodyMarkerLine(src string) (int, bool) {
	for i, l := range strings.Split(src, "\n") {
		if strings.Contains(l, "/* ü */") {
			return i + 1, strings.HasPrefix(l, "\t")
		}
	}
	return 0, false
}
