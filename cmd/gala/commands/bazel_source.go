package commands

import (
	"path/filepath"
	"strings"

	"martianoff/gala/internal/transpiler/transformer"
)

// bazelSourceOptions are the transformer options for the source file at path,
// as a Bazel action names it: relative to the execution root. A file of
// another Bazel repository is a dependency module's (transformer.ForDependency);
// Bazel places those under external/, or, with the sibling repository layout,
// beside the main repository (../). Every other file is the module being built.
func bazelSourceOptions(path string) []transformer.Option {
	if inExternalRepository(path) {
		return []transformer.Option{transformer.ForDependency()}
	}
	return nil
}

// inExternalRepository reports whether path, relative to a Bazel execution
// root, is a file of another repository than the main one.
func inExternalRepository(path string) bool {
	if filepath.IsAbs(path) {
		return false
	}
	p := filepath.ToSlash(filepath.Clean(path))
	return strings.HasPrefix(p, "external/") || strings.HasPrefix(p, "../")
}
