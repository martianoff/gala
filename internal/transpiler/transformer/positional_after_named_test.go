package transformer_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"martianoff/gala/galaerr"
)

// TestPositionalAfterNamed pins GALA-E0105: once an argument is named, every
// later argument is named too, in every call form. A positional argument after
// a named one used to be accepted by a function call and rejected, under
// another code, by a construction. The accepted shapes are all-positional,
// named only at the end, and all-named in any order; examples/
// named_argument_order runs the evaluation order of the last.
func TestPositionalAfterNamed(t *testing.T) {
	const decls = `package main

func h(a int, b int, c int = 0) int = a * 100 + b * 10 + c

func g[T any](x T, y T) T = y

func ap(f func(int) int, n int) int = f(n)

struct Point(X int, Y int)

struct Calc(Base int)

func (k Calc) Mix(a int, b int) int = k.Base + a * 10 + b

sealed type Shape {
    case Circ(Tag int, R int)
    case Sq(Side int)
}
`
	rejected := []struct {
		name      string
		body      string
		wantError string
	}{
		{
			name:      "function call",
			body:      `func main() { Println(h(b = 2, 1)) }`,
			wantError: "main.gala:19:31 positional argument `1` follows named argument `b`",
		},
		{
			name:      "function call, the first positional after several named",
			body:      `func main() { Println(h(1, b = 2, c = 3, 4 + 1, 5)) }`,
			wantError: "positional argument `4 + 1` follows named argument `b`",
		},
		{
			name:      "generic function call",
			body:      `func main() { Println(g(y = 2, 1)) }`,
			wantError: "positional argument `1` follows named argument `y`",
		},
		{
			name:      "method call",
			body:      `func main() { Println(Calc(1).Mix(b = 2, 1)) }`,
			wantError: "positional argument `1` follows named argument `b`",
		},
		{
			name:      "struct construction",
			body:      `func main() { Println(Point(X = 1, 2)) }`,
			wantError: "positional argument `2` follows named argument `X`",
		},
		{
			name:      "sealed case construction",
			body:      `func main() { Println(Circ(Tag = 1, 2)) }`,
			wantError: "positional argument `2` follows named argument `Tag`",
		},
		{
			name:      "Copy",
			body:      `func main() { Println(Point(1, 2).Copy(X = 3, 4)) }`,
			wantError: "positional argument `4` follows named argument `X`",
		},
		{
			name:      "lambda after a named argument",
			body:      `func main() { Println(ap(n = 2, (x) => x + 5)) }`,
			wantError: "positional argument `(x) => x + 5` follows named argument `n`",
		},
		{
			name: "an argument on several lines is not quoted",
			body: `func main() {
    Println(ap(n = 2, (x) => {
        x + 1
    }))
}`,
			wantError: "main.gala:20:22 a positional argument follows named argument `n`",
		},
	}
	for _, tc := range rejected {
		t.Run("rejects "+tc.name, func(t *testing.T) {
			_, err := transpileBareVariant(t, decls+tc.body)
			require.Error(t, err)
			require.Contains(t, err.Error(), string(galaerr.CodePositionalAfterNamed))
			require.Contains(t, err.Error(), tc.wantError)
			require.Contains(t, err.Error(), "name it, or move it before the named arguments")
		})
	}

	accepted := []struct {
		name     string
		body     string
		contains string
	}{
		{"all positional", `func main() { Println(h(1, 2, 3), Point(1, 2), Calc(1).Mix(2, 3)) }`, `h(1, 2, 3)`},
		{"named only at the end", `func main() { Println(h(1, c = 3, b = 2), Point(1, Y = 2), Calc(1).Mix(2, b = 3)) }`, `Y: std.NewImmutable(2)`},
		{"all named, in any order", `func main() { Println(h(c = 3, b = 2, a = 1), Circ(R = 2, Tag = 1), Point(1, 2).Copy(Y = 4, X = 3)) }`, `Circ{}.Apply(1, 2)`},
	}
	for _, tc := range accepted {
		t.Run("accepts "+tc.name, func(t *testing.T) {
			out, err := transpileBareVariant(t, decls+tc.body)
			require.NoError(t, err)
			require.Contains(t, out, tc.contains)
		})
	}
}
