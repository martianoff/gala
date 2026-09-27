package build

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"martianoff/gala/internal/transpiler/analyzer"
)

// cacheFixture is a project with one input of every class the build cache keys
// declare (see cachekey.go), plus files that must NOT affect them.
type cacheFixture struct {
	projectDir string
	depDir     string
	toolchain  toolchainKey
	sourceDir  string // "" for a plain build
}

// fixtureEmbeds are the //go:embed patterns the fixture's previous transpile is
// taken to have emitted; the transpile key resolves them to assets.
var fixtureEmbeds = []string{"greeting.txt", "static/*"}

func newCacheFixture(t *testing.T) *cacheFixture {
	t.Helper()
	isolatedConfig(t)
	root := t.TempDir()
	fx := &cacheFixture{
		projectDir: filepath.Join(root, "app"),
		depDir:     filepath.Join(root, "localdep"),
		toolchain: toolchainKey{
			GalaVersion: "dev",
			Transpiler:  "transpiler-a",
			GoSDK:       "/go|go1.25.0",
			Stdlib:      "stdlib-a",
		},
	}
	files := map[string]string{
		// Project inputs.
		"app/gala.mod": "module example.com/app\n\ngala 0.0.0\n\n" +
			"require example.com/localdep v0.0.0\n\n" +
			"replace example.com/localdep => ../localdep\n",
		"app/go.mod":            "module example.com/app\n",
		"app/main.gala":         "package main\n\nfunc main() {\n    Println(\"hi\")\n}\n",
		"app/util/util.gala":    "package util\n\nfunc One() int = 1\n",
		"app/hook.go":           "package main\n",
		"app/native/native.go":  "package native\n",
		"app/native/native.h":   "int x;\n",
		"app/native/embed.go":   "package native\n\nimport _ \"embed\"\n\n//go:embed page.html\nvar Page string\n",
		"app/native/page.html":  "<p>v1</p>",
		"app/greeting.txt":      "hello",
		"app/static/site.css":   "body{}",
		"app/cmd/app/main.gala": "package main\n\nfunc main() {}\n",
		// Files that are not inputs of either key.
		"app/README.md":          "readme",
		"app/app.exe":            "a binary the previous build wrote here",
		"app/main_test.gala":     "package main\n",
		"app/stale.gen.go":       "package main\n",
		"app/.git/HEAD":          "ref: refs/heads/main\n",
		"app/testdata/case.gala": "package testdata\n",
		// Dependency behind a local replace.
		"localdep/gala.mod":  "module example.com/localdep\n\ngala 0.0.0\n",
		"localdep/lib.gala":  "package localdep\n\nfunc Message() string = \"v1\"\n",
		"localdep/asset.txt": "dep asset",
	}
	for rel, content := range files {
		writeFixtureFile(t, filepath.Join(root, filepath.FromSlash(rel)), content)
	}
	return fx
}

func writeFixtureFile(t *testing.T, path, content string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0755))
	require.NoError(t, os.WriteFile(path, []byte(content), 0644))
}

// keys computes both cache keys the way a fresh `gala build` would.
func (fx *cacheFixture) keys(t *testing.T) (source, deps string) {
	t.Helper()
	b, err := NewBuilder(fx.projectDir, "dev", false)
	require.NoError(t, err)
	tc := fx.toolchain
	b.toolchainID = &tc
	if fx.sourceDir != "" {
		b.SetSourceDir(filepath.Join(fx.projectDir, filepath.FromSlash(fx.sourceDir)))
	}
	galaFiles, err := findGalaFilesRecursive(fx.projectDir)
	require.NoError(t, err)
	source = b.sourceKey(galaFiles, fixtureEmbeds)
	deps, err = b.depsKey()
	require.NoError(t, err)
	require.NotEmpty(t, source)
	require.NotEmpty(t, deps)
	return source, deps
}

func (fx *cacheFixture) write(t *testing.T, rel, content string) {
	t.Helper()
	writeFixtureFile(t, filepath.Join(fx.projectDir, filepath.FromSlash(rel)), content)
}

