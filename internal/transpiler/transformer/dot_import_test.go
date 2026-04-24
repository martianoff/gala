package transformer_test

import (
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

func TestDotImportClashError(t *testing.T) {
	// Create two temp packages with clashing symbol names
	tempDir, err := os.MkdirTemp("", "dot_import_clash_test")
	assert.NoError(t, err)
	defer os.RemoveAll(tempDir)

	// Create go.mod
	err = os.WriteFile(filepath.Join(tempDir, "go.mod"), []byte("module testmod\n\ngo 1.22\n"), 0644)
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

	_, transpileErr := trans.Transpile(input, "")

	// Should return a hard error about clashing symbols
	assert.Error(t, transpileErr)
	assert.Contains(t, transpileErr.Error(), "dot-import symbol collision")
	assert.Contains(t, transpileErr.Error(), "Greet")
	assert.Contains(t, transpileErr.Error(), "pkg_a")
	assert.Contains(t, transpileErr.Error(), "pkg_b")
}

// TestDotImportVarReExportClash checks that when a Go package re-exports
// symbols from another package via `var X = other.X` (a common facade
// pattern, e.g. concurrent re-exporting go_interop helpers), and the user
// dot-imports BOTH packages, the collision is detected at the GALA level
// instead of deferred to the Go compiler's opaque "X redeclared in this
// block" error.
func TestDotImportVarReExportClash(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "dot_import_var_reexport_test")
	assert.NoError(t, err)
	defer os.RemoveAll(tempDir)

	err = os.WriteFile(filepath.Join(tempDir, "go.mod"), []byte("module testmod\n\ngo 1.22\n"), 0644)
	assert.NoError(t, err)

	// pkg_origin declares Helper as a function.
	originDir := filepath.Join(tempDir, "pkg_origin")
	err = os.MkdirAll(originDir, 0755)
	assert.NoError(t, err)
	err = os.WriteFile(filepath.Join(originDir, "origin.go"), []byte(`package pkg_origin

func Helper() int { return 7 }
`), 0644)
	assert.NoError(t, err)

	// pkg_facade re-exports pkg_origin.Helper via a var. Mixed with a .gala
	// file so the extraction path that is gated on "Go-only package" is
	// NOT the one that produces the Helper symbol — the var re-export
	// regex must fire unconditionally.
	facadeDir := filepath.Join(tempDir, "pkg_facade")
	err = os.MkdirAll(facadeDir, 0755)
	assert.NoError(t, err)
	err = os.WriteFile(filepath.Join(facadeDir, "facade.go"), []byte(`package pkg_facade

import "testmod/pkg_origin"

var Helper = pkg_origin.Helper
`), 0644)
	assert.NoError(t, err)
	err = os.WriteFile(filepath.Join(facadeDir, "extra.gala"), []byte(`package pkg_facade

func Other() int = 1
`), 0644)
	assert.NoError(t, err)

	originalWd, err := os.Getwd()
	assert.NoError(t, err)
	defer os.Chdir(originalWd)
	err = os.Chdir(tempDir)
	assert.NoError(t, err)

	p := transpiler.NewAntlrGalaParser()
	a := analyzer.NewGalaAnalyzer(p, nil)
	tr := transformer.NewGalaASTTransformer()
	g := generator.NewGoCodeGenerator()
	trans := transpiler.NewGalaToGoTranspiler(p, a, tr, g)

	input := `package testpkg

import (
    . "testmod/pkg_origin"
    . "testmod/pkg_facade"
)

func test() int {
    return 42
}
`

	_, transpileErr := trans.Transpile(input, "")
	assert.Error(t, transpileErr)
	assert.Contains(t, transpileErr.Error(), "dot-import symbol collision")
	assert.Contains(t, transpileErr.Error(), "Helper")
	assert.Contains(t, transpileErr.Error(), "pkg_origin")
	assert.Contains(t, transpileErr.Error(), "pkg_facade")
}

func TestDotImportNoQualifiedReferences(t *testing.T) {
	p := transpiler.NewAntlrGalaParser()
	a := analyzer.NewGalaAnalyzer(p, getStdSearchPath())
	tr := transformer.NewGalaASTTransformer()
	g := generator.NewGoCodeGenerator()
	trans := transpiler.NewGalaToGoTranspiler(p, a, tr, g)

	input := `package testpkg

import . "martianoff/gala/std"

func test() Option[int] {
    val x = Some(42)
    return x
}
`

	got, err := trans.Transpile(input, "")
	assert.NoError(t, err)

	assert.NotContains(t, got, "std.Option",
		"Dot-imported package should not produce qualified references, got:\n%s", got)
	assert.NotContains(t, got, "std.Some",
		"Dot-imported package should not produce qualified references, got:\n%s", got)
	assert.NotContains(t, got, "std.Immutable",
		"Dot-imported package should not produce qualified references, got:\n%s", got)
	assert.Contains(t, got, "Option[int]",
		"Should use unqualified type references with dot import, got:\n%s", got)
}

func TestDotImportNoClashNoError(t *testing.T) {
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

	_, err := trans.Transpile(input, "")
	assert.NoError(t, err)
}
