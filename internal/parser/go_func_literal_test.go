package parser

import (
	"testing"

	"martianoff/gala/galaerr"

	"github.com/stretchr/testify/require"
)

// TestGoFuncLiteralIsCoded pins GALA-E0117: a Go function literal written as
// a value is one diagnostic at its `func`, with a hint that respells its
// signature as the lambda that replaces it, instead of the raw parser cascade
// ("no viable alternative at input '{return'", then "extraneous input ','
// expecting {<EOF>, ...}" for the tokens the parser skipped afterwards).
//
// Before the grammar parsed the literal, each shape failed in a different
// rule — a struct literal's field in compositeLiteral at the '{', an argument
// or a `val` in the statement rule it unwound to, a `return` value at the
// `func` itself — or, with a body that is one expression, parsed as a
// composite literal of a function type, so each is pinned.
func TestGoFuncLiteralIsCoded(t *testing.T) {
	cases := []struct {
		name   string
		input  string
		line   int
		col    int // 0-based, as the parser reports it
		lambda string
	}{
		{
			// The shape of an external cobra CLI written in Go style.
			name: "unnamed_parameters_in_a_struct_literal",
			input: "package main\n\nfunc ChecksumCmd() *cobra.Command {\n" +
				"    var cmd = &cobra.Command{\n" +
				"        Use:  \"checksum\",\n" +
				"        RunE: func(*cobra.Command, []string) error {\n" +
				"            val r = Resolve(files)\n" +
				"            if r.IsFailure() {\n" +
				"                return r.GetError()\n" +
				"            }\n" +
				"            return nil\n" +
				"        },\n" +
				"    }\n" +
				"    return cmd\n" +
				"}\n",
			line:   6,
			col:    14,
			lambda: "(_ *cobra.Command, _ []string) error",
		},
		{
			name:   "val_initializer",
			input:  "package main\n\nfunc main() {\n    val f = func(x int) int { return x * 2 }\n    Println(f(2))\n}\n",
			line:   4,
			col:    12,
			lambda: "(x int) int",
		},
		{
			name:   "argument",
			input:  "package main\n\nfunc main() {\n    Println(xs.Map(func(x int) int { return x * 2 }))\n}\n",
			line:   4,
			col:    19,
			lambda: "(x int) int",
		},
		{
			name:   "multi_line_body",
			input:  "package main\n\nfunc main() {\n    Println(run(func(n int) int {\n        val m = n + 1\n        return m\n    }))\n}\n",
			line:   4,
			col:    16,
			lambda: "(n int) int",
		},
		{
			name:   "return_value",
			input:  "package main\n\nfunc mk() func(int) int {\n    return func(x int) int { return x + 1 }\n}\n",
			line:   4,
			col:    11,
			lambda: "(x int) int",
		},
		{
			name:   "top_level_var",
			input:  "package main\n\nvar h = func(w http.ResponseWriter, r *http.Request) {\n    val x = 1\n}\n",
			line:   3,
			col:    8,
			lambda: "(w http.ResponseWriter, r *http.Request)",
		},
		{
			// Go's grouped parameters each get the group's type, and a Go
			// result list, which a lambda cannot declare, is left out.
			name:   "grouped_parameters_and_result_list",
			input:  "package main\n\nfunc main() {\n    a(func(x, y int) (int, error) { return Success(x + y) })\n}\n",
			line:   4,
			col:    6,
			lambda: "(x int, y int)",
		},
		{
			// Each name takes the type of its own group.
			name:   "several_groups",
			input:  "package main\n\nfunc main() {\n    a(func(x, y int, s, t string) { Println(s) })\n}\n",
			line:   4,
			col:    6,
			lambda: "(x int, y int, s string, t string)",
		},
		{
			name: "function_typed_and_variadic_parameters",
			input: "package main\n\nfunc main() {\n" +
				"    run(func(f func(int) int, xs ...string) Option[int] {\n        val y = 2\n        return y\n    })\n}\n",
			line:   4,
			col:    8,
			lambda: "(f func(int) int, xs ...string) Option[int]",
		},
		{
			// A body that is one expression would parse as a composite
			// literal's element list; the literal comes first.
			name:   "body_that_parses_as_an_element_list",
			input:  "package main\n\nfunc main() {\n    run(func(_ *cobra.Command, _ []string) { Println(\"x\") })\n}\n",
			line:   4,
			col:    8,
			lambda: "(_ *cobra.Command, _ []string)",
		},
		{
			// The parser reports the literal before the transformer sees the
			// `defer` (GALA-E0036), whatever the body holds; once it is a
			// lambda, E0036 names the GALA form of the statement.
			name:   "deferred",
			input:  "package main\n\nfunc main() {\n    defer func() { val x = 1 }()\n}\n",
			line:   4,
			col:    10,
			lambda: "()",
		},
		{
			name:   "goroutine",
			input:  "package main\n\nfunc main() {\n    go func() { Println(\"x\") }()\n}\n",
			line:   4,
			col:    7,
			lambda: "()",
		},
		{
			name:   "called_in_place",
			input:  "package main\n\nfunc main() {\n    func() { Println(\"hi\") }()\n}\n",
			line:   4,
			col:    4,
			lambda: "()",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			errs := parseGalop(t, tc.input).Errors
			require.Len(t, errs, 1, "one literal is one diagnostic, got: %v", errs)
			se, ok := errs[0].(*galaerr.SemanticError)
			require.True(t, ok, "expected GALA-E0117, got: %v", errs[0])
			require.Equal(t, galaerr.CodeGoFuncLiteral, se.Code)
			require.Equal(t, tc.line, se.Line)
			require.Equal(t, tc.col, se.Column)
			require.Contains(t, se.Hint, "`"+tc.lambda+" => { ... }`", "the hint must spell the lambda")
		})
	}
}