func (fx *cacheFixture) writeDep(t *testing.T, rel, content string) {
	t.Helper()
	writeFixtureFile(t, filepath.Join(fx.depDir, filepath.FromSlash(rel)), content)
}

// TestCacheKeyMutationMatrix mutates one input at a time and checks which of
// the two build caches must be invalidated by it. Each input class the keys
// declare in cachekey.go has a row that must MISS; the files a build does not
// read have rows that must HIT, so the keys cannot pass by hashing everything.
//
// Adding an input class to a key starts with a row here.
func TestCacheKeyMutationMatrix(t *testing.T) {
	const (
		hit  = false
		miss = true
	)
	tests := []struct {
		name       string
		mutate     func(t *testing.T, fx *cacheFixture)
		sourceMiss bool // transpile cache (gen/)
		depsMiss   bool // dependency cache (deps/)
	}{
		// Inputs of the transpile key.
		{"gala source edited", func(t *testing.T, fx *cacheFixture) {
			fx.write(t, "main.gala", "package main\n\nfunc main() {\n    Println(\"bye\")\n}\n")
		}, miss, hit},
		{"gala source in a subpackage edited", func(t *testing.T, fx *cacheFixture) {
			fx.write(t, "util/util.gala", "package util\n\nfunc One() int = 2\n")
		}, miss, hit},
		{"gala source added", func(t *testing.T, fx *cacheFixture) {
			fx.write(t, "extra.gala", "package main\n")
		}, miss, hit},
		{"hand-written Go file edited", func(t *testing.T, fx *cacheFixture) {
			fx.write(t, "hook.go", "package main\n\nfunc init() {}\n")
		}, miss, hit},
		{"hand-written Go file in a subpackage edited", func(t *testing.T, fx *cacheFixture) {
			fx.write(t, "native/native.go", "package native\n\nconst X = 1\n")
		}, miss, hit},
		{"cgo header edited", func(t *testing.T, fx *cacheFixture) {
			fx.write(t, "native/native.h", "int y;\n")
		}, miss, hit},
		{"go.mod edited", func(t *testing.T, fx *cacheFixture) {
			fx.write(t, "go.mod", "module example.com/renamed\n")
		}, miss, hit},
		{"embedded file edited", func(t *testing.T, fx *cacheFixture) {
			fx.write(t, "greeting.txt", "goodbye")
		}, miss, hit},
		{"file embedded by a hand-written Go file edited", func(t *testing.T, fx *cacheFixture) {
			fx.write(t, "native/page.html", "<p>v2</p>")
		}, miss, hit},
		{"file added under an embedded glob", func(t *testing.T, fx *cacheFixture) {
			fx.write(t, "static/extra.css", "p{}")
		}, miss, hit},
		{"tree shape changed (subdirectory target)", func(t *testing.T, fx *cacheFixture) {
			fx.sourceDir = "cmd/app"
		}, miss, hit},

		// Inputs of both keys.
		{"gala.mod edited without touching requirements", func(t *testing.T, fx *cacheFixture) {
			fx.write(t, "gala.mod", "module example.com/app\n\ngala 0.0.1\n\n"+
				"require example.com/localdep v0.0.0\n\n"+
				"replace example.com/localdep => ../localdep\n")
		}, miss, hit},
		{"gala.mod requirement changed", func(t *testing.T, fx *cacheFixture) {
			fx.write(t, "gala.mod", "module example.com/app\n\ngala 0.0.0\n\n"+
				"require example.com/localdep v0.0.1\n\n"+
				"replace example.com/localdep => ../localdep\n")
		}, miss, miss},
		{"local-replace dependency source edited", func(t *testing.T, fx *cacheFixture) {
			fx.writeDep(t, "lib.gala", "package localdep\n\nfunc Message() string = \"v2\"\n")
		}, miss, miss},
		{"local-replace dependency asset edited", func(t *testing.T, fx *cacheFixture) {
			fx.writeDep(t, "asset.txt", "changed")
		}, miss, miss},
		{"local-replace dependency gala.mod edited", func(t *testing.T, fx *cacheFixture) {
			fx.writeDep(t, "gala.mod", "module example.com/localdep\n\ngala 0.0.1\n")
		}, miss, miss},
		{"stdlib snapshot changed", func(t *testing.T, fx *cacheFixture) {
			fx.toolchain.Stdlib = "stdlib-b"
		}, miss, miss},
		{"transpiler binary changed", func(t *testing.T, fx *cacheFixture) {
			fx.toolchain.Transpiler = "transpiler-b"
		}, miss, miss},
		{"Go SDK changed", func(t *testing.T, fx *cacheFixture) {
			fx.toolchain.GoSDK = "/go|go1.26.0"
		}, miss, miss},
		{"gala version changed", func(t *testing.T, fx *cacheFixture) {
			fx.toolchain.GalaVersion = "0.99.0"
		}, miss, miss},

		// Files neither step reads.
		{"README edited", func(t *testing.T, fx *cacheFixture) {
			fx.write(t, "README.md", "changed")
		}, hit, hit},
		{"binary in the project directory rewritten", func(t *testing.T, fx *cacheFixture) {
			fx.write(t, "app.exe", "a different binary")
		}, hit, hit},
		{"test source edited (not part of a build)", func(t *testing.T, fx *cacheFixture) {
			fx.write(t, "main_test.gala", "package main\n\nfunc TestX(t T) T = t\n")
		}, hit, hit},
		{"stale generated Go in the project edited", func(t *testing.T, fx *cacheFixture) {
			fx.write(t, "stale.gen.go", "package main\n\nvar x = 1\n")
		}, hit, hit},
		{"file in a hidden directory edited", func(t *testing.T, fx *cacheFixture) {
			fx.write(t, ".git/HEAD", "ref: refs/heads/other\n")
		}, hit, hit},
		{"testdata edited", func(t *testing.T, fx *cacheFixture) {
			fx.write(t, "testdata/case.gala", "package testdata\n\nval x = 1\n")
		}, hit, hit},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fx := newCacheFixture(t)
			sourceBefore, depsBefore := fx.keys(t)

			// Recomputing with nothing changed must hit, or every row below is
			// meaningless.
			sourceAgain, depsAgain := fx.keys(t)
			require.Equal(t, sourceBefore, sourceAgain, "transpile key is not stable")
			require.Equal(t, depsBefore, depsAgain, "dependency key is not stable")

			tt.mutate(t, fx)
			sourceAfter, depsAfter := fx.keys(t)

			require.Equal(t, tt.sourceMiss, sourceBefore != sourceAfter,
				"transpile cache: want miss=%v", tt.sourceMiss)
			require.Equal(t, tt.depsMiss, depsBefore != depsAfter,
				"dependency cache: want miss=%v", tt.depsMiss)
		})
	}
}

