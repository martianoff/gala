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

// TestCrossFilePackageValUnwrap verifies that a package-level `val` declared in
// one file and referenced from another file of the same package is unwrapped
// from its std.Immutable[T] wrapper at the use site, exactly as a same-file
// reference is.
//
// Regression: the analyzer never recorded package-level val/var declarations,
// so when transforming the using file the transformer could not tell that a
// bare identifier resolved to an Immutable global. It emitted the raw wrapper
// where a plain T was expected, and `go build` rejected the generated code with
// "cannot use code (variable of struct type std.Immutable[int]) as int value".
func TestCrossFilePackageValUnwrap(t *testing.T) {
	tmpDir := t.TempDir()

	constsCode := `package main

val code = -32700
val name = "hi"

func errResp(n int) string = s"e$n"
func mkName(x string) string = s"[$x]"
`
	constsPath := filepath.Join(tmpDir, "consts.gala")
	assert.NoError(t, os.WriteFile(constsPath, []byte(constsCode), 0644))

	mainCode := `package main

func main() {
    Println(errResp(code))
    Println(mkName(name))
}
`
	mainPath := filepath.Join(tmpDir, "main.gala")
	assert.NoError(t, os.WriteFile(mainPath, []byte(mainCode), 0644))

	p := transpiler.NewAntlrGalaParser()

	tree, err := p.Parse(mainCode)
	assert.NoError(t, err)

	// Analyze main.gala with consts.gala as a sibling (multi-file mode).
	a := analyzer.NewGalaAnalyzerWithPackageFiles(p, getStdSearchPath(), []string{constsPath})
	richAST, err := a.Analyze(tree, mainPath)
	assert.NoError(t, err)

	// The analyzer must surface the sibling file's package-level vals with
	// their inferred element types and val classification.
	assert.Contains(t, richAST.PackageVals, "code", "sibling val 'code' must be recorded")
	assert.Contains(t, richAST.PackageVals, "name", "sibling val 'name' must be recorded")
	if codeMeta := richAST.PackageVals["code"]; assert.NotNil(t, codeMeta) {
		assert.True(t, codeMeta.IsVal)
		assert.Equal(t, "int", codeMeta.Type.String())
	}
	if nameMeta := richAST.PackageVals["name"]; assert.NotNil(t, nameMeta) {
		assert.True(t, nameMeta.IsVal)
		assert.Equal(t, "string", nameMeta.Type.String())
	}

	tr := transformer.NewGalaASTTransformer()
	g := generator.NewGoCodeGenerator()
	fset, file, err := tr.Transform(richAST)
	assert.NoError(t, err)
	result, err := g.Generate(fset, file)
	assert.NoError(t, err)

	// The cross-file reads must unwrap via .Get(), not pass the raw wrapper.
	assert.Contains(t, result, "errResp(code.Get())", "cross-file val must unwrap with .Get()")
	assert.Contains(t, result, "mkName(name.Get())", "cross-file val must unwrap with .Get()")
}
