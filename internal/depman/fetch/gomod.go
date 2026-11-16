package fetch

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
)

// GoModCacheDir returns the Go module cache (GOMODCACHE) GALA builds use for
// a GALA home: <galaHome>/go/pkg/mod.
func GoModCacheDir(galaHome string) string {
	return filepath.Join(galaHome, "go", "pkg", "mod")
}

// GoModDownload runs `go mod download args...`, downloading into the Go
// module cache goModCache, with its output going to stdout and stderr. It
// runs outside any module: in a new empty directory (the go command looks for
// a go.mod in every parent of its working directory, so the module cache's
// own directory is inside any module around it), with GOWORK=off and without
// the -mod and -modfile flags GOFLAGS may set. So no go.mod, go.work or
// module flag of the user's can change what is downloaded.
func GoModDownload(goModCache string, stdout, stderr io.Writer, args ...string) error {
	dir, err := os.MkdirTemp("", "gala-go-mod-download-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	cmd := exec.Command("go", append([]string{"mod", "download"}, args...)...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOMODCACHE="+goModCache, "GOWORK=off", "GOFLAGS="+withoutModFlags(os.Getenv("GOFLAGS")))
	cmd.Stdout, cmd.Stderr = stdout, stderr
	return cmd.Run()
}

// withoutModFlags returns goflags, a GOFLAGS value, without its -mod and
// -modfile flags, which select or redirect a main module.
func withoutModFlags(goflags string) string {
	flags := strings.Fields(goflags)
	flags = slices.DeleteFunc(flags, func(f string) bool {
		name, _, _ := strings.Cut(strings.TrimLeft(f, "-"), "=")
		return name == "mod" || name == "modfile"
	})
	return strings.Join(flags, " ")
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
// query is a version, a pseudo-version, or a query such as "latest", so a
// module with no tagged version resolves to the pseudo-version of its latest
// commit. Go reads the module from GOPROXY (or its repository, for GOPRIVATE
// and GOPROXY=direct) and checks it against the checksum database, so vanity
// import paths (golang.org/x/text) and major-version suffixes resolve as they
// do for Go.
func DownloadGoModule(goModCache, modulePath, query string) (*GoModule, error) {
	if err := os.MkdirAll(goModCache, 0755); err != nil {
		return nil, err
	}
	var stdout, stderr bytes.Buffer
	runErr := GoModDownload(goModCache, &stdout, &stderr, "-json", modulePath+"@"+query)
	// On failure the JSON, when there is any, says why in Error.
	var m GoModule
	jsonErr := json.Unmarshal(stdout.Bytes(), &m)
	switch {
	case jsonErr == nil && m.Error != "":
		return nil, errors.New(m.Error)
	case runErr != nil:
		return nil, fmt.Errorf("go mod download %s@%s: %w: %s", modulePath, query, runErr, strings.TrimSpace(stderr.String()))
	case jsonErr != nil:
		return nil, fmt.Errorf("go mod download %s@%s: cannot read its output: %w", modulePath, query, jsonErr)
	}
	return &m, nil
}
