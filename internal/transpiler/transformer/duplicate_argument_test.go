package transformer_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"martianoff/gala/galaerr"
)

// TestDuplicateArgument pins GALA-E0136: a call that gives one argument twice,
// by naming a parameter, field or method in two named arguments or by naming
// one a positional argument already fills, is rejected in every call form. A
// name given twice used to keep the last value silently for a GALA function
// or method, a Go function and Copy. The error points at the second
// occurrence.
func TestDuplicateArgument(t *testing.T) {
	const decls = `package main

import "strings"

func h(a int, b int = 0) int = a + b

func g[T any](x T, y T) T = y

struct Point(X int, Y int)

struct Box[T any](V T, W T)

struct Calc(Base int)

func (k Calc) Mix(a int, b int = 0) int = k.Base + a * 10 + b

func (k Calc) Pick[T any](first T, second T) T = second

type Pairer interface {
    Pair(first string, second string) string
}

func pairer(p Pairer) Pairer = p

sealed type Shape {
    case Circ(Tag int, R int)
    case Sq(Side int)
}

func lower() string = strings.ToLower("A")

func k(b int, a int, c int = 0) int = a + b + c
`
	rejected := []struct {
		name      string
		body      string
		wantError string
	}{
		{
			name:      "function, a name given twice",
			body:      `func main() { Println(h(a = 1, a = 2)) }`,
			wantError: "main.gala:33:31 argument `a` is given more than once in call to `h`",
		},
		{
			name:      "function, a name a positional argument fills",
			body:      `func main() { Println(h(1, a = 2)) }`,
			wantError: "main.gala:33:27 argument `a` is given both by position and by name in call to `h`",
		},
		{
			// Reported at the first such argument as written, not in
			// parameter or name order.
			name:      "function, several names positional arguments fill",
			body:      `func main() { Println(k(1, 2, b = 3, a = 4)) }`,
			wantError: "main.gala:33:30 argument `b` is given both by position and by name in call to `k`",
		},
		{
			name:      "generic function",
			body:      `func main() { Println(g(x = 1, y = 2, y = 3)) }`,
			wantError: "argument `y` is given more than once in call to `g`",
		},
		{
			name:      "method",
			body:      `func main() { Println(Calc(1).Mix(b = 1, b = 2, a = 0)) }`,
			wantError: "argument `b` is given more than once in call to `Calc(1).Mix`",
		},
		{
			name:      "method, a name a positional argument fills",
			body:      `func main() { Println(Calc(1).Mix(0, a = 2)) }`,
			wantError: "argument `a` is given both by position and by name in call to `Calc(1).Mix`",
		},
		{
			name:      "generic method",
			body:      `func main() { Println(Calc(1).Pick(first = 1, first = 2, second = 3)) }`,
			wantError: "argument `first` is given more than once in call to `Calc(1).Pick`",
		},
		{
			name:      "interface method",
			body:      `func main() { Println(pairer(nil).Pair(first = "a", first = "b", second = "c")) }`,
			wantError: "argument `first` is given more than once",
		},
		{
			name:      "Go function",
			body:      `func main() { Println(strings.Repeat(s = "a", count = 2, s = "b")) }`,
			wantError: "argument `s` is given more than once in call to `strings.Repeat`",
		},
		{
			name:      "Go function, a name a positional argument fills",
			body:      `func main() { Println(strings.Repeat("a", s = "b")) }`,
			wantError: "argument `s` is given both by position and by name in call to `strings.Repeat`",
		},
		{
			name:      "Go method",
			body:      `func main() { Println(strings.NewReplacer("a", "b").Replace(s = "a", s = "b")) }`,
			wantError: "argument `s` is given more than once",
		},
		{
			name:      "struct construction",
			body:      `func main() { Println(Point(X = 1, Y = 2, X = 3)) }`,
			wantError: "argument `X` is given more than once in call to `Point`",
		},
		{
			name:      "struct construction, a field a positional argument fills",
			body:      `func main() { Println(Point(1, X = 3, Y = 2)) }`,
			wantError: "argument `X` is given both by position and by name in call to `Point`",
		},
		{
			name:      "generic struct construction",
			body:      `func main() { Println(Box(V = 1, W = 2, V = 3)) }`,
			wantError: "argument `V` is given more than once in call to `Box`",
		},
		{
			name:      "sealed case construction",
			body:      `func main() { Println(Circ(Tag = 1, R = 2, R = 3)) }`,
			wantError: "argument `R` is given more than once in call to `Circ`",
		},
		{
			name:      "sealed case construction, a field a positional argument fills",
			body:      `func main() { Println(Circ(1, Tag = 2, R = 3)) }`,
			wantError: "argument `Tag` is given both by position and by name in call to `Circ`",
		},
		{
			name:      "Go struct construction",
			body:      `func main() { Println(strings.Builder(x = 1, x = 2)) }`,
			wantError: "argument `x` is given more than once in call to `strings.Builder`",
		},
		{
			name:      "Copy",
			body:      `func main() { Println(Point(1, 2).Copy(X = 5, X = 6)) }`,
			wantError: "main.gala:33:46 argument `X` is given more than once in call to `Point(1, 2).Copy`",
		},
		{
			name:      "interface literal",
			body:      `func main() { Println(Pairer(Pair = (a, b) => a, Pair = (a, b) => b)) }`,
			wantError: "argument `Pair` is given more than once in call to `Pairer`",
		},
	}
	for _, tc := range rejected {
		t.Run("rejects "+tc.name, func(t *testing.T) {
			_, err := transpileBareVariant(t, decls+tc.body)
			require.Error(t, err)
			require.Contains(t, err.Error(), string(galaerr.CodeDuplicateArgument))
			require.Contains(t, err.Error(), tc.wantError)
			require.Contains(t, err.Error(), "remove the duplicate; each parameter, field or method takes one argument")
		})
	}

	// A name no parameter or field has is reported before a named argument
	// for a parameter or field a positional argument fills, whatever the
	// order they are written in. (A name given twice is reported before
	// names are resolved, like a positional argument after a named one.)
	for _, tc := range []struct{ body, wantError string }{
		{`func main() { Println(h(1, a = 2, zz = 3)) }`, `unknown parameter "zz" in call to h`},
		{`func main() { Println(h(1, aa = 3, a = 2)) }`, `unknown parameter "aa" in call to h`},
		{`func main() { Println(Calc(1).Mix(1, a = 2, zz = 3)) }`, `unknown parameter "zz" in call to Mix`},
		{`func main() { Println(strings.Repeat("a", s = "b", zz = 1)) }`, `unknown parameter "zz" in call to strings.Repeat`},
		{`func main() { Println(Point(1, X = 2, Nope = 3)) }`, `unknown field "Nope" in construction of "Point"`},
	} {
		t.Run("unknown name first: "+tc.body, func(t *testing.T) {
			_, err := transpileBareVariant(t, decls+tc.body)
			require.Error(t, err)
			require.NotContains(t, err.Error(), string(galaerr.CodeDuplicateArgument))
			require.Contains(t, err.Error(), tc.wantError)
		})
	}

	// A callee too long to quote is not named, by either kind of GALA-E0136;
	// the error still names the argument and points at it.
	const longRecv = `Calc(1111111111 + 1111111111 + 1111111111 + 1111111111 + 1111111111)`
	for _, tc := range []struct{ call, wantError string }{
		{longRecv + `.Mix(b = 1, b = 2, a = 0)`, "argument `b` is given more than once"},
		{longRecv + `.Mix(0, a = 2)`, "argument `a` is given both by position and by name"},
		{longRecv + `.Pick[int](1, first = 2)`, "argument `first` is given both by position and by name"},
	} {
		t.Run("long callee: "+tc.call, func(t *testing.T) {
			_, err := transpileBareVariant(t, decls+`func main() { Println(`+tc.call+`) }`)
			require.Error(t, err)
			require.Contains(t, err.Error(), string(galaerr.CodeDuplicateArgument))
			require.Contains(t, err.Error(), tc.wantError)
			require.NotContains(t, err.Error(), "in call to")
		})
	}

	accepted := []struct {
		name     string
		body     string
		contains string
	}{
		{"function, every name once", `func main() { Println(h(b = 1, a = 2), h(1, b = 2)) }`, `h(2, 1)`},
		{"method, every name once", `func main() { Println(Calc(1).Mix(b = 1, a = 2)) }`, `.Mix(2, 1)`},
		{"Go function, every name once", `func main() { Println(strings.Repeat(count = 2, s = "ab")) }`, `strings.Repeat("ab", 2)`},
		{"construction and Copy, every field once", `func main() { Println(Point(Y = 2, X = 1).Copy(Y = 3), Circ(1, R = 2)) }`, `Circ{}.Apply(1, 2)`},
	}
	for _, tc := range accepted {
		t.Run("accepts "+tc.name, func(t *testing.T) {
			got, err := transpileBareVariant(t, decls+tc.body)
			require.NoError(t, err)
			require.Contains(t, got, tc.contains)
		})
	}
}
