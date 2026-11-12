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

	_, err = trans.Transpile(`package main

func main() {
    val f = func() int
    Println(f)
}`, "")
	require.Error(t, err)
	var se *galaerr.SemanticError
	require.True(t, errors.As(err, &se), "%v", err)
	assert.Equal(t, 4, se.Line)
	assert.Contains(t, se.Msg, "function type func() int is not a value")
}
