package commands

import (
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
