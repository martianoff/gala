package build

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTrimPathEnabled(t *testing.T) {
	tests := []struct {
		goflags string
		want    bool
	}{
		{"", true},
		{"-p=2 -mod=mod", true},
		{"-trimpath", true},
		{"-trimpath=false", false},
		{"--trimpath=false", false},
		{"-trimpath=0", false},
		{"-trimpath=false -p=2 -trimpath", true},
		{"-trimpath -trimpath=false", false},
		{"-trimpathx=false", true},
		{"---trimpath=false", true},
		{"trimpath=false", true},
	}
	for _, tt := range tests {
		t.Run(tt.goflags, func(t *testing.T) {
			assert.Equal(t, tt.want, trimPathEnabled(tt.goflags))
		})
	}
}

func TestLineFileFor(t *testing.T) {
	module := filepath.Join(t.TempDir(), "app")
	tests := []struct {
		name string
		file string
		root string
		want string
	}{
		{"module root", filepath.Join(module, "main.gala"), "example.com/app", "example.com/app/main.gala"},
		{"subpackage", filepath.Join(module, "cmd", "tool", "main.gala"), "example.com/app", "example.com/app/cmd/tool/main.gala"},
		{"module cache", filepath.Join(module, "lib.gala"), "example.com/lib@v1.2.0", "example.com/lib@v1.2.0/lib.gala"},
		{"no gala.mod", filepath.Join(module, "cmd", "foo.gala"), "", "cmd/foo.gala"},
		{"outside the module", filepath.Join(filepath.Dir(module), "other", "x.gala"), "example.com/app", filepath.Join(filepath.Dir(module), "other", "x.gala")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, lineFileFor(tt.file, module, tt.root))
		})
	}
}

// TestBuild_TrimPathOffKeepsHostSourcePaths checks that a user who turns
// -trimpath off in GOFLAGS gets the host paths of the .gala files, as they do
// for every Go file.
func TestBuild_TrimPathOffKeepsHostSourcePaths(t *testing.T) {
	tmp, consumerDir := setUpBoomProject(t)
	chdirForTest(t, consumerDir)
	setEnvForTest(t, "GOFLAGS", "-trimpath=false")

	b, err := NewBuilder(consumerDir, "test", false)
	require.NoError(t, err)
	require.NoError(t, b.workspace.Ensure())
	require.NoError(t, b.ensureStdlib())
	require.NoError(t, b.transpileDeps())
	require.NoError(t, b.transpile())

	consumerGen := readFileString(t, filepath.Join(b.workspace.GenDir, "main.gen.go"))
	assert.Contains(t, consumerGen, "//line "+filepath.ToSlash(filepath.Join(consumerDir, "main.gala"))+":6\n")
	libGen := findGenFile(t, b.workspace.Dir, "boom.gen.go")
	assert.Contains(t, libGen, "//line "+filepath.ToSlash(filepath.Join(tmp, "boomlib", "boom.gala"))+":4\n")
}
