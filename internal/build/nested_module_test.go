package build

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// nestedModules are modules nested in a project: a Go module (its own go.mod,
// built by another tool) and a GALA module (its own gala.mod). Neither belongs
// to the enclosing project, and neither builds as part of it: the Go module
// names a package only its own build generates, the GALA module an undefined
// symbol. The go tool leaves a nested module out of ./..., and so must gala.
var nestedModules = []struct {
	name, dir string
	files     map[string]string
}{
	{
		name: "go module",
		dir:  "differential",
		files: map[string]string{
			"differential/go.mod": "module example.com/differential\n\ngo 1.22\n",
			"differential/diff.go": "package differential\n\n" +
				"import \"example.com/differential/generated\"\n\n" +
				"var Answer = generated.Answer\n",
		},
	},
	{
		name: "gala module",
		dir:  "tool",
		files: map[string]string{
			"tool/gala.mod":  "module example.com/tool\n\ngala 0.0.0\n",
			"tool/tool.gala": "package tool\n\nfunc Broken() int = undefinedSymbol\n",
			"tool/tool_test.gala": "package tool\n\nimport . \"martianoff/gala/test\"\n\n" +
				"func TestBroken(t T) T = Eq(t, Broken(), 1)\n",
		},
	},
}

func withFiles(base, extra map[string]string) map[string]string {
	for name, content := range extra {
		base[name] = content
	}
	return base
}

// assertLeftOut checks a nested module never reached gen/.
func assertLeftOut(t *testing.T, genDir, dir string) {
	t.Helper()
	_, err := os.Stat(filepath.Join(genDir, dir))
	assert.True(t, os.IsNotExist(err), "nested module %s/ must not be copied into gen/", dir)
}

func TestBuild_SkipsNestedModules(t *testing.T) {
	for _, nested := range nestedModules {
		t.Run(nested.name, func(t *testing.T) {
			projectDir := newForeignGenGoProject(t, "example.com/nestedmod", withFiles(map[string]string{
				"main.gala": "package main\n\nfunc main() {\n    Println(\"ok\")\n}\n",
			}, nested.files))
			assert.Equal(t, "ok", buildAndRun(t, projectDir))

			b, err := NewBuilder(projectDir, "test", false)
			require.NoError(t, err)
			assertLeftOut(t, b.workspace.GenDir, nested.dir)
		})
	}
}

func TestTest_SkipsNestedModules(t *testing.T) {
	for _, nested := range nestedModules {
		t.Run(nested.name, func(t *testing.T) {
			projectDir := newForeignGenGoProject(t, "example.com/nestedmodtest", withFiles(map[string]string{
				"lib.gala": "package lib\n\nfunc Hello() string = \"hi\"\n",
				"lib_test.gala": "package lib\n\nimport . \"martianoff/gala/test\"\n\n" +
					"func TestHello(t T) T = Eq(t, Hello(), \"hi\")\n",
			}, nested.files))
			chdirForTest(t, projectDir)

			b, err := NewBuilderForMode(projectDir, "test", false, ModeTest)
			require.NoError(t, err)
			testErr := b.Test(false)
			if testErr != nil && isToolchainEnvError(testErr.Error()) {
				t.Skipf("Go toolchain unavailable/mismatched in this environment: %v", testErr)
			}
			require.NoError(t, testErr)
			assertLeftOut(t, b.workspace.GenDir, nested.dir)
		})
	}
}

// The walks that discover a project's GALA sources stop at a nested module.
func TestFindGalaFiles_SkipNestedModules(t *testing.T) {
	projectDir := t.TempDir()
	files := map[string]string{
		"lib.gala":      "package lib\n",
		"lib_test.gala": "package lib\n",
		"sub/sub.gala":  "package sub\n",
	}
	for _, nested := range nestedModules {
		withFiles(files, nested.files)
	}
	for name, content := range files {
		writeFixtureFile(t, filepath.Join(projectDir, filepath.FromSlash(name)), content)
	}

	src, err := findGalaFilesRecursive(projectDir)
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{
		filepath.Join(projectDir, "lib.gala"),
		filepath.Join(projectDir, "sub", "sub.gala"),
	}, src)

	tests, err := findGalaTestFilesRecursive(projectDir)
	require.NoError(t, err)
	assert.Equal(t, []string{filepath.Join(projectDir, "lib_test.gala")}, tests)
}
