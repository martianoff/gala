package build

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
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

	// Use a builder pointed at an isolated build workspace inside the temp
	// project dir so we don't touch the user's cache.
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
// synthesized test runner's main() can compile in the same package. Without
// this step, `go build ./gen/...` fails with "main redeclared in this block".
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

	// Only main.gen.go and lib.gen.go are user source files; main_test.gen.go
	// is a test file and must be left untouched.
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

	// Non-main source files are unchanged.
	libAfter, err := os.ReadFile(filepath.Join(dir, "lib.gen.go"))
	require.NoError(t, err)
	require.Equal(t, userLib, string(libAfter), "lib.gen.go should be unchanged")

	// Test files are not in the sourceNames map so they are untouched.
	testAfter, err := os.ReadFile(filepath.Join(dir, "main_test.gen.go"))
	require.NoError(t, err)
	require.Equal(t, testFile, string(testAfter), "test file should be unchanged")
}

// TestTestGenFileName verifies that the helper matches the naming used by
// transpileFilesToDir (subdir separators folded to '_', .gala suffix replaced
// by .gen.go). This keeps the rename pass and the transpile pass in sync.
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
