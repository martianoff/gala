package transformer_test

import (
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
	"martianoff/gala/internal/parser/native"
	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/analyzer"
	"martianoff/gala/internal/transpiler/generator"
	"martianoff/gala/internal/transpiler/transformer"
)

// walkRun is one transpile of a file: its Go, or its error, how many of its
// top-level declarations the native walk lowered, and whether the analysis
// converted the parse's native tree, which the walk reads only then.
type walkRun struct {
	path      string // the file transpiled
	out       string
	err       error
	native    int
	stmts     int                     // the statements the native walk lowered, each counted once
	stmtLines map[int]bool            // the lines those statements start on
	stmtNodes map[int]bool            // those statements' node indices
	root      native.GalaNodeRef      // the native tree's root, when the analysis converted it
	lambdas   int                     // the lambdas lowered on the native tree, each counted once
	captures  int                     // the Sendable capture checks run on the native tree
	exprs     int                     // the expressions the native walk lowered, each counted once
	exprLines map[int]bool            // the lines those expressions start on
	shapes    map[int]map[string]bool // the expression shapes it lowered, by line
	cases     int                     // the case clauses of matches and partial functions read on the native tree, each counted once
	caseLines map[int]bool            // the lines those case clauses start on
	// parsedAt is what it read on a tree of its own, a default value's or an
	// interpolated expression's: stmt, lambda, expr, case.
	parsedAt map[parsedLine]map[string]bool
	// hints is each look at the hint stack's top, in order: a take or a
	// withhold, and whether the hint there was the asking value's.
	hints     []byte
	converted bool
}

// walkPipeline transpiles files the way the CLI does, with the engine the
// test requested, each with an analyzer of its own, so no package-load
// failure carries over from one file to the next. refuseNative parses every
// default value and interpolated expression with ANTLR's parser, as when the
// engine's does not settle it (RefuseNativeParse). root is the project root
// of the analysis, "" for none.
type walkPipeline struct {
	searchPaths  []string
	root         string
	refuseNative bool
}

// parsedLine is a line of an expression parsed on its own: of a default value
// or of an interpolated expression (what), in file.
type parsedLine struct {
	what, file string
	line       int
}

// The origins parseExprAt parses an expression on its own as.
const (
	parsedDefault = "default value"
	parsedInterp  = "interpolated expression"
)

// parsedOn is what r read on line of a tree parsed on its own as what, in
// file.
func (r walkRun) parsedOn(what, file string, line int) map[string]bool {
	return r.parsedAt[parsedLine{what, filepath.ToSlash(filepath.Clean(file)), line}]
}

func (w *walkPipeline) transpile(path, src string) walkRun {
	tr := transformer.NewGalaASTTransformer()
	run := walkRun{stmtLines: map[int]bool{}, stmtNodes: map[int]bool{}, exprLines: map[int]bool{}, parsedAt: map[parsedLine]map[string]bool{}, shapes: map[int]map[string]bool{}, caseLines: map[int]bool{}}
	transformer.OnNativeDecl(tr, func() { run.native++ })
	lambdas, exprs, cases := map[int]bool{}, map[int]bool{}, map[int]bool{}
	transformer.OnNativeNodes(tr, transformer.NativeNodeHooks{
		Case:   func(index, line int) { cases[index], run.caseLines[line] = true, true },
		Stmt:   func(index, line int) { run.stmtNodes[index], run.stmtLines[line] = true, true },
		Lambda: func(index, _ int) { lambdas[index] = true },
		Expr:   func(index, line int) { exprs[index], run.exprLines[line] = true, true },
		Parsed: func(kind string, o transformer.ParsedOrigin, line int) {
			at := parsedLine{o.What, o.File, line}
			if run.parsedAt[at] == nil {
				run.parsedAt[at] = map[string]bool{}
			}
			run.parsedAt[at][kind] = true
		},
	})
	if w.refuseNative {
		transformer.RefuseNativeParse(tr)
	}
	transformer.OnNativeCapture(tr, func() { run.captures++ })
	transformer.OnNativeShape(tr, func(line int, shape string) {
		if run.shapes[line] == nil {
			run.shapes[line] = map[string]bool{}
		}
		run.shapes[line][shape] = true
	})
	transformer.OnHint(tr, func(op string, owned bool) {
		// One byte a look: t or w for a take or a withhold, upper-case when
		// the hint was the asking value's.
		b := op[0]
		if owned {
			b -= 'a' - 'A'
		}
		run.hints = append(run.hints, b)
	})
	p :=&handleParser{GalaParser: transpiler.NewAntlrGalaParser()}
	run.path = path
	run.out, run.err = transpiler.NewGalaToGoTranspiler(p, analyzer.NewGalaAnalyzer(p, w.searchPaths, w.root), tr, generator.NewGoCodeGenerator()).Transpile(src, path)
	run.converted = p.compiled.Converted()
	run.root, _ = p.compiled.ConvertedRoot()
	run.stmts, run.lambdas, run.exprs, run.cases = len(run.stmtNodes), len(lambdas), len(exprs), len(cases)
	return run
}

