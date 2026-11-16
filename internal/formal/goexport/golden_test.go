package goexport_test

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"martianoff/gala/internal/formal/goexport"
)

// goldenDirEnv, when set (bazel test --test_env=GOEXPORT_GOLDEN_DIR=<the
// absolute path of testdata>), makes the golden test write its output there
// instead of comparing it.
const goldenDirEnv = "GOEXPORT_GOLDEN_DIR"

// TestGolden pins the document the S1 shapes export to, the format the Lean
// decoder reads.
func TestGolden(t *testing.T) {
	prog, err := goexport.Export([]goexport.PackageSource{{Path: "main", Files: []goexport.Source{{Name: "shapes.go", Text: s1Shapes}}}})
	require.NoError(t, err)
	got, err := goexport.Marshal(prog)
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
