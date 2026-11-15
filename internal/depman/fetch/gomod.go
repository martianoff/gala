package fetch

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// GoModCacheDir returns the Go module cache (GOMODCACHE) GALA builds use for
// a GALA home: <galaHome>/go/pkg/mod.
func GoModCacheDir(galaHome string) string {
	return filepath.Join(galaHome, "go", "pkg", "mod")
}

// GoModDownloadCmd returns a `go mod download args...` command that downloads
// into the Go module cache goModCache. It runs outside any module, so a
// go.mod or go.work around the working directory cannot change what is
// downloaded. goModCache must exist.
func GoModDownloadCmd(goModCache string, args ...string) *exec.Cmd {
	cmd := exec.Command("go", append([]string{"mod", "download"}, args...)...)
	cmd.Dir = goModCache
	cmd.Env = append(os.Environ(), "GOMODCACHE="+goModCache, "GOWORK=off")
	return cmd
}

// GoModule is a module version `go mod download -json` reports.
type GoModule struct {
	Path    string
	Version string
	Error   string
	Dir     string
}

// DownloadGoModule resolves modulePath@query with the Go toolchain, as `go
// get` does, and downloads that version into the Go module cache goModCache.
// query is a version, a pseudo-version, or a query such as "latest" (the
// default when empty), so a module with no tagged version resolves to the
// pseudo-version of its latest commit. Go reads the module from GOPROXY (or
// its repository, for GOPRIVATE and GOPROXY=direct) and checks it against
// the checksum database, so vanity import paths (golang.org/x/text) and
// major-version suffixes resolve as they do for Go.
func DownloadGoModule(goModCache, modulePath, query string) (*GoModule, error) {
	if query == "" {
		query = "latest"
	}
	if err := os.MkdirAll(goModCache, 0755); err != nil {
		return nil, err
	}
	cmd := GoModDownloadCmd(goModCache, "-json", modulePath+"@"+query)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	runErr := cmd.Run()
	var m GoModule
	if err := json.Unmarshal(stdout.Bytes(), &m); err != nil {
		if runErr != nil {
			return nil, fmt.Errorf("go mod download %s@%s: %w: %s", modulePath, query, runErr, strings.TrimSpace(stderr.String()))
		}
		return nil, fmt.Errorf("go mod download %s@%s: cannot read its output: %w", modulePath, query, err)
	}
	if m.Error != "" {
		return nil, fmt.Errorf("%s", m.Error)
	}
	if runErr != nil {
		return nil, fmt.Errorf("go mod download %s@%s: %w: %s", modulePath, query, runErr, strings.TrimSpace(stderr.String()))
	}
	return &m, nil
}
