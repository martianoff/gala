package fetch

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// untaggedModuleFiles is a module whose repository has no tag.
var untaggedModuleFiles = map[string]string{
	"gala.mod":   "module example.com/untagged\n",
	"queue.gala": "package untagged\n",
}

// newUntaggedRepo creates a local git repository holding untaggedModuleFiles
// in one commit made at when, with no tag. It returns the repository and the
// commit.
func newUntaggedRepo(t *testing.T, when time.Time) (string, plumbing.Hash) {
	t.Helper()
	dir := t.TempDir()
	_, err := git.PlainInit(dir, false)
	require.NoError(t, err)
	return dir, commitFiles(t, dir, untaggedModuleFiles, "untagged module", when)
}

// The latest version of a module whose repository has no semver tag is the
// pseudo-version of its default branch's head, as Go resolves @latest. Such a
// module used to fail with "no versions found".
func TestGitFetcher_FetchLatest_UntaggedRepo(t *testing.T) {
	repo, head := newUntaggedRepo(t, time.Date(2022, 6, 3, 1, 37, 15, 0, time.UTC))
	fetcher, _ := newLocalFetcher(t, repo)

	ver, modDir, _, err := fetcher.FetchLatest("example.com/untagged")
	require.NoError(t, err)
	assert.Equal(t, "v0.0.0-20220603013715-"+head.String()[:12], ver)
	assert.FileExists(t, filepath.Join(modDir, "queue.gala"))
}

// The commit time in a pseudo-version is UTC, whatever zone the commit was
// made in.
func TestGitFetcher_FetchLatest_PseudoVersionTimeIsUTC(t *testing.T) {
	when := time.Date(2022, 6, 3, 3, 37, 15, 0, time.FixedZone("CEST", 2*60*60))
	repo, head := newUntaggedRepo(t, when)
	fetcher, _ := newLocalFetcher(t, repo)

	ver, _, _, err := fetcher.FetchLatest("example.com/untagged")
	require.NoError(t, err)
	assert.Equal(t, "v0.0.0-20220603013715-"+head.String()[:12], ver)
}

// The pseudo-version of a module whose path ends in a major-version suffix
// carries that major version, as Go's does.
func TestGitFetcher_FetchLatest_UntaggedMajorVersionPath(t *testing.T) {
	repo, head := newUntaggedRepo(t, time.Date(2022, 6, 3, 1, 37, 15, 0, time.UTC))
	fetcher, _ := newLocalFetcher(t, repo)

	ver, _, _, err := fetcher.FetchLatest("example.com/untagged/v2")
	require.NoError(t, err)
	assert.Equal(t, "v2.0.0-20220603013715-"+head.String()[:12], ver)
}

// A pseudo-version fetches the commit it names, not the branch head, in each
// of Go's three pseudo-version forms. It used to fail with "version not
// found".
func TestGitFetcher_Fetch_PseudoVersion(t *testing.T) {
	when := time.Date(2022, 6, 3, 1, 37, 15, 0, time.UTC)
	repo, first := newUntaggedRepo(t, when)
	commitFiles(t, repo, map[string]string{"extra.gala": "package untagged\n"}, "later", when.Add(time.Hour))
	rev := first.String()[:12]

	for _, ver := range []string{
		"v0.0.0-20220603013715-" + rev,
		"v1.2.4-0.20220603013715-" + rev,
		"v1.2.3-pre.0.20220603013715-" + rev,
	} {
		t.Run(ver, func(t *testing.T) {
			fetcher, _ := newLocalFetcher(t, repo)
			modDir, _, err := fetcher.Fetch("example.com/untagged", ver)
			require.NoError(t, err)
			assert.FileExists(t, filepath.Join(modDir, "queue.gala"))
			assert.NoFileExists(t, filepath.Join(modDir, "extra.gala"), "the module is at the commit the pseudo-version names")
		})
	}
}

// The pseudo-version FetchLatest records fetches the same content again into a
// fresh cache, as another machine resolves gala.mod's version.
func TestGitFetcher_PseudoVersionRoundTrip(t *testing.T) {
	repo, _ := newUntaggedRepo(t, time.Date(2022, 6, 3, 1, 37, 15, 0, time.UTC))
	fetcher, _ := newLocalFetcher(t, repo)
	ver, _, hash, err := fetcher.FetchLatest("example.com/untagged")
	require.NoError(t, err)

	other, _ := newLocalFetcher(t, repo)
	_, again, err := other.Fetch("example.com/untagged", ver)
	require.NoError(t, err)
	assert.Equal(t, hash, again)
}

// A pseudo-version whose timestamp is not its commit's time is rejected, as Go
// rejects it: it is not the version Go gives that commit.
func TestGitFetcher_Fetch_PseudoVersionWrongTime(t *testing.T) {
	repo, head := newUntaggedRepo(t, time.Date(2022, 6, 3, 1, 37, 15, 0, time.UTC))
	fetcher, _ := newLocalFetcher(t, repo)

	_, _, err := fetcher.Fetch("example.com/untagged", "v0.0.0-20220603013716-"+head.String()[:12])
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid pseudo-version")
}

// A tagged repository's latest version is its highest semver tag, even with
// untagged commits after it.
func TestGitFetcher_FetchLatest_TaggedRepo(t *testing.T) {
	repo := newModuleRepo(t, untaggedModuleFiles, "v1.0.0")
	commitVersion(t, repo, map[string]string{"extra.gala": "package untagged\n"}, "v1.1.0")
	commitFiles(t, repo, map[string]string{"later.gala": "package untagged\n"}, "untagged head", time.Unix(100, 0))
	fetcher, _ := newLocalFetcher(t, repo)

	ver, modDir, _, err := fetcher.FetchLatest("example.com/untagged")
	require.NoError(t, err)
	assert.Equal(t, "v1.1.0", ver)
	assert.FileExists(t, filepath.Join(modDir, "extra.gala"))
	assert.NoFileExists(t, filepath.Join(modDir, "later.gala"))
}
