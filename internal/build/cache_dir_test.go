package build

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"

	"martianoff/gala/internal/depman/fetch"
)

// The builder reads fetched modules from the directory the fetcher writes
// them to, whichever of GALA_HOME and GALA_CACHE is set. Each used to resolve
// the location on its own: GALA_HOME moved only the builder's copy and
// GALA_CACHE only the fetcher's, so a dependency was fetched to one place and
// looked for in another.
func TestGalaPkgDir_MatchesFetchCache(t *testing.T) {
	home := t.TempDir()
	cache := t.TempDir()

	tests := []struct {
		name      string
		galaHome  string
		galaCache string
		want      string
	}{
		{"GALA_HOME only", home, "", filepath.Join(home, "pkg", "mod")},
		{"GALA_CACHE only", "", cache, cache},
		{"both set", home, cache, cache},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("GALA_HOME", tt.galaHome)
			t.Setenv("GALA_CACHE", tt.galaCache)

			buildDir := DefaultConfig().GalaPkgDir
			fetchDir := fetch.DefaultConfig().CacheDir
			assert.Equal(t, tt.want, buildDir)
			assert.Equal(t, buildDir, fetchDir, "builder and fetcher must agree on the module cache")
		})
	}
}
