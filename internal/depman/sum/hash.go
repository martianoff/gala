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

// Module hashes come in two schemes, told apart by their prefix.
//
//   - "h2:" covers the whole module tree as the fetch cache stores it: every
//     file except VCS metadata and gala's own bookkeeping (see
//     IsModuleContent). Data files a module needs at build time — `//go:embed`
//     assets, templates, fixtures — are part of what is verified.
//   - "h1:" is the earlier scheme. It covers only .gala and .go files,
//     gala.mod, go.sum and BUILD.bazel, outside hidden, vendor and testdata
//     directories, so it says nothing about a module's data files. HashDir no
//     longer produces it; Verify still checks it, so a gala.sum written by an
//     earlier gala stays valid until `gala mod tidy` rewrites the entry as h2.
const (
	hashPrefixH1 = "h1:"
	hashPrefixH2 = "h2:"
)

// vcsDirs are version-control metadata directories: how a module was
// delivered, not part of it.
var vcsDirs = map[string]bool{".git": true, ".hg": true, ".svn": true, ".bzr": true}

// bookkeepingPrefix marks files gala itself writes into a module directory,
// such as the fetch cache's completion marker. They are not module content.
const bookkeepingPrefix = ".gala-"

// IsModuleContent reports whether a directory entry named name is module
// content: what the fetch cache stores and what an h2 hash covers. Everything
// is, except VCS metadata directories and gala's bookkeeping files.
func IsModuleContent(name string, isDir bool) bool {
	if isDir {
		return !vcsDirs[name]
	}
	return !strings.HasPrefix(name, bookkeepingPrefix)
}

// HashDir computes the h2 hash of a module directory: every module-content
// file (see IsModuleContent), by relative path and content. It is
// deterministic regardless of file system ordering and of line endings.
func HashDir(dir string) (string, error) {
	return hashTree(dir, hashPrefixH2,
		func(name string) bool { return !IsModuleContent(name, true) },
		func(name string) bool { return IsModuleContent(name, false) })
}

// hashDirH1 computes the legacy h1 hash (see hashPrefixH1).
func hashDirH1(dir string) (string, error) {
	return hashTree(dir, hashPrefixH1,
		func(name string) bool {
			return strings.HasPrefix(name, ".") || name == "vendor" || name == "testdata"
		},
		func(name string) bool {
			ext := filepath.Ext(name)
			return ext == ".gala" || ext == ".go" || name == "gala.mod" || name == "go.sum" || name == "BUILD.bazel"
		})
}

// hashTree hashes the files under dir that include accepts, skipping the
// directories skipDir names. Each file contributes its slash-separated
// relative path and its content with line endings normalized.
func hashTree(dir, prefix string, skipDir, include func(name string) bool) (string, error) {
	var files []string
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			if path != dir && skipDir(info.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if !include(info.Name()) {
			return nil
		}
		relPath, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		files = append(files, relPath)
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("failed to walk directory: %w", err)
	}

	// Sort for determinism
	sort.Strings(files)

	h := sha256.New()
	for _, relPath := range files {
		h.Write([]byte(filepath.ToSlash(relPath)))
		h.Write([]byte{0}) // null separator

		content, err := os.ReadFile(filepath.Join(dir, relPath))
		if err != nil {
			return "", fmt.Errorf("failed to read file %s: %w", relPath, err)
		}
		h.Write(normalizeLineEndings(content))
		h.Write([]byte{0}) // null separator
	}

	return prefix + base64.StdEncoding.EncodeToString(h.Sum(nil)), nil
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

// Verify checks if a directory's hash matches the expected hash, computed with
// the scheme the expected hash names.
func Verify(dir, expected string) error {
	var actual string
	var err error
	switch {
	case strings.HasPrefix(expected, hashPrefixH2):
		actual, err = HashDir(dir)
	case strings.HasPrefix(expected, hashPrefixH1):
		actual, err = hashDirH1(dir)
	default:
		return fmt.Errorf("unsupported hash scheme in %q: expected h1: or h2:", expected)
	}
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
