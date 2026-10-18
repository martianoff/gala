package transformer_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"martianoff/gala/galaerr"
)

// A call to a function that returns nothing has no value: it may stand alone,
// but using its value — an argument, a `val` initializer, an operand, a
// returned value — is a GALA error at the call, not Go's "(no value) used as
// value" on the generated code. A block lambda with no declared result type
// returns nothing, so calling one is such a call.
//
// Every successful output also goes through the package's Go type-check oracle.

const voidValueDecls = `package main

struct Box(N int)

func (b Box) Show() {
    Println(b.N)
}

func id[T any](t T) T = t

func v() {
    Println("v")
}

`

// TestVoidCallValueIsAnError crosses the positions a value is used in with
// calls of void type. Line 15 is the first line after voidValueDecls; the error
// points at the call's first token.
func TestVoidCallValueIsAnError(t *testing.T) {
	trans := newAliasExpectedTranspiler()
	tests := []struct {
		name, input, msg string
		line, col        int
		lambda           bool
	}{
		{"block lambda as an argument", "func main() {\n    val g = () => {\n        Some(4)\n    }\n    Println(g())\n}", "`g()` produces no value, but its value is used", 19, 12, true},
		{"function as an argument", "func main() {\n    Println(v())\n}", "`v()` produces no value, but its value is used", 16, 12, false},
		{"block lambda as a val initializer", "func main() {\n    val k = () => {\n        Println(\"k\")\n    }\n    val x = k()\n    Println(x)\n}", "`k()` produces no value", 19, 12, true},
		{"function as a val initializer", "func main() {\n    val x = v()\n    Println(x)\n}", "`v()` produces no value", 16, 12, false},
		{"function as a generic argument", "func main() {\n    Println(id(v()))\n}", "`v()` produces no value", 16, 15, false},
		{"method as a val initializer", "func main() {\n    val x = Box(1).Show()\n    Println(x)\n}", "`Box(1).Show()` produces no value", 16, 12, false},
		{"function in string interpolation", "func main() {\n    Println(s\"got ${v()}\")\n}", "`v()` produces no value", 16, 20, false},
		{"function as an operand", "func main() {\n    val n = 1 + v()\n    Println(n)\n}", "`v()` produces no value", 16, 16, false},
		{"function as a constructor argument", "func main() {\n    Println(Box(v()))\n}", "`v()` produces no value", 16, 16, false},
		{"function as a lambda's value", "func main() {\n    val g = () => {\n        Println(\"g\")\n        v()\n    }\n    val h = (x int) => x + g()\n    Println(h(1))\n}", "`g()` produces no value", 20, 27, true},
		{"function as a returned value", "func f() int {\n    return v()\n}", "`v()` produces no value", 16, 11, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := trans.Transpile(voidValueDecls+tt.input+"\n", "")
			require.Error(t, err)
			assert.NotContains(t, err.Error(), "used as value")
			var se *galaerr.SemanticError
			require.True(t, errors.As(err, &se), "want a SemanticError, got %v", err)
			assert.Contains(t, se.Error(), tt.msg)
			assert.Equal(t, tt.line, se.Line, "line")
			assert.Equal(t, tt.col, se.Column, "column")
			if tt.lambda {
				assert.Contains(t, se.Hint, "declare its result type, as in `() Option[int] => { ... }`")
			} else {
				assert.Contains(t, se.Hint, "call it as a statement on its own line")
			}
		})
	}
}

// TestVoidCallStatementStaysValid pins the positions where a call of void
// type has its value unused, and calls that have a value: none is an error,
// and the output type-checks.
func TestVoidCallStatementStaysValid(t *testing.T) {
	trans := newAliasExpectedTranspiler()
	tests := []struct {
		name, input string
	}{
		{"statement", "func main() {\n    v()\n    Box(1).Show()\n}"},
		{"block lambda discarding a call", "func main() {\n    val g = () => {\n        Some(4)\n    }\n    g()\n}"},
		{"tail of a function with no result", "func f() = v()"},
		{"tail of a block function with no result", "func f() {\n    Println(\"f\")\n    v()\n}"},
		{"tail of an expression lambda", "func main() {\n    val g = () => v()\n    g()\n}"},
		{"tail of a block lambda", "func main() {\n    val g = () => {\n        Println(\"g\")\n        v()\n    }\n    g()\n}"},
		{"lambda with a declared result type", "func main() {\n    val g = () Option[int] => {\n        Some(4)\n    }\n    Println(g())\n}"},
		{"void lambda passed to a higher-order method", "func main() {\n    Some(1).ForEach((x) => Println(x))\n}"},
		{"arms of a statement match", "func f(n int) {\n    n match {\n        case 1 => v()\n        case _ => Box(n).Show()\n    }\n}"},
		{"branches of a statement if", "func f(ok bool) {\n    if (ok) v() else Box(1).Show()\n}"},
		{"generic call", "func main() {\n    Println(id(1))\n}"},
		{"Go call with a result", "import \"strings\"\n\nfunc main() {\n    Println(strings.ToUpper(\"a\"))\n}"},
		{"diverging call as a function's value", "func f(n int) int {\n    if (n > 0) {\n        return n\n    }\n    Panic(\"negative\")\n}"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			src := voidValueDecls + tt.input + "\n"
			if strings.HasPrefix(tt.input, "import ") {
				src = strings.Replace(voidValueDecls, "package main\n", "package main\n\n"+strings.SplitN(tt.input, "\n\n", 2)[0]+"\n", 1) + strings.SplitN(tt.input, "\n\n", 2)[1] + "\n"
			}
			_, err := trans.Transpile(src, "")
			require.NoError(t, err)
		})
	}
}
