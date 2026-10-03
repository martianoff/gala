package build

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"martianoff/gala/internal/depman/fetch"
	"martianoff/gala/internal/depman/mod"
)

// TestBuild_DependencyGetsItsOwnGoTypes builds a project whose GALA library
// dependency calls into a third-party Go module that only the library's
// gala.mod requires.
//
// The dependency used to be transpiled with no Go module sources wired into
// its analyzer: only the root's gala.mod requirements were. The Go call's
// result type was unknown, so the library's `Try(gotypes.Open(n))` was emitted
// as an uninstantiated `std.Try{}.Apply(...)`, which `go build` rejects with
// "cannot use generic type std.Try[T any] without instantiation" — although
// the same library built fine as a root project.
//
// Both `gala build` and `gala test` transpile dependencies the same way, so
// the test runs the dependency transpile under each workspace mode, followed
// by the project's own transpile, which reads the library's sources too.
func TestBuild_DependencyGetsItsOwnGoTypes(t *testing.T) {
	for _, mode := range []Mode{ModeBuild, ModeTest} {
		t.Run(string(mode), func(t *testing.T) {
			const version = "v1.0.0"
			isolateUserState(t)
			t.Setenv("GALA_HOME", t.TempDir())
			t.Setenv("GALA_CACHE", "")

			// A Go module, cached source-only as `gala mod add --go` leaves
			// it, so the test needs no network.
			modules := map[string]map[string]string{
				"example.com/gotypes": {
					"go.mod": "module example.com/gotypes\n\ngo 1.21\n",
					"gotypes.go": "package gotypes\n\ntype State struct{ N int }\n\n" +
						"func Open(n int) (*State, error) { return &State{N: n}, nil }\n",
				},
				"example.com/galalib": {
					"gala.mod": "module example.com/galalib\n\ngala 0.0.0\n\nrequire example.com/gotypes " + version + " // go\n",
					"galalib.gala": "package galalib\n\nimport \"example.com/gotypes\"\n\n" +
						"func Opens(n int) bool = Try(gotypes.Open(n)).IsSuccess()\n\n" +
						"func Touch(n int) {\n    val f = () => {\n        Try(gotypes.Open(n))\n        Println(\"opened\")\n    }\n    f()\n}\n",
				},
			}
			cache := fetch.NewCache(fetch.DefaultConfig())
			for path, files := range modules {
				src := t.TempDir()
				for name, content := range files {
					require.NoError(t, os.WriteFile(filepath.Join(src, name), []byte(content), 0o644))
				}
				require.NoError(t, cache.Store(path, version, src))
			}

			// The root requires only the library, not the Go module.
			projectDir := t.TempDir()
			for name, content := range map[string]string{
				"gala.mod": "module example.com/app\n\ngala 0.0.0\n\nrequire example.com/galalib " + version + "\n",
				"main.gala": "package main\n\nimport \"example.com/galalib\"\n\n" +
					"func main() {\n    galalib.Touch(1)\n    Println(galalib.Opens(1))\n}\n",
			} {
				require.NoError(t, os.WriteFile(filepath.Join(projectDir, name), []byte(content), 0o644))
			}
			chdirForTest(t, projectDir)

			b, err := NewBuilderForMode(projectDir, "test", false, mode)
			require.NoError(t, err)
			require.NoError(t, b.workspace.Ensure())
			require.NoError(t, b.ensureStdlib())
			require.NoError(t, b.transpileDeps())
			require.NoError(t, b.transpile())

			gen, err := os.ReadFile(filepath.Join(b.workspace.DepModuleDir("example.com/galalib", version), "galalib.gen.go"))
			require.NoError(t, err)
			code := string(gen)
			assert.NotContains(t, code, "std.Try{}", "Try must be instantiated with the Go call's result type")
			assert.Equal(t, 2, strings.Count(code, "std.Try[*gotypes.State]{}"),
				"both Try calls must be instantiated with the Go call's result type:\n%s", code)
		})
	}
}

// TestResolveGoModuleSrcDirs checks which cached modules count as resolved and
// that a module that stays missing marks the result incomplete, which keeps
// the transpile from being recorded as up to date.
func TestResolveGoModuleSrcDirs(t *testing.T) {
	home := t.TempDir()
	config := &Config{GalaPkgDir: filepath.Join(home, "pkg", "mod"), GoPkgDir: filepath.Join(home, "go", "pkg", "mod")}
	// A module with no package at its root, in the Go module cache (with the
	// cache's case escaping).
	noRoot := filepath.Join(config.GoPkgDir, "example.com", "!no!root@v1.0.0", "sub")
	require.NoError(t, os.MkdirAll(noRoot, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(noRoot, "sub.go"), []byte("package sub\n"), 0o644))
	present := mod.Require{Path: "example.com/NoRoot", Version: "v1.0.0", Go: true}

	dirs, complete := resolveGoModuleSrcDirs(config, []mod.Require{present}, false)
	assert.True(t, complete)
	assert.Equal(t, filepath.Dir(noRoot), dirs[present.Path])

	// No `go` on PATH: the download fails, and the module stays unresolved.
	t.Setenv("PATH", t.TempDir())
	missing := mod.Require{Path: "example.com/missing", Version: "v1.0.0", Go: true}
	dirs, complete = resolveGoModuleSrcDirs(config, []mod.Require{present, missing}, false)
	assert.False(t, complete)
	assert.Equal(t, map[string]string{present.Path: filepath.Dir(noRoot)}, dirs)
}

// TestGoRequiresWithDeps pins how the Go requirements a transpile reads types
// from are collected: the module's own first, then those of every GALA module
// it requires, transitively, with the highest version kept for a module.
func TestGoRequiresWithDeps(t *testing.T) {
	root := t.TempDir()
	write := func(dir, content string) {
		require.NoError(t, os.MkdirAll(filepath.Join(root, dir), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(root, dir, "gala.mod"), []byte(content), 0o644))
	}
	write("a", "module example.com/a\n\ngala 0.0.0\n\nrequire (\n\texample.com/b v1.0.0\n\tgolang.org/x/term v0.20.0 // go\n)\n")
	write("b", "module example.com/b\n\ngala 0.0.0\n\nrequire (\n\texample.com/a v1.0.0\n\tgolang.org/x/term v0.25.0 // go\n\tgithub.com/google/uuid v1.6.0 // go\n)\n")

	f, err := mod.Parse("module example.com/app\n\ngala 0.0.0\n\nrequire (\n\texample.com/a v1.0.0\n\tgithub.com/BurntSushi/toml v1.4.0 // go\n)\n")
	require.NoError(t, err)
	depDir := func(req mod.Require) string {
		return filepath.Join(root, strings.TrimPrefix(req.Path, "example.com/"))
	}

	var got []string
	for _, req := range goRequiresWithDeps(f, depDir) {
		got = append(got, req.Path+"@"+req.Version)
	}
	assert.Equal(t, []string{
		"github.com/BurntSushi/toml@v1.4.0",
		"golang.org/x/term@v0.25.0",
		"github.com/google/uuid@v1.6.0",
	}, got)
	assert.Empty(t, goRequiresWithDeps(nil, depDir))
}
