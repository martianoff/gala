package transformer_test

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestNamedSpreadArgs pins a spread `xs...` passed by name. Into a variadic
// parameter it keeps its `...` once the named arguments are laid out in
// parameter order; it used to be dropped, so `label(prefix = "r:", parts =
// ws...)` was emitted as `label("r:", ws)` and failed in go build. Into any
// other parameter, by name or by position, or into a struct field, it is
// reported in GALA terms rather than left to go build.
// examples/named_args_variadic_spread.gala runs the accepted shapes.
func TestNamedSpreadArgs(t *testing.T) {
	const decls = `package main

func label(prefix string, parts ...string) string = s"$prefix${parts.Size()}"

func count[T any](label string, xs ...T) int = xs.Size()

func pair(a string, b string) string = a + b

struct Counter(N int)

func (c Counter) Plus(x int, xs ...int) int = c.N + x + xs.Size()

func (c Counter) Two(x int, y int) int = c.N + x + y

struct Box[T any](V T)

func (b Box[T]) Count(extra int, xs ...T) int = xs.Size() + extra

func (b Box[T]) Pick(x T, y T) T = x

func (b Box[T]) Tag[U any](label U, xs ...T) string = s"$label${xs.Size()}"

func (b Box[T]) Both[U any](x U, y T) U = x

struct Cfg(Name string)
`
	cases := []struct {
		name      string
		body      string
		contains  string
		wantError string
	}{
		{
			name:     "function",
			body:     `func f(ws ...string) string = label(prefix = "r:", parts = ws...)`,
			contains: `label("r:", ws...)`,
		},
		{
			name:     "function, spread written first",
			body:     `func f(ws ...string) string = label(parts = ws..., prefix = "r:")`,
			contains: `label("r:", ws...)`,
		},
		{
			name:     "generic function",
			body:     `func f(ns ...int) int = count(label = "n", xs = ns...)`,
			contains: `"n", ns...)`,
		},
		{
			name:     "method",
			body:     `func f(ns ...int) int = Counter(1).Plus(x = 2, xs = ns...)`,
			contains: `.Plus(2, ns...)`,
		},
		{
			name:     "method of a generic type",
			body:     `func f(ns ...int) int = Box(0).Count(extra = 2, xs = ns...)`,
			contains: `.Count(2, ns...)`,
		},
		{
			name:     "generic method",
			body:     `func f(ns ...int) string = Box(0).Tag(label = "t", xs = ns...)`,
			contains: `"t", ns...)`,
		},
		{
			name:      "function, non-variadic parameter",
			body:      `func f(ws ...string) string = pair(a = "x", b = ws...)`,
			wantError: `a spread argument cannot fill parameter "b" of pair: only a variadic last parameter takes one`,
		},
		{
			name:      "method, non-variadic parameter",
			body:      `func f(ns ...int) int = Counter(1).Two(x = 1, y = ns...)`,
			wantError: `a spread argument cannot fill parameter "y" of Two`,
		},
		{
			name:      "method of a generic type, non-variadic parameter",
			body:      `func f(ns ...int) int = Box(0).Pick(x = 1, y = ns...)`,
			wantError: `a spread argument cannot fill parameter "y" of Pick`,
		},
		{
			name:      "generic method, non-variadic parameter",
			body:      `func f(ns ...int) string = Box(0).Both(x = "a", y = ns...)`,
			wantError: `a spread argument cannot fill parameter "y" of Both`,
		},
		{
			name:      "function, non-variadic parameter by position",
			body:      `func f(ws ...string) string = pair("x", ws...)`,
			wantError: `a spread argument cannot fill parameter "b" of pair`,
		},
		{
			name:      "method, non-variadic parameter by position",
			body:      `func f(ns ...int) int = Counter(1).Two(1, ns...)`,
			wantError: `a spread argument cannot fill parameter "y" of Two`,
		},
		{
			name:      "method of a generic type, non-variadic parameter by position",
			body:      `func f(ns ...int) int = Box(0).Pick(1, ns...)`,
			wantError: `a spread argument cannot fill parameter "y" of Pick`,
		},
		{
			name:      "generic method, non-variadic parameter by position",
			body:      `func f(ns ...int) string = Box(0).Both("a", ns...)`,
			wantError: `a spread argument cannot fill parameter "y" of Both`,
		},
		{
			name:      "function, spread past the parameters",
			body:      `func f(ws ...string) string = pair("x", "y", ws...)`,
			wantError: `a spread argument cannot fill a parameter of pair: it declares no variadic parameter`,
		},
		{
			name:      "function, spread after a variadic argument",
			body:      `func f(ws ...string) string = label("r:", "a", ws...)`,
			wantError: `a spread argument must be the only argument of variadic parameter "parts" of label`,
		},
		{
			name:     "function, variadic parameter by position",
			body:     `func f(ws ...string) string = label("r:", ws...)`,
			contains: `label("r:", ws...)`,
		},
		{
			name:      "struct field",
			body:      `func f(ws ...string) Cfg = Cfg(Name = ws...)`,
			wantError: `a spread argument cannot fill field "Name" in construction of "Cfg"`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := transpileBareVariant(t, decls+tc.body+"\n\nfunc main() {}\n")
			if tc.wantError != "" {
				require.Error(t, err)
				require.Contains(t, err.Error(), tc.wantError)
				return
			}
			require.NoError(t, err)
			require.Contains(t, out, tc.contains)
		})
	}
}
