package build

import (
	"fmt"
	"os"
	"path/filepath"
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
// stdlib snapshot for a given version: the normalized version followed by the
// content hash of the embedded packages, so both a version change and a change
// to the embedded sources invalidate a previously extracted copy.
//
// The marker is only ever compared for equality, so it is stored verbatim
// rather than digested again — reading it tells whoever is debugging a cache
// which binary wrote the directory. The two parts cannot run together
// ambiguously because the fingerprint is a fixed-width hex digest.
func snapshotFingerprint(version string) string {
	return normalizeStdlibVersion(version) + " " + stdlib.Fingerprint()
}

// checkStdlibVersionDir verifies that dir is a direct child of the stdlib cache
// root, i.e. a plausible version directory. Anything else — the root itself, a
// path escaping the root, a nested path — is rejected.
func (c *Config) checkStdlibVersionDir(dir string) error {
	root := filepath.Clean(c.StdlibDir)
	target := filepath.Clean(dir)
	fail := &UnsafeStdlibDirError{Dir: target, Root: root}

	// A degenerate root can never be a legitimate stdlib cache. Each of these
	// is its own parent: unset (Clean("") == "."), relative-current, and a
	// filesystem or volume root such as "/" or "C:\".
	if filepath.Dir(root) == root {
		return fail
	}

	// The target must be one named element directly inside the root: not the
	// root itself (Rel reports "."), not at or above it (".." — and on Windows
	// Rel matches case-insensitively, so a differently-cased spelling of the
	// root lands here too), and not nested deeper (any separator).
	rel, err := filepath.Rel(root, target)
	if err != nil || rel == "." || rel == ".." || strings.ContainsRune(rel, filepath.Separator) {
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
//
// There is no cross-process lock. Two binaries that normalize to the same
// version but carry different snapshots — two unstamped "dev" builds from
// different trees — will each wipe and rewrite the other's copy, and a third
// process reading the directory at that moment can see it half-written.
// Released binaries carry distinct versions and so distinct directories, and a
// developer's CLI and LSP come from one tree, so this is narrow; it is also
// noisy and self-correcting, unlike the silent stale cache it replaces.
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
