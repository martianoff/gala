package transformer_test

import (
	"testing"

	"martianoff/gala/galaerr"

	"github.com/stretchr/testify/require"
)

// TestRangeOverGalaType pins GALA-E0133: `range` over a GALA struct, which
// every GALA collection is, is rejected in GALA rather than lowered to a Go
// range statement that fails go build ("cannot range over xs (variable of
// struct type ...)"). The hint names the methods the type has. Everything Go
// can range over keeps compiling, and so does a type that is not GALA's to
// judge.
func TestRangeOverGalaType(t *testing.T) {
	rejected := []struct{ name, src, want string }{
		{
			"an Array",
			"package main\n\nimport . \"martianoff/gala/collection_immutable\"\n\nfunc main() {\n    val xs = ArrayOf(1, 2, 3)\n    for _, x := range xs {\n        Println(x)\n    }\n}\n",
			"here, `xs.ForEach((x) => ...)`, `xs.ZipWithIndex()` for the index, `xs.FoldLeft(init, (acc, x) => ...)` for an accumulator, or `range xs.ToGoSlice()` if a Go loop is really needed",
		},
		{
			"a List",
			"package main\n\nimport . \"martianoff/gala/collection_immutable\"\n\nfunc main() {\n    for i, x := range ListOf(1, 2) {\n        Println(i, x)\n    }\n}\n",
			"cannot range over `ListOf(1, 2)`: it is a collection_immutable.List[int]",
		},
		{
			"a HashMap",
			"package main\n\nimport . \"martianoff/gala/collection_immutable\"\n\nfunc main() {\n    val m = HashMapOf((\"a\", 1))\n    for k, v := range m {\n        Println(k, v)\n    }\n}\n",
			"here, `m.ForEachKV((k, v) => ...)`, `m.FoldLeftKV(init, (acc, k, v) => ...)` for an accumulator, or `range m.ToGoMap()` if a Go loop is really needed",
		},
		{
			"a HashSet",
			"package main\n\nimport . \"martianoff/gala/collection_immutable\"\n\nfunc main() {\n    for x := range HashSetOf(1) {\n        Println(x)\n    }\n}\n",
			"`HashSetOf(1).ForEach((x) => ...)`",
		},
		{
			"a mutable HashMap behind a pointer",
			"package main\n\nimport \"martianoff/gala/collection_mutable\"\n\nfunc main() {\n    val m = collection_mutable.EmptyHashMap[string, int]()\n    for k := range m {\n        Println(k)\n    }\n}\n",
			"`m.ForEachKV((k, v) => ...)`",
		},
		{
			"a user struct",
			"package main\n\nstruct Point(X int, Y int)\n\nfunc main() {\n    val p = Point(1, 2)\n    for x := range p {\n        Println(x)\n    }\n}\n",
			"give Point a method that visits its elements",
		},
		{
			"a user struct with a Go slice field",
			"package main\n\nimport \"martianoff/gala/go_interop\"\n\nstruct Bag(names []string)\n\nfunc main() {\n    val b = Bag(go_interop.SliceOf(\"a\"))\n    for _, n := range b {\n        Println(n)\n    }\n}\n",
			"range over its Go slice or map field `names`, or give Bag a method that visits its elements",
		},
		{
			"an if-expression, parenthesized in the hint",
			"package main\n\nimport . \"martianoff/gala/collection_immutable\"\n\nfunc pick(c bool) {\n    for _, x := range if (c) ArrayOf(1) else ArrayOf(2) {\n        Println(x)\n    }\n}\n\nfunc main() {\n    pick(true)\n}\n",
			"`(if (c) ArrayOf(1) else ArrayOf(2)).ForEach((x) => ...)`",
		},
		{
			"a dereference, parenthesized in the hint",
			"package main\n\nimport . \"martianoff/gala/collection_immutable\"\n\nfunc each(p *Array[int]) {\n    for _, x := range *p {\n        Println(x)\n    }\n}\n\nfunc main() {\n    var xs = ArrayOf(1)\n    each(&xs)\n}\n",
			"`(*p).ForEach((x) => ...)`",
		},
		{
			"a Sendable collection",
			"package main\n\nimport . \"martianoff/gala/collection_immutable\"\n\nfunc each(xs Sendable[Array[int]]) {\n    for _, x := range xs {\n        Println(x)\n    }\n}\n\nfunc main() {\n    each(ArrayOf(1))\n}\n",
			"cannot range over `xs`: it is a collection_immutable.Array[int]",
		},
		{
			"an Array field",
			"package main\n\nimport . \"martianoff/gala/collection_immutable\"\n\nstruct Bag(items Array[int])\n\nfunc main() {\n    val b = Bag(ArrayOf(1))\n    for _, x := range b.items {\n        Println(x)\n    }\n}\n",
			"cannot range over `b.items`",
		},
	}
	for _, tc := range rejected {
		t.Run(tc.name, func(t *testing.T) {
			_, err := newForbiddenBuiltinTranspiler().Transpile(tc.src, "")
			require.Error(t, err)
			require.Contains(t, err.Error(), string(galaerr.CodeRangeOverGalaType))
			require.Contains(t, err.Error(), tc.want)
		})
	}

	accepted := []struct{ name, src string }{
		{"a Go slice", "package main\n\nimport \"strings\"\n\nfunc main() {\n    for i, w := range strings.Fields(\"a b\") {\n        Println(i, w)\n    }\n}\n"},
		{"a Go map", "package main\n\nimport \"martianoff/gala/go_interop\"\n\nfunc main() {\n    val m = go_interop.MapPut(go_interop.MapEmpty[string, int](), \"a\", 1)\n    for k, v := range m {\n        Println(k, v)\n    }\n}\n"},
		{"a Go named map type", "package main\n\nimport \"net/http\"\n\nfunc main() {\n    val h = http.Header{}\n    for k, v := range h {\n        Println(k, v)\n    }\n}\n"},
		{"a string", "package main\n\nfunc main() {\n    for i, r := range \"hé\" {\n        Println(i, r)\n    }\n}\n"},
		{"an int", "package main\n\nfunc main() {\n    for i := range 3 {\n        Println(i)\n    }\n}\n"},
		{"a channel", "package main\n\nimport \"time\"\n\nfunc main() {\n    for v := range time.After(time.Millisecond) {\n        Println(v.IsZero())\n        break\n    }\n}\n"},
		{"a go_interop slice", "package main\n\nimport \"martianoff/gala/go_interop\"\n\nfunc main() {\n    val xs = go_interop.SliceOf(1, 2, 3)\n    for _, x := range go_interop.SliceDrop(xs, 1) {\n        Println(x)\n    }\n}\n"},
		{"a GALA alias of a Go slice", "package main\n\nimport \"martianoff/gala/go_interop\"\n\ntype Names []string\n\nfunc show(ns Names) {\n    for _, n := range ns {\n        Println(n)\n    }\n}\n\nfunc main() {\n    show(go_interop.SliceOf(\"a\"))\n}\n"},
		{"an opaque integer", "package main\n\nopaque type Count int\n\nfunc main() {\n    val c = Count(3)\n    for i := range c {\n        Println(i)\n    }\n}\n"},
		{"a Go iterator function", "package main\n\nimport (\n    \"maps\"\n\n    \"martianoff/gala/go_interop\"\n)\n\nfunc main() {\n    val m = go_interop.MapPut(go_interop.MapEmpty[string, int](), \"a\", 1)\n    for k := range maps.Keys(m) {\n        Println(k)\n    }\n}\n"},
		{"a collection's Go slice", "package main\n\nimport . \"martianoff/gala/collection_immutable\"\n\nfunc main() {\n    for _, x := range ArrayOf(1, 2).ToGoSlice() {\n        Println(x)\n    }\n}\n"},
	}
	for _, tc := range accepted {
		t.Run(tc.name, func(t *testing.T) {
			_, err := newForbiddenBuiltinTranspiler().Transpile(tc.src, "")
			require.NoError(t, err)
		})
	}
}
