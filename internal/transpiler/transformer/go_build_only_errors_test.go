package transformer_test

import (
	"errors"
	"testing"

	"martianoff/gala/galaerr"
	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/analyzer"
	"martianoff/gala/internal/transpiler/generator"
	"martianoff/gala/internal/transpiler/transformer"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGoBuildOnlyErrorsReportedInGala: programs that used to pass GALA's
// checks and fail only in `go build`, or got a misleading GALA message, are
// rejected by GALA at the source position, with a message that names the
// problem (#1008).
func TestGoBuildOnlyErrorsReportedInGala(t *testing.T) {
	p := transpiler.NewAntlrGalaParser()
	a := analyzer.NewGalaAnalyzer(p, getStdSearchPath())
	trans := newCheckedTranspiler(p, a, transformer.NewGalaASTTransformer(), generator.NewGoCodeGenerator())

	cases := []struct {
		name     string
		input    string
		code     galaerr.ErrorCode // "" for an uncoded diagnostic
		line     int
		contains []string
	}{
		{
			name: "Ptr to a mutable collection passed where the collection is expected",
			input: `package main

import "martianoff/gala/collection_mutable"

func total(xs collection_mutable.Array[int]) int = xs.Length()

func main() {
    Println(total(collection_mutable.ArrayOf(1, 2)))
}`,
			code: galaerr.CodePointerForValue,
			line: 8,
			contains: []string{
				"cannot use collection_mutable.ArrayOf(1, 2) (Ptr[collection_mutable.Array[int]]) as collection_mutable.Array[int]",
				"Ptr[collection_mutable.Array[int]]",
			},
		},
		{
			name: "Ptr to a struct passed where the struct is expected",
			input: `package main

struct Pt(X int)

func show(p Pt) string = s"${p.X}"

func main() {
    var pt = Pt(1)
    Println(show(&pt))
}`,
			code:     galaerr.CodePointerForValue,
			line:     9,
			contains: []string{"cannot use &pt (Ptr[Pt]) as Pt", "declare the parameter, field or variable as Ptr[Pt], or pass the value itself: `pt`"},
		},
		{
			name: "field of a collection type initialised with EmptyArray()",
			input: `package main

import "martianoff/gala/collection_mutable"

struct Box(items collection_mutable.Array[int])

func main() {
    val b = Box(collection_mutable.EmptyArray())
    Println(b.items.Length())
}`,
			code: galaerr.CodePointerForValue,
			line: 8,
			contains: []string{
				"cannot use collection_mutable.EmptyArray(...) (Ptr[collection_mutable.Array[int]]) as collection_mutable.Array[int]",
				"as Ptr[collection_mutable.Array[int]], as mutable collections are passed",
			},
		},
		{
			name: "field of a collection type initialised with EmptyArray[int]()",
			input: `package main

import "martianoff/gala/collection_mutable"

struct Box(items collection_mutable.Array[int])

func main() {
    val b = Box(collection_mutable.EmptyArray[int]())
    Println(b.items.Length())
}`,
			code:     galaerr.CodePointerForValue,
			line:     8,
			contains: []string{"as collection_mutable.Array[int]"},
		},
		{
			name: "Ptr returned where the value is the result",
			input: `package main

import "martianoff/gala/collection_mutable"

func build() collection_mutable.Array[int] = collection_mutable.ArrayOf(1)

func main() {
    Println(build().Length())
}`,
			code: galaerr.CodePointerForValue,
			line: 5,
		},
		{
			name: "tuple field that does not exist",
			input: `package main

func main() {
    val tup = (1, "a", true)
    Println(tup._1)
}`,
			code:     galaerr.CodeUnknownMethod,
			line:     5,
			contains: []string{"Tuple3 has no field or method _1", "Tuple3's fields are V1, V2, V3"},
		},
		{
			name: "struct field that does not exist",
			input: `package main

struct Pt(X int, Y int)

func main() {
    val p = Pt(1, 2)
    Println(p.Z)
}`,
			code:     galaerr.CodeUnknownMethod,
			line:     7,
			contains: []string{"Pt has no field or method Z", "Pt's fields are X, Y"},
		},
		{
			name: "unused val",
			input: `package main

func main() {
    val x = 5
    Println("hi")
}`,
			code:     galaerr.CodeUnusedBinding,
			line:     4,
			contains: []string{"unused val 'x'"},
		},
		{
			name: "unused var",
			input: `package main

func main() {
    var n = 0
    Println("hi")
}`,
			code:     galaerr.CodeUnusedBinding,
			line:     4,
			contains: []string{"unused var 'n'"},
		},
		{
			name: "unused val in a lambda body",
			input: `package main

func main() {
    val f = (x int) int => {
        val doubled = x * 2
        return x + 1
    }
    Println(f(1))
}`,
			code:     galaerr.CodeUnusedBinding,
			line:     5,
			contains: []string{"unused val 'doubled'"},
		},
		{
			name: "unused component of a tuple destructuring",
			input: `package main

func main() {
    val (a, b) = (1, "x")
    Println(a)
}`,
			code:     galaerr.CodeUnusedBinding,
			line:     4,
			contains: []string{"unused val 'b'", "write `_` in its place"},
		},
		{
			name: "var that is only ever assigned",
			input: `package main

func main() {
    var n = 0
    n = 5
    Println("hi")
}`,
			code:     galaerr.CodeUnusedBinding,
			line:     4,
			contains: []string{"unused var 'n'"},
		},
		{
			name: "if-expression mixing int and float64",
			input: `package main

func main() {
    val c = true
    val v = if (c) 1 else 2.5
    Println(v)
}`,
			line: 5,
			contains: []string{
				"type mismatch in if-expression: the then-branch returns 'int' but the else-branch returns 'float64'",
				"numeric types do not convert implicitly",
			},
		},
		{
			// A branch with a `return` lowers the if-expression to
			// statements, which reported "no branch has a typed value".
			name: "if-expression mixing int and float64 with a return in a branch",
			input: `package main

func pick(c bool, d bool) float64 {
    val v = if (c) 1 else {
        if (d) {
            return 0.0
        }
        2.5
    }
    return v
}

func main() {
    Println(pick(true, false))
}`,
			line: 4,
			contains: []string{
				"type mismatch in if-expression: the then-branch returns 'int' but the else-branch returns 'float64'",
				"numeric types do not convert implicitly",
			},
		},
		{
			name: "lambda whose if-expression mixes int and float64",
			input: `package main

import "martianoff/gala/collection_immutable"

func main() {
    val xs = collection_immutable.ArrayOf(1, 2)
    Println(xs.Map((x) => if (x > 1) x else 0.5))
}`,
			line:     7,
			contains: []string{"the then-branch returns 'int' but the else-branch returns 'float64'"},
		},
		{
			name: "if-expression mixing string and int",
			input: `package main

func main() {
    val c = true
    val v = if (c) "a" else 1
    Println(v)
}`,
			line:     5,
			contains: []string{"the then-branch returns 'string' but the else-branch returns 'int'"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := trans.Transpile(tc.input, "")
			require.Error(t, err)
			var se *galaerr.SemanticError
			require.True(t, errors.As(err, &se), "want a GALA semantic error, got %T: %v", err, err)
			assert.Equal(t, tc.code, se.Code, err.Error())
			assert.Equal(t, tc.line, se.Line, err.Error())
			for _, want := range tc.contains {
				assert.Contains(t, se.Msg+"\n"+se.Hint, want)
			}
		})
	}
}

// TestGoBuildOnlyErrorsAccepted: what the checks above reject has a correct
// form, which they leave alone: the type-check oracle compiles each output.
func TestGoBuildOnlyErrorsAccepted(t *testing.T) {
	p := transpiler.NewAntlrGalaParser()
	a := analyzer.NewGalaAnalyzer(p, getStdSearchPath())
	trans := newCheckedTranspiler(p, a, transformer.NewGalaASTTransformer(), generator.NewGoCodeGenerator())

	inputs := map[string]string{
		"collections passed and held through Ptr": `package main

import "martianoff/gala/collection_mutable"

struct Box(items Ptr[collection_mutable.Array[int]])

func total(xs Ptr[collection_mutable.Array[int]]) int = xs.Length()

func main() {
    val b = Box(collection_mutable.EmptyArray())
    b.items.Append(3)
    Println(total(collection_mutable.ArrayOf(1, 2)) + total(b.items))
}`,
		"dereferenced Ptr fills a value slot": `package main

struct Pt(X int)

func show(p Pt) string = s"${p.X}"

func main() {
    var pt = Pt(1)
    val ptr = &pt
    Println(show(*ptr))
}`,
		"fields, method values and tuple components": `package main

struct Pt(X int, Y int)

func (p Pt) Sum() int = p.X + p.Y

func main() {
    val p = Pt(1, 2)
    val sum = p.Sum
    val tup = (1, "a", true)
    Println(p.X + sum() + tup.V1)
}`,
		"every binding read, by a closure, an interpolation or a destructuring": `package main

func main() {
    val a = 1
    val b = 2
    var c = 3
    c = c + 1
    val f = () => a
    val (d, _) = (4, "x")
    Println(s"$b ${f()} $c $d")
}`,
		"if-expression whose branches share a type, or fill a declared one": `package main

func main() {
    val c = true
    val x float64 = if (c) 1 else 2.5
    val y = if (c) 1.0 else 2.5
    Println(x + y)
}`,
	}
	for name, input := range inputs {
		t.Run(name, func(t *testing.T) {
			_, err := trans.Transpile(input, "")
			require.NoError(t, err)
		})
	}
}
