package build

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/stretchr/testify/require"

	"martianoff/gala/internal/stdlib"
)

// isolatedConfig returns a Config rooted at a throwaway GALA_HOME so tests
// never read or write the developer's real stdlib cache.
func isolatedConfig(t *testing.T) *Config {
	t.Helper()
	t.Setenv("GALA_HOME", t.TempDir())
	config := DefaultConfig()
	require.NoError(t, config.EnsureDirs())
	return config
}

// anyEmbeddedFile returns a deterministic (package, filename) pair from the
// embedded snapshot so tests can tamper with an extracted file without naming
// any particular stdlib package.
func anyEmbeddedFile(t *testing.T) (pkg, file string) {
	t.Helper()
	pkgNames := make([]string, 0, len(stdlib.EmbeddedPackages))
	for name, files := range stdlib.EmbeddedPackages {
		if len(files) > 0 {
			pkgNames = append(pkgNames, name)
		}
	}
	require.NotEmpty(t, pkgNames, "embedded stdlib snapshot is empty")
	sort.Strings(pkgNames)
	pkg = pkgNames[0]

	fileNames := make([]string, 0, len(stdlib.EmbeddedPackages[pkg]))
	for name := range stdlib.EmbeddedPackages[pkg] {
		fileNames = append(fileNames, name)
	}
	sort.Strings(fileNames)
	return pkg, fileNames[0]
}

// TestNormalizeStdlibVersion verifies the git describe suffix is stripped so
// that every stdlib cache consumer derives the same directory name.
func TestNormalizeStdlibVersion(t *testing.T) {
	tests := []struct {
		name    string
		version string
		want    string
	}{
		{name: "plain release", version: "0.29.4", want: "0.29.4"},
		{name: "git describe suffix", version: "0.29.4-1-ga528ffd", want: "0.29.4"},
		{name: "prerelease suffix", version: "1.0.0-rc1", want: "1.0.0"},
		{name: "unstamped dev build", version: "dev", want: "dev"},
		{name: "empty", version: "", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, normalizeStdlibVersion(tt.version))
		})
	}
}

// TestStdlibVersionDir_NormalizesForAllCallers verifies `gala build` (which
// reaches the cache through StdlibVersionDir) and `gala transpile`/`gala lsp`
// (which reach it through EnsureStdlib) resolve one and the same directory for
// a version carrying a git describe suffix. Before normalization was shared,
// the two produced sibling directories with independent freshness markers.
func TestStdlibVersionDir_NormalizesForAllCallers(t *testing.T) {
	config := isolatedConfig(t)

	suffixed := config.StdlibVersionDir("0.29.4-1-ga528ffd")
	plain := config.StdlibVersionDir("0.29.4")
	require.Equal(t, plain, suffixed)

	ensured := config.EnsureStdlib("0.29.4-1-ga528ffd")
	require.Equal(t, plain, ensured)

	// The freshness marker must match for both spellings too, otherwise the
	// shared directory would be wiped and re-extracted on every alternation.
	require.Equal(t, snapshotFingerprint("0.29.4"), snapshotFingerprint("0.29.4-1-ga528ffd"))
	require.NotEqual(t, snapshotFingerprint("0.29.4"), snapshotFingerprint("0.30.0"))
}

// TestEnsureStdlibExtracted_WritesFingerprintMarker verifies a first extraction
// populates the version directory and records the snapshot fingerprint.
func TestEnsureStdlibExtracted_WritesFingerprintMarker(t *testing.T) {
	config := isolatedConfig(t)

	dir, extracted, err := config.ensureStdlibExtracted("dev")
	require.NoError(t, err)
	require.True(t, extracted)
	require.Equal(t, filepath.Join(config.StdlibDir, "vdev"), dir)

	pkg, file := anyEmbeddedFile(t)
	require.FileExists(t, filepath.Join(dir, pkg, file))
	require.FileExists(t, filepath.Join(dir, pkg, "go.mod"))

	marker, err := os.ReadFile(filepath.Join(dir, stdlibMarkerName))
	require.NoError(t, err)
	require.Equal(t, snapshotFingerprint("dev"), string(marker))
}

// TestEnsureStdlibExtracted_FastPathWhenFingerprintMatches verifies a second
// call with an unchanged snapshot does no work.
func TestEnsureStdlibExtracted_FastPathWhenFingerprintMatches(t *testing.T) {
	config := isolatedConfig(t)

	dir, extracted, err := config.ensureStdlibExtracted("dev")
	require.NoError(t, err)
	require.True(t, extracted)

	// A sentinel survives only if the directory is left untouched.
	sentinel := filepath.Join(dir, "sentinel.txt")
	require.NoError(t, os.WriteFile(sentinel, []byte("keep"), 0644))

	dir2, extracted2, err := config.ensureStdlibExtracted("dev")
	require.NoError(t, err)
	require.False(t, extracted2, "matching fingerprint must take the fast path")
	require.Equal(t, dir, dir2)
	require.FileExists(t, sentinel)
}

