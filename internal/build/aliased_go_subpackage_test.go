package build

import (
	"path/filepath"
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

// TestBuild_SameNameGoSubpackagesKeepTheirOwnSignatures imports hand-written
// Go packages of the module that share a package name: under aliases in one
// file, from sibling files of one package, and as the name of the package
// being compiled. Go type info records each under its import path, so every
// call gets its own package's signature, in any import order.
func TestBuild_SameNameGoSubpackagesKeepTheirOwnSignatures(t *testing.T) {
	const a = "package util\n\nfunc Get() (int, error) { return 1, nil }\n"
	const b = "package util\n\nfunc Get() string { return \"b\" }\n"

	t.Run("aliases in one file, either order", func(t *testing.T) {
		for _, imports := range []string{
			"ua \"example.com/samename/a/util\"\n    ub \"example.com/samename/b/util\"",
			"ub \"example.com/samename/b/util\"\n    ua \"example.com/samename/a/util\"",
		} {
			projectDir := newForeignGenGoProject(t, "example.com/samename", map[string]string{
				"a/util/util.go": a,
				"b/util/util.go": b,
				"main.gala": `package main

import (
    ` + imports + `
)

func main() {
    Println(ua.Get().GetOrElse(0), ub.Get())
}
`,
			})
			mainGen := transpileMainGen(t, projectDir)
			assert.Contains(t, mainGen, "std.GoTry(ua.Get())", "ua.Get returns (int, error):\n%s", mainGen)
			assert.NotContains(t, mainGen, "std.GoTry(ub.Get())", "ub.Get returns one value:\n%s", mainGen)
			assert.Equal(t, "1|b", buildAndRun(t, projectDir))
		}
	})

	t.Run("sibling files", func(t *testing.T) {
		projectDir := newForeignGenGoProject(t, "example.com/samename", map[string]string{
			"a/util/util.go": a,
			"b/util/util.go": b,
			"main.gala": `package main

import ua "example.com/samename/a/util"

func main() {
    Println(ua.Get().GetOrElse(0), fromB())
}
`,
			"other.gala": `package main

import "example.com/samename/b/util"

func fromB() string = util.Get()
`,
		})
		assert.Equal(t, "1|b", buildAndRun(t, projectDir))
	})

	t.Run("the compiled package's own name", func(t *testing.T) {
		projectDir := newForeignGenGoProject(t, "example.com/samename", map[string]string{
			"lib/util.go":    "package util\n\nfunc Local() string { return \"local\" }\n",
			"lib/lib.gala":   "package util\n\nimport o \"example.com/samename/a/util\"\n\nfunc Both() string = s\"${o.Get().GetOrElse(0)} ${Local()}\"\n",
			"a/util/util.go": a,
			"main.gala": `package main

import u "example.com/samename/lib"

func main() {
    Println(u.Both())
}
`,
		})
		assert.Equal(t, "1|local", buildAndRun(t, projectDir))
	})
}
