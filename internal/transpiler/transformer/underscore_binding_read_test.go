package transformer_test

import (
	"testing"

	"martianoff/gala/galaerr"

	"github.com/stretchr/testify/require"
)

// TestUnderscoreBindingRead pins GALA-E0139 at a read of a `_` that a lambda
// parameter or typed pattern binds, inside an interpolated string or not. A
// placeholder lambda's `_`, even one inside such a lambda or arm, is its
// parameter and stays valid.
func TestUnderscoreBindingRead(t *testing.T) {
	const (
		header = "package main\n\nimport . \"martianoff/gala/collection_immutable\"\n\nfunc main() {\n    val xs = ArrayOf(1, 2)\n"
		anyV   = "    val v any = xs.Size()\n"
		want   = "`_` reads a `_` binding, which names no value (hint: bind a name instead of `_` and read that name; for example `(x) => x + 1` or `case n: int => n`)"
	)
	rejected := []struct{ name, body, at string }{
		{"a `_` lambda parameter", `    Println(xs.Map((_) => _ + 1))`, "line 7:26 "},
		{"a typed `_` lambda parameter", `    Println(xs.Map((_ int) => _ * 2))`, "line 7:30 "},
		{"a typed-pattern `_`", anyV + `    Println(v match { case _: int => _ case _ => 0 })`, "line 8:37 "},
		{"a `_` parameter read inside a string", `    Println(s"${xs.Map((_) => _ + 1)}")`, "line 7:30 "},
		{"a typed-pattern `_` read inside a string", anyV + `    Println(s"${v match { case _: int => _ case _ => 0 }}")`, "line 8:41 "},
		{"a `_` parameter of a lambda around a string", `    Println(xs.Map((_) => s"v${_}"))`, "line 7:31 "},
		{"a typed-pattern `_` of a match around a string", anyV + `    Println(v match { case _: int => s"${_}" case _ => "" })`, "line 8:41 "},
	}
	for _, tc := range rejected {
		t.Run(tc.name, func(t *testing.T) {
			_, err := newForbiddenBuiltinTranspiler().Transpile(header+tc.body+"\n}\n", "")
			require.Error(t, err)
			require.Contains(t, err.Error(), string(galaerr.CodeUnderscoreBindingRead))
			require.Contains(t, err.Error(), tc.at+want)
		})
	}

	accepted := []struct{ name, body string }{
		{"an unread `_` parameter", `    Println(xs.Map((_) => 0))`},
		{"a discard of another type in a lambda with a `_` parameter", "    Println(xs.Map((_) => {\n        _ = \"x\"\n        0\n    }))"},
		{"a discard among several targets in a lambda with a `_` parameter", "    Println(xs.Map((_) => {\n        var a = 0\n        a, _ = 1, 2\n        a\n    }))"},
		{"a placeholder lambda inside a lambda with a `_` parameter", `    Println(xs.Map((_) => xs.Map(_ + 1)))`},
		{"a placeholder lambda inside a typed-pattern arm", anyV + `    Println(v match { case _: int => xs.Map(_ * 2) case _ => xs })`},
		{"an unread typed-pattern `_`", anyV + `    Println(v match { case _: int => "int" case _ => "other" })`},
		{"a named typed pattern", anyV + `    Println(v match { case n: int => n case _ => 0 })`},
	}
	for _, tc := range accepted {
		t.Run(tc.name, func(t *testing.T) {
			_, err := newForbiddenBuiltinTranspiler().Transpile(header+tc.body+"\n}\n", "")
			require.NoError(t, err)
		})
	}
}
