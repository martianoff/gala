package parser

import (
	"errors"
	"testing"

	"martianoff/gala/galaerr"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGoHabitsAreCoded pins GALA-E0121 to GALA-E0132 and the parser's
// GALA-E0035 for `make`: each Go construct is one coded diagnostic at its
// first token, whose hint names the GALA form, and the syntax errors it used
// to cause ("mismatched input 'const'", "no viable alternative at input
// '[16'", the "extraneous input" cascade) are gone.
func TestGoHabitsAreCoded(t *testing.T) {
	cases := []struct {
		name  string
		input string
		code  galaerr.ErrorCode
		line  int
		col   int // 0-based, as the parser reports it
		hint  string
	}{
		{
			name:  "top_level_const",
			input: "package main\n\nconst N = 10\n\nfunc main() {\n    Println(N)\n}\n",
			code:  galaerr.CodeGoConst, line: 3, col: 0,
			hint: "`val N = 10`",
		},
		{
			name:  "local_const",
			input: "package main\n\nfunc main() {\n    const n = 3\n    Println(n)\n}\n",
			code:  galaerr.CodeGoConst, line: 4, col: 4,
			hint: "`val n = 3`",
		},
		{
			name:  "const_block",
			input: "package main\n\nconst (\n    A = 1\n    B = 2\n)\n\nfunc main() {\n    Println(A)\n}\n",
			code:  galaerr.CodeGoConst, line: 3, col: 0,
			hint: "`val A = 1`",
		},
		{
			name:  "const_block_with_iota",
			input: "package main\n\nconst (\n    Red = iota\n    Green\n)\n\nfunc main() {\n    Println(Red)\n}\n",
			code:  galaerr.CodeGoConst, line: 3, col: 0,
			hint: "`sealed type`",
		},
		{
			name:  "var_group",
			input: "package main\n\nvar (\n    a = 1\n    b = 2\n)\n\nfunc main() {\n    Println(a, b)\n}\n",
			code:  galaerr.CodeGoDeclGroup, line: 3, col: 0,
			hint: "`var a = 1`",
		},
		{
			name:  "local_var_group",
			input: "package main\n\nfunc main() {\n    var (\n        a = 1\n        b = 2\n    )\n    Println(a, b)\n}\n",
			code:  galaerr.CodeGoDeclGroup, line: 4, col: 4,
			hint: "`var a = 1`",
		},
		{
			name:  "type_group",
			input: "package main\n\ntype (\n    A int\n    B string\n)\n\nfunc main() {\n    Println(1)\n}\n",
			code:  galaerr.CodeGoDeclGroup, line: 3, col: 0,
			hint: "`type A int`",
		},
		{
			name:  "expression_switch",
			input: "package main\n\nfunc main() {\n    val x = 2\n    switch x {\n    case 1, 2:\n        Println(\"small\")\n    default:\n        Println(\"other\")\n    }\n}\n",
			code:  galaerr.CodeGoSwitch, line: 5, col: 4,
			hint: "`x match { case ... => ... case _ => ... }`",
		},
		{
			name:  "tagless_switch",
			input: "package main\n\nfunc main() {\n    val x = 2\n    switch {\n    case x < 1:\n        Println(\"one\")\n    }\n}\n",
			code:  galaerr.CodeGoSwitch, line: 5, col: 4,
			hint: "`if x < 1 { ... } else { ... }`",
		},
		{
			name:  "type_switch",
			input: "package main\n\nfunc f(x any) {\n    switch v := x.(type) {\n    case int:\n        Println(v)\n    }\n}\n\nfunc main() {\n    f(1)\n}\n",
			code:  galaerr.CodeGoSwitch, line: 4, col: 4,
			hint: "`x match { case v: int => ... case _ => ... }`",
		},
		{
			name:  "type_assertion",
			input: "package main\n\nfunc main() {\n    var x any = 1\n    val n = x.(int)\n    Println(n)\n}\n",
			code:  galaerr.CodeGoTypeAssertion, line: 5, col: 12,
			hint: "`x match { case v: int => ... case _ => ... }`",
		},
		{
			name:  "comma_ok_type_assertion",
			input: "package main\n\nfunc main() {\n    var x any = 1\n    n, ok := x.(int)\n    Println(n, ok)\n}\n",
			code:  galaerr.CodeGoTypeAssertion, line: 5, col: 13,
			hint: "case v: int",
		},
		{
			name:  "empty_interface",
			input: "package main\n\nfunc show(x interface{}) {\n    Println(x)\n}\n\nfunc main() {\n    show(1)\n}\n",
			code:  galaerr.CodeGoTypeForm, line: 3, col: 12,
			hint: "write `any`",
		},
		{
			name:  "fixed_array_var",
			input: "package main\n\nfunc main() {\n    var buf [16]byte\n    Println(buf)\n}\n",
			code:  galaerr.CodeGoTypeForm, line: 4, col: 12,
			hint: "`go_interop.SliceWithSize[byte](16)`",
		},
		{
			name:  "fixed_array_parameter",
			input: "package main\n\nfunc f(b [4]int) int = 1\n\nfunc main() {\n    Println(f)\n}\n",
			code:  galaerr.CodeGoTypeForm, line: 3, col: 9,
			hint: "`[]int`",
		},
		{
			name:  "embedded_struct_field",
			input: "package main\n\ntype Base struct {\n    ID int\n}\n\ntype User struct {\n    Base\n    Name string\n}\n\nfunc main() {\n    Println(1)\n}\n",
			code:  galaerr.CodeGoEmbedded, line: 8, col: 4,
			hint: "`Base Base`",
		},
		{
			name:  "embedded_pointer_field",
			input: "package main\n\ntype Base struct {\n    ID int\n}\n\ntype User struct {\n    *Base\n    Name string\n}\n\nfunc main() {\n    Println(1)\n}\n",
			code:  galaerr.CodeGoEmbedded, line: 8, col: 4,
			hint: "`Base *Base`",
		},
		{
			name:  "embedded_interface",
			input: "package main\n\nimport \"io\"\n\ntype RW interface {\n    io.Reader\n    io.Writer\n}\n\nfunc main() {\n    Println(1)\n}\n",
			code:  galaerr.CodeGoEmbedded, line: 6, col: 4,
			hint: "methods of `io.Reader`",
		},
		{
			name:  "union_with_tilde",
			input: "package main\n\ntype Number interface {\n    ~int | ~float64\n}\n\nfunc main() {\n    Println(1)\n}\n",
			code:  galaerr.CodeGoTypeSetConstraint, line: 4, col: 4,
			hint: "`cmp.Ordered`",
		},
		{
			name:  "union_in_type_parameter",
			input: "package main\n\nfunc sum[T int | float64](a T, b T) T = a + b\n\nfunc main() {\n    Println(sum(1, 2))\n}\n",
			code:  galaerr.CodeGoTypeSetConstraint, line: 3, col: 11,
			hint: "`cmp.Ordered`",
		},
		{
			name:  "approximation_in_type_parameter",
			input: "package main\n\nfunc sum[T ~int](a T, b T) T = a + b\n\nfunc main() {\n    Println(sum(1, 2))\n}\n",
			code:  galaerr.CodeGoTypeSetConstraint, line: 3, col: 11,
			hint: "`cmp.Ordered`",
		},
		{
			name:  "slice_expression",
			input: "package main\n\nfunc main() {\n    val s = go_interop.SliceOf(1, 2, 3)\n    Println(s[1:2])\n}\n",
			code:  galaerr.CodeGoSliceExpr, line: 5, col: 12,
			hint: "`s.Slice(1, 2)` on an `Array` or a `List`, `go_interop.Slice(s, 1, 2)` on a Go slice",
		},
		{
			name:  "slice_expression_from",
			input: "package main\n\nfunc main() {\n    val s = go_interop.SliceOf(1, 2, 3)\n    Println(s[1:])\n}\n",
			code:  galaerr.CodeGoSliceExpr, line: 5, col: 12,
			hint: "`s.Drop(1)`",
		},
		{
			name:  "slice_expression_to",
			input: "package main\n\nfunc main() {\n    val s = go_interop.SliceOf(1, 2, 3)\n    Println(s[:2])\n}\n",
			code:  galaerr.CodeGoSliceExpr, line: 5, col: 12,
			hint: "`go_interop.SliceTake(s, 2)`",
		},
		{
			name:  "make_slice",
			input: "package main\n\nfunc main() {\n    val s = make([]int, 3)\n    Println(s)\n}\n",
			code:  galaerr.CodeForbiddenGoBuiltin, line: 4, col: 12,
			hint: "`go_interop.SliceWithSize[int](3)`",
		},
		{
			name:  "make_slice_with_capacity",
			input: "package main\n\nfunc main() {\n    val s = make([]string, 0, 8)\n    Println(s)\n}\n",
			code:  galaerr.CodeForbiddenGoBuiltin, line: 4, col: 12,
			hint: "`go_interop.SliceWithCapacity[string](8)`",
		},
		{
			name:  "make_map",
			input: "package main\n\nfunc main() {\n    val m = make(map[string]int)\n    Println(m)\n}\n",
			code:  galaerr.CodeForbiddenGoBuiltin, line: 4, col: 12,
			hint: "`go_interop.MapEmpty[string, int]()`",
		},
		{
			name:  "make_chan",
			input: "package main\n\nfunc main() {\n    val c = make(chan int)\n    Println(c)\n}\n",
			code:  galaerr.CodeForbiddenGoBuiltin, line: 4, col: 12,
			hint: "no channels of its own",
		},
		{
			name: "label",
			input: "package main\n\nfunc main() {\nouter:\n    for i := 0; i < 3; i++ {\n" +
				"        for j := 0; j < 3; j++ {\n            if j == 1 {\n                continue outer\n            }\n" +
				"            Println(i, j)\n        }\n    }\n}\n",
			code: galaerr.CodeGoLabel, line: 4, col: 0,
			hint: "flag `var`",
		},
		{
			name:  "labeled_break",
			input: "package main\n\nfunc main() {\n    for i := 0; i < 3; i++ {\n        if i == 1 {\n            break outer\n        }\n    }\n}\n",
			code:  galaerr.CodeGoLabel, line: 6, col: 12,
			hint: "flag `var`",
		},
		{
			name:  "named_results",
			input: "package main\n\nfunc f() (n int, err error) {\n    return 1, nil\n}\n\nfunc main() {\n    Println(f())\n}\n",
			code:  galaerr.CodeGoNamedResults, line: 3, col: 9,
			hint: "`(int, error)`",
		},
		{
			name:  "one_named_result",
			input: "package main\n\nfunc f() (n int) {\n    n = 1\n    return\n}\n\nfunc main() {\n    Println(f())\n}\n",
			code:  galaerr.CodeGoNamedResults, line: 3, col: 9,
			hint: "here, `int`",
		},
		{
			name:  "grouped_named_results",
			input: "package main\n\nfunc (p P) f(x int) (a, b int) {\n    return x, x\n}\n",
			code:  galaerr.CodeGoNamedResults, line: 3, col: 20,
			hint: "`(int, int)`",
		},
		{
			name:  "grouped_type_parameters",
			input: "package main\n\nfunc first[T, U any](a T, b U) T = a\n\nfunc main() {\n    Println(first(1, \"a\"))\n}\n",
			code:  galaerr.CodeGoGroupedTypeParams, line: 3, col: 10,
			hint: "`[T any, U any]`",
		},
		{
			name:  "grouped_type_parameters_of_a_type",
			input: "package main\n\ntype Pair[K, V comparable] struct {\n    k K\n    v V\n}\n",
			code:  galaerr.CodeGoGroupedTypeParams, line: 3, col: 9,
			hint: "`[K comparable, V comparable]`",
		},
		{
			name:  "multiple_return",
			input: "package main\n\nfunc f() (int, string) {\n    return 1, \"a\"\n}\n\nfunc main() {\n    Println(f())\n}\n",
			code:  galaerr.CodeGoMultiReturn, line: 4, col: 4,
			hint: "`return (1, \"a\")`",
		},
		{
			name: "multiple_return_of_an_error",
			input: "package main\n\nimport \"errors\"\n\nfunc f(x int) (int, error) {\n    if x < 0 {\n        return 0, errors.New(\"neg\")\n    }\n" +
				"    return x, nil\n}\n\nfunc main() {\n    Println(f(1))\n}\n",
			code: galaerr.CodeGoMultiReturn, line: 7, col: 8,
			hint: "`return Failure(errors.New(\"neg\"))`",
		},
		{
			name:  "multiple_return_of_a_success",
			input: "package main\n\nfunc f(x int) (int, error) {\n    return x, nil\n}\n",
			code:  galaerr.CodeGoMultiReturn, line: 4, col: 4,
			hint: "`return Success(x)`",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			errs := parseGalop(t, tc.input).Errors
			require.NotEmpty(t, errs, "the Go construct parses")
			var first *galaerr.SemanticError
			for _, err := range errs {
				var se *galaerr.SemanticError
				require.Truef(t, errors.As(err, &se) && se.Code != "",
					"an uncoded error is left: %v (all: %v)", err, errs)
				if first == nil && se.Code == tc.code {
					first = se
				}
			}
			require.NotNilf(t, first, "no %s among %v", tc.code, errs)
			assert.Equal(t, tc.line, first.Line, "line")
			assert.Equal(t, tc.col, first.Column, "column")
			assert.Contains(t, first.Hint, tc.hint)
		})
	}
}

