package build

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bazelbuild/rules_go/go/tools/bazel"
)

// findBuilderSource locates builder.go on disk for static analysis.
// Under Bazel, the source is declared as test data and accessible via
// runfiles. Outside Bazel, we walk up from the cwd to the repo root.
func findBuilderSource(t *testing.T) string {
	t.Helper()
	if p, err := bazel.Runfile("internal/build/builder.go"); err == nil {
		return p
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("cwd: %v", err)
	}
	dir := cwd
	for {
		candidate := filepath.Join(dir, "internal", "build", "builder.go")
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not locate builder.go on disk or in runfiles")
		}
		dir = parent
	}
}

// TestTranspileWithSourceDir_UsesBatchAnalyzer is a static (AST) regression
// guard for the gala_tui-class perf fix: transpileWithSourceDir must use
// one BatchAnalyzer reused across files, not allocate a fresh galaAnalyzer
// per file. The latter pattern caused 67s cold builds because every file
// re-analyzed std (~1.3s each) instead of sharing analyzedPkgs across the
// loop.
//
// Regression signature it catches: a future change that reverts to
// `analyzer.NewGalaAnalyzer*` inside a for loop within
// transpileWithSourceDir. The test parses builder.go itself, finds the
// function, walks its body, and asserts:
//
//  1. NewBatchAnalyzer is called at least once (the optimization is in
//     place), AND
//  2. Inside any for-loop in the function body, no NewGalaAnalyzer or
//     NewGalaAnalyzerWithPackageFiles call appears (the regression is
//     not present).
//
// This is brittle by design — touching the function deliberately to
// rewrite the strategy is fine; reverting unintentionally fails loudly.
func TestTranspileWithSourceDir_UsesBatchAnalyzer(t *testing.T) {
	fset := token.NewFileSet()
	src := findBuilderSource(t)
	file, err := parser.ParseFile(fset, src, nil, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse builder.go: %v", err)
	}

	var fn *ast.FuncDecl
	for _, decl := range file.Decls {
		fd, ok := decl.(*ast.FuncDecl)
		if !ok {
			continue
		}
		if fd.Name.Name == "transpileWithSourceDir" && fd.Recv != nil {
			fn = fd
			break
		}
	}
	if fn == nil {
		t.Fatal("could not find (b *Builder) transpileWithSourceDir in builder.go")
	}

	// Pass 1: assert NewBatchAnalyzer is called somewhere in the body.
	sawBatch := false
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if name := callName(call); strings.HasSuffix(name, ".NewBatchAnalyzer") || name == "NewBatchAnalyzer" {
			sawBatch = true
		}
		return true
	})
	if !sawBatch {
		t.Error("transpileWithSourceDir does not call analyzer.NewBatchAnalyzer; the perf fix may have been reverted (gala_tui cold build regressed from ~18s back to ~67s)")
	}

	// Pass 2: walk every for-loop inside the function body and assert
	// no per-file NewGalaAnalyzer* allocation appears within.
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		fl, ok := n.(*ast.RangeStmt)
		if !ok {
			return true
		}
		ast.Inspect(fl.Body, func(inner ast.Node) bool {
			call, ok := inner.(*ast.CallExpr)
			if !ok {
				return true
			}
			name := callName(call)
			short := name
			if dot := strings.LastIndex(name, "."); dot >= 0 {
				short = name[dot+1:]
			}
			switch short {
			case "NewGalaAnalyzer", "NewGalaAnalyzerWithPackageFiles", "NewGalaAnalyzerWithBase":
				pos := fset.Position(call.Pos())
				t.Errorf("transpileWithSourceDir allocates a fresh analyzer per loop iteration at %s: %s — this is the regression that turned gala_tui cold builds into 67s. Use the shared BatchAnalyzer instead.", pos, name)
			}
			return true
		})
		return true
	})
}

// callName returns the textual name of a call expression's callee.
// Handles plain identifiers ("Foo") and selectors ("pkg.Foo", "x.Foo").
// Returns "" for indirect calls we can't statically resolve — this test
// only cares about direct factory calls.
func callName(call *ast.CallExpr) string {
	switch fn := call.Fun.(type) {
	case *ast.Ident:
		return fn.Name
	case *ast.SelectorExpr:
		if x, ok := fn.X.(*ast.Ident); ok {
			return x.Name + "." + fn.Sel.Name
		}
		return "." + fn.Sel.Name
	}
	return ""
}
