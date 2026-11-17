package build

import (
	"path"
	"path/filepath"
	"strconv"
	"strings"
)

// The //line directives of the generated Go name each .gala file, and the Go
// toolchain copies that name into the binary. -trimpath (see workspaceGoCmd)
// does not reach the .gala files, which live outside the workspace, so while
// it is on the build names them the way -trimpath names a Go file instead of
// by their host paths.

// trimPathEnabled reports whether the workspace's `go` commands run with
// -trimpath, given the user's GOFLAGS: workspaceGoCmd puts -trimpath ahead of
// them, so it is on unless a later -trimpath=<false> turns it off.
func trimPathEnabled(goflags string) bool {
	trim := true
	for _, flag := range strings.Fields(goflags) {
		// Like the flag package, accept -name and --name.
		name, value, hasValue := strings.Cut(strings.TrimPrefix(flag, "-"), "=")
		name = strings.TrimPrefix(name, "-")
		if !strings.HasPrefix(flag, "-") || name != "trimpath" {
			continue
		}
		if !hasValue {
			trim = true
		} else if b, err := strconv.ParseBool(value); err == nil {
			trim = b
		}
	}
	return trim
}

// lineFileFor returns the name the //line directives give file, a .gala source
// of the module rooted at moduleDir, under -trimpath: root, the name the
// module goes by (its import path, with @version for a module-cache copy, or
// "" for a project without a gala.mod), joined with the file's path inside
// the module.
func lineFileFor(file, moduleDir, root string) string {
	rel, err := filepath.Rel(moduleDir, file)
	if err != nil || !filepath.IsLocal(rel) {
		// Not under the module (the build reads none such): only the host
		// path names it without colliding with a file that is.
		return file
	}
	return path.Join(root, filepath.ToSlash(rel))
}
