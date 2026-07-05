package transformer_test

import (
	"testing"

	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/analyzer"
	"martianoff/gala/internal/transpiler/generator"
	"martianoff/gala/internal/transpiler/transformer"

	"github.com/stretchr/testify/assert"
)

func newBindTranspiler() *transpiler.GalaToGoTranspiler {
	p := transpiler.NewAntlrGalaParser()
	a := analyzer.NewGalaAnalyzer(p, getStdSearchPath())
	tr := transformer.NewGalaASTTransformer()
	g := generator.NewGoCodeGenerator()
	return transpiler.NewGalaToGoTranspiler(p, a, tr, g)
}

// A `bind` block lowers to a nested FlatMap chain. Every bound name stays in
// scope for later statements, and because each name is the raw FlatMap callback
// parameter it must NOT be unwrapped with `.Get()`.
func TestBindSequentialDesugarsToFlatMap(t *testing.T) {
	trans := newBindTranspiler()
	input := `package main

func half(n int) Try[int] =
    if (n % 2 == 0) Success(n / 2) else Failure[int](NoSuchElementError(Message = "odd"))

func run(x int) Try[int] {
    bind a = half(x)
    bind b = half(a)
    Success(a + b)
}
`
	got, err := trans.Transpile(input, "")
	assert.NoError(t, err)
	// Outer FlatMap: result element int, source element int.
	assert.Contains(t, got, "std.Try_FlatMap[int, int](half(x), func(a int) std.Try[int] {")
	// Inner FlatMap over the second bind, `a` still in scope.
	assert.Contains(t, got, "std.Try_FlatMap[int, int](half(a), func(b int) std.Try[int] {")
	// Trailing value returned, using both bound names raw (no `.Get()`).
	assert.Contains(t, got, "std.Success[int]{}.Apply(a + b)")
	assert.NotContains(t, got, "a.Get()", "raw FlatMap callback params must not be unwrapped")
}

// The desugaring is structural: a user-defined monad with no relationship to std
// works identically and is emitted as an unqualified `<Type>_FlatMap`.
func TestBindWorksOverUserMonad(t *testing.T) {
	trans := newBindTranspiler()
	input := `package main

sealed type Box[T any] {
    case Wrap(Value T)
}

func (b Box[T]) FlatMap[U any](f func(T) Box[U]) Box[U] = b match {
    case Wrap(v) => f(v)
}

func run() Box[int] {
    bind a = Wrap(1)
    bind c = Wrap(a + 1)
    Wrap(a + c)
}
`
	got, err := trans.Transpile(input, "")
	assert.NoError(t, err)
	assert.Contains(t, got, "Box_FlatMap[int, int]", "user monad lowers to its own <Type>_FlatMap")
	assert.NotContains(t, got, "std.Box_FlatMap", "user monad must not be std-qualified")
}

// `bind` on a value whose type has no FlatMap method is rejected.
func TestBindRejectsNonMonad(t *testing.T) {
	trans := newBindTranspiler()
	input := `package main

func run() Try[int] {
    bind a = 5
    Success(a)
}
`
	_, err := trans.Transpile(input, "")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "FlatMap")
}

// `also` with no preceding `bind` is rejected.
func TestAlsoRequiresBind(t *testing.T) {
	trans := newBindTranspiler()
	input := `package main

func run() Try[int] {
    also a = Success(1)
    Success(a)
}
`
	_, err := trans.Transpile(input, "")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "also")
}
