package goexport

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// semverRE matches the canonical semantic versions the go command accepts as
// a module version: vMAJOR.MINOR.PATCH with an optional prerelease and no
// build metadata.
var semverRE = regexp.MustCompile(`^v(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(-[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?$`)

// ModuleVersion maps a GALA release version (0.86.0, 0.87.0-rc.1, or an already
// v-prefixed form) to the Go module version it is published as.
func ModuleVersion(galaVersion string) (string, error) {
	v := galaVersion
	if !strings.HasPrefix(v, "v") {
		v = "v" + v
	}
	if !semverRE.MatchString(v) {
		return "", fmt.Errorf("version %q is not a semantic version the go command accepts (want vX.Y.Z or vX.Y.Z-pre)", galaVersion)
	}
	return v, nil
}

// WriteProxy writes files as module version `version` of `modulePath` in the
// file layout GOPROXY=file://dir serves, so a Go consumer can resolve the
// export with go get / go mod tidy and no network. Existing versions in dir
// are kept and listed alongside the new one.
func WriteProxy(dir, modulePath, version string, files []File, at time.Time) error {
	if err := checkModulePath(modulePath); err != nil {
		return err
	}
	if !semverRE.MatchString(version) {
		return fmt.Errorf("version %q is not a canonical semantic version", version)
	}
	var goMod []byte
	for _, f := range files {
		if f.Path == "go.mod" {
			goMod = f.Content
		}
	}
	if goMod == nil {
		return fmt.Errorf("export has no go.mod")
	}

	vdir := filepath.Join(dir, filepath.FromSlash(escapePath(modulePath)), "@v")
	if err := os.MkdirAll(vdir, 0o755); err != nil {
		return err
	}
	zipData, err := moduleZip(modulePath, version, files, at)
	if err != nil {
		return err
	}
	info, err := json.Marshal(struct {
		Version string
		Time    string
	}{version, at.UTC().Format(time.RFC3339)})
	if err != nil {
		return err
	}
	for name, data := range map[string][]byte{
		version + ".info": info,
		version + ".mod":  goMod,
		version + ".zip":  zipData,
	} {
		if err := os.WriteFile(filepath.Join(vdir, name), data, 0o644); err != nil {
			return err
		}
	}
	return appendVersionList(filepath.Join(vdir, "list"), version)
}

// moduleZip builds the module zip the go command downloads: every file under
// the prefix module@version/, in the order given, with a fixed timestamp so
// the same export always produces the same bytes.
func moduleZip(modulePath, version string, files []File, at time.Time) ([]byte, error) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	prefix := modulePath + "@" + version + "/"
	for _, f := range files {
		w, err := zw.CreateHeader(&zip.FileHeader{Name: prefix + f.Path, Method: zip.Deflate, Modified: at.UTC()})
		if err != nil {
			return nil, err
		}
		if _, err := w.Write(f.Content); err != nil {
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
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

// escapePath applies the go command's case encoding for module paths in a
// proxy: each upper-case letter becomes '!' followed by its lower-case form.
func escapePath(p string) string {
	var b strings.Builder
	for _, r := range p {
		if 'A' <= r && r <= 'Z' {
			b.WriteByte('!')
			r += 'a' - 'A'
		}
		b.WriteRune(r)
	}
	return b.String()
}
