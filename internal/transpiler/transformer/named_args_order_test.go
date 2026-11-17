package transformer_test

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestNamedArgsParameterOrder pins arguments passed by name to callees that
// are not GALA functions or concrete GALA methods. They are laid out in the
// callee's parameter order. A method of a Go type used to take them in the
// order they were written, so `re.ReplaceAllString(repl = "X", src =
// "banana")` compiled and swapped its arguments; a method declared in a GALA
// interface dropped them; a Go function was emitted as a composite literal.
// A callee whose parameter names are not known is reported, not guessed at.
// examples/named_args_parameter_order.gala runs the accepted shapes.
func TestNamedArgsParameterOrder(t *testing.T) {
	const decls = `package main

import (
    "net/http"
    "regexp"
    "strings"

    "martianoff/gala/go_interop"
)

type Pairer interface {
    Pair(first string, second string) string
    Join(sep string, parts ...string) string
}

type Ranker[T any] interface {
    Rank(lo T, hi T) string
}

// Every import is used whatever the case.
func handler(h http.Handler) http.Handler = h

func pattern(s string) *regexp.Regexp = regexp.MustCompile(strings.ToLower(s))

func size(m map[string]int) int = go_interop.MapLen(m)

`
	cases := []struct {
		name      string
		body      string
		contains  string
		wantError string
	}{
		{
			name:     "go method",
			body:     `func f(re *regexp.Regexp) string = re.ReplaceAllString(repl = "X", src = "banana")`,
			contains: `re.ReplaceAllString("banana", "X")`,
		},
		{
			name:     "go method, positional then named",
			body:     `func f(re *regexp.Regexp) string = re.ReplaceAllString("banana", repl = "X")`,
			contains: `re.ReplaceAllString("banana", "X")`,
		},
		{
			name:     "go method, lambda by name",
			body:     `func f(re *regexp.Regexp) string = re.ReplaceAllStringFunc(repl = (m) => strings.ToUpper(m), src = "banana")`,
			contains: `re.ReplaceAllStringFunc("banana", func(m string) string`,
		},
		{
			name:     "go function",
			body:     `func f() string = strings.Repeat(count = 3, s = "ab")`,
			contains: `strings.Repeat("ab", 3)`,
		},
		{
			name:     "go function, lambda by name",
			body:     `func f() string = strings.Map(s = "HAL", mapping = (r) => r + 1)`,
			contains: `strings.Map(func(r rune) rune`,
		},
		{
			name:     "interface method",
			body:     `func f(p Pairer) string = p.Pair(second = "b", first = "a")`,
			contains: `p.Pair("a", "b")`,
		},
		{
			name:     "interface method, spread by name",
			body:     `func f(p Pairer, ws ...string) string = p.Join(parts = ws..., sep = "|")`,
			contains: `p.Join("|", ws...)`,
		},
		{
			name:     "generic interface method",
			body:     `func f(r Ranker[int]) string = r.Rank(hi = 9, lo = 1)`,
			contains: `r.Rank(1, 9)`,
		},
		{
			name:      "go method with unnamed parameters",
			body:      `func f(h http.Handler, w http.ResponseWriter, r *http.Request) = h.ServeHTTP(r = r, w = w)`,
			wantError: `cannot pass "r" by name in call to ServeHTTP: its parameter names are not known`,
		},
		{
			name:      "go function, unknown name",
			body:      `func f() string = strings.Repeat(cnt = 3, s = "ab")`,
			wantError: `unknown parameter "cnt" in call to strings.Repeat`,
		},
		{
			name:      "go method, argument left out",
			body:      `func f(re *regexp.Regexp) string = re.ReplaceAllString(src = "banana")`,
			wantError: `missing required argument "repl" (parameter 2) in call to ReplaceAllString`,
		},
		{
			// A Go parameter takes no default, so the hint offers none.
			name:      "go method, argument left out, hint",
			body:      `func f(re *regexp.Regexp) string = re.ReplaceAllString(src = "banana")`,
			wantError: `hint: pass "repl"; ReplaceAllString takes (src, repl)`,
		},
		{
			name:      "go method, name given twice",
			body:      `func f(re *regexp.Regexp) string = re.ReplaceAllString(src = "a", src = "b", repl = "c")`,
			wantError: `parameter "src" is given more than once in call to ReplaceAllString`,
		},
		{
			name:     "generic go function, lambda before the value it is typed by",
			body:     `func f(m map[string]int) = go_interop.MapForEach(f = (k, v) => Println(k, v), m = m)`,
			contains: `go_interop.MapForEach(m, func(k string, v int)`,
		},
		{
			name:      "function-typed value",
			body:      `func f(g func(int, int) int) int = g(b = 1, a = 2)`,
			wantError: `cannot pass "a" by name in call to g: its parameter names are not known`,
		},
		{
			name:     "generic go function, lambda after the value it is typed by",
			body:     `func f(m map[string]int) = go_interop.MapForEach(m = m, f = (k, v) => Println(k, v))`,
			contains: `go_interop.MapForEach(m, func(k string, v int)`,
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
