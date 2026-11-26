package transformer_test

import (
	"testing"

	"martianoff/gala/galaerr"

	"github.com/stretchr/testify/require"
)

// TestParenthesizedPattern pins that parentheses in a pattern only group
// (#1353): `(p)` is read and lowered as p, at the top of a case, as an
// extractor's argument, as a tuple element and as an alternative.
func TestParenthesizedPattern(t *testing.T) {
	wrap := func(src string) string {
		return "package main\n\n" + src + "\nfunc main() {\n    Println(0)\n}\n"
	}
	accepted := []struct{ name, src string }{
		{
			name: "a parenthesized binding is a match's default",
			src: `func f(n int) int = n match {
    case 0 => 0
    case (k) => k
}
`,
		},
		{
			name: "a parenthesized binding of an alias subject keeps the subject's type",
			src: `struct Point(X int, Y int)

func (p Point) Sum() int = p.X + p.Y

type Coord Point

func f(c Coord) int = c match {
    case (d) => d.Sum()
}
`,
		},
		{
			name: "a parenthesized extractor argument binds",
			src: `val x = 3

func f(o Option[int]) int = o match {
    case Some((x)) => x
    case None() => 0
}
`,
		},
		{
			name: "parenthesized bool literals cover a bool field",
			src: `func f(o Option[bool]) int = o match {
    case Some((true)) => 1
    case Some((false)) => 2
    case None() => 0
}
`,
		},
		{
			name: "a parenthesized rest binds as the rest does",
			src: `import . "martianoff/gala/collection_immutable"

func f(xs Array[int]) int = xs match {
    case Array(a, (r)...) => a + r.Size()
    case _ => 0
}

val ys = ArrayOf(1)
`,
		},
		{
			name: "a parenthesized variant at the top of a case tests the value",
			src: `func f(o Option[int]) int = o match {
    case (Some(n)) => n
    case (None()) => 0
}
`,
		},
	}
	for _, tc := range accepted {
		t.Run(tc.name, func(t *testing.T) {
			_, err := newForbiddenBuiltinTranspiler().Transpile(wrap(tc.src), "")
			require.NoError(t, err)
		})
	}

	t.Run("a case after a parenthesized binding is unreachable, as after `x`", func(t *testing.T) {
		src := `func f(t Tuple[int, int]) int = t match {
    case ((a), b) => a + b
    case (1, _) => 0
}
`
		_, err := newForbiddenBuiltinTranspiler().Transpile(wrap(src), "")
		require.Error(t, err)
		require.Contains(t, err.Error(), string(galaerr.CodeUnreachableCase))
	})

	t.Run("a parenthesized wildcard alternative is rejected like `_`", func(t *testing.T) {
		src := `func f(n int) string = n match {
    case (_) | 1 => "a"
    case _ => "b"
}
`
		_, err := newForbiddenBuiltinTranspiler().Transpile(wrap(src), "")
		require.Error(t, err)
		require.Contains(t, err.Error(), string(galaerr.CodeInvalidAlternativePattern))
	})
}
