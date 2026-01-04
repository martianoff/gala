package transformer_test

import (
	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/generator"
	"martianoff/gala/internal/transpiler/transformer"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGenericMethodConflict(t *testing.T) {
	p := transpiler.NewAntlrGalaParser()
	tr := transformer.NewGalaASTTransformer()
	g := generator.NewGoCodeGenerator()
	trans := transpiler.NewGalaToGoTranspiler(p, tr, g)

	input := `package main

type Box[T any] struct { Value T }
func (b Box[T]) Transform[U any](f func(T) U) Box[U] = Box(f(b.Value))

type Other[T any] struct { Value T }
func (o Other[T]) Transform[U any](f func(T) U) Other[U] = Other(f(o.Value))
`
	got, err := trans.Transpile(input)
	assert.NoError(t, err)

	// Check that we have both Box_Transform and Other_Transform
	assert.True(t, strings.Contains(got, "func Box_Transform"), "Should contain Box_Transform")
	assert.True(t, strings.Contains(got, "func Other_Transform"), "Should contain Other_Transform")
	assert.False(t, strings.Contains(got, "func Transform"), "Should not contain standalone Transform")
}
