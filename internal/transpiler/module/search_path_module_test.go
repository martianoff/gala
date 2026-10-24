package module

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// searchPathModule caches findModuleRootForSearchPath and must return what it
// returns, for a search path inside a module and for one outside any.
func TestResolver_SearchPathModuleMatchesUncached(t *testing.T) {
	root := t.TempDir()
	mod := filepath.Join(root, "toolkit")
	pkg := filepath.Join(mod, "widgets")
	require.NoError(t, os.MkdirAll(pkg, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(mod, "gala.mod"),
		[]byte("module example.com/toolkit\n\ngala 0.0.0\n"), 0o644))
	bare := filepath.Join(root, "bare")
	require.NoError(t, os.MkdirAll(bare, 0o755))

	r := &Resolver{}
	for _, sp := range []string{mod, pkg, bare} {
		wantRoot, wantName := findModuleRootForSearchPath(sp)
		for i := 0; i < 2; i++ {
			gotRoot, gotName := r.searchPathModule(sp)
			assert.Equal(t, wantRoot, gotRoot, sp)
			assert.Equal(t, wantName, gotName, sp)
		}
		assert.Contains(t, r.spModules, sp)
	}
	gotRoot, gotName := r.searchPathModule(pkg)
	assert.Equal(t, mod, gotRoot)
	assert.Equal(t, "example.com/toolkit", gotName)
}
