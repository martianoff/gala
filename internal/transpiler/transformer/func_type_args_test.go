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

// TestFuncTypeArguments: a function type is written as an explicit type
// argument as any other type (#1008), and is rejected where a value is
// expected.
func TestFuncTypeArguments(t *testing.T) {
	p := transpiler.NewAntlrGalaParser()
	a := analyzer.NewGalaAnalyzer(p, getStdSearchPath())
	trans := newCheckedTranspiler(p, a, transformer.NewGalaASTTransformer(), generator.NewGoCodeGenerator())

	out, err := trans.Transpile(`package main

import "martianoff/gala/collection_mutable"

func main() {
    val fs = collection_mutable.EmptyArray[func() int]()
    val parse = None[func(string) (int, error)]()
    fs.Append(() => 1)
    Println(fs.Length(), parse.IsEmpty())
}`, "")
	require.NoError(t, err)
	assert.Contains(t, out, "collection_mutable.EmptyArray[func() int]()")
	assert.Contains(t, out, "func(string) (int, error)")

	// The written function type types the lambdas it fills, for a generic
	// function, a constructor and a generic method alike.
	_, err = trans.Transpile(`package main

import "martianoff/gala/collection_immutable"

struct Box[T any](Value T)

func main() {
    val inc = Some[func(int) int]((x) => x + 1)
    val dbl = Box[func(int) int]((x) => x * 2)
    val xs = collection_immutable.ArrayOf(1, 2)
    val add = xs.FoldLeft[func(int) int]((n) => n, (acc, x) => (n) => acc(n) + x)
    Println(inc.GetOrElse((x) => x)(1), dbl.Value(2), add(10))
}`, "")
	require.NoError(t, err)

	cases := []struct {
		name, src string
		code      galaerr.ErrorCode
		line      int
		msg       string
	}{
		{"function type as a value", `package main

func main() {
    val f = func() int
    Println(f)
}`, "", 4, "function type func() int is not a value"},
		{"sealed variant inside a function type argument", `package main

import "martianoff/gala/collection_mutable"

sealed type Shape {
    case Circle(R int)
    case Square(S int)
}

func main() {
    val fs = collection_mutable.EmptyArray[func(Circle) int]()
    Println(fs.Length())
}`, galaerr.CodeSealedVariantAsType, 11, "Circle"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := trans.Transpile(tc.src, "")
			require.Error(t, err)
			var se *galaerr.SemanticError
			require.True(t, errors.As(err, &se), "%v", err)
			assert.Equal(t, tc.code, se.Code, "%v", err)
			assert.Equal(t, tc.line, se.Line, "%v", err)
			assert.Contains(t, se.Msg, tc.msg)
		})
	}
}
