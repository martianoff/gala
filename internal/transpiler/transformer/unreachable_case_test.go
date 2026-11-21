package transformer_test

import (
	"testing"

	"martianoff/gala/galaerr"

	"github.com/stretchr/testify/require"
)

// unreachableCaseSource is src, which starts on line 5, in a file that uses
// collection_immutable.
func unreachableCaseSource(src string) string {
	return `package main

import . "martianoff/gala/collection_immutable"

` + src + `
func main() {
    Println(ArrayOf(1).Size())
}
`
}

// TestUnreachableCase pins GALA-E0141 at the first case written after an
// unguarded case that matches every value, in a match and in a
// partial-function literal. A match used to move such a catch-all last, so the
// cases after it won (#1303). A guarded case, and a pattern that may fail to
// match, is not a catch-all.
func TestUnreachableCase(t *testing.T) {
	rejected := []struct{ name, src, at, catchAll string }{
		{
			name: "a literal after `case _`",
			src: `func f(n int) string = n match {
    case 0 => "zero"
    case _ => "any"
    case 1 => "one"
}
`,
			at: "line 8:4 ", catchAll: "`case _` on line 7",
		},
		{
			name: "an extractor after a binding",
			src: `func f(o Option[int]) string = o match {
    case None() => "none"
    case x => s"bound $x"
    case Some(n) => s"some $n"
}
`,
			at: "line 8:4 ", catchAll: "`case x` on line 7",
		},
		{
			name: "a guarded typed pattern after `case _`",
			src: `func f(v any) string = v match {
    case _ => "any"
    case i: int if i > 0 => s"pos $i"
}
`,
			at: "line 7:4 ", catchAll: "`case _` on line 6",
		},
		{
			name: "a typed pattern after `case _` over a type parameter",
			src: `func f[T any](v T) string = v match {
    case s: string => s
    case _ => "any"
    case i: int => s"int $i"
}
`,
			at: "line 8:4 ", catchAll: "`case _` on line 7",
		},
		{
			name: "`case _` after a binding",
			src: `func f(x int) int = x match {
    case y => y + 1
    case _ => 0
}
`,
			at: "line 7:4 ", catchAll: "`case y` on line 6",
		},
		{
			name: "`case _` after a typed pattern of the subject's own type",
			src: `func f(x int) int = x match {
    case y: int => y + 1
    case _ => 0
}
`,
			at: "line 7:4 ", catchAll: "`case y: int` on line 6",
		},
		{
			name: "`case _` after a tuple of bindings",
			src: `func f(t Tuple[int, string]) string = t match {
    case (n, s) => s"$n $s"
    case _ => "unknown"
}
`,
			at: "line 7:4 ", catchAll: "`case (n, s)` on line 6",
		},
		{
			name: "a literal after a capitalized name that binds",
			src: `func f(n int) int = n match {
    case A => A + 1
    case 1 => 0
    case _ => 2
}
`,
			at: "line 7:4 ", catchAll: "`case A` on line 6",
		},
		{
			name: "`case _` after a tuple binding a capitalized name",
			src: `func f(t Tuple[int, int]) int = t match {
    case (A, b) => A + b
    case _ => 0
}
`,
			at: "line 7:4 ", catchAll: "`case (A, b)` on line 6",
		},
		{
			name: "a partial-function `case _` after a tuple binding a capitalized name",
			src: `func f() Array[int] = ArrayOf((1, 2)).Collect({ case (A, b) => A + b case _ => 0 })
`,
			at: "line 5:69 ", catchAll: "`case (A, b)` on line 5",
		},
		{
			name: "a tuple after a tuple of wildcards",
			src: `func f(t Tuple[int, Tuple[int, int]]) string = t match {
    case (_, (a, _)) => s"$a"
    case (1, _) => "one"
    case _ => "other"
}
`,
			at: "line 7:4 ", catchAll: "`case (_, (a, _))` on line 6",
		},
		{
			name: "a case after `case _` in statement position",
			src: `func f(n int) {
    n match {
        case _ => Println("any")
        case 1 => Println("one")
    }
}
`,
			at: "line 8:8 ", catchAll: "`case _` on line 7",
		},
		{
			name: "a partial-function case after a binding",
			src: `func f() Array[string] = ArrayOf(1, 2, 3).Collect({ case 1 => "one" case n => s"n$n" case 2 => "two" })
`,
			at: "line 5:85 ", catchAll: "`case n` on line 5",
		},
		{
			name: "a partial-function `case _` after `case _`",
			src: `func f() Array[string] = ArrayOf(1, 2, 3).Collect({ case _ => "a" case _ => "b" })
`,
			at: "line 5:66 ", catchAll: "`case _` on line 5",
		},
	}
	for _, tc := range rejected {
		t.Run(tc.name, func(t *testing.T) {
			_, err := newForbiddenBuiltinTranspiler().Transpile(unreachableCaseSource(tc.src), "")
			require.Error(t, err)
			require.Contains(t, err.Error(), string(galaerr.CodeUnreachableCase))
			require.Contains(t, err.Error(), tc.at+"unreachable case: "+tc.catchAll+" matches every value")
		})
	}

	// A typed pattern of a Ptr is its own error, not a catch-all, so that
	// error is the one reported.
	t.Run("a typed pattern of a Ptr reports the Ptr error", func(t *testing.T) {
		src := `func f(p Ptr[int]) int = p match {
    case q: Ptr[int] => 1
    case _ => 0
}
`
		_, err := newForbiddenBuiltinTranspiler().Transpile(unreachableCaseSource(src), "")
		require.Error(t, err)
		require.Contains(t, err.Error(), string(galaerr.CodePtrNullability))
		require.NotContains(t, err.Error(), string(galaerr.CodeUnreachableCase))
	})

	// A later case that matches every value too is only to be deleted:
	// moving it above the first would make the first unreachable.
	hints := []struct{ name, src, hint string }{
		{
			name: "a literal is to be moved or deleted",
			src: `func f(n int) string = n match {
    case _ => "any"
    case 1 => "one"
}
`,
			hint: "(hint: move this case above `case _`, or delete it)",
		},
		{
			name: "a second catch-all is to be deleted",
			src: `func f(x int) int = x match {
    case y => y + 1
    case _ => 0
}
`,
			hint: "(hint: delete this case: `case y` already matches every value)",
		},
	}
	for _, tc := range hints {
		t.Run(tc.name, func(t *testing.T) {
			_, err := newForbiddenBuiltinTranspiler().Transpile(unreachableCaseSource(tc.src), "")
			require.Error(t, err)
			require.Contains(t, err.Error(), tc.hint)
		})
	}

	accepted := []struct{ name, src string }{
		{
			name: "cases after a guarded binding",
			src: `func f(n int) string = n match {
    case x if x > 5 => "big"
    case 1 => "one"
    case _ => "other"
}
`,
		},
		{
			name: "cases after a guarded `case _`",
			src: `func f(n int, ok bool) string = n match {
    case _ if ok => "ok"
    case 1 => "one"
    case _ => "other"
}
`,
		},
		{
			name: "`case _` after a typed pattern over `any`, which misses nil",
			src: `func f(v any) string = v match {
    case a: any => s"some $a"
    case _ => "nil"
}
`,
		},
		{
			name: "`case _` after a typed pattern over an interface",
			src: `func f(e error) string = e match {
    case x: error => x.Error()
    case _ => "nil"
}
`,
		},
		{
			name: "`case _` after a typed pattern of a type parameter",
			src: `func f[T any](v T) string = v match {
    case x: T => s"t $x"
    case _ => "other"
}
`,
		},
		{
			name: "`case _` after a tuple with a literal",
			src: `func f(t Tuple[int, string]) string = t match {
    case (1, s) => s
    case _ => "other"
}
`,
		},
		{
			name: "a lowercase sealed variant name tests the value",
			src: `sealed type Frame {
    case endFrame()
    case lineFrame(text string)
}

func f(fr Frame) string = fr match {
    case endFrame => "end"
    case other => "other"
}
`,
		},
		{
			name: "a typed pattern of the subject's own type closes the match",
			src: `func f(x int) int = x match {
    case y: int => y + 1
}
`,
		},
		{
			name: "a tuple pattern of the subject's arity closes the match",
			src: `func f(t Tuple[int, string]) string = t match {
    case (n, s) => s"$n $s"
}
`,
		},
		{
			// Go types are never counted, so the Go SDK being found or not
			// cannot change whether this compiles.
			name: "`case _` after a typed pattern of a Go type",
			src: `import "time"

func f(d time.Duration) string = d match {
    case x: time.Duration => x.String()
    case _ => "z"
}
`,
		},
		{
			name: "a default binding named like the match subject",
			src: `func f(x int) int = x match {
    case 0 => 0
    case _subj => _subj + 1
}
`,
		},
		{
			name: "a typed catch-all named like the match subject",
			src: `func f(x int) int = x match {
    case _subj: int => _subj + 1
}
`,
		},
		{
			name: "partial-function cases after a guarded binding",
			src: `func f() Array[string] = ArrayOf(1, 2, 3).Collect({ case n if n > 2 => "big" case 1 => "one" case _ => "other" })
`,
		},
	}
	for _, tc := range accepted {
		t.Run(tc.name, func(t *testing.T) {
			_, err := newForbiddenBuiltinTranspiler().Transpile(unreachableCaseSource(tc.src), "")
			require.NoError(t, err)
		})
	}
}
