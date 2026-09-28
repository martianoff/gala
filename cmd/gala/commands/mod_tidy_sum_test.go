package commands

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"martianoff/gala/internal/depman/fetch"
	"martianoff/gala/internal/depman/mod"
	"martianoff/gala/internal/depman/sum"
)

// tidyCache is a module cache holding the given modules, each a map of file
// name to content, keyed by "path@version". Nothing is fetched over the network
// for a module it holds.
func tidyCache(t *testing.T, modules map[string]map[string]string) (*fetch.Cache, *fetch.GitFetcher) {
	t.Helper()
	cache := fetch.NewCache(fetch.NewConfig(t.TempDir()))
	for key, files := range modules {
		src := t.TempDir()
		for name, content := range files {
			require.NoError(t, os.WriteFile(filepath.Join(src, name), []byte(content), 0644))
		}
		at := strings.LastIndex(key, "@")
		require.NoError(t, cache.Store(key[:at], key[at+1:], src))
	}
	return cache, fetch.NewGitFetcher(cache)
}

// tidySum runs the part of `gala mod tidy` that resolves the requirements and
// computes gala.sum from them.
func tidySum(t *testing.T, galaModText string, cache *fetch.Cache, fetcher *fetch.GitFetcher) (*mod.File, *sum.File, error) {
	t.Helper()
	galaMod, err := mod.Parse(galaModText)
	require.NoError(t, err)
	required := make(map[string]bool)
	for _, req := range galaMod.Require {
		required[req.Path] = true
	}
	require.NoError(t, resolveModuleGraph(galaMod, required, cache, fetcher))
	f, err := galaSumFor(galaMod, cache, fetcher)
	return galaMod, f, err
}

// sumKeys lists a gala.sum's entries as "path version[suffix]".
func sumKeys(f *sum.File) []string {
	var keys []string
	for _, e := range f.Entries {
		keys = append(keys, e.Key())
	}
	return keys
}

func galaModRequiring(reqs ...string) string {
	return "module example.com/app\n\ngala dev\n\nrequire (\n\t" + strings.Join(reqs, "\n\t") + "\n)\n"
}

// gala.sum records exactly the requirements tidy leaves in gala.mod, at the
// versions it leaves them at — the version a requirement was bumped to, or the
// higher one another module requires — and nothing for a version that was
// replaced.
func TestTidyGalaSumMatchesResolvedRequirements(t *testing.T) {
	const (
		tui  = "example.com/tui"
		acp  = "example.com/acp"
		uuid = "github.com/google/uuid"
	)
	cache, fetcher := tidyCache(t, map[string]map[string]string{
		tui + "@v0.11.0": {"gala.mod": "module example.com/tui\n\ngala dev\n", "tui.gala": "package tui\n"},
		tui + "@v0.15.2": {"gala.mod": "module example.com/tui\n\ngala dev\n", "tui.gala": "package tui\n\nval V = 15\n"},
		tui + "@v0.16.0": {"gala.mod": "module example.com/tui\n\ngala dev\n", "tui.gala": "package tui\n\nval V = 16\n"},
		acp + "@v0.3.0": {
			"gala.mod": "module example.com/acp\n\ngala dev\n\nrequire (\n\texample.com/tui v0.16.0\n)\n",
			"acp.gala": "package acp\n",
		},
		uuid + "@v1.6.0": {"uuid.go": "package uuid\n"},
	})

	tests := []struct {
		name    string
		galaMod string
		want    []string
	}{
		{
			name:    "before the bump",
			galaMod: galaModRequiring(tui + " v0.11.0"),
			want:    []string{tui + " v0.11.0", tui + " v0.11.0/gala.mod"},
		},
		{
			name:    "after the bump",
			galaMod: galaModRequiring(tui + " v0.15.2"),
			want:    []string{tui + " v0.15.2", tui + " v0.15.2/gala.mod"},
		},
		{
			name:    "raised by another module's requirement",
			galaMod: galaModRequiring(acp+" v0.3.0", tui+" v0.15.2"),
			want: []string{
				acp + " v0.3.0", acp + " v0.3.0/gala.mod",
				tui + " v0.16.0", tui + " v0.16.0/gala.mod",
			},
		},
		{
			name:    "a cached Go module",
			galaMod: galaModRequiring(tui+" v0.15.2", uuid+" v1.6.0 // go"),
			want:    []string{tui + " v0.15.2", tui + " v0.15.2/gala.mod", uuid + " v1.6.0"},
		},
		{
			// A Go module is not fetched for gala.sum: its path need not be
			// a Git repository, and go.sum keeps its checksum.
			name:    "a Go module that is not cached",
			galaMod: galaModRequiring(tui+" v0.15.2", "127.0.0.1:1/x/term v0.25.0 // go"),
			want:    []string{tui + " v0.15.2", tui + " v0.15.2/gala.mod"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			galaMod, f, err := tidySum(t, tt.galaMod, cache, fetcher)
			require.NoError(t, err)
			assert.ElementsMatch(t, tt.want, sumKeys(f))

			// Every entry is for the version gala.mod now requires, and its
			// hash is the cached module's own.
			for _, e := range f.Entries {
				req := galaMod.GetRequire(e.Path)
				require.NotNil(t, req, e.Key())
				assert.Equal(t, req.Version, e.Version, e.Key())
				if e.Suffix == "" {
					hash, err := cache.Hash(e.Path, e.Version)
					require.NoError(t, err)
					assert.Equal(t, hash, e.Hash, e.Key())
				}
			}
		})
	}
}

// A GALA module that cannot be fetched stops tidy instead of being left out of
// gala.sum.
func TestTidyGalaSumUnfetchableModule(t *testing.T) {
	// Nothing listens on port 1, so the fetch fails without leaving the host.
	const unreachable = "127.0.0.1:1/example/missing"
	cache, fetcher := tidyCache(t, nil)

	_, _, err := tidySum(t, galaModRequiring(unreachable+" v1.0.0"), cache, fetcher)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot record "+unreachable+"@v1.0.0 in gala.sum")
}
