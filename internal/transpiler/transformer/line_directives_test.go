package transformer_test

import (
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

	// Write the generated Go into a throwaway module and build it. The program
	// is import-free, so a trivial go.mod with a low language version avoids any
	// toolchain download (kept offline via GOTOOLCHAIN=local).
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.go"), []byte(goCode), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "go.mod"),
		[]byte("module sourcemap_panic_demo\n\ngo 1.21\n"), 0o644))

	bin := filepath.Join(dir, "app")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}

	env := append(os.Environ(),
		"GOTOOLCHAIN=local",
		"GOCACHE="+filepath.Join(dir, "gocache"),
		"GOFLAGS=-mod=mod",
	)

	build := exec.Command("go", "build", "-o", bin, ".")
	build.Dir = dir
	build.Env = env
	if out, buildErr := build.CombinedOutput(); buildErr != nil {
		t.Fatalf("go build of generated program failed: %v\n--- output ---\n%s\n--- generated Go ---\n%s",
			buildErr, out, goCode)
	}

	run := exec.Command(bin)
	run.Env = env
	out, runErr := run.CombinedOutput()
	// The program must panic (non-zero exit).
	require.Error(t, runErr, "expected the program to panic; output:\n%s", out)

	trace := string(out)
	assert.Contains(t, trace, "integer divide by zero",
		"expected a division-by-zero panic; full output:\n%s", trace)

	wantPos := panicDemoFile + ":" + panicDemoDivideLine
	assert.Contains(t, trace, wantPos,
		"panic stack trace should report the GALA source position %q; full output:\n%s",
		wantPos, trace)
}
