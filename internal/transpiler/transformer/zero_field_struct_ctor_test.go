package transformer_test

import (
	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/analyzer"
	"martianoff/gala/internal/transpiler/generator"
	"martianoff/gala/internal/transpiler/transformer"
	"testing"

	"github.com/stretchr/testify/assert"
)

// A zero-field shorthand struct constructed as `Foo()` previously lowered to a
// bare Go call `Foo()`, which Go reads as a type conversion needing one argument
// ("missing argument in conversion to Foo"). It must lower to a composite
// literal `Foo{}`. Non-empty shorthand structs (`Foo(x)`) and the explicit
// `Foo{}` literal already worked and must keep working.
func TestZeroFieldStructConstructor(t *testing.T) {
	p := transpiler.NewAntlrGalaParser()
	a := analyzer.NewGalaAnalyzer(p, getStdSearchPath())
	tr := transformer.NewGalaASTTransformer()
	g := generator.NewGoCodeGenerator()
	trans := transpiler.NewGalaToGoTranspiler(p, a, tr, g)

	t.Run("Foo() lowers to composite literal", func(t *testing.T) {
		input := `package main

struct Mock()

func (m Mock) Tag() string = "mock"

func main() {
    val m = Mock()
    Println(m.Tag())
}`
		got, err := trans.Transpile(input, "")
		assert.NoError(t, err)
		assert.Contains(t, got, "Mock{}", "Foo() must construct via composite literal")
		assert.NotContains(t, got, "Mock()", "Foo() must not lower to a Go type conversion")
	})

	t.Run("non-empty shorthand struct still uses field literal", func(t *testing.T) {
		input := `package main

struct Mock(N int)

func (m Mock) Tag() string = "mock"

func main() {
    val m = Mock(0)
    Println(m.Tag())
}`
		got, err := trans.Transpile(input, "")
		assert.NoError(t, err)
		assert.Contains(t, got, "Mock{N:", "Foo(x) must keep positional struct construction")
	})
}
