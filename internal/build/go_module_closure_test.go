package build

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"martianoff/gala/internal/depman/mod"
)

// TestGoModuleSrcDirClosure: the Go modules a required Go module's go.mod
// requires resolve too (cobra's pflag), at the highest version anything
// requires; the modules those require in turn do not.
func TestGoModuleSrcDirClosure(t *testing.T) {
	home := t.TempDir()
	config := &Config{GalaPkgDir: filepath.Join(home, "pkg", "mod"), GoPkgDir: filepath.Join(home, "go", "pkg", "mod")}
	module := func(path, version, goMod string) string {
		dir := filepath.Join(config.GoPkgDir, filepath.FromSlash(path)+"@"+version)
		require.NoError(t, os.MkdirAll(dir, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "go.mod"), []byte(goMod), 0o644))
		return dir
	}
	cobra := module("example.com/cobra", "v1.0.0",
		"module example.com/cobra\n\ngo 1.21\n\nrequire (\n\texample.com/pflag v1.0.9\n\texample.com/old v1.0.0\n)\n")
	module("example.com/pflag", "v1.0.5", "module example.com/pflag\n\ngo 1.21\n")
	pflag := module("example.com/pflag", "v1.0.9", "module example.com/pflag\n\ngo 1.21\n\nrequire example.com/deep v1.0.0\n")
	old := module("example.com/old", "v1.2.0", "module example.com/old\n\ngo 1.21\n")

	reqs := []mod.Require{
		{Path: "example.com/cobra", Version: "v1.0.0", Go: true},
		// Required directly at a lower version than cobra needs: v1.0.9 wins.
		{Path: "example.com/pflag", Version: "v1.0.5", Go: true},
		// Required directly at a higher version than cobra needs: it stays.
		{Path: "example.com/old", Version: "v1.2.0", Go: true},
	}
	var downloaded []mod.Require
	dirs, complete := goModuleSrcDirClosure(config, reqs, nil, func(missing []mod.Require) { downloaded = append(downloaded, missing...) })
	assert.True(t, complete)
	assert.Empty(t, downloaded)
	assert.Equal(t, map[string]string{
		"example.com/cobra": cobra,
		"example.com/pflag": pflag,
		"example.com/old":   old,
	}, dirs, "example.com/deep, required by pflag's go.mod only, is not part of the closure")

	// A module of the closure that is in neither cache is downloaded. One that
	// is still missing afterwards is left out, without making the result
	// incomplete: it may serve only a test of the module requiring it.
	require.NoError(t, os.RemoveAll(pflag))
	dirs, complete = goModuleSrcDirClosure(config, reqs[:1], nil, func(missing []mod.Require) { downloaded = append(downloaded, missing...) })
	assert.True(t, complete)
	assert.Equal(t, []mod.Require{
		{Path: "example.com/pflag", Version: "v1.0.9", Indirect: true, Go: true},
		{Path: "example.com/old", Version: "v1.0.0", Indirect: true, Go: true},
	}, downloaded)
	assert.Equal(t, map[string]string{"example.com/cobra": cobra}, dirs)
}

// TestGoModuleSrcDirClosureAppliesReplaces: the analyzer reads a replaced Go
// module's types from its replacement, as `go build` compiles it: a directory
// replacement from that directory, a module replacement from the replacing
// module's source, downloaded when no cache holds it. Both stay keyed by the
// required path, the one the module's packages are imported by.
func TestGoModuleSrcDirClosureAppliesReplaces(t *testing.T) {
	home := t.TempDir()
	config := &Config{GalaPkgDir: filepath.Join(home, "pkg", "mod"), GoPkgDir: filepath.Join(home, "go", "pkg", "mod")}
	local := filepath.Join(home, "project", "greet")
	require.NoError(t, os.MkdirAll(local, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(local, "go.mod"), []byte("module example.com/greet\n\ngo 1.21\n"), 0o644))
	fork := filepath.Join(config.GoPkgDir, "example.com", "fork@v1.1.0")

	reqs := []mod.Require{
		{Path: "example.com/greet", Version: "v0.0.0", Go: true},
		{Path: "example.com/pinned", Version: "v1.0.0", Go: true},
	}
	replacement := func(req mod.Require) (mod.Require, string) {
		switch req.Path {
		case "example.com/greet":
			return req, local
		case "example.com/pinned":
			return mod.Require{Path: "example.com/fork", Version: "v1.1.0", Go: true}, ""
		}
		return req, ""
	}
	var downloaded []mod.Require
	dirs, complete := goModuleSrcDirClosure(config, reqs, replacement, func(missing []mod.Require) {
		downloaded = append(downloaded, missing...)
		require.NoError(t, os.MkdirAll(fork, 0o755))
	})
	assert.True(t, complete)
	assert.Equal(t, []mod.Require{{Path: "example.com/fork", Version: "v1.1.0", Go: true}}, downloaded,
		"the replacing module is downloaded, not the replaced one")
	assert.Equal(t, map[string]string{"example.com/greet": local, "example.com/pinned": fork}, dirs)

	// A replacement directory that is not there leaves the result incomplete,
	// so no transpile made without its types is recorded as up to date.
	require.NoError(t, os.RemoveAll(local))
	dirs, complete = goModuleSrcDirClosure(config, reqs[:1], replacement, nil)
	assert.False(t, complete)
	assert.Empty(t, dirs)
}
