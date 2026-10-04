package transformer_test

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"martianoff/gala/galaerr"
)

// A `return`, `break` or `continue` in an arm of a match, or a branch of an
// if-expression, whose value initializes a local `val` or `var` acts on the
// enclosing function or loop: the construct is lowered as statements storing
// its value, not as a function literal the `return` would leave. Elsewhere a
// `return` that would leave only the construct is GALA-E0069.
//
// The runtime behaviour is pinned by examples/value_match_return.gala and
// examples/value_match_control_flow.gala; these tests pin the lowering, and
// every output also goes through the package's Go type-check oracle.

// TestValueMatchControlFlowLowersAsStatements crosses the declaration forms
// with match and if-expression arms holding control flow.
func TestValueMatchControlFlowLowersAsStatements(t *testing.T) {
	trans := newAliasExpectedTranspiler()
	tests := []struct {
		name, input string
		want        []string
	}{
		{
			name:  "val initialized by a match with a return arm",
			input: "func pick(o Option[int]) int {\n    val x = o match {\n        case Some(v) => v\n        case None() => { return -1 }\n    }\n    x * 10\n}",
			want:  []string{"var _tmp_1 int\n", "return -1", "_tmp_1 = v"},
		},
		{
			name:  "var initialized by an if-expression with a return branch",
			input: "func pick(n int) int {\n    var x = if (n > 0) n else { return -1 }\n    x = x + 1\n    x\n}",
			want:  []string{"return -1"},
		},
		{
			name:  "declared type types the return-free arms",
			input: "func pick(o Option[int]) string {\n    val x int = o match {\n        case Some(v) => v\n        case None() => { return \"none\" }\n    }\n    s\"$x\"\n}",
			want:  []string{"return \"none\""},
		},
		{
			name:  "nested match in an arm stores into the same variable",
			input: "func pick(o Option[int], ok bool) int {\n    val x = o match {\n        case Some(v) => ok match {\n            case true => v\n            case false => { return -2 }\n        }\n        case None() => { return -1 }\n    }\n    x\n}",
			want:  []string{"return -2", "return -1"},
		},
		{
			name:  "break and continue in a loop",
			input: "func loop() {\n    for i := 0; i < 6; i++ {\n        val x = i match {\n            case 1 => { continue }\n            case 4 => { break }\n            case n => n\n        }\n        val y = if (x == 2) { continue } else x\n        Println(y)\n    }\n}",
			want:  []string{"continue", "break"},
		},
		{
			name:  "a return in a lambda inside an arm stays the lambda's",
			input: "func pick(n int) int {\n    val f = (k int) int => {\n        val x = k match {\n            case 0 => { return -1 }\n            case _ => k\n        }\n        x\n    }\n    f(n)\n}",
			want:  []string{"return -1"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := trans.Transpile("package main\n\n"+tt.input+"\n", "")
			require.NoError(t, err)
			for _, w := range tt.want {
				assert.Contains(t, got, w)
			}
		})
	}
}

// TestReturnLeavingOnlyAValueMatchIsAnError pins GALA-E0069 for a `return` in
// a construct lowered to a function literal whose value is used other than as
// the enclosing function's result, and the forms that are not errors because
// leaving the construct is leaving the function.
func TestReturnLeavingOnlyAValueMatchIsAnError(t *testing.T) {
	trans := newAliasExpectedTranspiler()
	const header = "package main\n\nfunc twice(n int) int = n * 2\n\n"
	errorCases := []struct {
		name, input, msg string
		line, col        int
	}{
		{"match as an argument", "func f(o Option[int]) int {\n    val y = twice(o match {\n        case Some(v) => v\n        case None() => { return -1 }\n    })\n    y\n}", "`return` inside a match whose value is used leaves only the match, not the function", 8, 25},
		{"if-expression as an argument", "func f(n int) int {\n    val y = twice(if (n > 0) n else { return -1 })\n    y\n}", "`return` inside an if-expression whose value is used leaves only the if-expression, not the function", 6, 38},
		{"match as an operand", "func f(o Option[int]) int {\n    val x = 1 + (o match {\n        case Some(v) => v\n        case None() => { return -1 }\n    })\n    x\n}", "`return` inside a match whose value is used", 8, 25},
		{"match assigned to an existing var", "func f(o Option[int]) int {\n    var x = 0\n    x = o match {\n        case Some(v) => v\n        case None() => { return -1 }\n    }\n    x\n}", "`return` inside a match whose value is used", 9, 25},
	}
	for _, tt := range errorCases {
		t.Run(tt.name, func(t *testing.T) {
			_, err := trans.Transpile(header+tt.input+"\n", "")
			require.Error(t, err)
			var se *galaerr.SemanticError
			require.True(t, errors.As(err, &se), "want a SemanticError, got %v", err)
			assert.Equal(t, galaerr.CodeReturnInBranchingValue, se.Code)
			assert.Contains(t, se.Error(), tt.msg)
			assert.Equal(t, tt.line, se.Line, "line")
			assert.Equal(t, tt.col, se.Column, "column")
		})
	}

	validCases := []struct{ name, input string }{
		{"returned match", "func f(o Option[int]) int {\n    return o match {\n        case Some(v) => v\n        case None() => { return -1 }\n    }\n}"},
		{"trailing match of a function", "func f(o Option[int]) int {\n    Println(\"x\")\n    o match {\n        case Some(v) => v\n        case None() => { return -1 }\n    }\n}"},
		{"trailing match of a lambda", "func f(n int) int {\n    val g = (k int) int => k match {\n        case 0 => { return -1 }\n        case _ => k\n    }\n    g(n)\n}"},
	}
	for _, tt := range validCases {
		t.Run(tt.name, func(t *testing.T) {
			_, err := trans.Transpile(header+tt.input+"\n", "")
			require.NoError(t, err)
		})
	}
}

// TestDeclarationOfAConstructThatAlwaysLeaves pins a declaration whose match
// or if-expression leaves on every branch: with `return` values, its variable
// takes their type and the code compiles (the declaration is never reached);
// with only `break` / `continue` it has no type at all, GALA-E0068.
func TestDeclarationOfAConstructThatAlwaysLeaves(t *testing.T) {
	trans := newAliasExpectedTranspiler()
	_, err := trans.Transpile("package main\n\nfunc f(c bool) int {\n    val x = if (c) { return 1 } else { return 2 }\n    x\n}\n", "")
	require.NoError(t, err)

	_, err = trans.Transpile("package main\n\nfunc f(c bool) {\n    for i := 0; i < 3; i++ {\n        val x = if (c) { break } else { continue }\n        Println(x)\n    }\n}\n", "")
	require.Error(t, err)
	var se *galaerr.SemanticError
	require.True(t, errors.As(err, &se), "want a SemanticError, got %v", err)
	assert.Equal(t, galaerr.CodeUntypedBranchingValue, se.Code)
	assert.Contains(t, se.Error(), "this if-expression has no value: every branch leaves with `return`, `break` or `continue`")
}
