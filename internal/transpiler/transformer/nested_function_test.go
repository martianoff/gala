package transformer_test

import (
	"errors"
	"testing"

	"martianoff/gala/galaerr"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNestedFunctionDeclarationRejected pins GALA-E0052: a named `func` inside
// a function body is refused with a coded diagnostic at the declaration, never
// emitted as Go that fails to parse (which surfaced as the internal-error code
// GALA-E0017).
func TestNestedFunctionDeclarationRejected(t *testing.T) {
	trans := newTranspiler()

	cases := []struct {
		name      string
		input     string
		line      int
		column    int
		endColumn int
		msg       string
		hint      string
	}{
		{
			name: "block-bodied function in main",
			input: `package main

func main() {
    func helper(x int) int { return x + 1 }
    Println(helper(2))
}`,
			line: 4, column: 4, endColumn: 15,
			msg:  "function `helper` is declared inside a function body",
			hint: "write it as a lambda bound to a `val`; here, `val helper = (x int) int => ...`",
		},
		{
			name: "expression-bodied function without a result type",
			input: `package main

func main() {
    func greet(name string) = Println(s"hi $name")
    greet("gala")
}`,
			line: 4, column: 4, endColumn: 14,
			msg:  "function `greet` is declared inside a function body",
			hint: "write it as a lambda bound to a `val`; here, `val greet = (name string) => ...`",
		},
		{
			name: "multi-parameter signature keeps its spacing",
			input: `package main

func main() {
    func add(a int, b int) int = a + b
    Println(add(1, 2))
}`,
			line: 4, column: 4, endColumn: 12,
			msg:  "function `add` is declared inside a function body",
			hint: "write it as a lambda bound to a `val`; here, `val add = (a int, b int) int => ...`",
		},
		{
			name: "inside an if block",
			input: `package main

func run(flag bool) {
    if flag {
        func inner() int = 1
        Println(inner())
    }
}

func main() {
    run(true)
}`,
			line: 5, column: 8, endColumn: 18,
			msg:  "function `inner` is declared inside a function body",
			hint: "write it as a lambda bound to a `val`; here, `val inner = () int => ...`",
		},
		{
			name: "inside a lambda body",
			input: `package main

func main() {
    val f = () => {
        func twice(x int) int = x * 2
        return twice(3)
    }
    Println(f())
}`,
			line: 5, column: 8, endColumn: 18,
			msg:  "function `twice` is declared inside a function body",
			hint: "write it as a lambda bound to a `val`; here, `val twice = (x int) int => ...`",
		},
		{
			name: "inside a lambda passed as an argument",
			input: `package main

import . "martianoff/gala/collection_immutable"

func main() {
    val ys = ArrayOf(1, 2).Map((x) => {
        func dbl(v int) int = v * 2
        return dbl(x)
    })
    Println(ys)
}`,
			line: 7, column: 8, endColumn: 16,
			msg:  "function `dbl` is declared inside a function body",
			hint: "write it as a lambda bound to a `val`; here, `val dbl = (v int) int => ...`",
		},
		{
			name: "multi-line signature is respelled on one line",
			input: `package main

func main() {
    func add(
        a int,
        b int,
    ) int = a + b
    Println(add(1, 2))
}`,
			line: 4, column: 4, endColumn: 12,
			msg:  "function `add` is declared inside a function body",
			hint: "write it as a lambda bound to a `val`; here, `val add = (a int, b int) int => ...`",
		},
		{
			name: "a parameter without both a name and a type has no direct lambda spelling",
			input: `package main

func main() {
    func one(int) int = 1
    Println(one(0))
}`,
			line: 4, column: 4, endColumn: 12,
			msg:  "function `one` is declared inside a function body",
			hint: "write it as a lambda bound to a `val`; a lambda names every parameter, as in `(x int) =>`",
		},
		{
			name: "function with a default parameter value",
			input: `package main

func main() {
    func greet(name string = "gala") string = s"hi $name"
    Println(greet())
}`,
			line: 4, column: 4, endColumn: 14,
			msg:  "function `greet` is declared inside a function body",
			hint: "declare `greet` at the top level of the file; a lambda cannot take default parameter values",
		},
		{
			name: "generic local function",
			input: `package main

func main() {
    func id[T any](x T) T = x
    Println(id(1))
}`,
			line: 4, column: 4, endColumn: 11,
			msg:  "function `id` is declared inside a function body",
			hint: "declare `id` at the top level of the file; a lambda cannot take type parameters",
		},
		{
			name: "method declared inside a body",
			input: `package main

struct Box(V int)

func main() {
    func (b Box) Double() int = b.V * 2
    Println(Box(1).Double())
}`,
			line: 6, column: 4, endColumn: 23,
			msg:  "method `Double` is declared inside a function body",
			hint: "declare `Double` at the top level of the file; a method sits beside its receiver type",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := trans.Transpile(tc.input, "nested_function_test.gala")
			require.Error(t, err)
			assert.NotContains(t, err.Error(), string(galaerr.CodeInternalTransformerPanic),
				"a nested func must never reach the internal-error path")

			var se *galaerr.SemanticError
			require.True(t, errors.As(err, &se), "expected a SemanticError, got %T: %v", err, err)
			assert.Equal(t, galaerr.CodeNestedFunctionDeclaration, se.Code)
			assert.Equal(t, tc.msg, se.Msg)
			assert.Equal(t, tc.hint, se.Hint)
			assert.Equal(t, tc.line, se.Line)
			assert.Equal(t, tc.column, se.Column)
			assert.Equal(t, tc.endColumn, se.EndColumn)
		})
	}
}

// TestLocalFunctionAsLambdaAccepted is the complement of the rejection: every
// shape the GALA-E0052 hint points at must transpile — a lambda with the
// declaration's own signature, and a recursive one declared through `var` and
// assigned. (A generic helper kept at the top level is covered by the
// local_function_lambda example.)
func TestLocalFunctionAsLambdaAccepted(t *testing.T) {
	trans := newTranspiler()

	cases := []struct {
		name   string
		input  string
		expect string
	}{
		{
			name: "lambda carrying the result type the hint copies",
			input: `package main

func main() {
    val helper = (x int) int => x + 1
    Println(helper(2))
}`,
			expect: "func(x int) int",
		},
		{
			name: "recursive lambda through var",
			input: `package main

func main() {
    var fact func(int) int
    fact = (n int) => if (n <= 1) 1 else n * fact(n - 1)
    Println(fact(5))
}`,
			expect: "var fact func(int) int",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := trans.Transpile(tc.input, "nested_function_test.gala")
			require.NoError(t, err)
			assert.Contains(t, out, tc.expect)
		})
	}
}