// TestEnsureStdlibExtracted_StaleSnapshotIsReplaced verifies that a cache
// written by a different snapshot is detected through the marker contents and
// fully replaced. Previously the marker's mere existence was accepted, so an
// outdated stdlib could be used indefinitely — silently changing the type
// information the transpiler compiles against.
func TestEnsureStdlibExtracted_StaleSnapshotIsReplaced(t *testing.T) {
	config := isolatedConfig(t)

	dir, _, err := config.ensureStdlibExtracted("dev")
	require.NoError(t, err)

	pkg, file := anyEmbeddedFile(t)
	tampered := filepath.Join(dir, pkg, file)
	require.NoError(t, os.WriteFile(tampered, []byte("// outdated contents\n"), 0644))

	// A file that no longer exists upstream must not survive re-extraction.
	orphan := filepath.Join(dir, pkg, "removed_upstream.gala")
	require.NoError(t, os.WriteFile(orphan, []byte("// dropped upstream\n"), 0644))

	// Simulate a marker left behind by an older binary.
	markerPath := filepath.Join(dir, stdlibMarkerName)
	require.NoError(t, os.WriteFile(markerPath, []byte("fingerprint-from-an-older-snapshot"), 0644))

	dir2, extracted, err := config.ensureStdlibExtracted("dev")
	require.NoError(t, err)
	require.True(t, extracted, "differing fingerprint must force re-extraction")
	require.Equal(t, dir, dir2)

	restored, err := os.ReadFile(tampered)
	require.NoError(t, err)
	require.Equal(t, stdlib.EmbeddedPackages[pkg][file], string(restored))
	require.NoFileExists(t, orphan, "files dropped upstream must not linger")

	marker, err := os.ReadFile(markerPath)
	require.NoError(t, err)
	require.Equal(t, snapshotFingerprint("dev"), string(marker))
}

// TestEnsureStdlibExtracted_MissingMarkerReExtracts verifies a version
// directory with no marker at all is treated as stale.
func TestEnsureStdlibExtracted_MissingMarkerReExtracts(t *testing.T) {
	config := isolatedConfig(t)

	dir, _, err := config.ensureStdlibExtracted("dev")
	require.NoError(t, err)
	require.NoError(t, os.Remove(filepath.Join(dir, stdlibMarkerName)))

	_, extracted, err := config.ensureStdlibExtracted("dev")
	require.NoError(t, err)
	require.True(t, extracted)
}

// TestCheckStdlibVersionDir_RejectsNonVersionDirs verifies the guard that
// protects the re-extraction wipe: only a direct child of the stdlib cache root
// may ever be removed, so no version string can escalate into deleting the
// cache root or the user's home directory.
func TestCheckStdlibVersionDir_RejectsNonVersionDirs(t *testing.T) {
	config := isolatedConfig(t)

	tests := []struct {
		name    string
		dir     string
		wantErr bool
	}{
		{name: "version dir", dir: filepath.Join(config.StdlibDir, "v0.29.4"), wantErr: false},
		{name: "dev version dir", dir: config.StdlibVersionDir("dev"), wantErr: false},
		{name: "cache root itself", dir: config.StdlibDir, wantErr: true},
		{name: "gala home", dir: config.GalaHome, wantErr: true},
		{name: "parent escape", dir: filepath.Join(config.StdlibDir, ".."), wantErr: true},
		{name: "deep escape", dir: filepath.Join(config.StdlibDir, "..", "..", ".."), wantErr: true},
		{name: "nested path", dir: filepath.Join(config.StdlibDir, "v0.29.4", "std"), wantErr: true},
		{name: "unrelated path", dir: t.TempDir(), wantErr: true},
		{name: "empty", dir: "", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := config.checkStdlibVersionDir(tt.dir)
			if !tt.wantErr {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			var unsafeErr *UnsafeStdlibDirError
			require.True(t, errors.As(err, &unsafeErr))
		})
	}
}

// TestCheckStdlibVersionDir_RejectsDegenerateRoot verifies a Config whose
// stdlib root is unset or is a filesystem/volume root can never authorize a
// delete, however plausible the version string looks.
func TestCheckStdlibVersionDir_RejectsDegenerateRoot(t *testing.T) {
	volumeRoot := filepath.VolumeName(t.TempDir()) + string(filepath.Separator)

	for _, root := range []string{"", ".", string(filepath.Separator), volumeRoot} {
		config := &Config{StdlibDir: root}
		require.Error(t, config.checkStdlibVersionDir(config.StdlibVersionDir("dev")),
			"root %q must be rejected", root)
	}
}

// TestRemoveStdlibVersionDir_LeavesRejectedPathsIntact verifies the guard is
// enforced by the removal path itself, not only by its callers.
func TestRemoveStdlibVersionDir_LeavesRejectedPathsIntact(t *testing.T) {
	config := isolatedConfig(t)

	victim := filepath.Join(config.GalaHome, "precious.txt")
	require.NoError(t, os.WriteFile(victim, []byte("do not delete"), 0644))

	err := config.removeStdlibVersionDir(config.GalaHome)
	require.Error(t, err)
	var unsafeErr *UnsafeStdlibDirError
	require.True(t, errors.As(err, &unsafeErr))
	require.FileExists(t, victim)
	require.DirExists(t, config.StdlibDir)

	// A real version directory is removed.
	dir := config.StdlibVersionDir("dev")
	require.NoError(t, os.MkdirAll(dir, 0755))
	require.NoError(t, config.removeStdlibVersionDir(dir))
	require.NoDirExists(t, dir)
}
