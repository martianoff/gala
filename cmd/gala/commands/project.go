package commands

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// findProjectRoot resolves the project root directory from a given path argument.
// If the argument is a file, its directory is used as the starting point.
// The function walks up the directory tree looking for gala.mod, similar to
// how `go` finds go.mod.
//
// Returns the absolute path to the directory containing gala.mod, or an error message.
func findProjectRoot(pathArg string) (string, error) {
	absPath, err := filepath.Abs(pathArg)
	if err != nil {
		return "", err
	}

	// If the path points to a file, start from its directory
	info, err := os.Stat(absPath)
	if err == nil && !info.IsDir() {
		absPath = filepath.Dir(absPath)
	} else if err != nil {
		// Path doesn't exist — might be a file that hasn't been created yet.
		// If it looks like a file (has extension), use parent dir.
		if strings.Contains(filepath.Base(absPath), ".") {
			absPath = filepath.Dir(absPath)
		}
	}

	// Walk up looking for gala.mod
	dir := absPath
	for {
		galaModPath := filepath.Join(dir, "gala.mod")
		if _, err := os.Stat(galaModPath); err == nil {
			return dir, nil
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			// Reached filesystem root without finding gala.mod
			break
		}
		dir = parent
	}

	return "", &projectNotFoundError{startDir: absPath}
}

type projectNotFoundError struct {
	startDir string
}

func (e *projectNotFoundError) Error() string {
	return "gala.mod not found in " + e.startDir + " or any parent directory"
}

// resolveRunTarget is a variant of findProjectRoot tailored to `gala run`. It
// matches `go run main.go` semantics: when the user points at a single .gala
// file and there is no gala.mod anywhere up the tree, fall back to an ephemeral
// module rooted at the file's containing directory. This removes the first-30-
// minute "gala.mod not found" wall for toy programs while leaving existing
// project-resolution behavior untouched for directories and files inside real
// modules.
//
// Returns (projectRoot, ephemeral, error). When ephemeral is true, the caller
// should expect the Builder to synthesize an in-memory gala.mod for this run.
func resolveRunTarget(pathArg string) (string, bool, error) {
	root, err := findProjectRoot(pathArg)
	if err == nil {
		return root, false, nil
	}

	// Only fall back to ephemeral mode when the specific failure is "no
	// gala.mod found". Any other error (permission, invalid path, etc.)
	// should propagate unchanged.
	var notFound *projectNotFoundError
	if !errors.As(err, &notFound) {
		return "", false, err
	}

	// Ephemeral mode is only safe for a single concrete .gala file. For
	// directory arguments, "no gala.mod" remains a hard error — the user
	// likely forgot `gala mod init` for a real project, and silently treating
	// a whole directory as an ephemeral module would hide that.
	absPath, absErr := filepath.Abs(pathArg)
	if absErr != nil {
		return "", false, err
	}
	info, statErr := os.Stat(absPath)
	if statErr != nil || info.IsDir() {
		return "", false, err
	}
	if !strings.HasSuffix(strings.ToLower(absPath), ".gala") {
		return "", false, err
	}

	return filepath.Dir(absPath), true, nil
}
