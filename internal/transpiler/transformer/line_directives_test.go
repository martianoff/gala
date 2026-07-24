package transformer_test

import (
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/analyzer"
	"martianoff/gala/internal/transpiler/generator"
	"martianoff/gala/internal/transpiler/transformer"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// panicDemoSource is a self-contained GALA program that panics at runtime with a
// division by zero. It is deliberately import-free so the generated Go compiles
// standalone (no std/module dependencies), which lets the acceptance test build
// and run it in isolation. Line numbers are load-bearing — see the assertions
// below. The `return a / b` divide is on line 4.
//
//	line 1: package main
//	line 2: (blank)
//	line 3: func divide(a int, b int) int {
//	line 4:     return a / b
//	line 5: }
//	line 6: (blank)
//	line 7: func main() {
//	line 8:     divide(10, 0)
//	line 9: }
const panicDemoSource = `package main

func divide(a int, b int) int {
    return a / b
}

func main() {
    divide(10, 0)
}
`

const (
	// panicDemoFile is the GALA source filename threaded through transpilation;
	// it becomes the path in the emitted //line directives and therefore the
	// filename Go reports in the panic stack trace.
	panicDemoFile = "sourcemap_panic_demo.gala"
	// panicDemoDivideLine is the GALA line of `return a / b`, where the
	// division-by-zero panic originates.
	panicDemoDivideLine = "4"
)

func newLineDirectiveTranspiler() *transpiler.GalaToGoTranspiler {
	p := transpiler.NewAntlrGalaParser()
	a := analyzer.NewGalaAnalyzer(p, getStdSearchPath())
	tr := transformer.NewGalaASTTransformer()
	g := generator.NewGoCodeGenerator()
	return transpiler.NewGalaToGoTranspiler(p, a, tr, g)
}

// TestLineDirectives_EmittedWithSourceFile is the transformer unit test: the
// generated Go must carry `//line <file>:<n>` directives naming the GALA source
// file, one for the panicking statement.
func TestLineDirectives_EmittedWithSourceFile(t *testing.T) {
	trans := newLineDirectiveTranspiler()

	goCode, err := trans.Transpile(panicDemoSource, panicDemoFile)
	require.NoError(t, err)

	assert.Contains(t, goCode, "//line ",
		"generated Go should contain //line directives")
	assert.Contains(t, goCode, "//line "+panicDemoFile+":",
		"//line directives should name the GALA source file")
	assert.Contains(t, goCode, "//line "+panicDemoFile+":"+panicDemoDivideLine,
		"a //line directive should map the panicking statement to its GALA line")

	// Every directive must be at column 0 — an indented //line is treated as an
	// ordinary comment by the Go compiler and does not re-map positions.
	for _, line := range strings.Split(goCode, "\n") {
		if strings.Contains(line, "//line ") {
			assert.True(t, strings.HasPrefix(line, "//line "),
				"line directive must start at column 0, got: %q", line)
		}
	}
}

// TestLineDirectives_NotEmittedWithoutSourceFile verifies the suppression path:
// transpiling an anonymous snippet (empty filePath) emits no directives and no
// leftover markers — there is no source file to map to.
func TestLineDirectives_NotEmittedWithoutSourceFile(t *testing.T) {
	trans := newLineDirectiveTranspiler()

	goCode, err := trans.Transpile(panicDemoSource, "")
	require.NoError(t, err)

	assert.NotContains(t, goCode, "//line ",
		"no //line directives should be emitted without a source file")
	assert.NotContains(t, goCode, transpiler.LineMarkerPrefix,
		"no raw line markers should leak into output")
}

// TestLineDirectives_PanicTraceReportsGalaPosition is the acceptance test: it
// transpiles a panicking GALA program, compiles the generated Go, runs it, and
// asserts the panic stack trace reports the GALA source position
// (sourcemap_panic_demo.gala:4) rather than a generated-Go position. This is the
// proof the source map actually reaches the runtime.
func TestLineDirectives_PanicTraceReportsGalaPosition(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not on PATH; cannot compile+run generated program")
	}

	trans := newLineDirectiveTranspiler()
	goCode, err := trans.Transpile(panicDemoSource, panicDemoFile)
	require.NoError(t, err)

	out, runErr := buildAndRunGeneratedGo(t, goCode)
	// The program must panic (non-zero exit).
	require.Error(t, runErr, "expected the program to panic; output:\n%s", out)

	assert.Contains(t, out, "integer divide by zero",
		"expected a division-by-zero panic; full output:\n%s", out)

	wantPos := panicDemoFile + ":" + panicDemoDivideLine
	assert.Contains(t, out, wantPos,
		"panic stack trace should report the GALA source position %q; full output:\n%s",
		wantPos, out)
}

