package gocoreexport_test

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"martianoff/gala/internal/formal/gocoreexport"
)

// goldenDirEnv, when set (bazel test --test_env=GOCOREEXPORT_GOLDEN_DIR=<the
// absolute path of testdata>), makes the golden test write its output there
// instead of comparing it.
const goldenDirEnv = "GOCOREEXPORT_GOLDEN_DIR"

// TestGolden pins the document the S1 shapes export to, the format the Lean
// decoder reads.
func TestGolden(t *testing.T) {
	prog, err := gocoreexport.Export([]gocoreexport.PackageSource{{Path: "main", Files: []gocoreexport.Source{{Name: "shapes.go", Text: s1Shapes}}}})
	require.NoError(t, err)
	got, err := gocoreexport.Marshal(prog)
	require.NoError(t, err)
	if dir := os.Getenv(goldenDirEnv); dir != "" {
		require.NoError(t, os.WriteFile(filepath.Join(dir, "shapes.json"), got, 0o644))
		return
	}
	want, err := os.ReadFile("testdata/shapes.json")
	require.NoError(t, err)
	want = bytes.ReplaceAll(want, []byte("\r\n"), []byte("\n"))
	require.Equal(t, string(want), string(got), "regenerate with --test_env=%s=<testdata dir>", goldenDirEnv)
}
