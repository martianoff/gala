package fetch

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"martianoff/gala/internal/depman/sum"
)

// newModuleRepo creates a local git repository holding files, committed and
// tagged ver. It stands in for a module's remote.
func newModuleRepo(t *testing.T, files map[string]string, ver string) string {
	t.Helper()
	dir := t.TempDir()
	repo, err := git.PlainInit(dir, false)
	require.NoError(t, err)
	wt, err := repo.Worktree()
	require.NoError(t, err)
	for rel, content := range files {
		path := filepath.Join(dir, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0755))
		require.NoError(t, os.WriteFile(path, []byte(content), 0644))
		_, err := wt.Add(rel)
		require.NoError(t, err)
	}
	head, err := wt.Commit("module fixture", &git.CommitOptions{
		Author: &object.Signature{Name: "fixture", Email: "fixture@example.com", When: time.Unix(0, 0)},
	})
	require.NoError(t, err)
	_, err = repo.CreateTag(ver, head, nil)
	require.NoError(t, err)
	return dir
}

// newLocalFetcher returns a fetcher over a fresh cache that clones every
// module from repoDir.
func newLocalFetcher(t *testing.T, repoDir string) (*GitFetcher, *Cache) {
	t.Helper()
	cache := NewCache(NewConfig(filepath.Join(t.TempDir(), "pkg", "mod")))
	fetcher := NewGitFetcher(cache)
	fetcher.gitURL = func(string) string { return repoDir }
	return fetcher, cache
}

// embedModuleFiles is a module whose Go package embeds a data file, as
// `//go:embed` needs it at build time.
var embedModuleFiles = map[string]string{
	"gala.mod":             "module example.com/assets\n",
	"assets.gala":          "package assets\n",
	"data/data.go":         "package data\n\nimport _ \"embed\"\n\n//go:embed greeting.txt\nvar Greeting string\n",
	"data/greeting.txt":    "hello from an embedded asset\n",
	"templates/page.tmpl":  "<h1>{{.}}</h1>\n",
	"testdata/fixture.txt": "fixture\n",
}

// A fetched module keeps the data files its build needs. The cache used to
// store only .gala/.go/gala.mod/go.sum/BUILD.bazel, so a dependency whose Go
// code embedded a file reached the build without it, and `go build` failed
// with "pattern greeting.txt: no matching files found".
func TestGitFetcher_Fetch_KeepsDataFiles(t *testing.T) {
	repo := newModuleRepo(t, embedModuleFiles, "v1.0.0")
	fetcher, _ := newLocalFetcher(t, repo)

	modDir, hash, err := fetcher.Fetch("example.com/assets", "v1.0.0")
	require.NoError(t, err)

	for rel, content := range embedModuleFiles {
		got, err := os.ReadFile(filepath.Join(modDir, filepath.FromSlash(rel)))
		require.NoError(t, err, "fetched module is missing %s", rel)
		assert.Equal(t, content, string(got))
	}
	assert.NoDirExists(t, filepath.Join(modDir, ".git"), "VCS metadata is not module content")
	assert.True(t, strings.HasPrefix(hash, "h2:"), "a fresh fetch records the h2 hash, got %s", hash)
	require.NoError(t, sum.Verify(modDir, hash))
}

// The recorded hash covers the data files: an asset changed after the fetch
// fails verification. An h1 hash could not see that.
func TestGitFetcher_Fetch_HashCoversDataFiles(t *testing.T) {
	repo := newModuleRepo(t, embedModuleFiles, "v1.0.0")
	fetcher, _ := newLocalFetcher(t, repo)

	modDir, hash, err := fetcher.Fetch("example.com/assets", "v1.0.0")
	require.NoError(t, err)

	require.NoError(t, os.WriteFile(filepath.Join(modDir, "data", "greeting.txt"), []byte("tampered\n"), 0644))
	var mismatch *sum.HashMismatchError
	assert.ErrorAs(t, sum.Verify(modDir, hash), &mismatch)
}

// A module published under the earlier completion marker holds only sources,
// so it is fetched again rather than trusted.
func TestGitFetcher_Fetch_RefetchesSourceOnlyCacheEntry(t *testing.T) {
	repo := newModuleRepo(t, embedModuleFiles, "v1.0.0")
	fetcher, cache := newLocalFetcher(t, repo)

	modDir := cache.Config().ModulePath("example.com/assets", "v1.0.0")
	require.NoError(t, os.MkdirAll(filepath.Join(modDir, "data"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(modDir, "data", "data.go"), []byte(embedModuleFiles["data/data.go"]), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(modDir, ".gala-module-complete"), nil, 0644))
	require.False(t, cache.Config().IsCached("example.com/assets", "v1.0.0"))

	_, _, err := fetcher.Fetch("example.com/assets", "v1.0.0")
	require.NoError(t, err)
	assert.FileExists(t, filepath.Join(modDir, "data", "greeting.txt"))
}