// TestGoHabitsLeaveGALAAlone holds the scan to shapes valid GALA never has:
// GALA forms that look like a Go construct are not reported.
func TestGoHabitsLeaveGALAAlone(t *testing.T) {
	cases := map[string]string{
		"tuple_destructuring":       "package main\n\nfunc main() {\n    val (a, b) = (1, 2)\n    var (c, d) = (3, 4)\n    Println(a, b, c, d)\n}\n",
		"declared_empty_interface":  "package main\n\ntype Any interface{}\n",
		"interface_method_split":    "package main\n\ntype I interface {\n    M\n    (x int) int\n    N()\n    *int\n}\n",
		"interface_methods":         "package main\n\ntype I interface {\n    Close() error\n    Map[U any](f func(int) U) Array[U]\n}\n",
		"struct_field_split":        "package main\n\ntype P struct {\n    Name\n    string\n    val Age int\n}\n",
		"index_and_keyed_literal":   "package main\n\nfunc main() {\n    val p = Point{\n        X: xs[0],\n    }\n    Println(xs[0][1], m[Point{X: 1}])\n}\n",
		"generic_result_type":       "package main\n\nfunc (e Either[A, B]) FlatMap[C any](f func(B) Either[A, C]) Either[A, C] = f(e.b)\n",
		"go_result_list":            "package main\n\nfunc f() (int, error) = Success(1)\n\nfunc g() (*int, []byte, Option[int], pkg.T) = (nil, nil, None(), pkg.T())\n",
		"return_one_value":          "package main\n\nfunc f() Tuple[int, int] {\n    return (1, 2)\n}\n\nfunc g() int {\n    return h(1, 2)\n}\n",
		"break_and_continue":        "package main\n\nfunc main() {\n    for i := 0; i < 3; i++ {\n        if i == 1 { continue }\n        if i == 2 {\n            break\n        }\n    }\n}\n",
		"typed_pattern":             "package main\n\nfunc f(x any) string = x match {\n    case n: int => \"int\"\n    case _ => \"other\"\n}\n",
		"type_parameters":           "package main\n\nfunc f[T any, C Iterable[T, C]](c C) int = 1\n\ntype Box[T comparable] struct {\n    v T\n}\n",
		"dot_import":                "package main\n\nimport . \"martianoff/gala/collection_immutable\"\n",
		"keyed_element_on_its_line": "package main\n\nfunc main() {\n    val p = Point{\n        X:\n            1,\n    }\n    Println(p)\n}\n",
	}
	for name, input := range cases {
		t.Run(name, func(t *testing.T) {
			assert.Empty(t, goHabits(lexGalop(input, 0, 0)))
		})
	}
}

// TestGoHabitsNoneInCleanSources holds every GALA source of the repository
// that lexes and parses cleanly to no Go construct: the scan reports only
// what is not GALA.
func TestGoHabitsNoneInCleanSources(t *testing.T) {
	for _, src := range sourceTexts(t) {
		ts, _, parseErrs, err := galopParse(src, 0, 0, parseSourceFile)
		if err != nil || len(parseErrs) > 0 || len(ts.Errors) > 0 {
			continue
		}
		for _, h := range goHabits(ts) {
			t.Errorf("a clean source reports %v:\n%s", h.err, firstLines(src, 6))
		}
	}
}

// firstLines is the first n lines of src, to name a source in a failure.
func firstLines(src string, n int) string {
	end := 0
	for i := 0; i < n && end < len(src); i++ {
		next := end
		for next < len(src) && src[next] != '\n' {
			next++
		}
		end = next + 1
	}
	return src[:min(end, len(src))]
}