// rawStringDemoSource embeds a line whose trimmed text is exactly a marker
// (`__gala_line_2`) INSIDE a GALA raw (backtick) string. A naive line-by-line
// text rewrite of the generated Go would mistake that string interior for a
// marker and corrupt the string's runtime value; the AST-based rewrite must
// leave it untouched. `println` is Go's import-free builtin, so the program
// compiles standalone.
//
//	line 1: package main
//	line 2: (blank)
//	line 3: func main() {
//	line 4:     println(`
//	line 5: __gala_line_2
//	line 6: `)
//	line 7: }
const rawStringDemoSource = "package main\n\nfunc main() {\n    println(`\n__gala_line_2\n`)\n}\n"

const rawStringDemoFile = "rawstring_demo.gala"

// TestLineDirectives_RawStringNotCorrupted is the regression test for the
// raw-string miscompile: a marker-looking line inside a string literal must
// survive transpilation verbatim, while real statements still get //line
// directives. Verified both in the generated Go and at runtime.
func TestLineDirectives_RawStringNotCorrupted(t *testing.T) {
	trans := newLineDirectiveTranspiler()
	goCode, err := trans.Transpile(rawStringDemoSource, rawStringDemoFile)
	require.NoError(t, err)

	// The string literal content must be preserved, not rewritten to a //line
	// directive, and real statements must still carry directives.
	assert.Contains(t, goCode, "__gala_line_2",
		"raw-string interior must survive transpilation; generated:\n%s", goCode)
	assert.Contains(t, goCode, "//line "+rawStringDemoFile+":",
		"real statements should still get //line directives; generated:\n%s", goCode)

	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not on PATH; skipping compile+run half")
	}
	out, _ := buildAndRunGeneratedGo(t, goCode)
	assert.Contains(t, out, "__gala_line_2",
		"the raw string's runtime value must be intact; program output:\n%s", out)
}

// buildAndRunGeneratedGo writes goCode into a throwaway module, builds it, runs
// it, and returns the combined stdout+stderr and the run error (non-nil if the
// program exits non-zero, e.g. panics). The generated programs used here are
// import-free, so a trivial go.mod with a low language version avoids any
// toolchain download (kept offline via GOTOOLCHAIN=local).
func buildAndRunGeneratedGo(t *testing.T, goCode string) (string, error) {
	t.Helper()

	goBin, env := consistentGoToolchain(t)

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.go"), []byte(goCode), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "go.mod"),
		[]byte("module sourcemap_demo\n\ngo 1.21\n"), 0o644))

	bin := filepath.Join(dir, "app")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}

	env = append(env,
		"GOTOOLCHAIN=local",
		"GOCACHE="+filepath.Join(dir, "gocache"),
		"GOFLAGS=-mod=mod",
	)

	build := exec.Command(goBin, "build", "-o", bin, ".")
	build.Dir = dir
	build.Env = env
	if out, buildErr := build.CombinedOutput(); buildErr != nil {
		t.Fatalf("go build of generated program failed: %v\n--- output ---\n%s\n--- generated Go ---\n%s",
			buildErr, out, goCode)
	}

	run := exec.Command(bin)
	run.Env = env
	out, runErr := run.CombinedOutput()
	return string(out), runErr
}

// consistentGoToolchain returns a Go binary and a subprocess environment whose
// compiler tool and standard library always agree.
//
// On CI the ambient environment sets GOROOT to the hermetic rules_go SDK (via
// `.bazelrc --action_env=GOROOT`, needed for the transpiler's Go type
// inference), while PATH resolves a DIFFERENT host `go` (e.g. from setup-go).
// Building the generated program with the PATH `go` under that inherited GOROOT
// pairs one toolchain's tool with another's std and fails:
//
//	compile: version "go1.25.5" does not match go tool version "go1.25.12"
//
// Two self-consistent options avoid the mismatch:
//   - if GOROOT is set and $GOROOT/bin/go exists, invoke THAT go — its tool and
//     std are the same SDK, so they always agree; keep GOROOT in the env.
//   - otherwise fall back to PATH `go`, but strip any inherited GOROOT so the
//     PATH go uses its own bundled std.
func consistentGoToolchain(t *testing.T) (goBin string, env []string) {
	t.Helper()

	goExe := "go"
	if runtime.GOOS == "windows" {
		goExe = "go.exe"
	}

	env = os.Environ()
	if goroot := os.Getenv("GOROOT"); goroot != "" {
		candidate := filepath.Join(goroot, "bin", goExe)
		if _, err := os.Stat(candidate); err == nil {
			return candidate, env // GOROOT's own go: tool + std are consistent.
		}
	}

	// Fall back to PATH go with any inherited GOROOT removed.
	p, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go toolchain not on PATH; cannot compile+run generated program")
	}
	return p, stripEnvVar(env, "GOROOT")
}

