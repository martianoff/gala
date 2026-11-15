package build

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A gala.mod requirement at a version query, not a version, stops the build
// with an error that says gala.mod needs a concrete version and how to record
// one. `latest` reached `go mod download` and `go build` as a version.
func TestNewBuilder_RejectsVersionQueryInGalaMod(t *testing.T) {
	tests := []struct {
		require, wantCmd string
	}{
		{"github.com/hut8/goq latest // go", "gala mod add github.com/hut8/goq@latest --go"},
		{"example.com/lib ^1.2.0", "gala mod add example.com/lib@^1.2.0"},
	}
	for _, tt := range tests {
		t.Run(tt.require, func(t *testing.T) {
			t.Setenv("GALA_HOME", t.TempDir())
			t.Setenv("GALA_BUILD_DIR", t.TempDir())
			projectDir := t.TempDir()
			galaMod := "module example.com/app\n\ngala 0.1.0\n\nrequire " + tt.require + "\n"
			require.NoError(t, os.WriteFile(filepath.Join(projectDir, "gala.mod"), []byte(galaMod), 0644))

			_, err := NewBuilder(projectDir, "test", false)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "gala.mod needs a concrete version")
			assert.Contains(t, err.Error(), tt.wantCmd)
		})
	}
}

// A pseudo-version is a concrete version.
func TestNewBuilder_AcceptsPseudoVersionInGalaMod(t *testing.T) {
	t.Setenv("GALA_HOME", t.TempDir())
	t.Setenv("GALA_BUILD_DIR", t.TempDir())
	projectDir := t.TempDir()
	galaMod := "module example.com/app\n\ngala 0.1.0\n\nrequire github.com/hut8/goq v0.0.0-20220603013715-3360bf22bea4 // go\n"
	require.NoError(t, os.WriteFile(filepath.Join(projectDir, "gala.mod"), []byte(galaMod), 0644))

	_, err := NewBuilder(projectDir, "test", false)
	require.NoError(t, err)
}
