package transformer_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"martianoff/gala/galaerr"
)

// A match or if-expression none of whose branches has a typed value, with no
// slot type, has no value. Its value may go unused — a statement, the tail of
// a lambda or function body that then returns nothing, an arm of such a
// construct — and anywhere else it is GALA-E0068 at the construct, not Go's
// "(no value) used as value" on the generated code.
//
// Every successful output also goes through the package's Go type-check oracle.

const untypedBranchingDecls = `package main

sealed type Shape {
    case A()
    case B()
}

struct Box[T any](V T)

func id[T any](t T) T = t

func side() {
    Println("side")
}

`

// TestUntypedBranchingValueIsAnError crosses each position a value is used in
// with the arm shapes that have no typed value. Line 16 is the first line after
// untypedBranchingDecls; the error points at the construct's first token.
func TestUntypedBranchingValueIsAnError(t *testing.T) {
	trans := newAliasExpectedTranspiler()
	const matchMsg = "cannot infer the type of this match: its value is used, but no arm has a typed value"
	const ifMsg = "cannot infer the type of this if-expression: its value is used, but no branch has a typed value"
	tests := []struct {
		name, input, msg string
		line, col        int
	}{
		{"val initializer", "func f(s Shape) {\n    val x = s match {\n        case A() => {}\n        case B() => side()\n    }\n    Println(x)\n}", matchMsg, 17, 12},
		{"argument", "func f(s Shape) {\n    Println(id(s match {\n        case A() => {}\n        case B() => {}\n    }))\n}", matchMsg, 17, 15},
		{"struct field", "func f(s Shape) {\n    val b = Box(s match {\n        case A() => {}\n        case B() => {}\n    })\n    Println(b.V)\n}", matchMsg, 17, 16},
		{"string interpolation", "func f(s Shape) {\n    Println(s\"v=${s match { case A() => {} case B() => {} }}\")\n}", matchMsg, 17, 18},
		{"method receiver", "func f(s Shape) {\n    Println((s match {\n        case A() => {}\n        case B() => {}\n    }).String())\n}", matchMsg, 17, 13},
		{"value of an outer value match", "func f(s Shape, n int) {\n    val x = n match {\n        case 1 => s match {\n            case A() => {}\n            case B() => {}\n        }\n        case _ => {}\n    }\n    Println(x)\n}", matchMsg, 17, 12},
		{"nil arms", "func f(s Shape) {\n    val x = s match {\n        case A() => nil\n        case B() => nil\n    }\n    Println(x)\n}", matchMsg, 17, 12},
		{"assignment arms", "func f(s Shape) {\n    var n = 0\n    val x = s match {\n        case A() => n = 1\n        case B() => n = 2\n    }\n    Println(x, n)\n}", matchMsg, 18, 12},
		{"void call arms", "func f(s Shape) {\n    val x = s match {\n        case A() => side()\n        case B() => side()\n    }\n    Println(x)\n}", matchMsg, 17, 12},
		{"if-expression with empty branches", "func f(ok bool) {\n    val x = if (ok) {} else {}\n    Println(x)\n}", ifMsg, 17, 12},
		{"if-expression with an empty and a void branch", "func f(ok bool) {\n    Println(id(if (ok) {} else side()))\n}", ifMsg, 17, 15},
		{"if-expression as a method receiver", "func f(ok bool) {\n    Println((if (ok) side() else side()).String())\n}", ifMsg, 17, 13},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := trans.Transpile(untypedBranchingDecls+tt.input+"\n", "")
			require.Error(t, err)
			var se *galaerr.SemanticError
			require.True(t, errors.As(err, &se), "want a SemanticError, got %v", err)
			assert.Equal(t, galaerr.CodeUntypedBranchingValue, se.Code)
			assert.Contains(t, se.Error(), tt.msg)
			assert.Equal(t, tt.line, se.Line, "line")
			assert.Equal(t, tt.col, se.Column, "column")
			assert.Contains(t, se.Hint, "as a statement on its own line")
		})
	}
}

// TestUntypedIfExpressionValueIsAnError pins that an if-expression whose
// branches have values of no known type is GALA-E0068 rather than a closure
// returning `any`.
func TestUntypedIfExpressionValueIsAnError(t *testing.T) {
	trans := newAliasExpectedTranspiler()
	_, err := trans.Transpile(untypedBranchingDecls+"func f(ok bool) {\n    val x = if (ok) nil else nil\n    Println(x)\n}\n", "")
	require.Error(t, err)
	var se *galaerr.SemanticError
	require.True(t, errors.As(err, &se), "want a SemanticError, got %v", err)
	assert.Equal(t, galaerr.CodeUntypedBranchingValue, se.Code)
	assert.Contains(t, se.Error(), "cannot infer the type of this if-expression: no branch has a known type")
	assert.Equal(t, 17, se.Line)
	assert.Equal(t, 12, se.Column)
}

// TestUntypedBranchingUnusedValueStaysValid pins the positions where a match
// or if-expression with no typed branch has its value unused: it lowers to a
// closure run as a statement, and the output type-checks.
func TestUntypedBranchingUnusedValueStaysValid(t *testing.T) {
	trans := newAliasExpectedTranspiler()
	tests := []struct {
		name, input string
	}{
		{"statement", "func f(s Shape) {\n    s match {\n        case A() => side()\n        case B() => {}\n    }\n}"},
		{"tail of a function with no result", "func f(s Shape) = s match {\n    case A() => {}\n    case B() => side()\n}"},
		{"tail of an expression lambda", "func f(s Shape) {\n    val run = (x Shape) => x match {\n        case A() => side()\n        case B() => {}\n    }\n    run(s)\n}"},
		{"tail of a block lambda", "func f(s Shape) {\n    val g = () => {\n        Println(\"g\")\n        s match {\n            case A() => side()\n            case B() => {}\n        }\n    }\n    g()\n}"},
		{"arm of a statement match", "func f(s Shape, n int) {\n    n match {\n        case 1 => s match {\n            case A() => {}\n            case B() => side()\n        }\n        case _ => {}\n    }\n}"},
		{"if-expression statement", "func f(ok bool) {\n    if (ok) {} else side()\n}"},
		{"if-expression lambda tail", "func f(ok bool) {\n    val g = () => if (ok) side() else {}\n    g()\n}"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := trans.Transpile(untypedBranchingDecls+tt.input+"\n", "")
			require.NoError(t, err)
			_, body, found := strings.Cut(got, "\nfunc f(")
			require.True(t, found)
			assert.NotContains(t, body, "void")
			assert.NotContains(t, body, "any")
		})
	}
}
