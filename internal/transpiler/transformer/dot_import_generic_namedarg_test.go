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

// TestDotImportGenericNamedArgs verifies that named-argument construction of
// a generic Go-style block-form struct (declared in a dot-imported package
// with no GALA source visible to the analyzer) does not get rejected.
//
// Before the fix, the validator's dot-import fallback only matched bare
// *ast.Ident, so call targets like `Container[int]` (an *ast.IndexExpr) and
// `Pair[A, B]` (an *ast.IndexListExpr) fell through to a SemanticError:
// "named arguments only supported for Copy method or struct construction".
//
// The fix walks through IndexExpr/IndexListExpr to the base identifier and
// emits a plain Go composite literal — same as for non-generic dot-imported
// types.
func TestDotImportGenericNamedArgs(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "dot_import_generic_namedarg_test")
	assert.NoError(t, err)
	defer os.RemoveAll(tempDir)

	err = os.WriteFile(filepath.Join(tempDir, "go.mod"), []byte("module testmod\n\ngo 1.22\n"), 0644)
	assert.NoError(t, err)

	// Go-only library exposing one non-generic and two generic struct types.
	libDir := filepath.Join(tempDir, "lib")
	err = os.MkdirAll(libDir, 0755)
	assert.NoError(t, err)
	err = os.WriteFile(filepath.Join(libDir, "lib.go"), []byte(`package lib

type Banner struct {
	Title   string
	Content string
}

type Container[A any] struct {
	Value A
}

type Pair[A any, B any] struct {
	First  A
	Second B
}
`), 0644)
	assert.NoError(t, err)

	originalWd, err := os.Getwd()
	assert.NoError(t, err)
	defer os.Chdir(originalWd)
	err = os.Chdir(tempDir)
	assert.NoError(t, err)

	p := transpiler.NewAntlrGalaParser()
	a := analyzer.NewGalaAnalyzer(p, getStdSearchPath())
	tr := transformer.NewGalaASTTransformer()
	g := generator.NewGoCodeGenerator()
	trans := transpiler.NewGalaToGoTranspiler(p, a, tr, g)

	input := `package main

import . "testmod/lib"

func main() {
    val banner = Banner(Title = "hi", Content = "there")
    val box = Container[int](Value = 42)
    val pair = Pair[string, int](First = "answer", Second = 42)
}
`

	got, err := trans.Transpile(input, "")
	assert.NoError(t, err, "expected named-arg construction of generic dot-imported struct to succeed; got error:\n%v", err)
	if err != nil {
		return
	}

	// Generic instantiations must produce composite literals with type args
	// preserved. The exact shape is `Container[int]{Value: 42}` and
	// `Pair[string, int]{First: ..., Second: ...}`.
	assert.Contains(t, got, "Container[int]{",
		"expected Container[int]{...} composite literal, got:\n%s", got)
	assert.Contains(t, got, "Pair[string, int]{",
		"expected Pair[string, int]{...} composite literal, got:\n%s", got)
	assert.Contains(t, got, "Banner{",
		"expected Banner{...} composite literal, got:\n%s", got)

	// Sanity: the validator's error message must not appear.
	assert.False(t, strings.Contains(got, "named arguments only supported"),
		"output unexpectedly contains validator error string:\n%s", got)
}
