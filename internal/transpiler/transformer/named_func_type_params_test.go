package transformer_test

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestLambdaAgainstNamedFuncTypeParams pins an untyped lambda typed by a
// function type that names its parameters, `func(msg string)`: the names are
// not part of the type, so the lambda's parameter is a string. The type used to
// be read from the parameter list's text without whitespace, `msgstring`.
func TestLambdaAgainstNamedFuncTypeParams(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{
			name: "a call argument",
			body: `func apply(f func(msg string), v string) { f(v) }
func main() { apply((m) => Println(m), "x") }`,
			want: "func(m string)",
		},
		{
			name: "a struct field",
			body: `struct Logger(Info func(msg string))
func main() { Logger(Info = (m) => Println(m)).Info("x") }`,
			want: "func(m string)",
		},
		{
			name: "a struct field default",
			body: `struct Logger(Info func(msg string))
struct Svc(name string, log Logger = Logger(Info = (m) => Println(m)))
func main() { Svc(name = "a").log.Info("x") }`,
			want: "func(m string)",
		},
		{
			name: "a named function type",
			body: `type Handler func(req string, attempt int) Option[string]
func main() {
    val h Handler = (r, n) => Some(s"$r#$n")
    Println(h("r", 1))
}`,
			want: "func(r string, n int) std.Option[string]",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := transpileBareVariant(t, "package main\n\n"+tc.body+"\n")
			require.NoError(t, err)
			require.Contains(t, out, tc.want)
			require.NotContains(t, out, "msgstring")
		})
	}
}
