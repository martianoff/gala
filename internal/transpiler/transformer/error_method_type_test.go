package transformer_test

import (
	"testing"

	"martianoff/gala/internal/transpiler/transformer"

	"github.com/stretchr/testify/require"
)

// TestErrorMethodCallIsTyped covers a method call on a value of the
// predeclared `error` interface: `e.Error()` is a string wherever the error
// came from — a Failure pattern, a Go result, an Option's value, a parameter.
func TestErrorMethodCallIsTyped(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{
			name: "error bound by a Failure pattern",
			src: `package main

import "strconv"

func main() {
    Println(Try(strconv.Atoi("x")) match {
        case Success(n) => s"ok $n"
        case Failure(e) => s"failed: ${e.Error()}"
    })
}`,
		},
		{
			name: "error parameter",
			src: `package main

import "errors"

func describe(e error) string = e.Error()

func main() {
    Println(describe(errors.New("boom")))
}`,
		},
		{
			name: "error inside an Option",
			src: `package main

import "errors"

func main() {
    val failed = Some(errors.New("boom"))
    Println(s"${failed.Get().Error()}")
}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("GALA_WARN_TYPES", "1")
			trans, tr := newTranspilerWithTransformer()
			_, err := trans.Transpile(tc.src, "main.gala")
			require.NoError(t, err)

			var got []string
			for _, u := range transformer.UnresolvedTypes(tr) {
				got = append(got, u.Expr)
			}
			require.Empty(t, got)
		})
	}
}
