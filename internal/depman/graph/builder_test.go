package graph

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"martianoff/gala/internal/depman/fetch"
	"martianoff/gala/internal/depman/mod"
)

// The graph follows the gala.mod of a GALA requirement, but never reads or
// fetches a Go requirement (`// go`): it has no gala.mod, and fetching it
// would clone its repository for nothing. Both modules below are cached with
// a gala.mod requiring another module; only the GALA one's is followed.
func TestBuilderDoesNotFollowGoRequirements(t *testing.T) {
	cache := fetch.NewCache(fetch.NewConfig(t.TempDir()))
	for _, m := range []struct{ path, requires string }{
		{"example.com/galalib", "example.com/from-gala"},
		{"example.com/golib", "example.com/from-go"},
	} {
		src := t.TempDir()
		galaMod := "module " + m.path + "\n\nrequire " + m.requires + " v1.0.0\n"
		require.NoError(t, os.WriteFile(filepath.Join(src, "gala.mod"), []byte(galaMod), 0644))
		require.NoError(t, cache.Store(m.path, "v1.0.0", src))
	}
	// The required modules are not cached; a fetch of either fails at once.
	builder := NewBuilder(cache, nil)

	root := mod.NewFile("example.com/app")
	root.Require = []mod.Require{
		{Path: "example.com/galalib", Version: "v1.0.0"},
		{Path: "example.com/golib", Version: "v1.0.0", Go: true},
	}
	g, err := builder.Build(root)
	require.NoError(t, err)

	assert.NotNil(t, g.GetNode("example.com/golib"), "a Go requirement is still a node")
	assert.NotNil(t, g.GetNode("example.com/from-gala"), "a GALA module's requirements are followed")
	assert.Nil(t, g.GetNode("example.com/from-go"), "a Go module's files are not read")
}
