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
	const header = "package main\n\nimport . \"martianoff/gala/collection_immutable\"\n\nfunc main() {\n    val xs = ArrayOf(1, 2)\n"
	rejected := []struct{ name, body, at string }{
		{"s-string in a function slot", `    Println(xs.Map(s"v${_}"))`, "line 7:24 "},
		{"f-string in a function slot", `    Println(xs.Map(f"v${_}%03d"))`, "line 7:24 "},
		{"`_` inside a larger expression", `    Println(xs.Map(s"v${_ + 1}"))`, "line 7:24 "},
		{"nested interpolated string", `    Println(xs.Map(s"<${s"v${_}"}>"))`, "line 7:29 "},
		{"placeholder outside and inside the string", `    Println(xs.Map(_ * 2 + s"${_}".Size()))`, "line 7:31 "},
		{"slot that wants no function", `    Println(s"v${_}" + xs.String())`, "line 7:17 "},
		{"selector on `_`", `    Println(xs.Map(s"v${_.String()}"))`, "line 7:24 "},
		{"inside a lambda inside the string", `    Println(s"${xs.Map((x) => s"${_}")}")`, "line 7:34 "},
		{"a `_` parameter read inside the string", `    Println(s"${xs.Map((_) => _ + 1)}")`, "line 7:30 "},
	}
	for _, tc := range rejected {
		t.Run(tc.name, func(t *testing.T) {
			_, err := newForbiddenBuiltinTranspiler().Transpile(header+tc.body+"\n}\n", "")
			require.Error(t, err)
			require.Contains(t, err.Error(), string(galaerr.CodePlaceholderInInterpolation))
			require.Contains(t, err.Error(), tc.at+"`_` inside an interpolated string is not a placeholder")
		})
	}
}
