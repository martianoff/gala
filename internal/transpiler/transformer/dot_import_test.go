package transformer_test

import (
	"bytes"
	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/analyzer"
	"martianoff/gala/internal/transpiler/generator"
	"martianoff/gala/internal/transpiler/transformer"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDotImportNoDuplicate(t *testing.T) {
	p := transpiler.NewAntlrGalaParser()
	a := analyzer.NewGalaAnalyzer(p, getStdSearchPath())
	tr := transformer.NewGalaASTTransformer()
	g := generator.NewGoCodeGenerator()
	trans := transpiler.NewGalaToGoTranspiler(p, a, tr, g)

	input := `package testpkg

import . "martianoff/gala/std"

type MyStruct struct {
    Value int
}

func test() int {
    val x = 42
    return x
}
`

	got, err := trans.Transpile(input, "")
	assert.NoError(t, err)

	// Count how many times std is imported
	stdImportCount := strings.Count(got, `"martianoff/gala/std"`)

	// Should only have ONE import of std (the dot import), not two
	assert.Equal(t, 1, stdImportCount,
		"Should have exactly one std import (dot import), got:\n%s", got)

	// Should have the dot import
	assert.Contains(t, got, `. "martianoff/gala/std"`,
		"Should contain dot import, got:\n%s", got)

	// Should NOT have a separate regular import
	lines := strings.Split(got, "\n")
	regularImportCount := 0
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == `import "martianoff/gala/std"` {
			regularImportCount++
		}
	}
	assert.Equal(t, 0, regularImportCount,
		"Should not have separate regular std import, got:\n%s", got)
}

func TestDotImportClashWarning(t *testing.T) {
	// Create two temp packages with clashing symbol names
	tempDir, err := os.MkdirTemp("", "dot_import_clash_test")
	assert.NoError(t, err)
	defer os.RemoveAll(tempDir)

	// Create go.mod
	err = os.WriteFile(filepath.Join(tempDir, "go.mod"), []byte("module testmod\n\ngo 1.21\n"), 0644)
	assert.NoError(t, err)

	// Create pkg_a with Greet function
	pkgADir := filepath.Join(tempDir, "pkg_a")
	err = os.MkdirAll(pkgADir, 0755)
	assert.NoError(t, err)
	err = os.WriteFile(filepath.Join(pkgADir, "a.gala"), []byte("package pkg_a\n\nfunc Greet() string = \"hello from a\"\n"), 0644)
	assert.NoError(t, err)

	// Create pkg_b with Greet function (clashes with pkg_a)
	pkgBDir := filepath.Join(tempDir, "pkg_b")
	err = os.MkdirAll(pkgBDir, 0755)
	assert.NoError(t, err)
	err = os.WriteFile(filepath.Join(pkgBDir, "b.gala"), []byte("package pkg_b\n\nfunc Greet() string = \"hello from b\"\n"), 0644)
	assert.NoError(t, err)

	// Change to temp directory so the resolver finds the temp go.mod
	originalWd, err := os.Getwd()
	assert.NoError(t, err)
	defer os.Chdir(originalWd)
	err = os.Chdir(tempDir)
	assert.NoError(t, err)

	// Capture stderr to detect the warning
	oldStderr := os.Stderr
	r, w, err := os.Pipe()
	assert.NoError(t, err)
	os.Stderr = w

	p := transpiler.NewAntlrGalaParser()
	a := analyzer.NewGalaAnalyzer(p, nil)
	tr := transformer.NewGalaASTTransformer()
	g := generator.NewGoCodeGenerator()
	trans := transpiler.NewGalaToGoTranspiler(p, a, tr, g)

	input := `package testpkg

import (
    . "testmod/pkg_a"
    . "testmod/pkg_b"
)

func test() int {
    return 42
}
`

	_, _ = trans.Transpile(input, "")

	// Read captured stderr
	w.Close()
	var buf bytes.Buffer
	buf.ReadFrom(r)
	os.Stderr = oldStderr
	stderrOutput := buf.String()

	// Should have warnings about clashing symbols
	assert.Contains(t, stderrOutput, "Warning: symbol")
	assert.Contains(t, stderrOutput, "Greet")
	assert.Contains(t, stderrOutput, "pkg_a")
	assert.Contains(t, stderrOutput, "pkg_b")
}

func TestDotImportNoClashWarning(t *testing.T) {
	// Capture stderr — should be empty when no clash
	oldStderr := os.Stderr
	r, w, err := os.Pipe()
	assert.NoError(t, err)
	os.Stderr = w

	p := transpiler.NewAntlrGalaParser()
	a := analyzer.NewGalaAnalyzer(p, getStdSearchPath())
	tr := transformer.NewGalaASTTransformer()
	g := generator.NewGoCodeGenerator()
	trans := transpiler.NewGalaToGoTranspiler(p, a, tr, g)

	// std and time_utils should not clash
	input := `package testpkg

import (
    . "martianoff/gala/std"
    . "martianoff/gala/time_utils"
)

func test() int {
    return 42
}
`

	_, _ = trans.Transpile(input, "")

	w.Close()
	var buf bytes.Buffer
	buf.ReadFrom(r)
	os.Stderr = oldStderr
	stderrOutput := buf.String()

	// Should NOT have any symbol clash warnings
	assert.NotContains(t, stderrOutput, "Warning: symbol")
}
