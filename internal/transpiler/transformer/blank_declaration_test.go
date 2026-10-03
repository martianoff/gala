package transformer_test

import (
	"errors"
	"testing"

	"martianoff/gala/galaerr"

	"github.com/stretchr/testify/require"
)

// TestBlankDeclarationIsAnError pins GALA-E0060: a `val` or `var` whose only
// name is `_` binds nothing, so the expression has to stand on its own. The
// error points at the declaration keyword and spans through the `_`.
func TestBlankDeclarationIsAnError(t *testing.T) {
	trans := newForbiddenBuiltinTranspiler()
	cases := []struct {
		name          string
		input         string
		line, col     int
		endCol        int
		msg, hintPart string
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
			msg:      "`val _ = ...` binds nothing",
			hintPart: "write the expression as a bare statement",
		},
		{
			name: "var in a function body",
			input: `package main

func compute() int = 42

func main() {
    var _ = compute()
}
`,
			line: 6, col: 4, endCol: 9,
			msg:      "`var _ = ...` binds nothing",
			hintPart: "write the expression as a bare statement",
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
			msg:      "`val _ = ...` binds nothing",
			hintPart: "write `FromError(call())`",
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
			msg:      "`val _ = ...` binds nothing",
			hintPart: "write the expression as a bare statement",
		},
		{
			name: "package-level val",
			input: `package main

func compute() int = 42

val _ = compute()

func main() {}
`,
			line: 5, col: 0, endCol: 5,
			msg:      "`val _ = ...` binds nothing",
			hintPart: "call it from `func init()`",
		},
		{
			name: "package-level var",
			input: `package main

func compute() int = 42

var _ = compute()

func main() {}
`,
			line: 5, col: 0, endCol: 5,
			msg:      "`var _ = ...` binds nothing",
			hintPart: "call it from `func init()`",
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
			require.Contains(t, se.Hint, tc.hintPart)
			require.Equal(t, tc.line, se.Line, "line of %v", err)
			require.Equal(t, tc.col, se.Column, "column of %v", err)
			require.Equal(t, tc.endCol, se.EndColumn, "end column of %v", err)
		})
	}
}

// TestBlankNameElsewhereIsAllowed pins what GALA-E0060 leaves alone: `_` among
// several names, in a tuple pattern, as a lambda parameter, in a match
// pattern and in a for-range clause, and a typed `val _ T = expr`, which
// checks at compile time that the value is a T.
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
    Println(n, m)
}
`,
		},
		{
			name: "typed package-level conformance check",
			input: `package main

type Shape interface {
    Area() float64
}

struct Circle(R float64)

func (c Circle) Area() float64 = c.R

val _ Shape = Circle(1.0)

func main() {}
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
