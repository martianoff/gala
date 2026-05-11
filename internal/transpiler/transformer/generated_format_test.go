package transformer_test

import (
	"go/format"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/analyzer"
	"martianoff/gala/internal/transpiler/generator"
	"martianoff/gala/internal/transpiler/transformer"
)

// TestGeneratedGoIsGofmtClean is the audit's Phase 3b quality gate. For
// every single-file .gala example that the compilation gate accepts,
// the test asserts the generated Go is gofmt-idempotent — i.e.,
// `go/format.Source(out)` returns `out` unchanged. The transpiler
// already emits via `go/printer`, so any deviation from gofmt's
// normalisation surfaces a print-side bug (e.g., missing whitespace,
// wrong line breaks in composite literals, malformed comments).
//
// Compared to TestCompilationGate (which only checks the output
// parses), this catches a class of regressions the parser tolerates:
// the generated code is *valid* Go but not *clean* Go. CI tooling that
// later runs `gofmt -l` against the output would otherwise flag every
// example as needing reformatting.
//
// The test reuses the same skip list as TestCompilationGate so we
// don't double-maintain the catalogue of "examples that need a multi-
// file or imported-package context to transpile". Files that fail the
// compilation gate also fail (or skip) this test for the same reason.
func TestGeneratedGoIsGofmtClean(t *testing.T) {
	// Mirror the compilation gate's skip list. Keep these in sync — if
	// you add a skip for compilation_gate_test, mirror it here.
	skipFiles := map[string]string{
		"use_lib.gala":                       "imports examples/lib",
		"use_lib_alias.gala":                 "imports examples/lib with alias",
		"use_lib_generic.gala":               "imports examples/lib",
		"use_lib_qualified.gala":             "imports examples/lib qualified",
		"use_multi_file_lib.gala":            "imports multi_file_lib",
		"use_cross_file_import.gala":         "imports cross_file_import_lib",
		"use_cross_file_unwrap_lib.gala":     "imports cross_file_unwrap_lib",
		"match_lib_test.gala":                "imports match_lib",
		"type_alias_lib_match_test.gala":     "imports type_alias_lib_match",
		"try_match_extractor_any_types.gala": "imports cross_pkg_try_match",
		"named_arg_sealed_model.gala":        "support file for named_arg_sealed",
		"named_arg_sealed.gala":              "requires named_arg_sealed_model companion file",
		"slice_type_param.gala":              "uses []T as type param (unsupported grammar)",
		"match_interface_return.gala":        "known semantic error in match branch type unification",
		"byte_conversion.gala":               "imports go_interop",
		"doc_io_verify.gala":                 "imports io",
		"either_match_in_lambda.gala":        "imports concurrent",
		"execution_context.gala":             "imports concurrent",
		"extractor_type_inference.gala":      "imports concurrent (semantic error without full pkg)",
		"future_pattern_match.gala":          "imports concurrent",
		"go_type_inference_cross_pkg.gala":   "imports examples/go_type_inference_lib",
		"if_else_implicit_return.gala":       "imports stream",
		"json_codec.gala":                    "imports json",
		"json_serialization.gala":            "imports json (semantic error without full pkg)",
		"regex_usage.gala":                   "imports regex",
		"string_builder.gala":                "imports strings",
		"type_alias_lambda_inference.gala":   "imports test",
	}

	examplesDir := findExamplesDir(t)
	entries, err := os.ReadDir(examplesDir)
	if err != nil {
		t.Fatalf("failed to read examples directory: %v", err)
	}

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

	p := transpiler.NewAntlrGalaParser()
	a := analyzer.NewGalaAnalyzer(p, getStdSearchPath())
	tr := transformer.NewGalaASTTransformer()
	g := generator.NewGoCodeGenerator()
	trans := transpiler.NewGalaToGoTranspiler(p, a, tr, g)

	for _, name := range galaFiles {
		name := name
		t.Run(strings.TrimSuffix(name, ".gala"), func(t *testing.T) {
			if reason, ok := skipFiles[name]; ok {
				t.Skipf("skipped: %s", reason)
			}

			filePath := filepath.Join(examplesDir, name)
			src, err := os.ReadFile(filePath)
			if err != nil {
				t.Fatalf("failed to read %s: %v", name, err)
			}

			goCode, transpileErr := transpileWithTimeout(t, trans, string(src), filePath, 30*time.Second)
			if transpileErr != nil {
				// Mirror the compilation gate's auto-skip for sandboxed
				// imports that cannot resolve at single-file transpile time.
				errMsg := transpileErr.Error()
				if strings.Contains(errMsg, "package not found") ||
					strings.Contains(errMsg, "could not be resolved") {
					t.Skipf("skipped: imported package not available in test sandbox: %v", transpileErr)
				}
				t.Fatalf("transpilation failed for %s: %v", name, transpileErr)
			}

			code := stripGeneratedHeader(goCode)

			// Run gofmt on the generated code. If the output differs,
			// the transpiler's print path emitted Go that compiles but
			// is not gofmt-clean — a regression that would surface in
			// any CI pipeline running `gofmt -l` over generated code.
			formatted, err := format.Source([]byte(code))
			if err != nil {
				// Should not happen — TestCompilationGate already asserted
				// the code parses. If gofmt rejects it, that is itself a
				// regression worth surfacing here.
				t.Fatalf("gofmt rejected generated code for %s: %v\n--- generated ---\n%s",
					name, err, code)
			}

			if string(formatted) != code {
				diff := firstFormatDifference(code, string(formatted))
				t.Errorf("generated Go is not gofmt-idempotent for %s — gofmt would change the output\n%s",
					name, diff)
			}
		})
	}
}

// firstFormatDifference returns a short rendering of the first line
// where `original` and `formatted` differ, plus a few lines of context.
// Used by TestGeneratedGoIsGofmtClean's failure path to keep error
// messages bounded — full-file diffs of generated Go drown out the
// signal in test logs.
func firstFormatDifference(original, formatted string) string {
	origLines := strings.Split(original, "\n")
	fmtLines := strings.Split(formatted, "\n")
	for i := 0; i < len(origLines) && i < len(fmtLines); i++ {
		if origLines[i] == fmtLines[i] {
			continue
		}
		var b strings.Builder
		b.WriteString("first divergence at line ")
		b.WriteString(itoaSimple(i + 1))
		b.WriteString(":\n")
		b.WriteString("  -- emitted    : ")
		b.WriteString(quoteVisible(origLines[i]))
		b.WriteString("\n  -- gofmt wants: ")
		b.WriteString(quoteVisible(fmtLines[i]))
		return b.String()
	}
	if len(origLines) != len(fmtLines) {
		return "files differ in length: emitted=" + itoaSimple(len(origLines)) +
			" gofmt-wants=" + itoaSimple(len(fmtLines))
	}
	return "files differ in trailing bytes (no line-level divergence)"
}

// itoaSimple is a small int→string helper that avoids pulling strconv
// into the test for one call site.
func itoaSimple(i int) string {
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var buf [20]byte
	n := len(buf)
	for i > 0 {
		n--
		buf[n] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		n--
		buf[n] = '-'
	}
	return string(buf[n:])
}

// quoteVisible escapes a line so test output renders trailing whitespace
// (a common gofmt-divergence cause) visibly. Returns the line wrapped
// in pipes for clarity.
func quoteVisible(s string) string {
	var b strings.Builder
	b.WriteByte('|')
	for _, r := range s {
		switch r {
		case '\t':
			b.WriteString("\\t")
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('|')
	return b.String()
}
