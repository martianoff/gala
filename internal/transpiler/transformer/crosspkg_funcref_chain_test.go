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

func crossPkgFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()

	write := func(rel, content string) {
		t.Helper()
		full := filepath.Join(root, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(full), 0755))
		require.NoError(t, os.WriteFile(full, []byte(content), 0644))
	}

	write("gala.mod", "module example.com/crosspkg\n\ngala dev\n")
	write("helper/helper.gala", `package helper

func Shout(s string) string = s"$s!"
`)
	return root
}

func transpileCrossPkg(t *testing.T, root, src string) (string, error) {
	t.Helper()
	p := transpiler.NewAntlrGalaParser()
	searchPaths := append([]string{root}, getStdSearchPath()...)
	a := analyzer.NewGalaAnalyzer(p, searchPaths, root)
	tr := transformer.NewGalaASTTransformer()
	g := generator.NewGoCodeGenerator()
	return transpiler.NewGalaToGoTranspiler(p, a, tr, g).
		Transpile(src, filepath.Join(root, "main.gala"))
}

func TestCrossPackageFuncRef_ChainedIntoGenericMethod(t *testing.T) {
	root := crossPkgFixture(t)

	const src = `package main

import (
    . "martianoff/gala/collection_immutable"
    "example.com/crosspkg/helper"
)

func main() {
    ArrayOf("a").Map(helper.Shout).ForEach((g) => Println(g))
}`

	out, err := transpileCrossPkg(t, root, src)
	require.NoError(t, err, "transpiling a cross-package bare function reference must not error")

	assert.NotContains(t, out, "__mtp0",
		"a cross-package bare function reference chained into a generic method leaked an\n"+
			"uninstantiated method type parameter (__mtp0) into the generated Go;\ngenerated:\n%s", out)
}

func TestSamePackageFuncRef_ChainedIntoGenericMethod(t *testing.T) {
	root := crossPkgFixture(t)

	const src = `package main

import . "martianoff/gala/collection_immutable"

func shout(s string) string = s"$s!"

func main() {
    ArrayOf("a").Map(shout).ForEach((g) => Println(g))
}`

	out, err := transpileCrossPkg(t, root, src)
	require.NoError(t, err)
	assert.NotContains(t, out, "__mtp0",
		"same-package bare function reference must not leak a method type parameter:\n%s", out)
}

func TestCrossPackageFuncRef_NonGenericConsumer(t *testing.T) {
	root := crossPkgFixture(t)

	const src = `package main

import (
    . "martianoff/gala/collection_immutable"
    "example.com/crosspkg/helper"
)

func main() {
    Println(ArrayOf("a").Map(helper.Shout).String())
}`

	out, err := transpileCrossPkg(t, root, src)
	require.NoError(t, err)
	assert.NotContains(t, out, "__mtp0",
		"cross-package bare ref with a non-generic consumer must not leak a type parameter:\n%s", out)
}

func TestCrossPackageExplicitLambda_ChainedIntoGenericMethod(t *testing.T) {
	root := crossPkgFixture(t)

	const src = `package main

import (
    . "martianoff/gala/collection_immutable"
    "example.com/crosspkg/helper"
)

func main() {
    ArrayOf("a").Map((s) => helper.Shout(s)).ForEach((g) => Println(g))
}`

	out, err := transpileCrossPkg(t, root, src)
	require.NoError(t, err)
	assert.NotContains(t, out, "__mtp0",
		"explicit lambda must not leak a method type parameter:\n%s", out)
}
