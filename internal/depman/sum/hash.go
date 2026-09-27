package sum

import (
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"hash"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Module hashes come in two schemes, told apart by their prefix.
//
//   - "h2:" covers the whole module tree as the fetch cache stores it: every
//     regular file except VCS metadata and gala's own bookkeeping (see
//     IsModuleContent). Data files a module needs at build time — `//go:embed`
//     assets, templates, fixtures — are part of what is verified, so their
//     bytes are hashed as they are, and files are ordered by their
//     slash-separated path so the hash is the same on every OS. Symbolic
//     links are not module content: the fetch cache does not store them.
//   - "h1:" is the earlier scheme. It covers only .gala and .go files,
//     gala.mod, go.sum and BUILD.bazel, outside hidden, vendor and testdata
//     directories, with line endings normalized, so it says nothing about a
//     module's data files. HashDir no longer produces it; Verify still checks
//     it, so a gala.sum written by an earlier gala stays valid until `gala mod
//     tidy` rewrites the entry as h2.
//
// Single-file hashes (HashFile, the "/gala.mod" lines of gala.sum) keep the
// h1 prefix: what they cover did not change.
//
// Any other "h<digits>:" prefix is accepted by the gala.sum parser, so a
// gala.sum written by a later gala with a newer scheme still parses; Verify
// reports such a scheme as unsupported instead.
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

// isHashScheme reports whether hash starts with a scheme prefix "h<digits>:".
// Only h1 and h2 can be verified, but any scheme parses, so a gala.sum a later
// gala wrote with a newer scheme is read — and rewritten — without losing its
// entries.
func isHashScheme(hash string) bool {
	rest, ok := strings.CutPrefix(hash, "h")
	if !ok {
		return false
	}
	digits, _, ok := strings.Cut(rest, ":")
	if !ok || digits == "" {
		return false
	}
	for _, r := range digits {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// WalkModuleFiles calls visit for every module-content file under dir (see
// IsModuleContent) with its path and its slash-separated path relative to
// dir. Symbolic links are skipped. It is the one definition of a module's
// files: the fetch cache stores what it visits and HashDir hashes it.
func WalkModuleFiles(dir string, visit func(path, rel string) error) error {
	return filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil
		}
		if info.IsDir() {
			if path != dir && !IsModuleContent(info.Name(), true) {
				return filepath.SkipDir
			}
			return nil
		}
		if !IsModuleContent(info.Name(), false) {
			return nil
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		return visit(path, filepath.ToSlash(rel))
	})
}

// HashDir computes the h2 hash of a module directory (see hashPrefixH2).
func HashDir(dir string) (string, error) {
	var files []string
	if err := WalkModuleFiles(dir, func(_, rel string) error {
		files = append(files, rel)
		return nil
	}); err != nil {
		return "", fmt.Errorf("failed to walk directory: %w", err)
	}
	sort.Strings(files)

	h := sha256.New()
	for _, rel := range files {
		content, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
		if err != nil {
			return "", fmt.Errorf("failed to read file %s: %w", rel, err)
		}
		writeHashEntry(h, rel, content)
	}
	return hashPrefixH2 + base64.StdEncoding.EncodeToString(h.Sum(nil)), nil
}

// hashDirH1 computes the legacy h1 hash (see hashPrefixH1). It is the h1-era
// HashDir, kept as it was: it must keep producing exactly what earlier gala
// versions recorded, including their OS-native ordering and line-ending
// normalization.
func hashDirH1(dir string) (string, error) {
	var files []string
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		name := info.Name()
		if info.IsDir() {
			if path != dir && (strings.HasPrefix(name, ".") || name == "vendor" || name == "testdata") {
				return filepath.SkipDir
			}
			return nil
		}
		ext := filepath.Ext(name)
		if ext != ".gala" && ext != ".go" && name != "gala.mod" && name != "go.sum" && name != "BUILD.bazel" {
			return nil
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		files = append(files, rel)
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("failed to walk directory: %w", err)
	}
	sort.Strings(files)

	h := sha256.New()
	for _, rel := range files {
		content, err := os.ReadFile(filepath.Join(dir, rel))
		if err != nil {
			return "", fmt.Errorf("failed to read file %s: %w", rel, err)
		}
		writeHashEntry(h, filepath.ToSlash(rel), normalizeLineEndings(content))
	}
	return hashPrefixH1 + base64.StdEncoding.EncodeToString(h.Sum(nil)), nil
}

// writeHashEntry writes one file's slash-separated path and content to h,
// each followed by a NUL separator.
func writeHashEntry(h hash.Hash, rel string, content []byte) {
	h.Write([]byte(rel))
	h.Write([]byte{0})
	h.Write(content)
	h.Write([]byte{0})
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
