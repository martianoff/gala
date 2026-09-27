package transformer_test

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestTypeParamsRecognisedByScope guards how the transformer tells a type
// parameter from a type. It used to decide by spelling: any single capital
// letter was a type parameter wherever it appeared. A user type named `T`, `A`
// or `V`, and a Go type such as `testing.B`, was therefore treated as an
// unresolved parameter — the lambdas passed to its collection methods lost
// their parameter types (`func(t any) bool`), and a match yielding it was typed
// `any`. Conversely a multi-letter parameter such as `Elem` was never
// recognised at all.
//
// A name is a type parameter when an enclosing generic declaration binds it
// (and then it shadows a same-named package type), or when it is a callee's
// placeholder that no visible type answers to.
func TestTypeParamsRecognisedByScope(t *testing.T) {
	const header = "package main\n\nimport . \"martianoff/gala/collection_immutable\"\n\n"
	cases := []struct {
		name        string
		input       string
		mustContain []string
		mustNotHave []string
	}{
		{
			name: "struct named T as a collection element",
			input: header + `type T struct { N int }

func pick(xs Array[T]) Option[T] = xs.Find((t) => t.N > 1)`,
			mustContain: []string{"func(t T) bool"},
			mustNotHave: []string{"func(t any)"},
		},
		{
			name: "struct named A through Option",
			input: header + `type A struct { Name string }

func names(o Option[A]) Option[string] = o.Filter((a) => a.Name != "").Map((a) => a.Name)`,
			mustContain: []string{"func(a A) bool", "func(a A) string"},
			mustNotHave: []string{"func(a any)"},
		},
		{
			name: "sealed type named V",
			input: header + `sealed type V {
    case Small(n int)
    case Big(n int)
}

func size(v V) int = v match {
    case Small(n) => n
    case Big(n) => n * 10
}

func firstBig(vs Array[V]) Option[V] = vs.Find((v) => size(v) > 5)`,
			mustContain: []string{"func(v V) bool"},
			mustNotHave: []string{"func(v any)"},
		},
		{
			name: "match yielding a Go type named B",
			input: `package main

import (
    "testing"
    . "martianoff/gala/collection_immutable"
)

func pick(bs Array[*testing.B]) int {
    val r = bs.HeadOption() match {
        case Some(b) => b
        case None() => bs.Last()
    }
    return r.N
}`,
			mustContain: []string{"func(obj std.Option[*testing.B]) *testing.B"},
			mustNotHave: []string{") any {"},
		},
		{
			name: "type parameter A shadows a package struct A",
			input: header + `type A struct { Name string }

func firstOr[A any](xs Array[A], d A) A {
    val r = xs.Find((x) => true) match {
        case Some(a) => a
        case None() => d
    }
    return r
}`,
			mustContain: []string{"func(x A) bool", "func(obj std.Option[A]) A"},
			mustNotHave: []string{"func(x any)", ") any {"},
		},
		{
			name: "multi-letter type parameter",
			input: header + `func countIf[Elem any](xs Array[Elem], p func(Elem) bool) int = xs.Filter((x) => p(x)).Size()`,
			mustContain: []string{"func(x Elem) bool"},
			mustNotHave: []string{"func(x any)"},
		},
	}

	trans := newForbiddenBuiltinTranspiler()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := trans.Transpile(tc.input, "")
			require.NoError(t, err)
			for _, want := range tc.mustContain {
				require.Contains(t, out, want, "generated Go:\n%s", out)
			}
			for _, bad := range tc.mustNotHave {
				require.NotContains(t, out, bad, "generated Go:\n%s", out)
			}
		})
	}
}
