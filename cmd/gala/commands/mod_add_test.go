package commands

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"martianoff/gala/internal/depman/fetch"
	"martianoff/gala/internal/depman/mod"
	"martianoff/gala/internal/depman/sum"
)

// fakeGoDownloader resolves every query to ver, as the Go toolchain would,
// and records the queries it was asked.
func fakeGoDownloader(ver string, queries *[]string) goDownloader {
	return func(modulePath, query string) (*fetch.GoModule, error) {
		*queries = append(*queries, modulePath+"@"+query)
		return &fetch.GoModule{Path: modulePath, Version: ver, Dir: "/gomodcache/" + modulePath + "@" + ver}, nil
	}
}

// A module with no .gala file or gala.mod is a Go module, and is required
// the way `--go` requires it: resolved by the Go toolchain, marked `// go`,
// and with no gala.sum line, since go.sum holds its checksum. It used to be
// recorded from the Git fetch, with a gala.sum line.
func TestAddModule_DetectedGoModuleIsRecordedAsWithGoFlag(t *testing.T) {
	_, fetcher := tidyCache(t, map[string]map[string]string{
		"example.com/golib@v1.0.0": {"lib.go": "package golib\n"},
	})
	galaMod, err := mod.Parse("module example.com/app\n")
	require.NoError(t, err)
	galaSum := sum.NewFile()
	var queries []string

	added, err := addModule(galaMod, galaSum, fetcher, fakeGoDownloader("v1.0.0", &queries), "example.com/golib", "v1.0.0", false)
	require.NoError(t, err)

	assert.True(t, added.goModule)
	assert.Equal(t, []string{"example.com/golib@v1.0.0"}, queries, "the Go toolchain resolves it")
	req := galaMod.GetRequire("example.com/golib")
	require.NotNil(t, req)
	assert.True(t, req.Go)
	assert.Equal(t, "v1.0.0", req.Version)
	assert.Empty(t, galaSum.Entries)
}

// A GALA module stays on the Git path, with its gala.sum lines, and the Go
// toolchain is not asked.
func TestAddModule_GalaModule(t *testing.T) {
	_, fetcher := tidyCache(t, map[string]map[string]string{
		"example.com/galalib@v1.0.0": {"gala.mod": "module example.com/galalib\n", "lib.gala": "package galalib\n"},
	})
	galaMod, err := mod.Parse("module example.com/app\n")
	require.NoError(t, err)
	galaSum := sum.NewFile()
	var queries []string

	added, err := addModule(galaMod, galaSum, fetcher, fakeGoDownloader("v9.9.9", &queries), "example.com/galalib", "v1.0.0", false)
	require.NoError(t, err)

	assert.False(t, added.goModule)
	assert.Empty(t, queries)
	assert.False(t, galaMod.GetRequire("example.com/galalib").Go)
	assert.NotNil(t, galaSum.Get("example.com/galalib", "v1.0.0", ""))
	assert.NotNil(t, galaSum.Get("example.com/galalib", "v1.0.0", "/gala.mod"))
}

// Requiring a module as a Go module drops every gala.sum line it has, also
// when the version stays the same: one required as a GALA module before keeps
// no stale lines. Only a version change used to drop them.
func TestSetGoRequire_DropsGalaSumLinesAtSameVersion(t *testing.T) {
	galaMod, err := mod.Parse("module example.com/app\n\nrequire example.com/lib v1.0.0\n")
	require.NoError(t, err)
	galaSum := sum.NewFile()
	galaSum.Add("example.com/lib", "v1.0.0", "", "h2:abc")
	galaSum.Add("example.com/lib", "v1.0.0", "/gala.mod", "h1:def")
	galaSum.Add("example.com/other", "v1.0.0", "", "h2:keep")

	setGoRequire(galaMod, galaSum, "example.com/lib", "v1.0.0")

	assert.True(t, galaMod.GetRequire("example.com/lib").Go)
	assert.Equal(t, []sum.Entry{{Path: "example.com/other", Version: "v1.0.0", Hash: "h2:keep"}}, galaSum.Entries)
}

// `gala mod update` never moves a requirement to a lower version, as `go get
// -u` does not: a pinned pseudo-version newer than the latest tag, or a
// prerelease above the latest release, stays.
func TestUpdateRequire_NeverDowngrades(t *testing.T) {
	tests := []struct {
		name, pinned, latest string
		goModule             bool
		want                 string
	}{
		{"go: pseudo-version newer than latest tag", "v1.0.1-0.20230101000000-abcdefabcdef", "v1.0.0", true, "v1.0.1-0.20230101000000-abcdefabcdef"},
		{"go: prerelease above latest release", "v2.0.0-rc.1", "v1.9.0", true, "v2.0.0-rc.1"},
		{"go: newer release", "v1.0.0", "v1.1.0", true, "v1.1.0"},
		{"gala: pseudo-version newer than latest tag", "v1.0.1-0.20230101000000-abcdefabcdef", "v1.0.0", false, "v1.0.1-0.20230101000000-abcdefabcdef"},
		{"gala: newer release", "v1.0.0", "v1.1.0", false, "v1.1.0"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			goMark := ""
			if tt.goModule {
				goMark = " // go"
			}
			galaMod, err := mod.Parse("module example.com/app\n\nrequire example.com/lib " + tt.pinned + goMark + "\n")
			require.NoError(t, err)
			req := galaMod.GetRequire("example.com/lib")

			changed, err := updateRequire(galaMod, sum.NewFile(), req, latestModule{version: tt.latest, dir: t.TempDir(), hash: "h2:x"})
			require.NoError(t, err)
			assert.Equal(t, tt.want != tt.pinned, changed)
			assert.Equal(t, tt.want, galaMod.GetRequire("example.com/lib").Version)
		})
	}
}
