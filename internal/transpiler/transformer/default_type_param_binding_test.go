package transformer_test

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"martianoff/gala/galaerr"
)

// TestDefaultTypeParamBinding: a default that names its declaration's own type
// parameter (`None[T]()`) is lowered at each use site with the type argument
// the use site binds, never with the declaration's `T`, which names nothing
// there or, inside a caller with a `T` of its own, the wrong type.
func TestDefaultTypeParamBinding(t *testing.T) {
	trans := newDefaultsTranspiler()

	const decls = `package main

func describe[T any](v Option[T] = None[T]()) string = v match {
    case Some(x) => s"some $x"
    case _ => "none"
}

func orFallback[T any](x T, fallback Option[T] = None[T]()) Option[T] = fallback.OrElse(Some(x))

func pick[T any](d Option[T] = None[T]()) Option[T] = d

struct slot[T any](Value Option[T] = None[T](), Label string = "slot")

struct Box[T any](V T)

func (b Box[T]) Or(alt Option[T] = None[T]()) Option[T] = alt.OrElse(Some(b.V))

func (b Box[T]) With[U any](u Option[U] = None[U]()) Tuple[T, Option[U]] = (b.V, u)
`

	cases := []struct {
		name     string
		body     string
		contains string
	}{
		{"explicit type argument", `func main() { Println(describe[int]()) }`, "describe[int](std.None[int]{}.Apply())"},
		{"inferred from another argument", `func main() { Println(orFallback("a")) }`, `orFallback("a", std.None[string]{}.Apply())`},
		{"from the slot of an all-defaults call", `func main() {
    val p Option[int] = pick()
    Println(p)
}`, "pick(std.None[int]{}.Apply())"},
		{"struct type argument", `func main() { Println(slot[int](Label = "a").Value) }`, "std.None[int]{}.Apply()"},
		{"receiver type argument", `func main() { Println(Box(V = 7).Or()) }`, "std.None[int]{}.Apply()"},
		{"caller with a type parameter of the same name", `func inner[T any](t T) string = describe[int]() + s" $t"

func main() { Println(inner("x")) }`, "describe[int](std.None[int]{}.Apply())"},
		{"caller binding a type parameter of another name", `func viaParam[U any](u U) Option[U] = orFallback(u)

func main() { Println(viaParam(true)) }`, "orFallback(u, std.None[U]{}.Apply())"},
		{"method type argument", `func main() { Println(Box(V = 1).With[string]()) }`, "std.None[string]{}.Apply()"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := trans.Transpile(decls+"\n"+tc.body, "")
			require.NoError(t, err)
			assert.Contains(t, got, tc.contains)
			// The declarations lower no default themselves (slot is
			// unexported, so it has no Go constructor applying its defaults),
			// so any None[T] is a use site naming the callee's T.
			assert.NotContains(t, got, "None[T]")
		})
	}
}

// TestDefaultTypeParamRecursiveCall: inside the declaration itself, a call
// binding T to the enclosing T keeps the default's T as written.
func TestDefaultTypeParamRecursiveCall(t *testing.T) {
	trans := newDefaultsTranspiler()

	got, err := trans.Transpile(`package main

func describe[T any](v Option[T] = None[T]()) string = v match {
    case Some(x) => s"some $x, then " + describe[T]()
    case None() => "none"
}

func main() {
    Println(describe(Some(1)))
}`, "")
	require.NoError(t, err)
	assert.Contains(t, got, "describe[T](std.None[T]{}.Apply())")
}

// TestDefaultTypeParamUnbound: a call that binds nothing to a type parameter
// its default names is a GALA error at the call — not Go's `undefined: T`,
// and not, in a caller with a T of its own, silently that unrelated T.
func TestDefaultTypeParamUnbound(t *testing.T) {
	trans := newDefaultsTranspiler()

	cases := []struct {
		name   string
		caller string
	}{
		{"plain caller", `func main() {
    Println(describe())
}`},
		{"caller with a type parameter of the same name", `func inner[T any](t T) string = describe() + s" $t"

func main() {
    Println(inner(1))
}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := trans.Transpile(`package main

func describe[T any](v Option[T] = None[T]()) string = v match {
    case Some(x) => s"some $x"
    case None() => "none"
}

`+tc.caller, "")
			require.Error(t, err)
			var se *galaerr.SemanticError
			require.True(t, errors.As(err, &se), "want a SemanticError, got %T: %v", err, err)
			assert.Equal(t, galaerr.CodeUninferredTypeArgument, se.Code)
			assert.Contains(t, se.Error(), "cannot infer type argument T")
		})
	}
}

// TestDefaultInferredFromCarriedTypeParam: a default that leaves its type
// argument to the declared type (`None()` for an `Option[T]` parameter) is
// lowered at a call inside a generic declaration that binds T to a type
// parameter of its own, of the same name or another.
func TestDefaultInferredFromCarriedTypeParam(t *testing.T) {
	trans := newDefaultsTranspiler()

	const decls = `package main

func pick[T any](v T, d Option[T] = None()) Option[T] = d.OrElse(Some(v))

struct Slot[T any](V T, Extra Option[T] = None())

sealed type Box[T any] {
    case Full(V T, Extra Option[T] = None())
    default case Empty()
}
`
	cases := []struct {
		name     string
		body     string
		contains string
	}{
		{"function, same name", `func f[T any](v T) Option[T] = pick(v)`, "pick(v, std.None[T]{}.Apply())"},
		{"function, another name", `func f[U any](u U) Option[U] = pick(u)`, "pick(u, std.None[U]{}.Apply())"},
		{"struct, same name", `func f[T any](v T) Slot[T] = Slot(v)`, "std.None[T]{}.Apply()"},
		{"sealed case, same name", `func f[T any](v T) Box[T] = Full(v)`, "Full[T]{}.Apply(v, std.None[T]{}.Apply())"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := trans.Transpile(decls+"\n"+tc.body+"\n\nfunc main() { Println(f(1)) }", "")
			require.NoError(t, err)
			assert.Contains(t, got, tc.contains)
		})
	}
}
