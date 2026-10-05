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

struct Slot[T any](Value Option[T] = None[T](), Label string = "slot")

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
		{"struct type argument", `func main() { Println(Slot[int](Label = "a").Value) }`, "std.None[int]{}.Apply()"},
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
			// The declarations lower no default themselves, so any None[T]
			// is a use site naming the callee's T.
			assert.NotContains(t, got, "None[T]")
		})
	}
}

// TestDefaultTypeParamRecursiveCall: inside the declaration itself a call that
// binds nothing keeps the default's T, the enclosing T, which Go infers the
// callee's from.
func TestDefaultTypeParamRecursiveCall(t *testing.T) {
	trans := newDefaultsTranspiler()

	got, err := trans.Transpile(`package main

func describe[T any](v Option[T] = None[T]()) string = v match {
    case Some(x) => s"some $x, then " + describe()
    case None() => "none"
}

func main() {
    Println(describe(Some(1)))
}`, "")
	require.NoError(t, err)
	assert.Contains(t, got, "describe(std.None[T]{}.Apply())")
}

// TestDefaultTypeParamUnbound: a call that binds nothing to a type parameter
// its default names is a GALA error at the call, not Go's `undefined: T`.
func TestDefaultTypeParamUnbound(t *testing.T) {
	trans := newDefaultsTranspiler()

	_, err := trans.Transpile(`package main

func describe[T any](v Option[T] = None[T]()) string = v match {
    case Some(x) => s"some $x"
    case _ => "none"
}

func main() {
    Println(describe())
}`, "")
	require.Error(t, err)
	var se *galaerr.SemanticError
	require.True(t, errors.As(err, &se), "want a SemanticError, got %T: %v", err, err)
	assert.Equal(t, galaerr.CodeUninferredTypeArgument, se.Code)
	assert.Contains(t, se.Error(), "cannot infer type argument T")
	assert.Equal(t, 9, se.Line)
}
