package build

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

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
// Re-extraction is serialized on the stdlib cache root and installs a fully
// staged tree with a single rename, so two binaries that normalize to the same
// version but carry different snapshots — two unstamped "dev" builds from
// different trees — no longer wipe and rewrite each other's copy. They will
// still take turns replacing it, which is noisy and self-correcting; what is
// gone is the window where a third process reading the directory as a
// transpiler search path saw it half-written.
func (c *Config) ensureStdlibExtracted(version string) (dir string, extracted bool, err error) {
	stdlibDir := c.StdlibVersionDir(version)
	markerPath := filepath.Join(stdlibDir, stdlibMarkerName)
	want := snapshotFingerprint(version)

	// Fast path, taken without any lock: a matching marker means some process
	// finished a complete extraction, and the directory is read-only from here.
	if got, readErr := os.ReadFile(markerPath); readErr == nil && string(got) == want {
		return stdlibDir, false, nil
	}

	// Everything below writes to the SHARED stdlib cache, which every project's
	// build reads as a transpiler search path. Two builds re-extracting at once
	// would each delete the other's half-written tree, so extraction is
	// serialized on the cache root. Different versions serialize too; extraction
	// happens about once per toolchain upgrade, so that costs nothing.
	// Worth announcing — extraction can take a moment and the wait is otherwise
	// invisible — but with no hint: this cache is shared by every project on the
	// machine, so a private build dir would not avoid it.
	lock, lockErr := lockDir(c.StdlibDir, stdlibLockTimeout, lockNotice{what: "stdlib cache"})
	if lockErr != nil {
		return "", false, lockErr
	}
	defer lock.Release()

	// Re-check under the lock: whoever we queued behind has very likely just
	// done this work, and re-extracting on top of it would be pure waste.
	if got, readErr := os.ReadFile(markerPath); readErr == nil && string(got) == want {
		return stdlibDir, false, nil
	}

	if err := c.extractStdlibVersion(stdlibDir, want); err != nil {
		return "", false, err
	}
	return stdlibDir, true, nil
}

// stdlibLockTimeout bounds the wait for another process's extraction. Writing
// the embedded snapshot takes well under a second, so a wait this long means
// something is wedged rather than slow.
const stdlibLockTimeout = 2 * time.Minute

// extractStdlibVersion writes the snapshot into a private staging directory and
// only then moves it into place.
//
// Extracting into stdlibDir directly would publish a half-populated cache: the
// old contents are deleted first, so for the length of the extraction every
// concurrent build resolving its transpiler search path sees files that exist
// one moment and not the next. Staging shrinks that window to a single rename,
// and the marker is inside the staged tree, so the directory is never visible
// without the marker that certifies it complete.
func (c *Config) extractStdlibVersion(stdlibDir, want string) error {
	staging := fmt.Sprintf("%s.staging-%d", stdlibDir, os.Getpid())

	// A previous run killed mid-extraction can leave staging behind.
	if err := os.RemoveAll(staging); err != nil {
		return fmt.Errorf("clearing stdlib staging directory: %w", err)
	}
	defer os.RemoveAll(staging) // no-op once the rename below succeeds

	if err := os.MkdirAll(staging, 0755); err != nil {
		return fmt.Errorf("creating stdlib staging directory: %w", err)
	}
	if err := stdlib.ExtractTo(staging); err != nil {
		return fmt.Errorf("extracting stdlib: %w", err)
	}
	if err := os.WriteFile(filepath.Join(staging, stdlibMarkerName), []byte(want), 0644); err != nil {
		return fmt.Errorf("writing stdlib marker: %w", err)
	}

	// Swap. The guard in removeStdlibVersionDir still applies: a degenerate
	// version string must not turn this into a delete of the cache root.
	if err := c.removeStdlibVersionDir(stdlibDir); err != nil {
		return err
	}
	if err := os.Rename(staging, stdlibDir); err != nil {
		return fmt.Errorf("installing extracted stdlib: %w", err)
	}
	return nil
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