// An unreadable input must never produce a key a recorded one could match.
func TestCacheKeys_UnreadableInputNeverHits(t *testing.T) {
	fx := newCacheFixture(t)
	tc := fx.toolchain

	require.Empty(t, computeSourceHash([]string{filepath.Join(fx.projectDir, "absent.gala")}, tc, "", ""))

	deps := []depInput{{Dir: filepath.Join(fx.projectDir, "no-such-dep")}}
	require.Empty(t, computeDepsHash(nil, nil, tc, deps))
}

// The transpile key records the embed patterns of the transpile that produced
// gen/, so the next build can key on the assets they match before transpiling.
func TestSourceStamp_RoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), sourceStampName)

	writeSourceStamp(path, sourceStamp{Key: "abc", Embeds: []string{"a.txt", "static/*"}})
	got, ok := readSourceStamp(path)
	require.True(t, ok)
	require.Equal(t, sourceStamp{Key: "abc", Embeds: []string{"a.txt", "static/*"}}, got)

	writeSourceStamp(path, sourceStamp{Key: "def"})
	got, ok = readSourceStamp(path)
	require.True(t, ok)
	require.Equal(t, "def", got.Key)
	require.Empty(t, got.Embeds)

	_, ok = readSourceStamp(filepath.Join(t.TempDir(), "missing"))
	require.False(t, ok)
}

