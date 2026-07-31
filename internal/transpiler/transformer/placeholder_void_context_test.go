package transformer_test

import (
	"strings"
	"testing"

	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/analyzer"
	"martianoff/gala/internal/transpiler/generator"
	"martianoff/gala/internal/transpiler/transformer"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func transpileForVoidPlaceholder(t *testing.T, src string) (string, error) {
	t.Helper()
	p := transpiler.NewAntlrGalaParser()
	a := analyzer.NewGalaAnalyzer(p, getStdSearchPath())
	tr := transformer.NewGalaASTTransformer()
	g := generator.NewGoCodeGenerator()
	return transpiler.NewGalaToGoTranspiler(p, a, tr, g).Transpile(src, "main.gala")
}

func TestPlaceholderInVoidContext_UserDefinedFunction(t *testing.T) {
	const src = `package main

import . "martianoff/gala/collection_immutable"

func shout(s string) {
    Println(s)
}

func main() {
    ArrayOf("a", "b").ForEach(shout(_))
}`

	out, err := transpileForVoidPlaceholder(t, src)
	require.NoError(t, err, "transpiling a placeholder in a void context must not error")

	assert.NotContains(t, out, "void",
		"placeholder lambda in a void context emitted the non-existent Go type `void`;\n"+
			"expected a result-less func literal such as `func(__p0 string) {`\ngenerated:\n%s", out)
}

func TestPlaceholderInVoidContext_PrintlnSugar(t *testing.T) {
	const src = `package main

import . "martianoff/gala/collection_immutable"

func main() {
    ArrayOf("a", "b").ForEach(Println(_))
}`

	out, err := transpileForVoidPlaceholder(t, src)
	require.NoError(t, err, "transpiling Println(_) in a void context must not error")

	assert.NotContains(t, out, "void",
		"ForEach(Println(_)) emitted the non-existent Go type `void`;\ngenerated:\n%s", out)
}

func TestVoidContext_ExplicitLambdaIsCorrect(t *testing.T) {
	const src = `package main

import . "martianoff/gala/collection_immutable"

func shout(s string) {
    Println(s)
}

func main() {
    ArrayOf("a", "b").ForEach((s) => shout(s))
}`

	out, err := transpileForVoidPlaceholder(t, src)
	require.NoError(t, err)
	assert.NotContains(t, out, "void",
		"explicit lambda in a void context must not emit `void`:\n%s", out)
}

func TestPlaceholderInValueContext_IsCorrect(t *testing.T) {
	const src = `package main

import . "martianoff/gala/collection_immutable"

func main() {
    Println(ArrayOf(1, 2).Map(_ + 1).Size())
}`

	out, err := transpileForVoidPlaceholder(t, src)
	require.NoError(t, err)
	assert.NotContains(t, out, "void",
		"placeholder in a value context must not emit `void`:\n%s", out)
	assert.True(t, strings.Contains(out, "func(__p0 int) int") || strings.Contains(out, "func(__p0 int) "),
		"expected a typed result on the value-context placeholder lambda:\n%s", out)
}
