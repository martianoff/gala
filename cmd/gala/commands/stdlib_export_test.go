package commands

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCallerPath(t *testing.T) {
	caller := t.TempDir()

	t.Setenv("BUILD_WORKING_DIRECTORY", caller)
	got, err := callerPath("out")
	require.NoError(t, err)
	require.Equal(t, filepath.Join(caller, "out"), got, "relative paths under bazel run resolve against the caller's directory")

	abs := filepath.Join(t.TempDir(), "x")
	got, err = callerPath(abs)
	require.NoError(t, err)
	require.Equal(t, abs, got)

	t.Setenv("BUILD_WORKING_DIRECTORY", "")
	got, err = callerPath("out")
	require.NoError(t, err)
	want, _ := filepath.Abs("out")
	require.Equal(t, want, got)
}
