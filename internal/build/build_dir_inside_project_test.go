package build

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// A --build-dir inside the project put the build workspaces in the tree the
// build copies its non-GALA files from, so each build copied the project —
// workspaces included — into the workspace's own gen directory, which is
// inside it: deeper on every build, and during the copy itself, until the disk
// filled. A workspace is build output, not source, and is left out of the
// project's tree. The build-dir documentation allows any directory, so this is
// supported rather than rejected.
func TestBuild_BuildDirInsideProject(t *testing.T) {
	projectDir := newForeignGenGoProject(t, "example.com/inproject", map[string]string{
		"main.gala":    "package main\n\nfunc main() {\n    Println(\"hi\")\n}\n",
		"assets/a.txt": "asset\n",
	})
	buildDir := filepath.Join(projectDir, "out")
	setEnvForTest(t, "GALA_BUILD_DIR", buildDir)

	for range 2 { // the second build sees the first one's workspace in the tree
		require.Equal(t, "hi", buildAndRun(t, projectDir))
	}

	// Nothing under the build dir is a copy of it.
	err := filepath.WalkDir(buildDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && path != buildDir && d.Name() == "out" {
			t.Errorf("the build dir was copied into a workspace: %s", path)
			return filepath.SkipDir
		}
		return nil
	})
	require.NoError(t, err)

	// The project's own non-GALA files are still copied.
	ws, err := NewWorkspace(DefaultConfig(), projectDir, ModeBuild)
	require.NoError(t, err)
	_, err = os.Stat(filepath.Join(ws.GenDir, "assets", "a.txt"))
	require.NoError(t, err, "a project file was not copied into the workspace")
}