// stripEnvVar returns env with every `KEY=...` entry removed. It copies into a
// fresh slice so the caller's os.Environ() backing array is left untouched.
func stripEnvVar(env []string, key string) []string {
	prefix := key + "="
	out := make([]string, 0, len(env))
	for _, e := range env {
		if strings.HasPrefix(e, prefix) {
			continue
		}
		out = append(out, e)
	}
	return out
}

// importBlockDemoSource uses a grouped `import (...)` block pulling in two GALA
// packages that resolve in the transformer test sandbox — std (Option.GetOrElse)
// and collection_immutable (ArrayTabulate). (Go-interop imports need the full
// dependency graph, so that combination is exercised by the bazel example
// examples/sourcemap_imports_demo.gala instead.)
const importBlockDemoSource = `package main

import (
    . "martianoff/gala/std"
    . "martianoff/gala/collection_immutable"
)

func main() {
    val rows = ArrayTabulate(3, (i) => i * 2)
    val label = Some("ok").GetOrElse("none")
    Println(s"$label ${rows.String()}")
}
`

const importBlockDemoFile = "sourcemap_import_block.gala"

// TestLineDirectives_ImportBlockIntact verifies that the //line marker rewrite
// leaves an `import (...)` block untouched: directives are emitted for the user
// statements, the imports survive verbatim (no directive lands inside the block
// or rewrites an import path), the output still parses, and no raw markers leak.
// This is a transpile-only test — CI-safe regardless of the Go toolchain.
func TestLineDirectives_ImportBlockIntact(t *testing.T) {
	trans := newLineDirectiveTranspiler()
	goCode, err := trans.Transpile(importBlockDemoSource, importBlockDemoFile)
	require.NoError(t, err)

	// Directives are emitted for the user statements.
	assert.Contains(t, goCode, "//line "+importBlockDemoFile+":",
		"user statements should get //line directives; generated:\n%s", goCode)
	// No raw markers leak into the output.
	assert.NotContains(t, goCode, transpiler.LineMarkerPrefix,
		"no raw line markers should remain; generated:\n%s", goCode)

	// The generated Go must still parse, and the import block must be intact:
	// both imported paths present, and no `//line` directive sitting between
	// `import (` and its closing `)`.
	fset := token.NewFileSet()
	file, parseErr := parser.ParseFile(fset, importBlockDemoFile+".go", goCode, parser.ParseComments)
	require.NoError(t, parseErr, "generated Go must parse; generated:\n%s", goCode)

	importPaths := make(map[string]bool)
	for _, imp := range file.Imports {
		importPaths[strings.Trim(imp.Path.Value, "\"")] = true
	}
	assert.True(t, importPaths["martianoff/gala/std"],
		"std import must survive the marker rewrite; imports: %v", importPaths)
	assert.True(t, importPaths["martianoff/gala/collection_immutable"],
		"collection_immutable import must survive the marker rewrite; imports: %v", importPaths)

	assertNoDirectiveInsideImportBlock(t, goCode)
}

// assertNoDirectiveInsideImportBlock fails if a `//line` directive appears
// between `import (` and its matching `)` — a directive there would break the
// grouped import block.
func assertNoDirectiveInsideImportBlock(t *testing.T, goCode string) {
	t.Helper()
	lines := strings.Split(goCode, "\n")
	inBlock := false
	for _, ln := range lines {
		trimmed := strings.TrimSpace(ln)
		switch {
		case strings.HasPrefix(trimmed, "import ("):
			inBlock = true
		case inBlock && trimmed == ")":
			inBlock = false
		case inBlock && strings.HasPrefix(trimmed, "//line "):
			t.Errorf("a //line directive must not appear inside the import block: %q", ln)
		}
	}
}

// TestLineDirectives_ReservedNameRejected verifies Fix 2: a user declaration
// whose name intrudes on the reserved `__gala_line_` marker namespace is
// rejected with a clear error rather than being silently deleted by the //line
// rewrite (a top-level var/val is the deletion vector).
func TestLineDirectives_ReservedNameRejected(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{"top-level var", "package main\n\nvar __gala_line_7 = 3\n"},
		{"top-level val", "package main\n\nval __gala_line_7 = 3\n"},
	}

	trans := newLineDirectiveTranspiler()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := trans.Transpile(tc.src, "reserved_demo.gala")
			require.Error(t, err, "reserved-prefix identifier must be rejected")
			assert.Contains(t, err.Error(), "reserved",
				"error should explain the name is reserved; got: %v", err)
			assert.Contains(t, err.Error(), transpiler.LineMarkerPrefix,
				"error should name the reserved prefix; got: %v", err)
		})
	}
}
