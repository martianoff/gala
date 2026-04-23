package build

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"martianoff/gala/internal/depman/mod"
)

// TestTest_NoTestFilesExitsZero verifies that running `gala test` on a project
// with no _test.gala files exits successfully (with a "no test files" notice)
// rather than failing. This matches Go's `go test ./...` behavior.
func TestTest_NoTestFilesExitsZero(t *testing.T) {
	projectDir := t.TempDir()

	// Minimal gala.mod
	require.NoError(t, os.WriteFile(filepath.Join(projectDir, "gala.mod"),
		[]byte("module example.com/demo\n\ngala 0.0.0\n"), 0644))

	// A single main.gala file with no tests
	require.NoError(t, os.WriteFile(filepath.Join(projectDir, "main.gala"),
		[]byte("package main\n\nfunc main() { Println(\"hi\") }\n"), 0644))

	origHome, hadHome := os.LookupEnv("HOME")
	origUserProfile, hadProfile := os.LookupEnv("USERPROFILE")
	isolated := t.TempDir()
	os.Setenv("HOME", isolated)
	os.Setenv("USERPROFILE", isolated)
	t.Cleanup(func() {
		if hadHome {
			os.Setenv("HOME", origHome)
		} else {
			os.Unsetenv("HOME")
		}
		if hadProfile {
			os.Setenv("USERPROFILE", origUserProfile)
		} else {
			os.Unsetenv("USERPROFILE")
		}
	})

	b, err := NewBuilder(projectDir, "test", false)
	require.NoError(t, err)

	// Capture stdout so the test can assert on the friendly notice.
	origStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	testErr := b.Test(false)

	// Restore stdout and read what was written.
	_ = w.Close()
	os.Stdout = origStdout
	var buf bytes.Buffer
	_, _ = io.Copy(&buf, r)

	require.NoError(t, testErr, "Test() must not return an error when there are no _test.gala files")
	require.True(t, strings.Contains(buf.String(), "[no test files]"),
		"expected '[no test files]' notice in stdout, got: %q", buf.String())
}

// TestRenameUserMainInDir_RenamesOnlyListedFiles verifies that the test-binary
// build path renames the user's `func main()` out of the way so the
// synthesized test runner's main() can compile in the same package.
func TestRenameUserMainInDir_RenamesOnlyListedFiles(t *testing.T) {
	dir := t.TempDir()

	userMain := `package main

func main() {
	Println("hi")
}
`
	userLib := `package main

func helper() int { return 1 }
`
	testFile := `package main

func TestX() {}
`

	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.gen.go"), []byte(userMain), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "lib.gen.go"), []byte(userLib), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main_test.gen.go"), []byte(testFile), 0644))

	sourceNames := map[string]bool{
		"main.gen.go": true,
		"lib.gen.go":  true,
	}

	require.NoError(t, renameUserMainInDir(dir, sourceNames, false))

	mainAfter, err := os.ReadFile(filepath.Join(dir, "main.gen.go"))
	require.NoError(t, err)
	require.False(t, strings.Contains(string(mainAfter), "func main()"),
		"user func main() should have been renamed, got:\n%s", mainAfter)
	require.True(t, strings.Contains(string(mainAfter), "func _galaUserMain()"),
		"renamed function should be named _galaUserMain, got:\n%s", mainAfter)

	libAfter, err := os.ReadFile(filepath.Join(dir, "lib.gen.go"))
	require.NoError(t, err)
	require.Equal(t, userLib, string(libAfter), "lib.gen.go should be unchanged")

	testAfter, err := os.ReadFile(filepath.Join(dir, "main_test.gen.go"))
	require.NoError(t, err)
	require.Equal(t, testFile, string(testAfter), "test file should be unchanged")
}

// TestTestGenFileName verifies that the helper matches the naming used by
// transpileFilesToDir (subdir separators folded to '_', .gala suffix replaced
// by .gen.go).
func TestTestGenFileName(t *testing.T) {
	projectDir := filepath.Clean("/p")
	cases := []struct {
		name string
		gala string
		want string
	}{
		{"root file", filepath.Join(projectDir, "main.gala"), "main.gen.go"},
		{"subdir file", filepath.Join(projectDir, "sub", "lib.gala"), "sub_lib.gen.go"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := testGenFileName(projectDir, tc.gala)
			require.Equal(t, tc.want, got)
		})
	}
}

