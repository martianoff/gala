package build

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
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

	ensured, err := config.EnsureStdlib("0.29.4-1-ga528ffd")
	require.NoError(t, err)
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

	// A marker exactly as an older binary left it: the bare version string,
	// which used to be written and then never read back. This is the shape the
	// regression takes in the field, so it is the shape the test uses.
	markerPath := filepath.Join(dir, stdlibMarkerName)
	require.NoError(t, os.WriteFile(markerPath, []byte("dev"), 0644))

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
		{name: "cache root with trailing separator", dir: config.StdlibDir + string(filepath.Separator), wantErr: true},
		{name: "cache root via dot element", dir: filepath.Join(config.StdlibDir, "."), wantErr: true},
		{name: "relative path", dir: filepath.Join("stdlib", "v0.29.4"), wantErr: true},
		{name: "version dir reached through a detour", dir: filepath.Join(config.StdlibDir, "v0.29.4", "..", "v0.29.4"), wantErr: false},
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

// TestCheckStdlibVersionDir_RejectsDifferentlyCasedRoot verifies the cache root
// cannot be smuggled past the guard by re-spelling it: on a case-insensitive
// filesystem filepath.Rel folds the case and reports ".", and elsewhere the
// re-spelling is simply a different path outside the root. Either way it is
// refused.
func TestCheckStdlibVersionDir_RejectsDifferentlyCasedRoot(t *testing.T) {
	config := isolatedConfig(t)

	for _, spelling := range []string{strings.ToUpper(config.StdlibDir), strings.ToLower(config.StdlibDir)} {
		require.Error(t, config.checkStdlibVersionDir(spelling), "spelling %q must be rejected", spelling)
	}
}

// TestStdlibVersionDir_HostileVersionCannotEscapeTheCacheRoot feeds version
// strings that try to climb out of the cache through the naming funnel and
// checks that whatever comes out is either refused outright or still a direct
// child of the cache root. The version is compiled into the binary rather than
// typed by a user, but it is the only input to the path a wipe is performed on,
// so it must not be able to widen that wipe.
func TestStdlibVersionDir_HostileVersionCannotEscapeTheCacheRoot(t *testing.T) {
	config := isolatedConfig(t)
	root := filepath.Clean(config.StdlibDir)

	versions := []string{
		"", ".", "..", "...", "/", "\\", string(filepath.Separator),
		"../..", "/../../etc", `..\..\Windows`, "../" + filepath.Base(root),
		`C:\Windows`, `\\server\share`, "//server/share",
		"-rc1", "v0.29.4", "0.1.0+meta", "a\nb",
	}

	for _, version := range versions {
		t.Run(strconv.Quote(version), func(t *testing.T) {
			dir := config.StdlibVersionDir(version)
			if err := config.checkStdlibVersionDir(dir); err != nil {
				var unsafeErr *UnsafeStdlibDirError
				require.True(t, errors.As(err, &unsafeErr))
				return
			}
			// Accepted: the worst this version string can reach is one direct
			// child of the cache root, never the root and never outside it.
			cleaned := filepath.Clean(dir)
			require.NotEqual(t, root, cleaned)
			require.Equal(t, root, filepath.Dir(cleaned),
				"accepted dir %q is not a direct child of %q", dir, root)
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

// TestRemoveStdlibVersionDir_DoesNotFollowASymlinkedVersionDir verifies that a
// version directory which is really a link is unlinked rather than walked into.
// The guard is purely lexical, so this property comes from os.RemoveAll itself
// and is worth pinning: it is what stops a swapped-in link from turning the
// cache wipe into a delete of whatever it points at.
func TestRemoveStdlibVersionDir_DoesNotFollowASymlinkedVersionDir(t *testing.T) {
	config := isolatedConfig(t)

	outside := t.TempDir()
	bystander := filepath.Join(outside, "bystander.txt")
	require.NoError(t, os.WriteFile(bystander, []byte("keep"), 0644))

	link := config.StdlibVersionDir("dev")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("host does not permit creating symlinks: %v", err)
	}

	require.NoError(t, config.removeStdlibVersionDir(link))
	require.NoDirExists(t, link)
	require.FileExists(t, bystander, "removing the version directory must not follow the link")
}
