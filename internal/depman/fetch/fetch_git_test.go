package fetch

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/filemode"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"martianoff/gala/internal/depman/sum"
)

// newModuleRepo creates a local git repository holding files, committed and
// tagged ver (see commitVersion). It stands in for a module's remote.
func newModuleRepo(t *testing.T, files map[string]string, ver string, executable ...string) string {
	t.Helper()
	dir := t.TempDir()
	_, err := git.PlainInit(dir, false)
	require.NoError(t, err)
	commitVersion(t, dir, files, ver, executable...)
	return dir
}

// commitVersion writes files into the repository at dir, commits them and
// tags the commit ver. The files named in executable are committed as mode
// 100755, whatever the host filesystem can record.
func commitVersion(t *testing.T, dir string, files map[string]string, ver string, executable ...string) {
	t.Helper()
	repo, err := git.PlainOpen(dir)
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
	if len(executable) > 0 {
		idx, err := repo.Storer.Index()
		require.NoError(t, err)
		for _, rel := range executable {
			e, err := idx.Entry(rel)
			require.NoError(t, err)
			e.Mode = filemode.Executable
		}
		require.NoError(t, repo.Storer.SetIndex(idx))
	}
	head, err := wt.Commit("module fixture "+ver, &git.CommitOptions{
		Author: &object.Signature{Name: "fixture", Email: "fixture@example.com", When: time.Unix(0, 0)},
	})
	require.NoError(t, err)
	_, err = repo.CreateTag(ver, head, nil)
	require.NoError(t, err)
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

// scriptModuleFiles is a module that ships a script committed as 100755.
var scriptModuleFiles = map[string]string{
	"gala.mod":         "module example.com/tool\n",
	"tool.gala":        "package tool\n",
	"scripts/build.sh": "#!/bin/sh\necho build\n",
}

// A module whose repository commits a file as 100755 is fetched like any
// other. Windows filesystems have no exec bit, so go-git saw every such file
// in the fresh clone as modified and refused the checkout with "worktree
// contains unstaged changes".
func TestGitFetcher_Fetch_ModuleWithExecutableFile(t *testing.T) {
	repo := newModuleRepo(t, scriptModuleFiles, "v1.0.0", "scripts/build.sh")
	fetcher, _ := newLocalFetcher(t, repo)

	modDir, _, err := fetcher.Fetch("example.com/tool", "v1.0.0")
	require.NoError(t, err)
	assert.FileExists(t, filepath.Join(modDir, "scripts", "build.sh"))
}

// Checking out a version succeeds when the clone's executable files lost their
// exec bit on disk, as every one does on Windows. Clearing the bit by hand
// reproduces that state on any platform.
func TestGitFetcher_CheckoutVersion_IgnoresLostExecBit(t *testing.T) {
	repoDir := newModuleRepo(t, scriptModuleFiles, "v1.0.0", "scripts/build.sh")

	// A second version, so the checkout has to move the clone off HEAD.
	commitVersion(t, repoDir, map[string]string{"extra.gala": "package tool\n"}, "v1.1.0")

	// A clone that checked HEAD out, then lost the exec bit.
	cloneDir := t.TempDir()
	repo, err := git.PlainClone(cloneDir, false, &git.CloneOptions{URL: repoDir, Tags: git.AllTags})
	require.NoError(t, err)
	require.NoError(t, os.Chmod(filepath.Join(cloneDir, "scripts", "build.sh"), 0644))

	require.NoError(t, checkoutVersion(repo, "v1.0.0"))
	assert.NoFileExists(t, filepath.Join(cloneDir, "extra.gala"), "the clone is at v1.0.0")
}
