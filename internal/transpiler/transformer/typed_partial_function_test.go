package transformer_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestTypedPartialFunction covers a partial-function literal whose function
// type comes from the slot it fills — a typed val or var, a parameter, a
// return value, a struct field. The slot gives the literal its parameter type
// (so a pattern that needs the subject's type type-checks) and the element
// type of its Option result; the parameter is never lowered as `any`.
func TestTypedPartialFunction(t *testing.T) {
	trans := newTranspiler()
	const decls = "package main\n\n" +
		"sealed type Shape {\n    case Circle(R int)\n    case Rect(W int, H int)\n    case Empty()\n}\n\n" +
		"sealed type Box[T any] {\n    case Full(V T)\n    case Nothing()\n}\n\n"
	tests := []struct {
		name string
		body string
		want []string
	}{
		{
			name: "val of a basic parameter type",
			body: "func main() {\n    val pf func(int) Option[int] = { case 1 => 10 case 2 => 20 }\n    Println(pf(2).GetOrElse(-1))\n}",
			want: []string{"func(_pf_arg int) std.Option[int] {"},
		},
		{
			name: "var of a sealed parameter type",
			body: "func main() {\n    var ps func(Shape) Option[int] = { case Circle(r) => r case Empty() => 0 }\n    Println(ps(Circle(3)).GetOrElse(-1))\n}",
			want: []string{"func(_pf_arg Shape) std.Option[int] {"},
		},
		{
			name: "result element type comes from the declared type",
			body: "func main() {\n    val pf func(int) Option[int64] = { case 1 => 10 }\n    Println(pf(1).GetOrElse(-1))\n}",
			want: []string{"func(_pf_arg int) std.Option[int64] {"},
		},
		{
			name: "arm value lowered against the result element type",
			body: "func main() {\n    val pf func(int) Option[func(int) int] = { case 1 => (x) => x + 1 }\n    Println(pf(1).Get()(2))\n}",
			want: []string{"func(_pf_arg int) std.Option[func(int) int] {", "func(x int) int {"},
		},
		{
			name: "named sub-pattern",
			body: "func main() {\n    val pf func(Shape) Option[int] = { case Rect(H = 3) => 1 }\n    Println(pf(Rect(1, 3)).GetOrElse(-1))\n}",
			want: []string{"func(_pf_arg Shape) std.Option[int] {"},
		},
		{
			name: "generic sealed extractor",
			body: "func main() {\n    val p func(Box[Option[int]]) Option[int] = { case Full(Some(2)) => 1 case Nothing() => 2 }\n    Println(p(Full(Some(2))).GetOrElse(-1))\n}",
			want: []string{"func(_pf_arg Box[std.Option[int]]) std.Option[int] {"},
		},
		{
			name: "tuple subject",
			body: "func main() {\n    val p func(Tuple[Shape, int]) Option[int] = { case (Circle(1), 2) => 1 }\n    Println(p((Circle(1), 2)).GetOrElse(-1))\n}",
			want: []string{"func(_pf_arg std.Tuple[Shape, int]) std.Option[int] {"},
		},
		{
			name: "return value",
			body: "func classify() func(Shape) Option[string] = { case Circle(_) => \"circle\" }\n\nfunc main() {\n    Println(classify()(Empty()).GetOrElse(\"none\"))\n}",
			want: []string{"func(_pf_arg Shape) std.Option[string] {"},
		},
		{
			name: "struct field",
			body: "struct Rules(Pick func(Shape) Option[int])\n\nfunc main() {\n    val r = Rules(Pick = { case Rect(w, _) => w })\n    Println(r.Pick(Rect(4, 5)).GetOrElse(-1))\n}",
			want: []string{"func(_pf_arg Shape) std.Option[int] {"},
		},
		{
			name: "function argument",
			body: "func apply(s Shape, f func(Shape) Option[int]) int = f(s).GetOrElse(-1)\n\nfunc main() {\n    Println(apply(Circle(2), { case Circle(r) => r * 10 }))\n}",
			want: []string{"func(_pf_arg Shape) std.Option[int] {"},
		},
		{
			name: "parenthesized literal",
			body: "func main() {\n    val pf func(int) Option[int] = ({ case 1 => 10 })\n    Println(pf(1).GetOrElse(-1))\n}",
			want: []string{"func(_pf_arg int) std.Option[int] {"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := trans.Transpile(decls+tt.body+"\n", "")
			require.NoError(t, err)
			for _, w := range tt.want {
				assert.Contains(t, got, w)
			}
			assert.NotContains(t, got, "_pf_arg any")
		})
	}
}

// TestTypedPartialFunctionQualifiedStd covers the declared type written with a
// package-qualified std import: `std.Option[int]`.
func TestTypedPartialFunctionQualifiedStd(t *testing.T) {
	trans := newTranspiler()
	const src = "package main\n\nimport \"martianoff/gala/std\"\n\n" +
		"func main() {\n    val pf func(int) std.Option[int] = { case 1 => 10 }\n    Println(pf(1).GetOrElse(-1))\n}\n"
	got, err := trans.Transpile(src, "")
	require.NoError(t, err)
	assert.Contains(t, got, "func(_pf_arg int) std.Option[int] {")
}
