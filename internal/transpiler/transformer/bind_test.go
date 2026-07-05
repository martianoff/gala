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
// scope for later statements. Each bound name is a GALA `val` (immutable): the
// raw FlatMap callback parameter is rebound to an Immutable-backed local
// (`name := std.NewImmutable(_bind_name)`) and later reads unwrap it via `.Get()`.
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
	// Outer FlatMap: result element int, source element int. The callback takes the
	// raw element as `_bind_a` and rebinds it to the immutable `a`.
	assert.Contains(t, got, "std.Try_FlatMap[int, int](half(x), func(_bind_a int) std.Try[int] {")
	assert.Contains(t, got, "a := std.NewImmutable(_bind_a)")
	// Inner FlatMap over the second bind, `a` still in scope and read via `.Get()`.
	assert.Contains(t, got, "std.Try_FlatMap[int, int](half(a.Get()), func(_bind_b int) std.Try[int] {")
	assert.Contains(t, got, "b := std.NewImmutable(_bind_b)")
	// Trailing value returned, both bound names read as immutable vals (`.Get()`).
	assert.Contains(t, got, "std.Success[int]{}.Apply(a.Get() + b.Get())")
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

// An `also` group over a fail-fast monad with no Zip lowers to the same
// sequential FlatMap chain as `bind` — both names in scope for the tail.
func TestAlsoSequentialFallback(t *testing.T) {
	trans := newBindTranspiler()
	input := `package main

func look(k int) Option[int] = if (k > 0) Some(k) else None[int]()

func run(x int, y int) Option[int] {
    bind a = look(x)
    also b = look(y)
    Some(a + b)
}
`
	got, err := trans.Transpile(input, "")
	assert.NoError(t, err)
	assert.Contains(t, got, "std.Option_FlatMap[int, int](look(x), func(_bind_a int) std.Option[int] {")
	assert.Contains(t, got, "a := std.NewImmutable(_bind_a)")
	assert.Contains(t, got, "std.Option_FlatMap[int, int](look(y), func(_bind_b int) std.Option[int] {")
	assert.Contains(t, got, "b := std.NewImmutable(_bind_b)")
	// Both bound names are immutable vals, read via `.Get()`.
	assert.Contains(t, got, "std.Some[int]{}.Apply(a.Get() + b.Get())")
}

// An `also` group over a monad that provides a `Zip` lowers to `ZipN(...).FlatMap`.
// The Zip result is a std tuple whose fields are Immutable-wrapped, so each bound
// name is a `val` bound STRAIGHT to the field (no extra NewImmutable wrap) and read
// via `.Get()`. Binding raw (a double NewImmutable) would be an Immutable[Immutable]
// type error downstream — this asserts the field is unwrapped exactly once.
func TestAlsoOverZipUnwrapsTupleFieldsAsVals(t *testing.T) {
	trans := newBindTranspiler()
	input := `package main

sealed type Box[T any] {
    case Wrap(Value T)
}

func (b Box[T]) FlatMap[U any](f func(T) Box[U]) Box[U] = b match {
    case Wrap(v) => f(v)
}

func (b Box[T]) Zip2[U any](o Box[U]) Box[Tuple[T, U]] = b match {
    case Wrap(v) => o match {
        case Wrap(w) => Wrap(Tuple(v, w))
    }
}

func run() Box[int] {
    bind a = Wrap(1)
    also b = Wrap(2)
    Wrap(a + b)
}
`
	got, err := trans.Transpile(input, "")
	assert.NoError(t, err)
	assert.Contains(t, got, "Box_Zip2[int, int]", "an `also` group over a Zip-providing monad lowers through ZipN")
	// Tuple fields are already Immutable, so the val is bound to the field directly.
	assert.Contains(t, got, "a := _zip.V1")
	assert.Contains(t, got, "b := _zip.V2")
	assert.NotContains(t, got, "std.NewImmutable(_zip.V1)", "tuple fields are already Immutable; must not be re-wrapped")
	// Bound names are immutable vals, read via `.Get()`.
	assert.Contains(t, got, "a.Get() + b.Get()")
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

// A clause in an `also` product group may not reference a binding introduced by
// a sibling clause of the same group: the clauses are evaluated independently
// (a product/Zip), so the value is genuinely not available. This is reported
// with a clear GALA diagnostic rather than the raw Go "undefined: a" that the
// generated code would otherwise produce.
func TestAlsoRejectsSiblingReference(t *testing.T) {
	trans := newBindTranspiler()
	input := `package main

func run() Try[int] {
    bind a = Success(1)
    also b = Success(a)
    Success(a + b)
}
`
	_, err := trans.Transpile(input, "")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "independently")
	assert.Contains(t, err.Error(), "`a`")
}

// The independence check must not flag a legitimate reference to a binding from
// an EARLIER group (which is in scope): only same-group siblings are rejected.
func TestBindAllowsCrossGroupReference(t *testing.T) {
	trans := newBindTranspiler()
	input := `package main

func run() Try[int] {
    bind a = Success(1)
    also b = Success(2)
    bind c = Success(a + b)
    Success(a + b + c)
}
`
	got, err := trans.Transpile(input, "")
	assert.NoError(t, err)
	assert.Contains(t, got, "FlatMap")
}

// `bind` on a user-defined type that has other methods but no FlatMap is rejected
// with a clear "not a bindable monad" diagnostic — not a cryptic downstream Go
// compile error.
func TestBindRejectsUserTypeWithoutFlatMap(t *testing.T) {
	trans := newBindTranspiler()
	input := `package main

sealed type Box[T any] {
    case Wrap(Value T)
}

// Box has a Map but deliberately no FlatMap.
func (b Box[T]) Map[U any](f func(T) U) Box[U] = b match {
    case Wrap(v) => Wrap(f(v))
}

func run() Box[int] {
    bind a = Wrap(1)
    Wrap(a)
}
`
	_, err := trans.Transpile(input, "")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "FlatMap")
	assert.Contains(t, err.Error(), "bindable")
}

// An `also` group over a user type with no FlatMap is rejected too: the group's
// leading `bind` requires FlatMap, so a missing implementation is caught up front
// rather than silently mis-lowered.
func TestAlsoRejectsUserTypeWithoutFlatMap(t *testing.T) {
	trans := newBindTranspiler()
	input := `package main

sealed type Box[T any] {
    case Wrap(Value T)
}

func (b Box[T]) Map[U any](f func(T) U) Box[U] = b match {
    case Wrap(v) => Wrap(f(v))
}

func run() Box[int] {
    bind a = Wrap(1)
    also b = Wrap(2)
    Wrap(a)
}
`
	_, err := trans.Transpile(input, "")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "FlatMap")
}
