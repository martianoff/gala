package transformer_test

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/analyzer"
	"martianoff/gala/internal/transpiler/generator"
	"martianoff/gala/internal/transpiler/transformer"

	"github.com/bazelbuild/rules_go/go/tools/bazel"
)

// TestCompilationGate discovers all single-file .gala examples, transpiles each
// one to Go, and verifies that the generated Go code parses as valid syntax via
// go/parser. This acts as a compilation gate: if the transpiler produces
// syntactically broken Go for any example, the test fails.
func TestCompilationGate(t *testing.T) {
	// Files that require multi-package or multi-file setup and cannot be
	// transpiled in isolation.
	skipFiles := map[string]string{
		// These import local example libraries that are not available during
		// single-file transpilation.
		"use_lib.gala":                    "imports examples/lib",
		"use_lib_alias.gala":              "imports examples/lib with alias",
		"use_lib_generic.gala":            "imports examples/lib",
		"use_lib_qualified.gala":          "imports examples/lib qualified",
		"use_multi_file_lib.gala":         "imports multi_file_lib",
		"use_cross_file_import.gala":      "imports cross_file_import_lib",
		"use_cross_file_unwrap_lib.gala":  "imports cross_file_unwrap_lib",
		"match_lib_test.gala":             "imports match_lib",
		"bug013_type_alias_lib_test.gala": "imports bug013_type_alias_lib",
		"bug013_try_match_extractor.gala": "imports bug013_cross_pkg",
		"named_arg_sealed_model.gala":     "support file for named_arg_sealed",
		"named_arg_sealed.gala":           "requires named_arg_sealed_model companion file",
		"slice_type_param.gala":           "uses []T as type param (unsupported grammar)",
		"match_interface_return.gala":     "known semantic error in match branch type unification",
	}

	examplesDir := findExamplesDir(t)

	entries, err := os.ReadDir(examplesDir)
	if err != nil {
		t.Fatalf("failed to read examples directory: %v", err)
	}

	// Collect single-file .gala examples.
	var galaFiles []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if !strings.HasSuffix(e.Name(), ".gala") {
			continue
		}
		galaFiles = append(galaFiles, e.Name())
	}

	if len(galaFiles) == 0 {
		t.Fatal("no .gala files found in examples directory")
	}

	// Create a single transpiler instance shared across all subtests.
	p := transpiler.NewAntlrGalaParser()
	a := analyzer.NewGalaAnalyzer(p, getStdSearchPath())
	tr := transformer.NewGalaASTTransformer()
	g := generator.NewGoCodeGenerator()
	trans := transpiler.NewGalaToGoTranspiler(p, a, tr, g)

	t.Logf("discovered %d .gala files (%d skipped)", len(galaFiles), countSkipped(galaFiles, skipFiles))

	for _, name := range galaFiles {
		name := name // capture
		t.Run(strings.TrimSuffix(name, ".gala"), func(t *testing.T) {
			if reason, ok := skipFiles[name]; ok {
				t.Skipf("skipped: %s", reason)
			}

			filePath := filepath.Join(examplesDir, name)
			src, err := os.ReadFile(filePath)
			if err != nil {
				t.Fatalf("failed to read %s: %v", name, err)
			}

			// Transpile with a timeout to catch hangs.
			goCode, transpileErr := transpileWithTimeout(t, trans, string(src), filePath, 30*time.Second)
			if transpileErr != nil {
				t.Fatalf("transpilation failed for %s: %v", name, transpileErr)
			}

			// Strip the generated header before parsing.
			code := stripGeneratedHeader(goCode)

			// Verify the generated Go code is syntactically valid.
			fset := token.NewFileSet()
			_, parseErr := parser.ParseFile(fset, name+".go", code, parser.AllErrors)
			if parseErr != nil {
				t.Errorf("generated Go code does not parse for %s:\n%v\n\n--- generated code ---\n%s",
					name, parseErr, code)
			}
		})
	}
}

// findExamplesDir locates the examples/ directory, trying Bazel runfiles first.
func findExamplesDir(t *testing.T) string {
	t.Helper()

	// Try Bazel runfiles: look for a known example file.
	if p, err := bazel.Runfile("examples/hello.gala"); err == nil {
		return filepath.Dir(p)
	}

	// Fallback: walk up from cwd to find the project root.
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("cannot get working directory: %v", err)
	}

	dir := cwd
	for {
		candidate := filepath.Join(dir, "examples")
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return candidate
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}

	t.Fatal("could not locate examples/ directory")
	return ""
}

// transpileWithTimeout runs the GALA-to-Go transpiler with a deadline.
func transpileWithTimeout(t *testing.T, trans *transpiler.GalaToGoTranspiler, input, filePath string, timeout time.Duration) (string, error) {
	t.Helper()

	type result struct {
		code string
		err  error
	}

	ch := make(chan result, 1)
	go func() {
		code, err := trans.Transpile(input, filePath)
		ch <- result{code, err}
	}()

	select {
	case r := <-ch:
		return r.code, r.err
	case <-time.After(timeout):
		t.Fatalf("transpilation timed out after %v for %s", timeout, filepath.Base(filePath))
		return "", nil
	}
}

// countSkipped returns how many files from the list are in the skip map.
func countSkipped(files []string, skip map[string]string) int {
	n := 0
	for _, f := range files {
		if _, ok := skip[f]; ok {
			n++
		}
	}
	return n
}