// TestGoFuncLiteralReportsEveryLiteral: every literal of a file is reported,
// a nested one included, in source order, and a syntax error elsewhere, or
// inside a literal's body, keeps its own message.
func TestGoFuncLiteralReportsEveryLiteral(t *testing.T) {
	input := "package main\n\nfunc main() {\n" +
		"    a(func(x int) int { return x })\n" +
		"    val z = 1\n" +
		"    b(func(s string) { c(func() { val q = s }) })\n" +
		"    d(func() { return 1 + })\n" +
		"}\n\nfunc g() int {\n    return 1 +\n}\n"
	var lines []int
	syntax := map[int]bool{}
	for _, e := range parseGalop(t, input).Errors {
		switch e := e.(type) {
		case *galaerr.SemanticError:
			require.Equal(t, galaerr.CodeGoFuncLiteral, e.Code, "unexpected error: %v", e)
			lines = append(lines, e.Line)
		case *galaerr.SyntaxError:
			syntax[e.Line] = true
		default:
			t.Fatalf("unexpected error: %v", e)
		}
	}
	require.Equal(t, []int{4, 6, 6, 7}, lines)
	require.Equal(t, map[int]bool{7: true, 12: true}, syntax, "the body's and g's own errors keep their messages")
}

// TestGoFuncLiteralLeadsItsBodysErrors: the literal's diagnostic comes before
// the syntax errors of its body, so the first error shown is the root cause.
func TestGoFuncLiteralLeadsItsBodysErrors(t *testing.T) {
	errs := parseGalop(t, "package main\n\nfunc main() {\n    d(func() { return 1 + })\n}\n").Errors
	require.NotEmpty(t, errs)
	se, ok := errs[0].(*galaerr.SemanticError)
	require.True(t, ok, "got: %v", errs)
	require.Equal(t, galaerr.CodeGoFuncLiteral, se.Code)
}

// TestGoFuncLiteralInAnExpressionParse: an expression parsed on its own, as an
// interpolation's or a parameter default's is, reports the literal too,
// rather than leaving it to read as a function type (GALA-E0103).
func TestGoFuncLiteralInAnExpressionParse(t *testing.T) {
	_, errs := ParseExpressionTree("run(func(x int) int { return x })")
	require.Len(t, errs, 1, "got: %v", errs)
	se, ok := errs[0].(*galaerr.SemanticError)
	require.True(t, ok, "got: %v", errs)
	require.Equal(t, galaerr.CodeGoFuncLiteral, se.Code)
}

// TestGoFuncLiteralResultListHint: a Go result list has no lambda form, so the
// hint leaves it out and says how the body gives the results instead of
// promising the body carries over.
func TestGoFuncLiteralResultListHint(t *testing.T) {
	errs := parseGalop(t, "package main\n\nfunc main() {\n    a(func(x /* n */ int) (int, error) { return Success(x) })\n}\n").Errors
	require.Len(t, errs, 1, "got: %v", errs)
	se := errs[0].(*galaerr.SemanticError)
	require.Contains(t, se.Hint, "`(x int) => { ... }`, whose body gives the Go results as a Try or a tuple")
}

// TestGoFuncLiteralLookalikesStillParse: a function TYPE followed by a block
// — a result type before a function body, a function-typed parameter or
// annotation — is not a literal and must not be reported as one.
func TestGoFuncLiteralLookalikesStillParse(t *testing.T) {
	inputs := []string{
		"package main\n\nfunc mk() func(int) int {\n    return (x int) int => x + 1\n}\n",
		"package main\n\nfunc run(f func(int) int) int {\n    return f(1)\n}\n",
		"package main\n\nfunc main() {\n    val f func(int) int = (x) => x + 1\n    Println(f(1))\n}\n",
		"package main\n\nfunc main() {\n    val g = (f func(int) int) => f(1)\n    Println(g((x) => x))\n}\n",
		"package main\n\ntype T struct {\n    N int\n}\n\nfunc (t T) Get() int {\n    return t.N\n}\n",
		"package main\n\nfunc main() {\n    val xs = EmptyArray[func() int]()\n    Println(xs)\n}\n",
	}
	for _, in := range inputs {
		require.Empty(t, parseGalop(t, in).Errors, "must parse: %s", in)
	}
}

// TestGoFuncLiteralLeavesOtherErrorsAlone: a syntax error near a function
// type that is not inside a literal keeps its own message.
func TestGoFuncLiteralLeavesOtherErrorsAlone(t *testing.T) {
	input := "package main\n\nfunc mk() func(int) int {\n    return 1 +\n}\n"
	errs := parseGalop(t, input).Errors
	require.NotEmpty(t, errs)
	for _, e := range errs {
		require.NotContains(t, e.Error(), "GALA-E0117", "no literal here: %v", e)
	}
}
