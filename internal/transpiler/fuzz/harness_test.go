// Package fuzz_test holds the transpiler's native Go fuzz targets.
//
// Every target runs as an ordinary unit test over its seed corpus (the f.Add
// seeds plus testdata/fuzz/<Target>/*) under `bazel test`, which is how CI
// exercises them. Actual fuzzing — generating new inputs — needs the Go
// toolchain's coverage-instrumented build; tools/fuzz.sh runs it. See
// CONTRIBUTING.MD ("Fuzzing the transpiler").
//
// The properties shared by every target live here:
//
//   - the transpiler never panics and never hangs;
//   - a rejected input is rejected with a GALA diagnostic — a SyntaxError or a
//     SemanticError — positioned inside the input, and never with GALA-E0017
//     (the internal-error code: a caught transformer panic, or generated Go
//     that did not parse). See requireCodes for uncoded SemanticErrors;
//   - an accepted input yields Go that parses and contains no leaked
//     transformer sentinel (the __gala_line_N source-map markers).
package fuzz_test

import (
	"errors"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime/debug"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/bazelbuild/rules_go/go/tools/bazel"

	"martianoff/gala/galaerr"
	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/analyzer"
	"martianoff/gala/internal/transpiler/generator"
	"martianoff/gala/internal/transpiler/transformer"
)

// transpileTimeout bounds a single transpile. Inputs are small; anything that
// takes this long is treated as a hang. It is generous because the seed pass
// also runs on slow shared CI machines.
const transpileTimeout = 60 * time.Second

// sourceFile is the file name every fuzz input is transpiled as. A non-empty
// name matters: it switches on the source-map line markers and their rewrite
// into //line directives, a pass with its own history of bugs.
const sourceFile = "fuzz.gala"

// requireCodes makes an uncoded SemanticError a failure. It is off by default
// because the transformer still has uncoded (but positioned) diagnostics, and
// giving each a code and a docs/errors page is its own piece of work; set
// GALA_FUZZ_REQUIRE_CODES=1 to have fuzzing hunt for them.
var requireCodes = os.Getenv("GALA_FUZZ_REQUIRE_CODES") == "1"

var (
	rootOnce sync.Once
	rootDir  string
)

// repoRoot returns the directory that holds std/ (and examples/): the Bazel
// runfiles root under `bazel test`, the module root under `go test`.
func repoRoot(tb testing.TB) string {
	tb.Helper()
	rootOnce.Do(func() {
		if p, err := bazel.Runfile("std/option.gala"); err == nil {
			rootDir = filepath.Dir(filepath.Dir(p))
			return
		}
		dir, err := os.Getwd()
		if err != nil {
			return
		}
		for {
			if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
				rootDir = dir
				return
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				return
			}
			dir = parent
		}
	})
	if rootDir == "" {
		tb.Fatal("cannot locate the repository root (std/ is not staged)")
	}
	return rootDir
}

// newTranspiler builds a fresh pipeline for every input so that no state can
// leak from one input into the next — the analyzer's package cache is global
// and warm after the first call, so this costs little.
func newTranspiler(tb testing.TB) *transpiler.GalaToGoTranspiler {
	p := transpiler.NewAntlrGalaParser()
	a := analyzer.NewGalaAnalyzer(p, []string{repoRoot(tb)})
	return transpiler.NewGalaToGoTranspiler(p, a, transformer.NewGalaASTTransformer(), generator.NewGoCodeGenerator())
}

// warmUp loads the standard library into the analyzer's process-wide cache
// before any input runs. Every fuzz target calls it ahead of f.Fuzz: the Go
// fuzzer treats an input that runs for more than ten seconds as a hang, and a
// cold first transpile on a machine busy with other fuzz workers can take
// that long on its own.
func warmUp(tb testing.TB) {
	tb.Helper()
	const program = "package main\n\nimport . \"martianoff/gala/collection_immutable\"\n\nfunc main() {\n    Println(ArrayOf(1, 2).Size())\n}\n"
	if res := transpile(tb, program); res.Err != nil || res.Panic != nil {
		tb.Fatalf("warm-up program failed to transpile: %v %v", res.Err, res.Panic)
	}
	// Under -fuzz every worker is a fresh process, and a mutated example
	// that is the first to import, say, concurrent pays for analyzing it —
	// on a loaded machine enough to trip the ten-second limit. Transpiling
	// the example seeds once up front loads every package they reach. The
	// plain seed pass (bazel test) skips this; it transpiles them anyway.
	if f := flag.Lookup("test.fuzz"); f != nil && f.Value.String() != "" {
		for _, src := range exampleSeeds(tb) {
			transpile(tb, src)
		}
	}
}

// outcome is the result of one bounded transpile.
type outcome struct {
	Go    string
	Err   error
	Panic any
	Stack string
}

// transpile runs the full pipeline on src, converting a panic into a reported
// value and a run past transpileTimeout into a test failure.
func transpile(tb testing.TB, src string) outcome {
	tb.Helper()
	tr := newTranspiler(tb)
	done := make(chan outcome, 1)
	go func() {
		var res outcome
		defer func() {
			if r := recover(); r != nil {
				res.Panic = r
				res.Stack = string(debug.Stack())
			}
			done <- res
		}()
		res.Go, res.Err = tr.Transpile(src, sourceFile)
	}()
	select {
	case res := <-done:
		return res
	case <-time.After(transpileTimeout):
		tb.Fatalf("transpile did not finish within %s (hang) on input:\n%q", transpileTimeout, src)
		return outcome{}
	}
}

