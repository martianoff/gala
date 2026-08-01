package transformer_test

import (
	"os"
	"path/filepath"
	"testing"

	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/analyzer"
	"martianoff/gala/internal/transpiler/generator"
	"martianoff/gala/internal/transpiler/transformer"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// genericFuncRefFixture builds a module whose helper package exports a GENERIC
// function, so tests can pass `helper.Identity` as a bare (unapplied) function
// reference to a generic collection method.
func genericFuncRefFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()

	write := func(rel, content string) {
		t.Helper()
		full := filepath.Join(root, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(full), 0755))
		require.NoError(t, os.WriteFile(full, []byte(content), 0644))
	}

	write("gala.mod", "module example.com/genericref\n\ngala dev\n")
	write("helper/helper.gala", `package helper

func Identity[T any](x T) T = x

func Shout(s string) string = s"$s!"
`)
	return root
}

func transpileGenericFuncRef(t *testing.T, root, src string) (string, error) {
	t.Helper()
	p := transpiler.NewAntlrGalaParser()
	searchPaths := append([]string{root}, getStdSearchPath()...)
	a := analyzer.NewGalaAnalyzer(p, searchPaths, root)
	tr := transformer.NewGalaASTTransformer()
	g := generator.NewGoCodeGenerator()
	return transpiler.NewGalaToGoTranspiler(p, a, tr, g).
		Transpile(src, filepath.Join(root, "main.gala"))
}

// Case 1: same-package GENERIC bare function reference chained into a consumer
// that must write the element type down (ForEach's lambda parameter).
func TestSamePackageGenericFuncRef_ChainedIntoTypeWritingConsumer(t *testing.T) {
	root := genericFuncRefFixture(t)

	const src = `package main

import . "martianoff/gala/collection_immutable"

func ident[T any](x T) T = x

func main() {
    ArrayOf("a").Map(ident).ForEach((g) => Println(g))
}`

	out, err := transpileGenericFuncRef(t, root, src)
	require.NoError(t, err, "transpiling a same-package generic bare function reference must not error")

	assert.NotContains(t, out, "__mtp0",
		"a same-package GENERIC bare function reference chained into a type-writing\n"+
			"consumer leaked an uninstantiated method type parameter (__mtp0);\ngenerated:\n%s", out)
}

// Case 2: cross-package GENERIC bare function reference chained into a
// type-writing consumer.
func TestCrossPackageGenericFuncRef_ChainedIntoTypeWritingConsumer(t *testing.T) {
	root := genericFuncRefFixture(t)

	const src = `package main

import (
    . "martianoff/gala/collection_immutable"
    "example.com/genericref/helper"
)

func main() {
    ArrayOf("a").Map(helper.Identity).ForEach((g) => Println(g))
}`

	out, err := transpileGenericFuncRef(t, root, src)
	require.NoError(t, err, "transpiling a cross-package generic bare function reference must not error")

	assert.NotContains(t, out, "__mtp0",
		"a cross-package GENERIC bare function reference chained into a type-writing\n"+
			"consumer leaked an uninstantiated method type parameter (__mtp0);\ngenerated:\n%s", out)
}

// Case 3 (control): cross-package generic bare ref consumed by .String(), which
// never names the element type.
func TestCrossPackageGenericFuncRef_NonTypeWritingConsumer(t *testing.T) {
	root := genericFuncRefFixture(t)

	const src = `package main

import (
    . "martianoff/gala/collection_immutable"
    "example.com/genericref/helper"
)

func main() {
    Println(ArrayOf("a").Map(helper.Identity).String())
}`

	out, err := transpileGenericFuncRef(t, root, src)
	require.NoError(t, err)
	assert.NotContains(t, out, "__mtp0",
		"cross-package generic bare ref with a non-type-writing consumer must not leak a type parameter:\n%s", out)
}

// Case 4 (control): cross-package NON-generic bare ref into a type-writing
// consumer — this is what was fixed earlier and must stay working.
func TestCrossPackageNonGenericFuncRef_ChainedIntoTypeWritingConsumer(t *testing.T) {
	root := genericFuncRefFixture(t)

	const src = `package main

import (
    . "martianoff/gala/collection_immutable"
    "example.com/genericref/helper"
)

func main() {
    ArrayOf("a").Map(helper.Shout).ForEach((g) => Println(g))
}`

	out, err := transpileGenericFuncRef(t, root, src)
	require.NoError(t, err)
	assert.NotContains(t, out, "__mtp0",
		"cross-package NON-generic bare ref must not leak a type parameter:\n%s", out)
}

// Case 5 (control): explicit lambda wrapping the generic function.
func TestCrossPackageGenericExplicitLambda_ChainedIntoTypeWritingConsumer(t *testing.T) {
	root := genericFuncRefFixture(t)

	const src = `package main

import (
    . "martianoff/gala/collection_immutable"
    "example.com/genericref/helper"
)

func main() {
    ArrayOf("a").Map((s) => helper.Identity(s)).ForEach((g) => Println(g))
}`

	out, err := transpileGenericFuncRef(t, root, src)
	require.NoError(t, err)
	assert.NotContains(t, out, "__mtp0",
		"explicit lambda over a generic function must not leak a type parameter:\n%s", out)
}
