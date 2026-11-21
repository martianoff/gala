package transformer_test

import (
	"testing"

	"martianoff/gala/galaerr"

	"github.com/stretchr/testify/require"
)

// TestUnreachableCase pins GALA-E0141 at the first case written after an
// unguarded case that matches every value, in a match and in a
// partial-function literal. A match used to move such a catch-all last, so the
// cases after it won (#1303). A guarded case, and a pattern that may fail to
// match, is not a catch-all.
func TestUnreachableCase(t *testing.T) {
	const (
		header = "package main\n\nimport . \"martianoff/gala/collection_immutable\"\n\n"
		footer = "\nfunc main() {\n    Println(ArrayOf(1).Size())\n}\n"
	)
	rejected := []struct{ name, src, at, catchAll string }{
		{"a literal after `case _`",
			"func f(n int) string = n match {\n    case 0 => \"zero\"\n    case _ => \"any\"\n    case 1 => \"one\"\n}\n",
			"line 8:4 ", "`case _` on line 7"},
		{"an extractor after a binding",
			"func f(o Option[int]) string = o match {\n    case None() => \"none\"\n    case x => s\"bound $x\"\n    case Some(n) => s\"some $n\"\n}\n",
			"line 8:4 ", "`case x` on line 7"},
		{"a guarded typed pattern after `case _`",
			"func f(v any) string = v match {\n    case _ => \"any\"\n    case i: int if i > 0 => s\"pos $i\"\n}\n",
			"line 7:4 ", "`case _` on line 6"},
		{"a typed pattern after `case _` over a type parameter",
			"func f[T any](v T) string = v match {\n    case s: string => s\n    case _ => \"any\"\n    case i: int => s\"int $i\"\n}\n",
			"line 8:4 ", "`case _` on line 7"},
		{"`case _` after a binding",
			"func f(x int) int = x match {\n    case y => y + 1\n    case _ => 0\n}\n",
			"line 7:4 ", "`case y` on line 6"},
		{"`case _` after a typed pattern of the subject's own type",
			"func f(x int) int = x match {\n    case y: int => y + 1\n    case _ => 0\n}\n",
			"line 7:4 ", "`case y: int` on line 6"},
		{"`case _` after a tuple of bindings",
			"func f(t Tuple[int, string]) string = t match {\n    case (n, s) => s\"$n $s\"\n    case _ => \"unknown\"\n}\n",
			"line 7:4 ", "`case (n, s)` on line 6"},
		{"a tuple after a tuple of wildcards",
			"func f(t Tuple[int, Tuple[int, int]]) string = t match {\n    case (_, (a, _)) => s\"$a\"\n    case (1, _) => \"one\"\n    case _ => \"other\"\n}\n",
			"line 7:4 ", "`case (_, (a, _))` on line 6"},
		{"a case after `case _` in statement position",
			"func f(n int) {\n    n match {\n        case _ => Println(\"any\")\n        case 1 => Println(\"one\")\n    }\n}\n",
			"line 8:8 ", "`case _` on line 7"},
		{"a partial-function case after a binding",
			"func f() Array[string] = ArrayOf(1, 2, 3).Collect({ case 1 => \"one\" case n => s\"n$n\" case 2 => \"two\" })\n",
			"line 5:85 ", "`case n` on line 5"},
		{"a partial-function `case _` after `case _`",
			"func f() Array[string] = ArrayOf(1, 2, 3).Collect({ case _ => \"a\" case _ => \"b\" })\n",
			"line 5:66 ", "`case _` on line 5"},
	}
	for _, tc := range rejected {
		t.Run(tc.name, func(t *testing.T) {
			_, err := newForbiddenBuiltinTranspiler().Transpile(header+tc.src+footer, "")
			require.Error(t, err)
			require.Contains(t, err.Error(), string(galaerr.CodeUnreachableCase))
			require.Contains(t, err.Error(), tc.at+"unreachable case: "+tc.catchAll+" matches every value")
		})
	}

	// A typed pattern of a Ptr is its own error, not a catch-all, so that
	// error is the one reported.
	t.Run("a typed pattern of a Ptr reports the Ptr error", func(t *testing.T) {
		src := "func f(p Ptr[int]) int = p match {\n    case q: Ptr[int] => 1\n    case _ => 0\n}\n"
		_, err := newForbiddenBuiltinTranspiler().Transpile(header+src+footer, "")
		require.Error(t, err)
		require.Contains(t, err.Error(), string(galaerr.CodePtrNullability))
		require.NotContains(t, err.Error(), string(galaerr.CodeUnreachableCase))
	})

	// A later case that matches every value too is only to be deleted:
	// moving it above the first would make the first unreachable.
	hints := []struct{ name, src, hint string }{
		{"a literal is to be moved or deleted",
			"func f(n int) string = n match {\n    case _ => \"any\"\n    case 1 => \"one\"\n}\n",
			"(hint: move this case above `case _`, or delete it)"},
		{"a second catch-all is to be deleted",
			"func f(x int) int = x match {\n    case y => y + 1\n    case _ => 0\n}\n",
			"(hint: delete this case: `case y` already matches every value)"},
	}
	for _, tc := range hints {
		t.Run(tc.name, func(t *testing.T) {
			_, err := newForbiddenBuiltinTranspiler().Transpile(header+tc.src+footer, "")
			require.Error(t, err)
			require.Contains(t, err.Error(), tc.hint)
		})
	}

	accepted := []struct{ name, src string }{
		{"cases after a guarded binding",
			"func f(n int) string = n match {\n    case x if x > 5 => \"big\"\n    case 1 => \"one\"\n    case _ => \"other\"\n}\n"},
		{"cases after a guarded `case _`",
			"func f(n int, ok bool) string = n match {\n    case _ if ok => \"ok\"\n    case 1 => \"one\"\n    case _ => \"other\"\n}\n"},
		{"`case _` after a typed pattern over `any`, which misses nil",
			"func f(v any) string = v match {\n    case a: any => s\"some $a\"\n    case _ => \"nil\"\n}\n"},
		{"`case _` after a typed pattern over an interface",
			"func f(e error) string = e match {\n    case x: error => x.Error()\n    case _ => \"nil\"\n}\n"},
		{"`case _` after a typed pattern of a type parameter",
			"func f[T any](v T) string = v match {\n    case x: T => s\"t $x\"\n    case _ => \"other\"\n}\n"},
		{"`case _` after a tuple with a literal",
			"func f(t Tuple[int, string]) string = t match {\n    case (1, s) => s\n    case _ => \"other\"\n}\n"},
		{"a lowercase sealed variant name tests the value",
			"sealed type Frame {\n    case endFrame()\n    case lineFrame(text string)\n}\n\nfunc f(fr Frame) string = fr match {\n    case endFrame => \"end\"\n    case other => \"other\"\n}\n"},
		{"a typed pattern of the subject's own type closes the match",
			"func f(x int) int = x match {\n    case y: int => y + 1\n}\n"},
		{"a tuple pattern of the subject's arity closes the match",
			"func f(t Tuple[int, string]) string = t match {\n    case (n, s) => s\"$n $s\"\n}\n"},
		{"a default binding named like the match subject",
			"func f(x int) int = x match {\n    case 0 => 0\n    case _subj => _subj + 1\n}\n"},
		{"a typed catch-all named like the match subject",
			"func f(x int) int = x match {\n    case _subj: int => _subj + 1\n}\n"},
		{"partial-function cases after a guarded binding",
			"func f() Array[string] = ArrayOf(1, 2, 3).Collect({ case n if n > 2 => \"big\" case 1 => \"one\" case _ => \"other\" })\n"},
	}
	for _, tc := range accepted {
		t.Run(tc.name, func(t *testing.T) {
			_, err := newForbiddenBuiltinTranspiler().Transpile(header+tc.src+footer, "")
			require.NoError(t, err)
		})
	}
}
