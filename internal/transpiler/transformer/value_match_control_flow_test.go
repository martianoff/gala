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
			name:  "an arm's trailing if/else with a return branch takes its type from the other branch",
			input: "func pick(o Option[int]) string {\n    val x = o match {\n        case Some(v) => {\n            if (v < 0) {\n                return \"neg\"\n            } else {\n                v\n            }\n        }\n        case _ => 0\n    }\n    s\"$x\"\n}",
			want:  []string{"var _tmp_1 int\n", "return \"neg\""},
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
		{"match in a string interpolation", "func f(o Option[int]) int {\n    val s = s\"v=${o match { case Some(v) => v case None() => { return -1 } }}\"\n    s.Size()\n}", "`return` inside a match whose value is used", 6, 63},
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
		{"match assigned to an existing var", "func f(o Option[int]) int {\n    var x = 0\n    x = o match {\n        case Some(v) => v\n        case None() => { return -1 }\n    }\n    x\n}"},
		{"returned match", "func f(o Option[int]) int {\n    return o match {\n        case Some(v) => v\n        case None() => { return -1 }\n    }\n}"},
		{"trailing match of a function", "func f(o Option[int]) int {\n    Println(\"x\")\n    o match {\n        case Some(v) => v\n        case None() => { return -1 }\n    }\n}"},
		{"parenthesized result of a function", "func f(o Option[int]) int = (o match {\n    case Some(v) => v\n    case None() => { return 0 }\n})"},
		{"trailing match of a lambda","func f(n int) int {\n    val g = (k int) int => k match {\n        case 0 => { return -1 }\n        case _ => k\n    }\n    g(n)\n}"},
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

	// An arm that may leave but may also fall through with no value is not
	// one that always leaves: the match has no typed value.
	_, err = trans.Transpile("package main\n\nfunc f(n int) int {\n    val x = n match {\n        case 1 => {\n            if (n > 0) { return 5 }\n            Println(\"x\")\n        }\n        case _ => { return 3 }\n    }\n    x\n}\n", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot infer the type of this match: its value is used, but no arm has a typed value")

	_, err = trans.Transpile("package main\n\nfunc f(c bool) {\n    for i := 0; i < 3; i++ {\n        val x = if (c) { break } else { continue }\n        Println(x)\n    }\n}\n", "")
	require.Error(t, err)
	var se *galaerr.SemanticError
	require.True(t, errors.As(err, &se), "want a SemanticError, got %v", err)
	assert.Equal(t, galaerr.CodeUntypedBranchingValue, se.Code)
	assert.Contains(t, se.Error(), "this if-expression has no value: every branch leaves with `return`, `break` or `continue`")
}

// TestLoopClauseIsNotHoisted pins that only a declaration or an assignment
// statement of its own lowers its match as statements. The post statement of
// a `for` runs on every iteration, so statements hoisted before the loop would
// run once: a `return` there leaves only the match, GALA-E0069, as it does in
// any other value.
func TestLoopClauseIsNotHoisted(t *testing.T) {
	trans := newAliasExpectedTranspiler()
	_, err := trans.Transpile("package main\n\nfunc f(n int) int {\n    var i = 0\n    for ; i < n; i = i match {\n        case 5 => { return -1 }\n        case _ => i + 1\n    } {\n        Println(i)\n    }\n    i\n}\n", "")
	require.Error(t, err)
	var se *galaerr.SemanticError
	require.True(t, errors.As(err, &se), "want a SemanticError, got %v", err)
	assert.Equal(t, galaerr.CodeReturnInBranchingValue, se.Code)

	// Nor is an assignment to an element: its index is evaluated before the
	// value, so the value's statements cannot run first.
	_, err = trans.Transpile("package main\n\nimport . \"martianoff/gala/go_interop\"\n\nfunc f(o Option[int], i int) int {\n    val arr = SliceOf(1, 2, 3)\n    arr[i] = o match {\n        case Some(v) => v\n        case None() => { return -1 }\n    }\n    arr[0]\n}\n", "")
	require.Error(t, err)
	require.True(t, errors.As(err, &se), "want a SemanticError, got %v", err)
	assert.Equal(t, galaerr.CodeReturnInBranchingValue, se.Code)
}

// TestAnyTypedIfExpressionBranches pins that branches typed `any` by a Go API
// give the if-expression the type `any`: it is not GALA-E0068.
func TestAnyTypedIfExpressionBranches(t *testing.T) {
	trans := newAliasExpectedTranspiler()
	got, err := trans.Transpile("package main\n\nimport \"reflect\"\n\nfunc f(ok bool) {\n    val v = if (ok) reflect.ValueOf(1).Interface() else reflect.ValueOf(2).Interface()\n    Println(v)\n}\n", "")
	require.NoError(t, err)
	assert.Contains(t, got, "func() any {")
}

