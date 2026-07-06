package transformer_test

import (
	"strings"
	"testing"

	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/analyzer"
	"martianoff/gala/internal/transpiler/generator"
	"martianoff/gala/internal/transpiler/transformer"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A literal value used as a constructor sub-pattern in a sealed-type match must
// be compared for equality, not bound as a variable. Regression guard: the
// boolean literals `true`/`false` are all-letters and were previously
// misclassified by the extractor path (isSimpleIdentifier) as simple bindings,
// yielding a spurious "unused variable 'true'" semantic error. Numeric literals
// are covered too since they travel the same binding-vs-literal fork.
func TestMatchLiteralSubPattern(t *testing.T) {
	p := transpiler.NewAntlrGalaParser()
	a := analyzer.NewGalaAnalyzer(p, getStdSearchPath())
	tr := transformer.NewGalaASTTransformer()
	g := generator.NewGoCodeGenerator()
	trans := transpiler.NewGalaToGoTranspiler(p, a, tr, g)

	input := `package main

sealed type Animal {
    case Dog(Name string, Tricks int)
    case Cat(Name string, Indoor bool)
    case Fish(Species string)
}

func describe(a Animal) string = a match {
    case Dog(name, 0)     => s"$name is a lazy dog"
    case Dog(name, _)     => s"$name is a good dog"
    case Cat(name, true)  => s"$name is an indoor cat"
    case Cat(name, false) => s"$name is an outdoor cat"
    case Fish(species)    => s"a $species fish"
}
`

	got, err := trans.Transpile(input, "")
	require.NoError(t, err, "boolean/numeric literal sub-patterns must not be treated as bindings")

	// Boolean and numeric literals become equality checks against the extracted
	// field value, never `name := true`-style bindings.
	assert.Contains(t, got, "== true", "true sub-pattern should compile to an equality check")
	assert.Contains(t, got, "== false", "false sub-pattern should compile to an equality check")
	assert.Contains(t, got, "== 0", "numeric sub-pattern should compile to an equality check")
	assert.NotContains(t, got, "true :=", "true must not be bound as a variable")
	assert.NotContains(t, got, "false :=", "false must not be bound as a variable")

	// Sanity: the string-binding sub-patterns still bind normally.
	assert.True(t, strings.Contains(got, "name :="), "identifier sub-patterns should still bind")
}
