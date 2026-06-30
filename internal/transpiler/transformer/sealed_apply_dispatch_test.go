package transformer_test

import (
	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/analyzer"
	"martianoff/gala/internal/transpiler/generator"
	"martianoff/gala/internal/transpiler/transformer"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A sealed type's parent layout is synthetic (merged variant fields plus the
// `_variant` discriminator). A call on the type name with an arg count that
// happens to equal that synthetic field count must still dispatch to the
// companion Apply method, never miscompile into a positional struct literal.
func TestSealedTypeCompanionApplyDispatch(t *testing.T) {
	p := transpiler.NewAntlrGalaParser()
	a := analyzer.NewGalaAnalyzer(p, getStdSearchPath())
	tr := transformer.NewGalaASTTransformer()
	g := generator.NewGoCodeGenerator()
	trans := transpiler.NewGalaToGoTranspiler(p, a, tr, g)

	// `Box` is sealed with a single case carrying one field, so the synthetic
	// parent struct has two fields: `value` + `_variant`. A two-argument call
	// `Box[int](() => 7, "tag")` has arg count 2 == field count 2, which used to
	// collide with positional struct construction.
	input := `package main

import . "martianoff/gala/std"

sealed type Box[T any] {
    case box(value T)
}

func (b Box[T]) Apply(body func() T, tag string) Box[T] = box(value = body())
func (b Box[T]) Value() T = b.value

func main() {
    val b = Box[int](() => 7, "tag")
    Println(b.Value())
}`

	out, err := trans.Transpile(input, "")
	require.NoError(t, err)

	idx := strings.Index(out, "func main()")
	require.GreaterOrEqual(t, idx, 0)
	mainBody := out[idx:]

	// Must dispatch to Apply with both arguments preserved.
	assert.Contains(t, mainBody, "Box[int]{}.Apply(", "two-arg call should dispatch to companion Apply")
	assert.Contains(t, mainBody, `"tag"`, "the second argument must be preserved")
	// Must NOT misroute the body thunk into the `value` field via a struct literal.
	assert.NotContains(t, mainBody, "Box[int]{value:", "must not emit a positional struct literal")
}