// TestBareReturnInHoistedMatch pins GALA-E0015 for a bare `return` in an arm
// of a match lowered as statements, in a function that returns a value: the
// `return` leaves that function, which needs a value, so Go would reject it
// with "not enough return values". In a function with no result it is fine,
// and so is a bare `return` in a lambda inside the arm, which leaves only the
// lambda.
func TestBareReturnInHoistedMatch(t *testing.T) {
	trans := newAliasExpectedTranspiler()
	_, err := trans.Transpile("package main\n\nfunc f(o Option[int]) int {\n    val x = o match {\n        case Some(v) => v\n        case None() => { return }\n    }\n    x + 1\n}\n", "")
	require.Error(t, err)
	var se *galaerr.SemanticError
	require.True(t, errors.As(err, &se), "want a SemanticError, got %v", err)
	assert.Equal(t, galaerr.CodeBareReturnInValueMatch, se.Code)
	assert.Equal(t, 4, se.Line, "line")
	assert.Equal(t, 12, se.Column, "column")
	assert.Contains(t, se.Error(), "the `return` leaves the enclosing function, which must return int")

	validCases := []struct{ name, input string }{
		{"function with no result", "func f(o Option[int]) {\n    val x = o match {\n        case Some(v) => v\n        case None() => { return }\n    }\n    Println(x)\n}"},
		{"bare return in a lambda in the arm", "func f(o Option[int]) int {\n    val x = o match {\n        case Some(v) => {\n            val g = () => { return }\n            g()\n            v\n        }\n        case None() => { return -1 }\n    }\n    x\n}"},
	}
	for _, tt := range validCases {
		t.Run(tt.name, func(t *testing.T) {
			_, err := trans.Transpile("package main\n\n"+tt.input+"\n", "")
			require.NoError(t, err)
		})
	}
}

// TestTupleDestructuringIsHoisted pins that a tuple destructuring
// `val (a, b) = ...` or `var (a, b) = ...` lowers its match or if-expression
// as statements like a single-name declaration, so a `return` in an arm
// leaves the function.
func TestTupleDestructuringIsHoisted(t *testing.T) {
	trans := newAliasExpectedTranspiler()
	tests := []struct{ name, input string }{
		{"val with a match", "func f(o Option[int]) Tuple[int, int] {\n    val (a, b) = o match {\n        case Some(v) => (v, v + 1)\n        case None() => { return (0, 0) }\n    }\n    (a * 10, b)\n}"},
		{"var with an if-expression", "func f(n int) int {\n    var (a, b) = if (n > 0) (n, n * 2) else { return -1 }\n    a = a + 1\n    a + b\n}"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := trans.Transpile("package main\n\n"+tt.input+"\n", "")
			require.NoError(t, err)
			assert.Contains(t, got, "var _tmp_1 std.Tuple[int, int]\n")
		})
	}
}

// TestUseInHoistedMatchIsReleasedAtItsEnd pins that the statements of a match
// lowered as statements run in a function literal of their own when an arm
// holds a `use`, so its resource is released at the end of the match rather
// than when the enclosing function returns; a `return`, `break` or
// `continue` in them is carried out of the literal. The runtime behaviour is
// pinned by examples/value_match_use_release.gala.
func TestUseInHoistedMatchIsReleasedAtItsEnd(t *testing.T) {
	trans := newAliasExpectedTranspiler()
	const header = "package main\n\ntype Res struct {\n    Name string\n}\n\nfunc (r Res) Close() error = nil\n\n"
	tests := []struct {
		name, input string
		want        []string
	}{
		{
			name:  "return in another arm",
			input: "func f(o Option[Res]) int {\n    val x = o match {\n        case Some(r) => {\n            use g = r\n            g.Name.Size()\n        }\n        case None() => { return -1 }\n    }\n    x\n}",
			want: []string{
				`var (_tmp_\d+) int\s+var (_tmp_\d+) int\s+func\(\) \{`,
				`defer g\.Close\(\)\s+_tmp_1 = `,
				`\{\s+(_tmp_\d+) = -1\s+(_tmp_\d+) = 1\s+return\s+\}`,
				`\}\(\)\s+if _tmp_\d+ == 1 \{\s+return _tmp_\d+\s+\}`,
			},
		},
		{
			name:  "break and continue",
			input: "func f() {\n    for i := 0; i < 5; i++ {\n        val x = i match {\n            case 0 => { continue }\n            case 3 => { break }\n            case _ => {\n                use g = Res(Name = s\"r$i\")\n                g.Name\n            }\n        }\n        Println(x)\n    }\n}",
			want: []string{
				`defer g\.Close\(\)`,
				`\{\s+_tmp_\d+ = 3\s+return\s+\}`,
				`\{\s+_tmp_\d+ = 2\s+return\s+\}`,
				`\}\(\)\s+if _tmp_\d+ == 2 \{\s+break\s+\}\s+if _tmp_\d+ == 3 \{\s+continue\s+\}`,
			},
		},
		{
			name:  "if-expression",
			input: "func f(n int) int {\n    val x = if (n > 0) {\n        use g = Res(Name = \"a\")\n        n\n    } else {\n        return -1\n    }\n    x\n}",
			want:  []string{`defer g\.Close\(\)`, `\}\(\)\s+if _tmp_\d+ == 1 \{\s+return _tmp_\d+\s+\}`},
		},
		{
			// The nested match runs in a literal of its own; the outer one,
			// whose statements then hold no `defer`, does not.
			name:  "use in a nested match",
			input: "func f(o Option[Res], ok bool) int {\n    val x = o match {\n        case Some(r) => ok match {\n            case true => {\n                use g = r\n                1\n            }\n            case false => { return -2 }\n        }\n        case None() => { return -1 }\n    }\n    x\n}",
			want:  []string{`defer g\.Close\(\)`, `_tmp_\d+ = -2\s`, `\sreturn -1\s`},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := trans.Transpile(header+tt.input+"\n", "")
			require.NoError(t, err)
			for _, w := range tt.want {
				assert.Regexp(t, w, got)
			}
		})
	}
}
