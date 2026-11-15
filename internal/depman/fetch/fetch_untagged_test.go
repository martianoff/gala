package fetch

import (
	"path/filepath"
	"strings"
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

// fixtureTime is when the untagged fixture's commit is made; its
// pseudo-version timestamp is fixtureStamp.
var fixtureTime = time.Date(2022, 6, 3, 1, 37, 15, 0, time.UTC)

const fixtureStamp = "20220603013715"

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
// pseudo-version of its default branch's head, as Go resolves @latest: its
// timestamp is the commit time in UTC, and its major version the one the
// module path names. Such a module used to fail with "no versions found".
func TestGitFetcher_FetchLatest_UntaggedRepo(t *testing.T) {
	tests := []struct {
		name, modulePath, major string
		when                    time.Time
	}{
		{"untagged", "example.com/untagged", "v0", fixtureTime},
		{"commit time in another zone", "example.com/untagged", "v0", fixtureTime.In(time.FixedZone("CEST", 2*60*60))},
		{"major version path", "example.com/untagged/v2", "v2", fixtureTime},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo, head := newUntaggedRepo(t, tt.when)
			fetcher, _ := newLocalFetcher(t, repo)

			ver, modDir, _, err := fetcher.FetchLatest(tt.modulePath)
			require.NoError(t, err)
			assert.Equal(t, tt.major+".0.0-"+fixtureStamp+"-"+head.String()[:12], ver)
			assert.FileExists(t, filepath.Join(modDir, "queue.gala"))
		})
	}
}

// A pseudo-version fetches the commit it names, not the branch head, in each
// of Go's three pseudo-version forms. It used to fail with "version not
// found".
func TestGitFetcher_Fetch_PseudoVersion(t *testing.T) {
	repo, first := newUntaggedRepo(t, fixtureTime)
	commitFiles(t, repo, map[string]string{"extra.gala": "package untagged\n"}, "later", fixtureTime.Add(time.Hour))
	rev := first.String()[:12]

	for _, ver := range []string{
		"v0.0.0-" + fixtureStamp + "-" + rev,
		"v1.2.4-0." + fixtureStamp + "-" + rev,
		"v1.2.3-pre.0." + fixtureStamp + "-" + rev,
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
	repo, _ := newUntaggedRepo(t, fixtureTime)
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
	repo, head := newUntaggedRepo(t, fixtureTime)
	fetcher, _ := newLocalFetcher(t, repo)

	_, _, err := fetcher.Fetch("example.com/untagged", "v0.0.0-20220603013716-"+head.String()[:12])
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid pseudo-version")
}

// A pseudo-version in a form Go never gives one is rejected before anything is
// cloned: a revision that is not a 12-character lowercase hash prefix, or a
// major version other than the module path's.
func TestGitFetcher_Fetch_NonCanonicalPseudoVersion(t *testing.T) {
	repo, head := newUntaggedRepo(t, fixtureTime)
	rev := head.String()[:12]
	tests := []struct {
		name, modulePath, ver string
	}{
		{"short revision", "example.com/untagged", "v0.0.0-" + fixtureStamp + "-" + rev[:7]},
		{"full hash", "example.com/untagged", "v0.0.0-" + fixtureStamp + "-" + head.String()},
		{"uppercase revision", "example.com/untagged", "v0.0.0-" + fixtureStamp + "-" + strings.ToUpper(rev)},
		{"major above the path's", "example.com/untagged", "v2.0.0-" + fixtureStamp + "-" + rev},
		{"major below the path's", "example.com/untagged/v2", "v0.0.0-" + fixtureStamp + "-" + rev},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fetcher, _ := newLocalFetcher(t, repo)
			_, _, err := fetcher.Fetch(tt.modulePath, tt.ver)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "invalid pseudo-version")
		})
	}
}

// FetchLatest takes only tags of the major version the module path names: a
// v2 tag is not a version of the module without a /v2 suffix, which falls back
// to its head's pseudo-version, and the /v2 module takes the v2 tag over a
// higher-numbered v1 history.
func TestGitFetcher_FetchLatest_TagsOfPathMajorOnly(t *testing.T) {
	repo := newModuleRepo(t, untaggedModuleFiles, "v1.5.0")
	commitVersion(t, repo, map[string]string{"v2.gala": "package untagged\n"}, "v2.0.0")

	t.Run("v2 path", func(t *testing.T) {
		fetcher, _ := newLocalFetcher(t, repo)
		ver, _, _, err := fetcher.FetchLatest("example.com/untagged/v2")
		require.NoError(t, err)
		assert.Equal(t, "v2.0.0", ver)
	})
	t.Run("path without suffix", func(t *testing.T) {
		fetcher, _ := newLocalFetcher(t, repo)
		ver, _, _, err := fetcher.FetchLatest("example.com/untagged")
		require.NoError(t, err)
		assert.Equal(t, "v1.5.0", ver)
	})

	onlyV2 := newModuleRepo(t, untaggedModuleFiles, "v2.0.0")
	t.Run("only a v2 tag", func(t *testing.T) {
		fetcher, _ := newLocalFetcher(t, onlyV2)
		ver, _, _, err := fetcher.FetchLatest("example.com/untagged")
		require.NoError(t, err)
		assert.True(t, strings.HasPrefix(ver, "v0.0.0-"), "the head's pseudo-version, got %s", ver)
	})
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
