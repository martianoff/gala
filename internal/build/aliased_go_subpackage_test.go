package build

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// transpileMainGen transpiles projectDir and returns the generated main.gen.go.
func transpileMainGen(t *testing.T, projectDir string) string {
	t.Helper()
	chdirForTest(t, projectDir)
	b, err := NewBuilder(projectDir, "test", false)
	require.NoError(t, err)
	require.NoError(t, b.workspace.Ensure())
	require.NoError(t, b.ensureStdlib())
	require.NoError(t, b.transpileDeps())
	require.NoError(t, b.transpile())
	return readFileString(t, filepath.Join(b.workspace.GenDir, "main.gen.go"))
}

// TestBuild_AliasedGoSubpackageKeepsGoResults builds a project whose GALA
// code imports a hand-written Go package of the same module under an alias
// that differs from the package's name. A call through the alias returning
// `(T, error)` is a Try value, as it is through the package's own name: the
// generated Go wraps it in std.GoTry, and the program builds and runs.
func TestBuild_AliasedGoSubpackageKeepsGoResults(t *testing.T) {
	projectDir := newForeignGenGoProject(t, "example.com/aliasgo", map[string]string{
		"store/store.go": "package store\n\nfunc Load() (string, error) { return \"ok\", nil }\n",
		"main.gala": `package main

import st "example.com/aliasgo/store"

func load() string = st.Load().OnFailure((err error) => {}).GetOrElse("failed")

func main() {
    Println(load())
}
`,
	})

	mainGen := transpileMainGen(t, projectDir)
	assert.Contains(t, mainGen, "std.GoTry(st.Load())",
		"a (T, error) call through the alias must become a Try value:\n%s", mainGen)
	assert.Equal(t, "ok", buildAndRun(t, projectDir))
}

// TestBuild_AliasedSameNameGoSubpackagesDoNotBorrowSignatures imports two
// hand-written Go packages of the module that share a package name, each under
// its own alias. Go type info is keyed by package name, so neither alias may
// take the other package's signature: the generated call must not depend on
// the order of the imports.
func TestBuild_AliasedSameNameGoSubpackagesDoNotBorrowSignatures(t *testing.T) {
	gen := func(imports string) string {
		projectDir := newForeignGenGoProject(t, "example.com/samename", map[string]string{
			"a/util/util.go": "package util\n\nfunc Get() (int, error) { return 1, nil }\n",
			"b/util/util.go": "package util\n\nfunc Get() string { return \"b\" }\n",
			"main.gala": `package main

import (
` + imports + `
)

func main() {
    val n, err = ua.Get()
    Println(n, err, ub.Get())
}
`,
		})
		return transpileMainGen(t, projectDir)
	}
	const a, b = `    ua "example.com/samename/a/util"`, `    ub "example.com/samename/b/util"`
	first, second := callLines(gen(a+"\n"+b)), callLines(gen(b+"\n"+a))
	for _, out := range []string{first, second} {
		assert.NotContains(t, out, "std.GoTry(ub.Get())", "ub.Get returns one value:\n%s", out)
	}
	assert.Equal(t, first, second, "the generated calls must not depend on the import order")
}

// callLines returns the lines of src that call Get.
func callLines(src string) string {
	var lines []string
	for _, line := range strings.Split(src, "\n") {
		if strings.Contains(line, "Get()") {
			lines = append(lines, strings.TrimSpace(line))
		}
	}
	return strings.Join(lines, "\n")
}
