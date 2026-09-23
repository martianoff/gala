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

// TestCrossFileTypeAliasConversion verifies that a `type X Y` alias declared in
// one file of a package is visible to its siblings, both as a type annotation
// and as the conversion `X(v)`.
//
// The grammar's alias rule is `typeAlias: identifier | type`, and a scalar or
// named target — `type Millis int64`, `type Coord Point` — takes the identifier
// branch. The analyzer read only the `type` branch, so those aliases were never
// exported: a sibling saw an unknown type, and the conversion was reported as
// GALA-E0043 "Millis is a type, not a constructor".
func TestCrossFileTypeAliasConversion(t *testing.T) {
	tmpDir := t.TempDir()

	typesCode := `package main

type Millis int64
type Name string
`
	typesPath := filepath.Join(tmpDir, "types.gala")
	assert.NoError(t, os.WriteFile(typesPath, []byte(typesCode), 0644))

	mainCode := `package main

func toMillis(v int64) Millis = Millis(v)

func toName(s string) Name = Name(s)

func main() {
    Println(toMillis(1500))
    Println(toName("gala"))
}
`
	mainPath := filepath.Join(tmpDir, "main.gala")
	assert.NoError(t, os.WriteFile(mainPath, []byte(mainCode), 0644))

	p := transpiler.NewAntlrGalaParser()

	tree, _, err := p.Parse(mainCode)
	assert.NoError(t, err)

	a := analyzer.NewGalaAnalyzerWithPackageFiles(p, getStdSearchPath(), []string{typesPath})
	richAST, err := a.Analyze(tree, nil, mainPath)
	assert.NoError(t, err)

	// The identifier-branch aliases must reach the sibling with their targets.
	assert.Contains(t, richAST.TypeAliases, "Millis", "sibling alias 'Millis' must be recorded")
	assert.Contains(t, richAST.TypeAliases, "Name", "sibling alias 'Name' must be recorded")
	assert.Equal(t, "int64", richAST.TypeAliases["Millis"].String())
	assert.Equal(t, "string", richAST.TypeAliases["Name"].String())

	tr := transformer.NewGalaASTTransformer()
	g := generator.NewGoCodeGenerator()
	fset, file, err := tr.Transform(richAST)
	assert.NoError(t, err)
	result, err := g.Generate(fset, file)
	assert.NoError(t, err)

	// The alias names a non-struct, so the call is a Go conversion — never a
	// composite literal, which would drop the argument.
	assert.Contains(t, result, "return Millis(v)", "alias conversion must emit a Go conversion")
	assert.Contains(t, result, "return Name(s)", "alias conversion must emit a Go conversion")
	assert.NotContains(t, result, "Millis{}", "alias conversion must not build a composite literal")
}
