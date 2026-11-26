package transformer_test

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestPlaceholderMixedParameterTypes pins that each `_` of a placeholder
// lambda reads the type of its own parameter when the slot's parameters have
// different types (#1387): `rs.FoldLeft(0, _ + _.Lo)` lowers as
// `(acc, r) => acc + r.Lo` does, the element's Immutable field unwrapped,
// where a `_` shared by both parameters was typed `any` and left
// `__p1.Lo` an Immutable[int] that go build rejects. Every output that
// transpiles is type-checked by the oracle (oracle_test.go);
// //examples:placeholder_mixed_types runs them.
func TestPlaceholderMixedParameterTypes(t *testing.T) {
	const (
		header = "package main\n\nimport . \"martianoff/gala/collection_immutable\"\n\n" +
			"struct R(Lo int, Name string)\n\nfunc (r R) Twice() int = r.Lo * 2\n\n" +
			"func main() {\n    val rs = ArrayOf(R(1, \"a\"), R(2, \"b\"))\n    val xs = ArrayOf(1, 2)\n"
		footer = "    Println(rs.Size() + xs.Size())\n}\n"
	)
	cases := []struct {
		name, body string
		want       []string // the outer lambda as the call passes it, and its body
	}{
		{"an int accumulator and a struct field", `    Println(rs.FoldLeft(0, _ + _.Lo))`,
			[]string{"Array_FoldLeft(rs, 0, func(__p0 int, __p1 R) int {", "return __p0 + __p1.Lo.Get()"}},
		{"a string accumulator and a string field", `    Println(rs.FoldLeft("", _ + _.Name))`,
			[]string{"Array_FoldLeft(rs, \"\", func(__p0 string, __p1 R) string {", "return __p0 + __p1.Name.Get()"}},
		{"a method of the element", `    Println(rs.FoldLeft(0, _ + _.Twice()))`,
			[]string{"Array_FoldLeft(rs, 0, func(__p0 int, __p1 R) int {", "return __p0 + __p1.Twice()"}},
		{"the element first", `    Println(rs.FoldRight(0, _.Lo + _))`,
			[]string{"Array_FoldRight(rs, 0, func(__p0 R, __p1 int) int {", "return __p0.Lo.Get() + __p1"}},
		// A call whose slot takes no function leaves its `_` to the outer
		// lambda, and the nested call on its result takes its own.
		{"a `_` in a call the nested call is made on", `    Println(rs.FoldLeft(0, _ + ArrayOf(_.Lo).Map(_ * 2).Size()))`,
			[]string{"Array_FoldLeft(rs, 0, func(__p0 int, __p1 R) int {", "ArrayOf(__p1.Lo.Get())"}},
		// The nested call takes the second `_` written, so the outer
		// lambda's second parameter is the third.
		{"a nested call taking a `_` between them", `    Println(rs.FoldLeft(0, _ + xs.Map(_ * 2).Size() + _.Lo))`,
			[]string{"Array_FoldLeft(rs, 0, func(__p0 int, __p1 R) int {", "+ __p1.Lo.Get()"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := newForbiddenBuiltinTranspiler().Transpile(header+tc.body+"\n"+footer, "")
			require.NoError(t, err)
			for _, want := range tc.want {
				require.Contains(t, out, want)
			}
		})
	}
}
