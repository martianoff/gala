package sum

import (
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// HashDir computes a hash of all GALA source files in a directory.
// It hashes all .gala files and the gala.mod file if present.
// The hash is deterministic regardless of file system ordering.
func HashDir(dir string) (string, error) {
	h := sha256.New()

	// Collect all relevant files
	var files []string
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			// Skip hidden directories and common non-source dirs
			name := info.Name()
			if strings.HasPrefix(name, ".") || name == "vendor" || name == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}

		// Include .gala files and gala.mod
		ext := filepath.Ext(path)
		name := info.Name()
		if ext == ".gala" || name == "gala.mod" || name == "BUILD.bazel" {
			relPath, err := filepath.Rel(dir, path)
			if err != nil {
				return err
			}
			files = append(files, relPath)
		}
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("failed to walk directory: %w", err)
	}

	// Sort for determinism
	sort.Strings(files)

	// Hash each file's path and content
	for _, relPath := range files {
		fullPath := filepath.Join(dir, relPath)

		// Write file path (normalized to forward slashes)
		normalizedPath := filepath.ToSlash(relPath)
		h.Write([]byte(normalizedPath))
		h.Write([]byte{0}) // null separator

		// Write file content
		content, err := os.ReadFile(fullPath)
		if err != nil {
			return "", fmt.Errorf("failed to read file %s: %w", relPath, err)
		}
		// Normalize line endings
		content = normalizeLineEndings(content)
		h.Write(content)
		h.Write([]byte{0}) // null separator
	}

	// Encode as base64
	sum := h.Sum(nil)
	encoded := base64.StdEncoding.EncodeToString(sum)

	return "h1:" + encoded, nil
}

// HashFile computes a hash of a single file.
func HashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("failed to open file: %w", err)
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", fmt.Errorf("failed to hash file: %w", err)
	}

	sum := h.Sum(nil)
	encoded := base64.StdEncoding.EncodeToString(sum)

	return "h1:" + encoded, nil
}

// HashGalaMod computes a hash of just the gala.mod file in a directory.
func HashGalaMod(dir string) (string, error) {
	modPath := filepath.Join(dir, "gala.mod")
	return HashFile(modPath)
}

// Verify checks if a directory's hash matches the expected hash.
func Verify(dir, expected string) error {
	actual, err := HashDir(dir)
	if err != nil {
		return err
	}

	if actual != expected {
		return &HashMismatchError{
			Path:     dir,
			Expected: expected,
			Actual:   actual,
		}
	}

	return nil
}

// HashMismatchError is returned when a hash verification fails.
type HashMismatchError struct {
	Path     string
	Expected string
	Actual   string
}

func (e *HashMismatchError) Error() string {
	return fmt.Sprintf("hash mismatch for %s: expected %s, got %s", e.Path, e.Expected, e.Actual)
}

// normalizeLineEndings converts all line endings to LF for consistent hashing.
func normalizeLineEndings(data []byte) []byte {
	// Replace CRLF with LF
	result := make([]byte, 0, len(data))
	for i := 0; i < len(data); i++ {
		if data[i] == '\r' {
			if i+1 < len(data) && data[i+1] == '\n' {
				// Skip CR in CRLF
				continue
			}
			// Standalone CR becomes LF
			result = append(result, '\n')
		} else {
			result = append(result, data[i])
		}
	}
	return result
}
