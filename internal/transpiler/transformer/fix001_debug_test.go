package transformer_test

import (
	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/analyzer"
	"martianoff/gala/internal/transpiler/generator"
	"martianoff/gala/internal/transpiler/transformer"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestFix001_SomeNoneInMultiFileMode verifies that Some()/None() produce the
// Apply constructor pattern in multi-file mode, not a plain type-conversion call.
// Regression test for BUG-008: analyzePackage was not clearing packageFiles before
// recursive Analyze calls, causing std type metadata to be lost in multi-file mode.
func TestFix001_SomeNoneInMultiFileMode(t *testing.T) {
	tmpDir := t.TempDir()

	lookupCode := `package main

func findValue(s string) Option[string] {
    if s == "hello" { return Some("found") }
    return None[string]()
}
`
	lookupPath := filepath.Join(tmpDir, "lookup.gala")
	err := os.WriteFile(lookupPath, []byte(lookupCode), 0644)
	assert.NoError(t, err)

	mainCode := `package main

import "fmt"

func main() {
    val r = findValue("hello")
    fmt.Println(r.IsDefined())
}
`
	mainPath := filepath.Join(tmpDir, "main.gala")
	err = os.WriteFile(mainPath, []byte(mainCode), 0644)
	assert.NoError(t, err)

	p := transpiler.NewAntlrGalaParser()

	t.Run("single-file-has-std-types", func(t *testing.T) {
		tree, err := p.Parse(lookupCode)
		assert.NoError(t, err)

		a := analyzer.NewGalaAnalyzer(p, getStdSearchPath())
		richAST, err := a.Analyze(tree, "")
		assert.NoError(t, err)

		assert.Contains(t, richAST.Types, "std.Some", "single-file should have std.Some type")
		assert.Contains(t, richAST.Types, "std.None", "single-file should have std.None type")
		assert.Contains(t, richAST.Types, "std.Option", "single-file should have std.Option type")

		tr := transformer.NewGalaASTTransformer()
		g := generator.NewGoCodeGenerator()
		fset, file, err := tr.Transform(richAST)
		assert.NoError(t, err)
		result, err := g.Generate(fset, file)
		assert.NoError(t, err)
		assert.Contains(t, result, ".Apply(", "single-file should produce Apply pattern")
	})

	t.Run("multi-file-has-std-types", func(t *testing.T) {
		tree, err := p.Parse(lookupCode)
		assert.NoError(t, err)

		a := analyzer.NewGalaAnalyzerWithPackageFiles(p, getStdSearchPath(), []string{mainPath})
		richAST, err := a.Analyze(tree, lookupPath)
		assert.NoError(t, err)

		// The key regression: multi-file mode must also have std types
		assert.Contains(t, richAST.Types, "std.Some", "multi-file should have std.Some type")
		assert.Contains(t, richAST.Types, "std.None", "multi-file should have std.None type")
		assert.Contains(t, richAST.Types, "std.Option", "multi-file should have std.Option type")

		tr := transformer.NewGalaASTTransformer()
		g := generator.NewGoCodeGenerator()
		fset, file, err := tr.Transform(richAST)
		assert.NoError(t, err)
		result, err := g.Generate(fset, file)
		assert.NoError(t, err)
		assert.Contains(t, result, ".Apply(", "multi-file should produce Apply pattern")
		assert.NotContains(t, result, "std.Some(\"found\")", "must NOT produce type-conversion style Some()")
	})
}