// loweredStmtOn reports whether the native walk lowered the outermost
// statement that starts on line, not only some statement on that line: the
// first one a parent-first walk of the tree meets there.
func (r walkRun) loweredStmtOn(line int) bool {
	found, lowered := false, false
	if r.root.Ok() {
		r.root.Walk(func(n native.GalaNodeRef) bool {
			if !found && n.Kind() == native.GalaKIND_statement && int(n.Start().Line) == line {
				found, lowered = true, r.stmtNodes[n.Index()]
			}
			return !found
		})
	}
	return lowered
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


// treeCounts is what the walk of a file must lower, counted on its native
// tree: its top-level declarations, the statements of its top-level
// functions', lambdas' and match arms' bodies, its lambdas, and the case
// clauses of its matches (bodyStatements).
type treeCounts struct{ decls, stmts, lambdas, cases int }

// countTree counts src's native tree, and false when src does not parse
// cleanly (no native tree to walk).
func countTree(src string) (treeCounts, bool) {
	tree, err := transpiler.NewAntlrGalaParser().ParseFile(src).CleanNative().Tree()
	if err != nil {
		return treeCounts{}, false
	}
	root := tree.Root()
	c := bodyStatements(root)
	for range galaparser.NativeKids(root, native.GalaKIND_topLevelDeclaration) {
		c.decls++
	}
	return c, true
}

// bodyStatements counts the statements the walk lowers in root's top-level
// function bodies, lambda bodies, match arms and if-expression branches: each
// statement of a body, and of the if and for blocks in it, the statements of
// a `bind` chain included. It counts the lambdas and the case clauses of
// matches and partial functions too.
func bodyStatements(root native.GalaNodeRef) treeCounts {
	var c treeCounts
	var block func(native.GalaNodeRef)
	var ifBlocks func(native.GalaNodeRef)
	block = func(b native.GalaNodeRef) {
		if !b.Ok() {
			return
		}
		for s := range galaparser.NativeKids(b, native.GalaKIND_statement) {
			c.stmts++
			d, ok := galaparser.OnlyNativeKid(s, native.GalaKIND_declaration)
			if !ok {
				continue
			}
			if is, ok := galaparser.OnlyNativeKid(d, native.GalaKIND_ifStatement); ok {
				ifBlocks(is)
			} else if fs, ok := galaparser.OnlyNativeKid(d, native.GalaKIND_forStatement); ok {
				if fb, ok := galaparser.OnlyNativeKid(fs, native.GalaKIND_block); ok {
					block(fb)
				}
			}
		}
	}
	ifBlocks = func(s native.GalaNodeRef) {
		for b := range galaparser.NativeKids(s, native.GalaKIND_block) {
			block(b)
		}
		if elseIf, ok := galaparser.OnlyNativeKid(s, native.GalaKIND_ifStatement); ok {
			ifBlocks(elseIf)
		}
	}
	for td := range galaparser.NativeKids(root, native.GalaKIND_topLevelDeclaration) {
		if fd, ok := galaparser.OnlyNativeKid(td, native.GalaKIND_functionDeclaration); ok {
			if fb, ok := galaparser.OnlyNativeKid(fd, native.GalaKIND_block); ok {
				block(fb)
			}
		}
	}
	// A lambda is lowered wherever it is written, and a match and a partial
	// function read their case clauses wherever they are, but in a
	// parameter's default value: a default is lowered at each call, from the
	// text the analysis recorded, parsed on a tree of its own (the hooks count
	// only the file's tree).
	root.Walk(func(n native.GalaNodeRef) bool {
		switch n.Kind() {
		case native.GalaKIND_paramDefault:
			return false
		case native.GalaKIND_lambdaExpression:
			c.lambdas++
			if b, ok := galaparser.OnlyNativeKid(n, native.GalaKIND_block); ok {
				block(b)
			}
		case native.GalaKIND_caseClause:
			c.cases++
			if b, ok := galaparser.OnlyNativeKid(n, native.GalaKIND_block); ok {
				block(b)
			}
		case native.GalaKIND_ifExprBranch:
			if b, ok := galaparser.OnlyNativeKid(n, native.GalaKIND_block); ok {
				block(b)
			}
		}
		return true
	})
	return c
}

// checkWalk transpiles src, written at path, with w, and fails tb unless its
// Go, or its error, is its golden (checkGolden; the corpus run, whose files
// have no goldens, passes golden false) and the walk lowered every top-level
// declaration of the file, every statement of its function bodies, every
// lambda and every match's case clauses (each one up to the failing one, on
// an error). It is false when src does not parse cleanly.
func checkWalk(tb testing.TB, w *walkPipeline, path, src string, golden bool) (run walkRun, want treeCounts, ok bool) {
	tb.Helper()
	want, ok = countTree(src)
	if !ok {
		return walkRun{}, want, false
	}
	n := w.transpile(path, src)
	if golden {
		checkGolden(tb, path, w.searchPaths, n.out, n.err)
	}
	if n.err == nil {
		assert.Equalf(tb, want.decls, n.native, "%s: top-level declarations the walk lowered", path)
		assert.Equalf(tb, want.stmts, n.stmts, "%s: body statements the walk lowered", path)
		assert.Equalf(tb, want.lambdas, n.lambdas, "%s: lambdas the walk lowered", path)
		assert.Equalf(tb, want.cases, n.cases, "%s: case clauses the walk read", path)
		// A function body's statements hold expressions, and those are
		// lowered too.
		if want.stmts > 0 {
			assert.Positivef(tb, n.exprs, "%s: the walk lowered no expression", path)
		}
	} else {
		assert.LessOrEqualf(tb, n.native, want.decls, "%s: the walk lowered more declarations than there are (error: %v)", path, n.err)
		assert.LessOrEqualf(tb, n.stmts, want.stmts, "%s: the walk lowered more statements than there are (error: %v)", path, n.err)
		assert.LessOrEqualf(tb, n.lambdas, want.lambdas, "%s: the walk lowered more lambdas than there are (error: %v)", path, n.err)
		assert.LessOrEqualf(tb, n.cases, want.cases, "%s: the walk read more case clauses than there are (error: %v)", path, n.err)
	}
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
