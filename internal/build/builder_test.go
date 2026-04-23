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