// checkOutcome asserts the properties every target shares (see the package
// comment) and returns the parsed Go file when the input was accepted.
func checkOutcome(tb testing.TB, src string, res outcome) *ast.File {
	tb.Helper()
	if res.Panic != nil {
		tb.Fatalf("transpiler panicked: %v\n%s\ninput:\n%q", res.Panic, res.Stack, src)
	}
	if res.Err != nil {
		checkDiagnostics(tb, src, res.Err)
		return nil
	}
	return checkGeneratedGo(tb, src, res.Go)
}

// checkGeneratedGo asserts that accepted output is parseable Go without leaked
// transformer sentinels, and returns the parsed file.
func checkGeneratedGo(tb testing.TB, src, code string) *ast.File {
	tb.Helper()
	if code == "" {
		tb.Fatalf("transpile reported success but produced no Go for input:\n%q", src)
	}
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "generated.go", code, parser.ParseComments|parser.AllErrors)
	if err != nil {
		tb.Fatalf("transpile reported success but the generated Go does not parse: %v\ninput:\n%q\ngenerated:\n%s", err, src, code)
	}
	// Walk identifiers, not text: the marker spelling may legitimately occur
	// inside a string literal of the user's program.
	ast.Inspect(file, func(n ast.Node) bool {
		if id, ok := n.(*ast.Ident); ok && strings.HasPrefix(id.Name, transpiler.LineMarkerPrefix) {
			tb.Fatalf("generated Go leaks the source-map marker %s\ninput:\n%q\ngenerated:\n%s", id.Name, src, code)
		}
		return true
	})
	return file
}

// checkDiagnostics asserts that err is made of GALA diagnostics — syntax
// errors or coded semantic errors — each positioned inside src.
func checkDiagnostics(tb testing.TB, src string, err error) {
	tb.Helper()
	lines := strings.Split(galaerr.StripBOM(src), "\n")
	for _, leaf := range leafErrors(err) {
		var (
			se   *galaerr.SemanticError
			sy   *galaerr.SyntaxError
			line int
			col  int
		)
		switch {
		case errors.As(leaf, &se):
			if se.Code == galaerr.CodeInternalTransformerPanic {
				tb.Fatalf("internal transpiler error (%s) on input:\n%q\nerror: %v", se.Code, src, se)
			}
			if se.Code == "" && requireCodes {
				tb.Fatalf("semantic diagnostic carries no GALA-Exxxx code: %v\ninput:\n%q", se, src)
			}
			line, col = se.Line, se.Column
		case errors.As(leaf, &sy):
			line, col = sy.Line, sy.Column
		default:
			tb.Fatalf("rejection is not a GALA diagnostic (%T): %v\ninput:\n%q", leaf, leaf, src)
		}
		if line < 1 || line > len(lines) {
			tb.Fatalf("diagnostic line %d is outside the input (1..%d): %v\ninput:\n%q", line, len(lines), leaf, src)
		}
		// Columns are 0-based code-point offsets (ANTLR's convention); a
		// diagnostic at end of line sits one past the last rune.
		if width := utf8.RuneCountInString(lines[line-1]); col < 0 || col > width {
			tb.Fatalf("diagnostic column %d is outside line %d (0..%d): %v\ninput:\n%q", col, line, width, leaf, src)
		}
	}
}

// leafErrors flattens MultiErrors (recursively) into their individual errors.
func leafErrors(err error) []error {
	var multi *galaerr.MultiError
	if errors.As(err, &multi) {
		var out []error
		for _, sub := range multi.Errors {
			out = append(out, leafErrors(sub)...)
		}
		return out
	}
	return []error{err}
}

// diagnosticCodes returns a stable signature of err: each leaf's code (or
// "syntax") and line, in order. Columns are left out on purpose — callers use
// it to compare two inputs that differ only within a line.
func diagnosticCodes(err error) []string {
	var out []string
	for _, leaf := range leafErrors(err) {
		var (
			se *galaerr.SemanticError
			sy *galaerr.SyntaxError
		)
		switch {
		case errors.As(leaf, &se):
			out = append(out, fmt.Sprintf("%s@%d", se.Code, se.Line))
		case errors.As(leaf, &sy):
			out = append(out, fmt.Sprintf("syntax@%d", sy.Line))
		default:
			out = append(out, fmt.Sprintf("%T", leaf))
		}
	}
	return out
}

// maxSeedExamples caps how many examples each target seeds from, so the seed
// pass that runs under `bazel test` stays in the tens of seconds. The sample
// is spread evenly over the sorted list, so it is stable and covers the whole
// alphabet of features rather than one prefix of it.
const maxSeedExamples = 60

// exampleSeeds returns the sources of a stable, evenly spaced sample of the
// single-file examples.
func exampleSeeds(tb testing.TB) []string {
	tb.Helper()
	dir := filepath.Join(repoRoot(tb), "examples")
	entries, err := os.ReadDir(dir)
	if err != nil {
		tb.Fatalf("cannot read %s: %v", dir, err)
	}
	var paths []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".gala") {
			paths = append(paths, filepath.Join(dir, e.Name()))
		}
	}
	sort.Strings(paths)
	if len(paths) == 0 {
		tb.Fatalf("no examples found under %s; the seed corpus would be empty", dir)
	}
	step := 1
	if len(paths) > maxSeedExamples {
		step = (len(paths) + maxSeedExamples - 1) / maxSeedExamples
	}
	var out []string
	for i := 0; i < len(paths); i += step {
		data, err := os.ReadFile(paths[i])
		if err != nil {
			tb.Fatalf("cannot read %s: %v", paths[i], err)
		}
		out = append(out, string(data))
	}
	return out
}
