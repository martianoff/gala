package sum

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeTree lays out files (slash-separated relative paths) under a temp dir.
func writeTree(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for rel, content := range files {
		path := filepath.Join(dir, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0755))
		require.NoError(t, os.WriteFile(path, []byte(content), 0644))
	}
	return dir
}

// moduleTree has files the two schemes treat differently: sources, data
// files, a testdata dir, VCS metadata and gala's completion marker.
var moduleTree = map[string]string{
	"gala.mod":                 "module github.com/test/lib\n",
	"lib.gala":                 "package lib\r\n",
	"sub/sub.go":               "package sub\n",
	"README.md":                "readme\n",
	"data/asset.txt":           "asset\n",
	"testdata/x.gala":          "package x\n",
	".git/config":              "cfg\n",
	".gala-module-complete-v2": "",
}

// An h1 entry written by an earlier gala still verifies. The expected value is
// what the h1-only HashDir produced for this tree; it pins the legacy scheme so
// existing gala.sum files keep verifying.
func TestVerify_LegacyH1(t *testing.T) {
	dir := writeTree(t, moduleTree)
	const legacy = "h1:E7cUI3XKDEY5S7BxHZ+AReXqKtShPQPcRelZ/2CjnXk="
	require.NoError(t, Verify(dir, legacy))

	// h1 never covered data files, so changing one does not break it...
	require.NoError(t, os.WriteFile(filepath.Join(dir, "data", "asset.txt"), []byte("changed\n"), 0644))
	require.NoError(t, Verify(dir, legacy))
	// ...but a source change still does.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "lib.gala"), []byte("package changed\n"), 0644))
	var mismatch *HashMismatchError
	assert.ErrorAs(t, Verify(dir, legacy), &mismatch)
}

// h2 covers every module-content file — data files and testdata included —
// and leaves out VCS metadata and gala's bookkeeping files.
func TestHashDir_H2Coverage(t *testing.T) {
	base, err := HashDir(writeTree(t, moduleTree))
	require.NoError(t, err)

	tests := []struct {
		name    string
		rel     string
		changes bool
	}{
		{"data file", "data/asset.txt", true},
		{"readme", "README.md", true},
		{"testdata", "testdata/x.gala", true},
		{"source", "sub/sub.go", true},
		{"VCS metadata", ".git/config", false},
		{"completion marker", ".gala-module-complete-v2", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			files := make(map[string]string, len(moduleTree))
			for k, v := range moduleTree {
				files[k] = v
			}
			files[tt.rel] = "changed\n"
			got, err := HashDir(writeTree(t, files))
			require.NoError(t, err)
			if tt.changes {
				assert.NotEqual(t, base, got)
			} else {
				assert.Equal(t, base, got)
			}
		})
	}
}

func TestVerify_UnknownScheme(t *testing.T) {
	dir := writeTree(t, map[string]string{"lib.gala": "package lib\n"})
	err := Verify(dir, "h9:abc==")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported hash scheme")
}

func TestParse_AcceptsH2(t *testing.T) {
	f, err := Parse("github.com/example/utils v1.2.3 h2:abc123==")
	require.NoError(t, err)
	require.Len(t, f.Entries, 1)
	assert.Equal(t, "h2:abc123==", f.Entries[0].Hash)
}
