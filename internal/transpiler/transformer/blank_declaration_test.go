package transformer_test

import (
	"errors"
	"testing"

	"martianoff/gala/galaerr"

	"github.com/stretchr/testify/require"
)

const (
	blankFunctionHint = "write the expression as a bare statement; if the value matters, bind it to a name and use it"
	blankLambdaHint   = "write the expression as a bare statement; in a lambda with no result, a call that returns only an `error` becomes `FromError(call())`; if the value matters, bind it to a name and use it"
	blankTopLevelHint = "to run it for its effect, call it from `func init()`; if the value matters, bind it to a name and use it"
)

// TestBlankDeclarationIsAnError pins GALA-E0060: a `val`, `var` or `:=` whose
// only name is `_` binds nothing, so the expression has to stand on its own.
// The error points at the declaration and spans through the `_`; the hint
// fits where the declaration sits.
func TestBlankDeclarationIsAnError(t *testing.T) {
	trans := newForbiddenBuiltinTranspiler()
	cases := []struct {
		name      string
		input     string
		line, col int
		endCol    int
		msg, hint string
	}{
		{
			name: "val in a function body",
			input: `package main

func compute() int = 42

func main() {
    val _ = compute()
}
`,
			line: 6, col: 4, endCol: 9,
			msg: "`val _ = ...` binds nothing", hint: blankFunctionHint,
		},
		{
			name: "var in a method body",
			input: `package main

struct Counter(N int)

func (c Counter) Next() int = c.N + 1

func (c Counter) Touch() {
    var _ = c.Next()
}

func main() {
    Counter(1).Touch()
}
`,
			line: 8, col: 4, endCol: 9,
			msg: "`var _ = ...` binds nothing", hint: blankFunctionHint,
		},
		{
			name: "short declaration in a function body",
			input: `package main

func compute() int = 42

func main() {
    _ := compute()
}
`,
			line: 6, col: 4, endCol: 5,
			msg: "`_ := ...` binds nothing", hint: blankFunctionHint,
		},
		{
			name: "val in a lambda with no result",
			input: `package main

func run(f func()) {
    f()
}

func compute() int = 42

func main() {
    run(() => {
        val _ = compute()
    })
}
`,
			line: 11, col: 8, endCol: 13,
			msg: "`val _ = ...` binds nothing", hint: blankLambdaHint,
		},
		{
			// A type annotation must not reopen the way around the check that
			// refuses to drop an error in a lambda with no result.
			name: "typed var holding an error in a lambda",
			input: `package main

import (
    "os"
    . "martianoff/gala/collection_immutable"
)

func main() {
    ArrayOf("a.tmp").ForEach((p) => {
        var _ error = os.Remove(p)
    })
}
`,
			line: 10, col: 8, endCol: 13,
			msg: "`var _ error = ...` binds nothing", hint: blankLambdaHint,
		},
		{
			name: "short declaration in a lambda",
			input: `package main

func compute() int = 42

func main() {
    val f = () => {
        _ := compute()
    }
    f()
}
`,
			line: 7, col: 8, endCol: 9,
			msg: "`_ := ...` binds nothing", hint: blankLambdaHint,
		},
		{
			name: "typed val in a function body",
			input: `package main

func compute() int = 42

func main() {
    val _ int = compute()
}
`,
			line: 6, col: 4, endCol: 9,
			msg: "`val _ int = ...` binds nothing", hint: blankFunctionHint,
		},
		{
			// Nested in a package-level initializer, the declaration is a
			// statement of a block, where a bare expression works.
			name: "val in a match arm of a package-level initializer",
			input: `package main

func compute() int = 42

val picked = 1 match {
    case 1 => {
        val _ = compute()
        1
    }
    case _ => 0
}

func main() {
    Println(picked)
}
`,
			line: 7, col: 8, endCol: 13,
			msg: "`val _ = ...` binds nothing", hint: blankFunctionHint,
		},
		{
			name: "val in a match arm",
			input: `package main

func compute() int = 42

func main() {
    3 match {
        case 1 => {
            val _ = compute()
        }
        case _ => Println("other")
    }
}
`,
			line: 8, col: 12, endCol: 17,
			msg: "`val _ = ...` binds nothing", hint: blankFunctionHint,
		},
		{
			name: "package-level val",
			input: `package main

func compute() int = 42

val _ = compute()

func main() {}
`,
			line: 5, col: 0, endCol: 5,
			msg: "`val _ = ...` binds nothing", hint: blankTopLevelHint,
		},
		{
			name: "package-level var",
			input: `package main

func compute() int = 42

var _ = compute()

func main() {}
`,
			line: 5, col: 0, endCol: 5,
			msg: "`var _ = ...` binds nothing", hint: blankTopLevelHint,
		},
		{
			name: "typed package-level val",
			input: `package main

type Shape interface {
    Area() float64
}

struct Circle(R float64)

func (c Circle) Area() float64 = c.R

val _ Shape = Circle(1.0)

func main() {}
`,
			line: 11, col: 0, endCol: 5,
			msg: "`val _ Shape = ...` binds nothing", hint: blankTopLevelHint,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := trans.Transpile(tc.input, "blank_declaration_test.gala")
			require.Error(t, err)
			var se *galaerr.SemanticError
			require.True(t, errors.As(err, &se), "want a semantic error, got %T: %v", err, err)
			require.Equal(t, galaerr.CodeBlankValDeclaration, se.Code, "got: %v", err)
			require.Equal(t, tc.msg, se.Msg)
			require.Equal(t, tc.hint, se.Hint)
			require.Equal(t, tc.line, se.Line, "line of %v", err)
			require.Equal(t, tc.col, se.Column, "column of %v", err)
			require.Equal(t, tc.endCol, se.EndColumn, "end column of %v", err)
		})
	}
}

// TestBlankNameElsewhereIsAllowed pins what GALA-E0060 leaves alone: `_` among
// several names, in a tuple pattern, as a lambda parameter, in a match pattern
// and in a for-range clause, and `var _ T` with no initializer.
func TestBlankNameElsewhereIsAllowed(t *testing.T) {
	trans := newForbiddenBuiltinTranspiler()
	cases := []struct {
		name  string
		input string
	}{
		{
			name: "tuple pattern in val and var",
			input: `package main

func main() {
    val (_, b) = (1, "x")
    var (_, d) = (2, "y")
    Println(b, d)
}
`,
		},
		{
			name: "blank among the names of a multi-value Go call",
			input: `package main

import "strconv"

func main() {
    val n, _ = strconv.Atoi("7")
    var m, _ = strconv.Atoi("8")
    k, _ := strconv.Atoi("9")
    Println(n, m, k)
}
`,
		},
		{
			name: "var with a type and no initializer",
			input: `package main

func main() {
    var _ int
    Println("ok")
}
`,
		},
		{
			name: "lambda parameter, match pattern and range clause",
			input: `package main

func main() {
    val f = (_ int) => 3
    val n = 4 match {
        case _ => 5
    }
    for _, r := range "a" {
        Println(r)
    }
    Println(f(1), n)
}
`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := trans.Transpile(tc.input, "blank_declaration_test.gala")
			require.NoError(t, err)
		})
	}
}
