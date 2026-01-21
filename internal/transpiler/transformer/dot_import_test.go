package transformer_test

import (
	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/analyzer"
	"martianoff/gala/internal/transpiler/generator"
	"martianoff/gala/internal/transpiler/transformer"
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