// Cleaning gen/ or deps/ removes the key that described it, so a build that
// fails half-way cannot leave a partial tree that the next build — with inputs
// the key still matches — trusts.
func TestCleaningATreeDropsItsKey(t *testing.T) {
	config := isolatedConfig(t)
	ws, err := NewWorkspace(config, t.TempDir(), ModeBuild)
	require.NoError(t, err)
	require.NoError(t, ws.Ensure())

	for _, tc := range []struct {
		stamp string
		clean func() error
	}{
		{sourceStampName, ws.CleanGen},
		{depsStampName, ws.CleanDeps},
	} {
		path := filepath.Join(ws.Dir, tc.stamp)
		require.NoError(t, os.WriteFile(path, []byte("key"), 0644))
		require.NoError(t, tc.clean())
		_, err := os.Stat(path)
		require.True(t, os.IsNotExist(err), "%s must be removed with its tree", tc.stamp)
	}
}

// The transpiler identity is the content of the running executable: stable
// within a process, and distinct from the version string an unstamped build
// shares with every other unstamped build.
func TestTranspilerIdentity_HashesTheRunningExecutable(t *testing.T) {
	exe, err := os.Executable()
	require.NoError(t, err)
	content, err := os.ReadFile(exe)
	require.NoError(t, err)
	sum := sha256.Sum256(content)

	require.Equal(t, hex.EncodeToString(sum[:]), transpilerIdentity())
	require.Equal(t, transpilerIdentity(), transpilerIdentity(), "computed once per process")
}

// The Go SDK identity names the SDK the analyzer resolves Go types from, and
// changes with its VERSION file.
func TestGoSDKIdentity_NamesTheAnalyzerSDK(t *testing.T) {
	root := analyzer.GoSDKRoot()
	if root == "" {
		require.Equal(t, "none", goSDKIdentity())
		return
	}
	id := goSDKIdentity()
	require.True(t, strings.HasPrefix(id, root+"|"), "identity %q must name the SDK root %q", id, root)
	if version, err := os.ReadFile(filepath.Join(root, "VERSION")); err == nil {
		require.Contains(t, id, strings.TrimSpace(string(version)))
	}
}

// The regression for an edit behind a local replace: the dependency transpile
// used to be keyed on the requirement list alone, so a second build served the
// dependency code generated from the first version of its source.
func TestTranspileDeps_RetranspilesAfterLocalReplaceEdit(t *testing.T) {
	fx := newCacheFixture(t)

	transpileDeps := func() string {
		t.Helper()
		b, err := NewBuilder(fx.projectDir, "test", false)
		require.NoError(t, err)
		require.NoError(t, b.workspace.Ensure())
		require.NoError(t, b.ensureStdlib())
		require.NoError(t, b.transpileDeps())
		gen, err := os.ReadFile(filepath.Join(b.workspace.DepModuleDir("example.com/localdep", "v0.0.0"), "lib.gen.go"))
		require.NoError(t, err)
		return string(gen)
	}

	require.Contains(t, transpileDeps(), `"v1"`)

	fx.writeDep(t, "lib.gala", "package localdep\n\nfunc Message() string = \"v2\"\n")
	second := transpileDeps()
	require.Contains(t, second, `"v2"`, "the edited dependency source must be re-transpiled")
	require.NotContains(t, second, `"v1"`)
}

// A //go:embed directive can list several patterns, quoted or not, with or
// without the "all:" prefix; each names files the key has to cover.
func TestExtractEmbedPatterns(t *testing.T) {
	tests := []struct {
		name string
		code string
		want []string
	}{
		{"single pattern", "//go:embed greeting.txt\nvar g string\n", []string{"greeting.txt"}},
		{"several patterns on one line", "//go:embed a.txt static/*\n", []string{"a.txt", "static/*"}},
		{"quoted patterns", "//go:embed \"a.txt\" `b.txt`\n", []string{"a.txt", "b.txt"}},
		{"all: prefix", "//go:embed all:static\n", []string{"static"}},
		{"indented directive", "\t//go:embed x.txt\n", []string{"x.txt"}},
		{"no directive", "// go:embed x.txt\nvar x string\n", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, extractEmbedPatterns(tt.code))
		})
	}
}
