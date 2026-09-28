package build

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"martianoff/gala/internal/depman/fetch"
	"martianoff/gala/internal/depman/sum"
)

// TestBuild_LeavesModuleCacheUntouched builds a project against dependencies
// that sit in the module cache, and checks each dependency's directory is
// exactly as it was fetched.
//
// The build used to write into them: the analyzer transpiled each imported
// GALA package to `<pkg>.gen.go` beside its sources, and a dependency's own
// transpile kept its analysis cache in a `.gala/` directory there. Both were
// covered by the module's h2 hash, so `gala mod tidy` after a build recorded a
// different hash than a clean fetch — a gala.sum nobody else could reproduce.
//
// One dependency has GALA code only in a subpackage. Such a module used to be
// taken for a Go module, and built only because the analyzer's .gen.go files
// were in the cache for the Go toolchain to find.
func TestBuild_LeavesModuleCacheUntouched(t *testing.T) {
	const version = "v1.0.0"
	isolateUserState(t)
	t.Setenv("GALA_HOME", t.TempDir())
	t.Setenv("GALA_CACHE", "")

	deps := map[string]map[string]string{
		// A root package that imports its own subpackage.
		"example.com/cachedep": {
			"gala.mod":         "module example.com/cachedep\n\ngala 0.0.0\n",
			"greet.gala":       "package cachedep\n\nimport \"example.com/cachedep/shout\"\n\nfunc Greet(name string) string = shout.Loud(s\"hello $name\")\n",
			"shout/shout.gala": "package shout\n\nfunc Loud(s string) string = s + \"!\"\n",
		},
		// GALA code only in a subpackage.
		"example.com/subonly": {
			"gala.mod":         "module example.com/subonly\n\ngala 0.0.0\n",
			"tools/tools.gala": "package tools\n\nfunc Twice(n int) int = n * 2\n",
		},
	}
	cache := fetch.NewCache(fetch.DefaultConfig())
	fetched := make(map[string]string)
	for path, files := range deps {
		src := t.TempDir()
		for name, content := range files {
			p := filepath.Join(src, filepath.FromSlash(name))
			require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
			require.NoError(t, os.WriteFile(p, []byte(content), 0o644))
		}
		require.NoError(t, cache.Store(path, version, src))
		hash, err := sum.HashDir(cache.Config().ModulePath(path, version))
		require.NoError(t, err)
		fetched[path] = hash
	}

	projectDir := t.TempDir()
	for name, content := range map[string]string{
		"gala.mod": "module example.com/app\n\ngala 0.0.0\n\nrequire (\n" +
			"\texample.com/cachedep " + version + "\n\texample.com/subonly " + version + "\n)\n",
		"main.gala": "package main\n\nimport (\n    \"example.com/cachedep\"\n    \"example.com/subonly/tools\"\n)\n\n" +
			"func main() {\n    Println(cachedep.Greet(\"gala\"), tools.Twice(2))\n}\n",
	} {
		require.NoError(t, os.WriteFile(filepath.Join(projectDir, name), []byte(content), 0o644))
	}
	chdirForTest(t, projectDir)

	b, err := NewBuilder(projectDir, "test", false)
	require.NoError(t, err)
	require.NoError(t, b.workspace.Ensure())
	require.NoError(t, b.ensureStdlib())
	require.NoError(t, b.transpileDeps())
	require.NoError(t, b.transpile())

	// The build did use both dependencies: their Go is in the workspace.
	for _, gen := range []string{"example.com/cachedep@" + version + "/greet.gen.go", "example.com/subonly@" + version + "/tools/tools.gen.go"} {
		_, err := os.Stat(filepath.Join(b.workspace.DepsDir, filepath.FromSlash(gen)))
		assert.NoError(t, err, "each dependency must be transpiled into the build workspace")
	}

	for path, want := range fetched {
		dir := cache.Config().ModulePath(path, version)
		files, err := sum.ModuleFiles(dir)
		require.NoError(t, err)
		assert.ElementsMatch(t, keys(deps[path]), files, "the build wrote into the module cache of %s", path)
		got, err := sum.HashDir(dir)
		require.NoError(t, err)
		assert.Equal(t, want, got, "the h2 hash of %s changed after a build", path)
	}
}

func keys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
