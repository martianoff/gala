package transformer_test

import (
	"testing"

	"martianoff/gala/galaerr"

	"github.com/stretchr/testify/require"
)

// TestPlaceholderInInterpolation pins GALA-E0137 at the `_`, whether or not
// the slot wants a function. The forms that stay valid are built and run by
// //examples:placeholder_interpolation.
func TestPlaceholderInInterpolation(t *testing.T) {
	const (
		header        = "package main\n\nimport . \"martianoff/gala/collection_immutable\"\n\nfunc main() {\n    val xs = ArrayOf(1, 2)\n"
		notBound      = "`_` inside an interpolated string is not a placeholder: a placeholder lambda takes only the `_` written outside the string (hint: write an explicit lambda; name its parameter and use the name inside the string: `(x) => s\"v${x}\"`)"
		readsABinding = "`_` inside an interpolated string reads a `_` binding, which names no value (hint: bind a name instead of `_` and read that name; for example `(x) => x + 1` or `case n: int => n`)"
	)
	rejected := []struct{ name, body, want string }{
		{"s-string in a function slot", `    Println(xs.Map(s"v${_}"))`, "line 7:24 " + notBound},
		{"f-string in a function slot", `    Println(xs.Map(f"v${_}%03d"))`, "line 7:24 " + notBound},
		{"`_` inside a larger expression", `    Println(xs.Map(s"v${_ + 1}"))`, "line 7:24 " + notBound},
		{"nested interpolated string", `    Println(xs.Map(s"<${s"v${_}"}>"))`, "line 7:29 " + notBound},
		{"placeholder outside and inside the string", `    Println(xs.Map(_ * 2 + s"${_}".Size()))`, "line 7:31 " + notBound},
		{"slot that wants no function", `    Println(s"v${_}" + xs.String())`, "line 7:17 " + notBound},
		{"selector on `_`", `    Println(xs.Map(s"v${_.String()}"))`, "line 7:24 " + notBound},
		{"inside a lambda inside the string", `    Println(s"${xs.Map((x) => s"${_}")}")`, "line 7:34 " + notBound},
		{"a `_` parameter read inside the string", `    Println(s"${xs.Map((_) => _ + 1)}")`, "line 7:30 " + readsABinding},
		{"a typed-pattern `_` read inside the string", "    val v any = xs.Size()\n" + `    Println(s"${v match { case _: int => _ case _ => 0 }}")`, "line 8:41 " + readsABinding},
	}
	for _, tc := range rejected {
		t.Run(tc.name, func(t *testing.T) {
			_, err := newForbiddenBuiltinTranspiler().Transpile(header+tc.body+"\n}\n", "")
			require.Error(t, err)
			require.Contains(t, err.Error(), string(galaerr.CodePlaceholderInInterpolation))
			require.Contains(t, err.Error(), tc.want)
		})
	}

	// Where it points depends on the file name, which this harness lacks (see
	// transformDefaultExpr); the CLI points at the `_` in the default.
	t.Run("in a default value", func(t *testing.T) {
		src := "package main\n\nfunc label(x int, s string = s\"v${_}\") string = s\n\nfunc main() {\n    Println(label(1))\n}\n"
		_, err := newForbiddenBuiltinTranspiler().Transpile(src, "")
		require.Error(t, err)
		require.Contains(t, err.Error(), string(galaerr.CodePlaceholderInInterpolation))
	})

	// Typed patterns on a primitive type need an `any` subject, which the
	// example corpus keeps out (TestNoTypeErasureInGeneratedGo); the example
	// runs them on an interface.
	for _, tc := range []struct{ name, body string }{
		{"a named int pattern read inside the string", `    Println(s"${v match { case n: int => n case _ => 0 }}")`},
		{"an unread `_: int` pattern inside the string", `    Println(s"${v match { case _: int => "int" case _ => "other" }}")`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := newForbiddenBuiltinTranspiler().Transpile(header+"    val v any = xs.Size()\n"+tc.body+"\n}\n", "")
			require.NoError(t, err)
		})
	}

	// A default is not written in the string it is expanded in.
	t.Run("a default placeholder lambda expanded inside the string", func(t *testing.T) {
		src := "package main\n\nfunc bump(x int, g func(int) int = _ + 1) int = g(x)\n\nfunc main() {\n    Println(s\"${bump(1)}\")\n}\n"
		_, err := newForbiddenBuiltinTranspiler().Transpile(src, "")
		require.NoError(t, err)
	})
}
