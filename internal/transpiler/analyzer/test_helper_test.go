package analyzer_test

import (
	"os"
	"path/filepath"

	"github.com/bazelbuild/rules_go/go/tools/bazel"
)

// getStdSearchPath returns the search path for the std package.
// In Bazel tests, it uses runfiles to find the std directory.
// Outside of Bazel, it falls back to finding go.mod and using the module root.
func getStdSearchPath() []string {
	// Try to find std in Bazel runfiles - use a known file to get the directory
	if stdFilePath, err := bazel.Runfile("std/option.gala"); err == nil {
		stdDir := filepath.Dir(stdFilePath)
		return []string{filepath.Dir(stdDir)} // Return parent of std dir
	}

	// Fallback: walk up to find go.mod (works when running outside Bazel)
	cwd, err := os.Getwd()
	if err != nil {
		return nil
	}

	dir := cwd
	for {
		modPath := filepath.Join(dir, "go.mod")
		if _, err := os.Stat(modPath); err == nil {
			return []string{dir}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}

	return nil
}
