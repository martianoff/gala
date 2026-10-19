package transformer_test

import (
	"strings"
	"testing"

	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/analyzer"
	"martianoff/gala/internal/transpiler/generator"
	"martianoff/gala/internal/transpiler/transformer"

	"github.com/stretchr/testify/assert"
)

func newBindTranspiler() *checkedTranspiler {
	p := transpiler.NewAntlrGalaParser()
	a := analyzer.NewGalaAnalyzer(p, getStdSearchPath())
	tr := transformer.NewGalaASTTransformer()
	g := generator.NewGoCodeGenerator()
	return newCheckedTranspiler(p, a, tr, g)
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

// A monad whose FlatMap and Zip have pointer receivers gets each lowered call's
// receiver as a method call would: an addressable value by its address, a call
// result through an addressed copy (#800).
func TestBindOverPointerReceiverMonad(t *testing.T) {
	trans := newBindTranspiler()
	input := `package main

struct Cell[T any](var V T)

func (c *Cell[T]) FlatMap[U any](f func(T) Cell[U]) Cell[U] = f(c.V)

func (c *Cell[T]) Zip2[U any](o Cell[U]) Cell[Tuple[T, U]] = Cell(Tuple(c.V, o.V))

func sum() Cell[int] {
    var x = Cell(1)
    bind a = x
    bind b = Cell(2)
    Cell(a + b)
}

func zip() Cell[int] {
    bind a = Cell(1)
    also b = Cell(2)
    Cell(a + b)
}
`
	got, err := trans.Transpile(input, "")
	assert.NoError(t, err)
	assert.Contains(t, got, "Cell_FlatMap[int, int](&x, ")
	assert.Contains(t, got, "Cell_FlatMap[int, int](std.AddrOfCopy(Cell[int]{V: 2}), ")
	assert.Contains(t, got, "Cell_FlatMap[int, std.Tuple[int, int]](std.AddrOfCopy(Cell_Zip2[int, int](std.AddrOfCopy(Cell[int]{V: 1}), Cell[int]{V: 2})), ")
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

// A sibling reference inside an interpolated string is one too: the string's
// expressions are parsed only when the clause is lowered.
func TestAlsoRejectsInterpolatedSiblingReference(t *testing.T) {
	input := "package main\n\nfunc run() Try[int] {\n    bind a = Success(1)\n    also b = Success(s\"v=$a\")\n    Success(a)\n}\n"
	_, err := newBindTranspiler().Transpile(input, "")
	if !assert.Error(t, err) {
		return
	}
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

// A block may hold several product groups back-to-back. Each group lowers through
// its own `Zip`, and consecutive groups compose via `FlatMap` (applicative within
// a group, monadic across groups). Here two `also` groups each lower through the
// monad's Zip2 and are threaded together, with every bound name in scope for the
// trailing value.
func TestBindMultipleZipGroupsCompose(t *testing.T) {
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
    bind c = Wrap(3)
    also d = Wrap(4)
    Wrap(a + b + c + d)
}
`
	got, err := trans.Transpile(input, "")
	assert.NoError(t, err)
	// Two distinct product groups, each lowered through its own Zip2 call
	// (matching the instantiated call site, not the generic definition).
	assert.Equal(t, 2, strings.Count(got, "Box_Zip2[int, int]("),
		"each of the two `also` groups lowers through its own Zip2")
	// All four bindings are in scope for the trailing value.
	assert.Contains(t, got, "a.Get() + b.Get() + c.Get() + d.Get()")
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

// A `bind _ = <effect>` sequences a monadic effect (e.g. FromError of a Go
// error-only call) but discards its value. The lowering must NOT emit
// `_ := std.NewImmutable(...)` — that is invalid Go ("no new variables on left
// side of :="). The FlatMap still threads the chain; the callback param is left
// unused (legal for a Go func parameter).
func TestBindDiscardNameEmitsNoInvalidAssign(t *testing.T) {
	trans := newBindTranspiler()
	input := `package main

func eff() Try[bool] = Success(true)

func run() Try[int] {
    bind _ = eff()
    Success(42)
}
`
	got, err := trans.Transpile(input, "")
	assert.NoError(t, err)
	assert.NotContains(t, got, "_ :=",
		"a discard bind must not emit an invalid `_ := ...` assignment:\n%s", got)
	assert.Contains(t, got, "FlatMap",
		"the discard bind should still lower to a FlatMap chain:\n%s", got)
}

// A `bind` in a braced if-expression branch lowers the rest of the branch to a
// FlatMap chain typed by the branch's monad (Option[bool]), not the function's.
func TestBindInIfExpressionBranch(t *testing.T) {
	trans := newBindTranspiler()
	input := `package main

func run(n int) Option[int] {
    val ok = if (n > 0) { bind y = Some(n); Some(y > 1) } else { Some(false) }
    ok.Map((b) => n)
}
`
	got, err := trans.Transpile(input, "")
	assert.NoError(t, err)
	assert.Contains(t, got, "std.Option_FlatMap[bool, int](", "the branch lowers to a FlatMap chain:\n%s", got)
	assert.Contains(t, got, ") std.Option[bool] {", "the chain is typed by the branch's monad:\n%s", got)
}

// With std dot-imported, a bare `Option_FlatMap[R, A]` is typed Option[R], not
// Option[R, A], so a match arm holding a bind chain agrees with its siblings.
func TestBindInMatchArmWithDotImportedStd(t *testing.T) {
	trans := newBindTranspiler()
	input := `package main

import . "martianoff/gala/std"

func run(n int) Option[bool] {
    val o = n match {
        case 0 => Some(false)
        case _ => { bind h = Some(n); Some(h > 1) }
    }
    o
}
`
	got, err := trans.Transpile(input, "")
	assert.NoError(t, err)
	assert.Contains(t, got, "Option_FlatMap[bool, int](", "the arm lowers to a FlatMap chain:\n%s", got)
}

// After a `bind` in an if-expression branch, a `return` runs inside the bind's
// FlatMap callback and could not leave the function, so it is rejected.
func TestBindInIfExpressionBranchRejectsReturn(t *testing.T) {
	trans := newBindTranspiler()
	input := `package main

func run(n int, c bool) Option[int] {
    val v = if (c) {
        bind a = Some(n)
        if (a > 3) {
            return None()
        }
        Some(a)
    } else Some(0)
    v
}
`
	_, err := trans.Transpile(input, "")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "after a `bind` in an if-expression branch cannot leave the enclosing function")
}

// A block that ends in its `bind` has no value for the chain to produce.
func TestBindBlockEndingInBindIsRejected(t *testing.T) {
	trans := newBindTranspiler()
	for name, body := range map[string]string{
		"function body":        "bind a = Some(n)",
		"if-expression branch": "val q = if (n > 0) { bind a = Some(n) } else Some(1)\n    q",
	} {
		t.Run(name, func(t *testing.T) {
			input := "package main\n\nfunc run(n int) Option[int] {\n    " + body + "\n}\n"
			_, err := trans.Transpile(input, "")
			assert.Error(t, err)
			assert.Contains(t, err.Error(), "a `bind` block must end with a value expression")
		})
	}
}

// Programs a `bind` makes invalid are rejected with a GALA diagnostic rather
// than lowered to Go that does not compile, or that silently drops the chain.
func TestBindMisuseIsRejected(t *testing.T) {
	trans := newBindTranspiler()
	for _, tc := range []struct {
		name, body, want string
	}{
		{
			name: "trailing value of another type",
			body: "bind y = Some(n)\n    Println(y)",
			want: "must end with a std.Option value, but `Println(y)` produces no value",
		},
		{
			name: "in the body of an if statement",
			body: "if (n == 1) {\n        bind y = Some(1)\n        Some(y)\n    }\n    Some(n)",
			want: "`bind` is not allowed in a block whose value is not used",
		},
		{
			name: "in a loop body",
			body: "for i := 0; i < 2; i++ {\n        bind y = Some(i)\n        Some(y)\n    }\n    Some(n)",
			want: "`bind` is not allowed in a block whose value is not used",
		},
		{
			name: "annotation that does not match the bound value",
			body: "bind y string = Some(n)\n    Some(5)",
			want: "`y` is annotated string, but std.Option[int] binds a value of type int",
		},
		{
			name: "trailing if with no else",
			body: "bind y = Some(n)\n    if (y > 1) { Some(y) }",
			want: "a `bind` block must end with a value expression; this `if` ends it",
		},
		{
			name: "return after a bind in a match arm lowered as statements",
			body: "val r = n match {\n        case 0 => { return None() }\n        case _ => {\n            bind h = Some(n)\n            if (h > 3) { return None() }\n            Some(h)\n        }\n    }\n    r",
			want: "after a `bind` in a match arm cannot leave the enclosing function",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := "package main\n\nfunc run(n int) Option[int] {\n    " + tc.body + "\n}\n"
			_, err := trans.Transpile(input, "")
			if assert.Error(t, err) {
				assert.Contains(t, err.Error(), tc.want)
			}
		})
	}
}

// A `bind` block may end in an if/else chain: each branch ends in the
// block's value, typed by the block's monad (so `None()` needs no type
// argument), and may hold a `bind` of its own.
func TestBindBlockEndingInIfElse(t *testing.T) {
	trans := newBindTranspiler()
	input := `package main

func run(n int) Option[int] {
    bind y = Some(n)
    if (y > 5) {
        None()
    } else if (y > 2) {
        bind z = Some(y * 2)
        Some(z)
    } else {
        Some(y)
    }
}
`
	got, err := trans.Transpile(input, "")
	if assert.NoError(t, err) {
		assert.Contains(t, got, "return std.None[int]{}.Apply()", "the None() branch is typed by the block:\n%s", got)
		assert.Equal(t, 2, strings.Count(got, "Option_FlatMap[int, int]("), "the nested bind lowers inside its branch:\n%s", got)
	}
}

// A `bind` in an arm of a match lowered as statements (another arm returns)
// is typed by the match, and its chain is stored as the arm's value rather
// than returned from the function.
func TestBindInHoistedMatchArm(t *testing.T) {
	trans := newBindTranspiler()
	input := `package main

func run(n int) string {
    val r = n match {
        case 0 => { return "zero" }
        case _ => {
            bind h = Some(n)
            Some(h > 2)
        }
    }
    s"$r"
}
`
	got, err := trans.Transpile(input, "")
	if assert.NoError(t, err) {
		assert.Contains(t, got, "Option_FlatMap[bool, int](", "the chain is typed by the match:\n%s", got)
		assert.NotContains(t, got, "return std.Option_FlatMap", "the chain is the arm's value, not the function's:\n%s", got)
	}
}
