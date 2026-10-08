package goexport

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/mod/module"
	"golang.org/x/mod/semver"
	"golang.org/x/mod/zip"
)

// ModuleVersion maps a GALA release version (0.86.0, 0.87.0-rc.1, or an
// already v-prefixed form) to the Go module version it is published as. The
// result must be a canonical semantic version, the only kind a published Go
// module version can be.
func ModuleVersion(galaVersion string) (string, error) {
	v := "v" + strings.TrimPrefix(galaVersion, "v")
	if semver.Canonical(v) != v {
		return "", fmt.Errorf("version %q is not a semantic version the go command accepts (want X.Y.Z or X.Y.Z-pre)", galaVersion)
	}
	return v, nil
}

// proxyTime is the timestamp recorded for every version written to a proxy,
// so the same export always produces the same bytes.
var proxyTime = time.Unix(0, 0).UTC()

// WriteProxy writes files as version `version` of module `modulePath` in the
// file layout GOPROXY=file://dir serves, so a Go consumer can resolve the
// export with go get / go mod tidy and no network. Versions already in dir
// are kept and listed alongside the new one.
func WriteProxy(dir, modulePath, version string, files []File) error {
	mv := module.Version{Path: modulePath, Version: version}
	escaped, err := module.EscapePath(modulePath)
	if err != nil {
		return err
	}
	var goMod []byte
	zipFiles := make([]zip.File, 0, len(files))
	for _, f := range files {
		if f.Path == "go.mod" {
			goMod = f.Content
		}
		zipFiles = append(zipFiles, memFile{f})
	}
	if goMod == nil {
		return fmt.Errorf("export has no go.mod")
	}
	// zip.Create applies the go command's rules for module zips (file names,
	// case-insensitive collisions, size limits), so an export the proxy
	// would reject fails here too.
	var zipData bytes.Buffer
	if err := zip.Create(&zipData, mv, zipFiles); err != nil {
		return err
	}
	info, err := json.Marshal(struct{ Version, Time string }{version, proxyTime.Format(time.RFC3339)})
	if err != nil {
		return err
	}

	vdir := filepath.Join(dir, filepath.FromSlash(escaped), "@v")
	if err := os.MkdirAll(vdir, 0o755); err != nil {
		return err
	}
	for name, data := range map[string][]byte{
		version + ".info": info,
		version + ".mod":  goMod,
		version + ".zip":  zipData.Bytes(),
	} {
		if err := os.WriteFile(filepath.Join(vdir, name), data, 0o644); err != nil {
			return err
		}
	}
	return appendVersionList(filepath.Join(vdir, "list"), version)
}

func appendVersionList(listPath, version string) error {
	existing, err := os.ReadFile(listPath)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	for _, line := range strings.Split(string(existing), "\n") {
		if line == version {
			return nil
		}
	}
	return os.WriteFile(listPath, append(existing, []byte(version+"\n")...), 0o644)
}

// memFile adapts an export File to zip.File.
type memFile struct{ f File }

var _ zip.File = memFile{}

func (m memFile) Path() string                { return m.f.Path }
func (m memFile) Lstat() (os.FileInfo, error) { return memInfo(m), nil }
func (m memFile) Open() (io.ReadCloser, error) {
	return io.NopCloser(bytes.NewReader(m.f.Content)), nil
}

// memInfo is the os.FileInfo zip.Create needs: a regular file's size.
type memInfo memFile

func (i memInfo) Name() string       { return path.Base(i.f.Path) }
func (i memInfo) Size() int64        { return int64(len(i.f.Content)) }
func (i memInfo) Mode() os.FileMode  { return 0o644 }
func (i memInfo) ModTime() time.Time { return proxyTime }
func (i memInfo) IsDir() bool        { return false }
func (i memInfo) Sys() any           { return nil }
