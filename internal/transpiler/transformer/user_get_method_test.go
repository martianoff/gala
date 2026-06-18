package transformer_test

import (
	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/analyzer"
	"martianoff/gala/internal/transpiler/generator"
	"martianoff/gala/internal/transpiler/transformer"
	"testing"

	"github.com/stretchr/testify/assert"
)

// A user type may declare its own method named `Get` that returns Option[T].
// Such a method must not be confused with the builtin Option/collection `Get`
// handling: a chained higher-order Option method (FlatMap/Map) on the result of
// the user `Get` must keep the concrete element type. Previously the type
// resolver fell back to the receiver type itself for a non-generic named type,
// erasing the Option return type — the lambda parameter was emitted as `any` and
// the call was keyed off the receiver (`Node_FlatMap`), an undefined helper.
func TestUserGetMethodReturningOptionChain(t *testing.T) {
	p := transpiler.NewAntlrGalaParser()
	a := analyzer.NewGalaAnalyzer(p, getStdSearchPath())
	tr := transformer.NewGalaASTTransformer()
	g := generator.NewGoCodeGenerator()
	trans := transpiler.NewGalaToGoTranspiler(p, a, tr, g)

	input := `package main

sealed type Node {
    case NLeaf(Str string)
    case NList(Items []Node)
}

func (n Node) AsString() Option[string] = n match {
    case NLeaf(s) => Some(s)
    case _ => None[string]()
}

func (n Node) Get() Option[Node] = None[Node]()

func (n Node) FirstString() Option[string] =
    n.Get().FlatMap[string]((x) => x.AsString())`

	got, err := trans.Transpile(input, "")
	assert.NoError(t, err)

	// The lambda parameter must be typed as the concrete element type, not `any`.
	assert.Contains(t, got, "func(x Node)")
	assert.NotContains(t, got, "func(x any)")
	// The chained FlatMap must be keyed off Option, not the receiver type.
	assert.Contains(t, got, "std.Option_FlatMap")
	assert.NotContains(t, got, "Node_FlatMap")
}