// TestFindGalaFilesRecursive_IncludesSubdirs verifies that the recursive
// file walker used by `gala build` discovers .gala files in subpackages.
func TestFindGalaFilesRecursive_IncludesSubdirs(t *testing.T) {
	projectDir := t.TempDir()

	require.NoError(t, os.WriteFile(filepath.Join(projectDir, "main.gala"),
		[]byte("package main\nfunc main() {}"), 0644))

	subDir := filepath.Join(projectDir, "sub")
	require.NoError(t, os.MkdirAll(subDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(subDir, "lib.gala"),
		[]byte("package sub\nfunc Hello() string = \"hi\""), 0644))

	// _test.gala must NOT be picked up by build-path recursion.
	require.NoError(t, os.WriteFile(filepath.Join(subDir, "extra_test.gala"),
		[]byte("package sub\nfunc TestX() {}"), 0644))

	// Hidden/build-cache directories must be skipped.
	require.NoError(t, os.MkdirAll(filepath.Join(projectDir, ".gala"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(projectDir, ".gala", "cached.gala"),
		[]byte("package whatever"), 0644))

	got, err := findGalaFilesRecursive(projectDir)
	require.NoError(t, err)

	sort.Strings(got)
	want := []string{
		filepath.Join(projectDir, "main.gala"),
		filepath.Join(subDir, "lib.gala"),
	}
	sort.Strings(want)
	require.Equal(t, want, got)
}

// TestResolveEffectiveDepDir_LocalReplace verifies that a `replace` directive
// pointing to a local path short-circuits the module cache lookup.
func TestResolveEffectiveDepDir_LocalReplace(t *testing.T) {
	projectDir := t.TempDir()
	localDep := filepath.Join(projectDir, "..", "parent")

	galaMod := &mod.File{
		Module: mod.Module{Path: "example.com/sub"},
		Require: []mod.Require{
			{Path: "example.com/parent", Version: "v0.0.0"},
		},
		Replace: []mod.Replace{
			{
				Old: mod.ModuleVersion{Path: "example.com/parent"},
				New: mod.ModuleVersion{Path: "../parent"},
			},
		},
	}

	config := DefaultConfig()
	got := resolveEffectiveDepDir(config, galaMod, projectDir,
		mod.Require{Path: "example.com/parent", Version: "v0.0.0"})

	want := filepath.Clean(localDep)
	require.Equal(t, want, got, "local replace must resolve to the replacement path")
}

// TestResolveEffectiveDepDir_NoMatch falls back to the module cache when no
// replace entry applies.
func TestResolveEffectiveDepDir_NoMatch(t *testing.T) {
	projectDir := t.TempDir()
	galaMod := &mod.File{
		Require: []mod.Require{
			{Path: "example.com/other", Version: "v1.0.0"},
		},
	}
	config := DefaultConfig()
	got := resolveEffectiveDepDir(config, galaMod, projectDir,
		mod.Require{Path: "example.com/other", Version: "v1.0.0"})

	require.Equal(t, config.GalaModulePath("example.com/other", "v1.0.0"), got)
}

// TestResolveEffectiveDepDir_ModuleReplace verifies that replace directives
// retargeting one module version to another (non-local form) redirect the
// lookup to the replacement's cache entry rather than the original.
func TestResolveEffectiveDepDir_ModuleReplace(t *testing.T) {
	galaMod := &mod.File{
		Require: []mod.Require{
			{Path: "example.com/foo", Version: "v1.0.0"},
		},
		Replace: []mod.Replace{
			{
				Old: mod.ModuleVersion{Path: "example.com/foo"},
				New: mod.ModuleVersion{Path: "example.com/fork", Version: "v1.2.3"},
			},
		},
	}
	config := DefaultConfig()
	got := resolveEffectiveDepDir(config, galaMod, "",
		mod.Require{Path: "example.com/foo", Version: "v1.0.0"})

	require.Equal(t, config.GalaModulePath("example.com/fork", "v1.2.3"), got)
}

// TestEnsureDeps_LocalReplaceSkipsFetch verifies the end-to-end behavior of
// the builder's ensureDeps: a local replacement must not trigger a fetch.
func TestEnsureDeps_LocalReplaceSkipsFetch(t *testing.T) {
	tmp := t.TempDir()
	parent := filepath.Join(tmp, "parent")
	require.NoError(t, os.MkdirAll(parent, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(parent, "gala.mod"),
		[]byte("module example.com/parent\n\ngala 0.0.0\n"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(parent, "lib.gala"),
		[]byte("package parent\nfunc Greeting() string = \"hi\"\n"), 0644))

	sub := filepath.Join(tmp, "sub")
	require.NoError(t, os.MkdirAll(sub, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(sub, "gala.mod"),
		[]byte("module example.com/sub\n\ngala 0.0.0\n\nrequire example.com/parent v0.0.0\n\nreplace example.com/parent => ../parent\n"),
		0644))
	require.NoError(t, os.WriteFile(filepath.Join(sub, "main.gala"),
		[]byte("package main\nimport \"example.com/parent\"\nfunc main() { Println(parent.Greeting()) }\n"),
		0644))

	origHome, hadHome := os.LookupEnv("HOME")
	origUserProfile, hadProfile := os.LookupEnv("USERPROFILE")
	isolated := t.TempDir()
	os.Setenv("HOME", isolated)
	os.Setenv("USERPROFILE", isolated)
	t.Cleanup(func() {
		if hadHome {
			os.Setenv("HOME", origHome)
		} else {
			os.Unsetenv("HOME")
		}
		if hadProfile {
			os.Setenv("USERPROFILE", origUserProfile)
		} else {
			os.Unsetenv("USERPROFILE")
		}
	})

	b, err := NewBuilder(sub, "test", false)
	require.NoError(t, err)
	require.NoError(t, b.workspace.Ensure())

	require.NoError(t, b.ensureDeps(), "local replace must short-circuit the fetch")

	got := b.effectiveDepDir(mod.Require{Path: "example.com/parent", Version: "v0.0.0"})
	require.Equal(t, filepath.Clean(parent), got)
}
