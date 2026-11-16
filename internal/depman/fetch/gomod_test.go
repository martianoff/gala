package fetch

import (
	"archive/zip"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// goProxyModule is one module version a file:// GOPROXY fixture serves.
type goProxyModule struct {
	path, version string
	files         map[string]string // file name -> content, go.mod included
}

// newGoProxy writes a GOPROXY directory serving mods (see `go help
// goproxy`). A module path listed in untagged has no tagged version: its
// @v/list is empty and @latest names its version, as proxy.golang.org serves
// a repository with no tag. It returns the GOPROXY URL.
func newGoProxy(t *testing.T, mods []goProxyModule, untagged map[string]bool) string {
	t.Helper()
	root := t.TempDir()
	lists := map[string][]string{}
	for _, m := range mods {
		dir := filepath.Join(root, filepath.FromSlash(m.path), "@v")
		require.NoError(t, os.MkdirAll(dir, 0755))
		info, err := json.Marshal(map[string]string{"Version": m.version, "Time": "2022-06-03T01:37:15Z"})
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filepath.Join(dir, m.version+".info"), info, 0644))
		require.NoError(t, os.WriteFile(filepath.Join(dir, m.version+".mod"), []byte(m.files["go.mod"]), 0644))
		zf, err := os.Create(filepath.Join(dir, m.version+".zip"))
		require.NoError(t, err)
		zw := zip.NewWriter(zf)
		for name, content := range m.files {
			w, err := zw.Create(m.path + "@" + m.version + "/" + name)
			require.NoError(t, err)
			_, err = w.Write([]byte(content))
			require.NoError(t, err)
		}
		require.NoError(t, zw.Close())
		require.NoError(t, zf.Close())
		if untagged[m.path] {
			require.NoError(t, os.WriteFile(filepath.Join(root, filepath.FromSlash(m.path), "@latest"), info, 0644))
			lists[m.path] = nil
		} else {
			lists[m.path] = append(lists[m.path], m.version)
		}
	}
	for path, versions := range lists {
		list := strings.Join(versions, "\n")
		if list != "" {
			list += "\n"
		}
		require.NoError(t, os.WriteFile(filepath.Join(root, filepath.FromSlash(path), "@v", "list"), []byte(list), 0644))
	}
	url := filepath.ToSlash(root)
	if !strings.HasPrefix(url, "/") {
		url = "/" + url // a Windows drive path: file:///C:/...
	}
	return "file://" + url
}

// hermeticGo points the go command at goProxy alone, with no checksum
// database, and returns a fresh Go module cache, removed after the test.
func hermeticGo(t *testing.T, goProxy string) string {
	t.Helper()
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not on PATH")
	}
	t.Setenv("GOPROXY", goProxy)
	t.Setenv("GOSUMDB", "off")
	t.Setenv("GOPRIVATE", "")
	t.Setenv("GONOPROXY", "")
	t.Setenv("GONOSUMDB", "")
	t.Setenv("GOFLAGS", "-modcacherw")
	t.Setenv("GOTOOLCHAIN", "local")
	return filepath.Join(t.TempDir(), "go", "pkg", "mod")
}

const goqPseudoVersion = "v0.0.0-20220603013715-3360bf22bea4"

var goqModule = goProxyModule{
	path:    "example.com/goq",
	version: goqPseudoVersion,
	files: map[string]string{
		"go.mod": "module example.com/goq\n\ngo 1.18\n",
		"goq.go": "package goq\n",
	},
}

// goqCache returns a fresh Go module cache whose go command reads from a
// proxy serving goqModule, a module with no tagged version.
func goqCache(t *testing.T) string {
	t.Helper()
	return hermeticGo(t, newGoProxy(t, []goProxyModule{goqModule}, map[string]bool{goqModule.path: true}))
}

// A Go module with no tagged version resolves @latest to the pseudo-version
// of its latest commit, as `go get` does, and is downloaded into the Go
// module cache the build reads.
func TestDownloadGoModule_UntaggedLatest(t *testing.T) {
	goModCache := goqCache(t)

	m, err := DownloadGoModule(goModCache, "example.com/goq", "latest")
	require.NoError(t, err)
	assert.Equal(t, goqPseudoVersion, m.Version)
	assert.Equal(t, filepath.Join(goModCache, "example.com", "goq@"+goqPseudoVersion), m.Dir)
	assert.FileExists(t, filepath.Join(m.Dir, "goq.go"))
}

// A pseudo-version downloads as given.
func TestDownloadGoModule_PseudoVersion(t *testing.T) {
	m, err := DownloadGoModule(goqCache(t), "example.com/goq", goqPseudoVersion)
	require.NoError(t, err)
	assert.Equal(t, goqPseudoVersion, m.Version)
}

// A tagged module resolves @latest to its highest tag.
func TestDownloadGoModule_TaggedLatest(t *testing.T) {
	mods := []goProxyModule{
		{path: "example.com/lib", version: "v1.0.0", files: map[string]string{"go.mod": "module example.com/lib\n", "lib.go": "package lib\n"}},
		{path: "example.com/lib", version: "v1.2.0", files: map[string]string{"go.mod": "module example.com/lib\n", "lib.go": "package lib\n"}},
	}
	goModCache := hermeticGo(t, newGoProxy(t, mods, nil))

	m, err := DownloadGoModule(goModCache, "example.com/lib", "latest")
	require.NoError(t, err)
	assert.Equal(t, "v1.2.0", m.Version)
}

// The download runs outside any module: a go.mod around the Go module cache
// (a GALA_HOME inside some project) or a GOFLAGS -modfile/-mod of the user's
// does not reach it. The go command walks up from its working directory to a
// go.mod, and this one is not a valid go.mod, so running in the module cache
// failed.
func TestDownloadGoModule_OutsideAnyModule(t *testing.T) {
	goModCache := goqCache(t)
	around := filepath.Dir(filepath.Dir(filepath.Dir(goModCache))) // above <home>/go/pkg/mod
	require.NoError(t, os.WriteFile(filepath.Join(around, "go.mod"), []byte("this is not a go.mod\n"), 0644))
	require.NoError(t, os.MkdirAll(goModCache, 0755))
	t.Setenv("GOFLAGS", "-modcacherw -mod=vendor -modfile="+filepath.Join(around, "missing.mod"))

	m, err := DownloadGoModule(goModCache, "example.com/goq", "latest")
	require.NoError(t, err)
	assert.Equal(t, goqPseudoVersion, m.Version)
}

// A version the module does not have is an error naming it.
func TestDownloadGoModule_UnknownVersion(t *testing.T) {
	_, err := DownloadGoModule(goqCache(t), "example.com/goq", "v9.9.9")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "v9.9.9")
}
