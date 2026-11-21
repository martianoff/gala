package transformer_test

import (
	"testing"

	"martianoff/gala/galaerr"

	"github.com/stretchr/testify/require"
)

// TestPlaceholderCompoundBody pins which `_` a placeholder lambda takes when
// its argument is, or holds, an if expression, a match or a block: the whole
// argument is the lambda's body, one parameter per `_` written outside a case
// clause and a nested lambda. Every output that transpiles is type-checked by
// the oracle (oracle_test.go); //examples:placeholder_compound_body runs them.
func TestPlaceholderCompoundBody(t *testing.T) {
	const (
		header = "package main\n\nimport . \"martianoff/gala/collection_immutable\"\n\nstruct R(Lo int)\n\n" +
			"func big(n int) bool = n > 2\n\nfunc main() {\n    val rs = ArrayOf(R(1), R(2))\n    val xs = ArrayOf(1, 2)\n    val limit = 1\n"
		footer = "    Println(rs.Size() + xs.Size() + limit)\n}\n"
	)
	accepted := []struct {
		name, body string
		want       string // the outer lambda, as the call passes it
	}{
		{"an if expression", `    Println(rs.Map(if (_.Lo > 1) "a" else "b"))`, "Array_Map(rs, func(__p0 R) string {"},
		{"a match on the placeholder", `    Println(xs.Map(_ match { case 1 => "one" case _ => "other" }))`, "Array_Map(xs, func(__p0 int) string {"},
		{"a destructuring match", `    Println(rs.Map(_ match { case R(lo) => lo case _ => 0 }))`, "Array_Map(rs, func(__p0 R) int {"},
		{"a block branch", "    Println(xs.Map(if (limit > 0) {\n        val d = _ * 2\n        d + 1\n    } else 0))", "Array_Map(xs, func(__p0 int) int {"},
		{"a nested if", `    Println(xs.Map(if (limit > 0) (if (_ > 1) "big" else "small") else "none"))`, "Array_Map(xs, func(__p0 int) string {"},
		{"a call whose slot takes no function", `    Println(xs.Map(if (big(_)) 1 else 0))`, "Array_Map(xs, func(__p0 int) int {"},
		{"two placeholders, left to right", `    Println(xs.FoldLeft(0, _ + (if (_ > 1) 10 else 1)))`, "Array_FoldLeft(xs, 0, func(__p0 int, __p1 int) int {"},
		{"a captured outer val", `    Println(xs.Map(if (_ > limit) "over" else "under"))`, "Array_Map(xs, func(__p0 int) string {"},
		{"a nested call whose slot takes a function", `    Println(rs.Map(if (_.Lo > 1) xs.Map(_ * 2).Size() else 0))`, "Array_Map(rs, func(__p0 R) int {"},
		// Negative cases: none of these `_` is a parameter of the outer lambda.
		{"a case clause's wildcard and its body", `    Println(xs.Map(_ match { case _ => xs.Map(_ + 1).Size() }))`, "Array_Map(xs, func(__p0 int) int {"},
		{"a `_` parameter of an inner lambda", `    Println(xs.Map(if (_ > 1) xs.Map((_) => 7).Size() else 0))`, "Array_Map(xs, func(__p0 int) int {"},
	}
	for _, tc := range accepted {
		t.Run(tc.name, func(t *testing.T) {
			out, err := newForbiddenBuiltinTranspiler().Transpile(header+tc.body+"\n"+footer, "")
			require.NoError(t, err)
			// A `_` left unrenamed is a Go read of the blank identifier,
			// which the oracle's type check rejects.
			require.Contains(t, out, tc.want)
		})
	}

	rejected := []struct{ name, body, code, at string }{
		// A `_` inside an inner explicit lambda is not the outer lambda's:
		// nothing binds it.
		{"a `_` read in an inner lambda", `    Println(xs.Map(if (_ > 1) xs.Map((y) => y + _).Size() else 0))`,
			string(galaerr.CodeUnderscoreBindingRead), "line 13:48 "},
		// A case clause's `_` is a pattern's, never a placeholder.
		{"a `_` read in a case body", `    Println(xs.Map(_ match { case 1 => _ case _ => 0 }))`,
			string(galaerr.CodeUnderscoreBindingRead), "line 13:39 "},
		{"a `_` inside an interpolated string", `    Println(xs.Map(if (_ > 1) s"v${_}" else ""))`,
			string(galaerr.CodePlaceholderInInterpolation), "line 13:35 "},
		// The nested call takes its `_`, which leaves the outer lambda one
		// parameter where FoldLeft's function has two.
		{"fewer parameters than the function's", `    Println(xs.FoldLeft(0, _ + xs.Map(_ * 2).Size()))`,
			string(galaerr.CodeUntypedLambdaParam), "line 13:27 placeholder lambda has 1 parameters where a function of 2 is expected"},
	}
	for _, tc := range rejected {
		t.Run(tc.name, func(t *testing.T) {
			_, err := newForbiddenBuiltinTranspiler().Transpile(header+tc.body+"\n"+footer, "")
			require.Error(t, err)
			require.Contains(t, err.Error(), tc.code)
			require.Contains(t, err.Error(), tc.at)
		})
	}
}
