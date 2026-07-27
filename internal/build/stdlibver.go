package build

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"martianoff/gala/internal/stdlib"
)

// stdlibMarkerName is the file written inside a versioned stdlib directory to
// record which embedded snapshot produced its contents.
const stdlibMarkerName = ".stdlib-extracted"

// UnsafeStdlibDirError reports that a computed stdlib version directory does
// not sit directly under the stdlib cache root and therefore must not be
// deleted. It guards the re-extraction path: a degenerate or hostile version
// string must never be able to turn cache invalidation into a recursive delete
// of the cache root or the user's home directory.
type UnsafeStdlibDirError struct {
	Dir  string // the rejected directory
	Root string // the stdlib cache root it was expected to live under
}

var _ error = (*UnsafeStdlibDirError)(nil)

func (e *UnsafeStdlibDirError) Error() string {
	return fmt.Sprintf("refusing to remove stdlib directory %q: not a version directory under %q", e.Dir, e.Root)
}

// normalizeStdlibVersion reduces a CLI version string to the base version used
// to name the stdlib cache directory: a `git describe` suffix such as
// "0.29.4-1-ga528ffd" collapses to "0.29.4". Every consumer of the stdlib cache
// path must apply the same normalization, otherwise `gala build` and
// `gala transpile`/`gala lsp` can extract to, and validate, two different
// directories for the same binary.
func normalizeStdlibVersion(version string) string {
	if i := strings.IndexByte(version, '-'); i >= 0 {
		return version[:i]
	}
	return version
}

// snapshotFingerprint returns the marker contents identifying the embedded
// stdlib snapshot for a given version. It combines the version with the content
// hash of the embedded packages, so both a version change and a change to the
// embedded sources invalidate a previously extracted copy.
func snapshotFingerprint(version string) string {
	h := sha256.New()
	for _, part := range []string{normalizeStdlibVersion(version), stdlib.Fingerprint()} {
		h.Write([]byte(strconv.Itoa(len(part))))
		h.Write([]byte{':'})
		h.Write([]byte(part))
	}
	return hex.EncodeToString(h.Sum(nil))
}

// checkStdlibVersionDir verifies that dir is a direct child of the stdlib cache
// root, i.e. a plausible version directory. Anything else — the root itself, a
// path escaping the root, a nested path — is rejected.
func (c *Config) checkStdlibVersionDir(dir string) error {
	root := filepath.Clean(c.StdlibDir)
	target := filepath.Clean(dir)
	fail := &UnsafeStdlibDirError{Dir: target, Root: root}

	// A degenerate root (unset, relative-current, or a filesystem/volume root
	// such as "/" or "C:\") can never be a legitimate stdlib cache.
	if c.StdlibDir == "" || root == "." || filepath.Dir(root) == root {
		return fail
	}
	if target == root {
		return fail
	}
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return fail
	}
	if rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fail
	}
	if strings.ContainsRune(rel, filepath.Separator) {
		return fail
	}
	return nil
}

// ensureStdlibExtracted makes the on-disk stdlib cache for `version` match the
// embedded snapshot and returns its directory.
//
// The marker file holds the snapshot fingerprint rather than acting as a bare
// "something was extracted here" flag, so a cache written by a different binary
// is detected instead of being trusted forever. When the fingerprints differ,
// the version directory is removed before re-extracting: ExtractTo overwrites
// files but never deletes them, so a file dropped upstream would otherwise
// linger and keep being resolved.
//
// extracted reports whether files were (re-)written, which callers use for
// verbose output.
func (c *Config) ensureStdlibExtracted(version string) (dir string, extracted bool, err error) {
	stdlibDir := c.StdlibVersionDir(version)
	markerPath := filepath.Join(stdlibDir, stdlibMarkerName)
	want := snapshotFingerprint(version)

	if got, readErr := os.ReadFile(markerPath); readErr == nil && string(got) == want {
		return stdlibDir, false, nil
	}

	// Stale or absent: drop whatever is there and write the snapshot afresh.
	if removeErr := c.removeStdlibVersionDir(stdlibDir); removeErr != nil {
		return "", false, removeErr
	}
	if mkErr := os.MkdirAll(stdlibDir, 0755); mkErr != nil {
		return "", false, fmt.Errorf("creating stdlib directory: %w", mkErr)
	}
	if extractErr := stdlib.ExtractTo(stdlibDir); extractErr != nil {
		return "", false, fmt.Errorf("extracting stdlib: %w", extractErr)
	}
	if writeErr := os.WriteFile(markerPath, []byte(want), 0644); writeErr != nil {
		return "", false, fmt.Errorf("writing stdlib marker: %w", writeErr)
	}
	return stdlibDir, true, nil
}

// removeStdlibVersionDir deletes a versioned stdlib directory after checking
// that it really is one.
func (c *Config) removeStdlibVersionDir(dir string) error {
	if err := c.checkStdlibVersionDir(dir); err != nil {
		return err
	}
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("removing stale stdlib directory: %w", err)
	}
	return nil
}
