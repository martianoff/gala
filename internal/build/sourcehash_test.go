package build

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"martianoff/gala/internal/depman/mod"
	"martianoff/gala/internal/stdlib"
)

// writeSourceTree writes a throwaway project and returns the file list that
// `transpile` would hash. Tests stay rooted in a temp GALA_HOME so nothing here
// can read or write the developer's real stdlib cache.
func writeSourceTree(t *testing.T) []string {
	t.Helper()
	t.Setenv("GALA_HOME", t.TempDir())

	dir := t.TempDir()
	main := filepath.Join(dir, "main.gala")
	require.NoError(t, os.WriteFile(main, []byte("fun main(): Unit = Println(\"hi\")\n"), 0644))
	galaMod := filepath.Join(dir, "gala.mod")
	require.NoError(t, os.WriteFile(galaMod, []byte("module example\n\ngala 0.1.0\n"), 0644))
	return []string{main, galaMod}
}

// TestComputeSourceHash_TracksStdlibFingerprint verifies that the transpile
// cache key follows the standard library the sources are compiled against.
//
// The stdlib supplies the signatures the analyses key off, so the same sources
// can legitimately produce different diagnostics under different stdlib
// contents. Keyed on the project's own files alone, a workspace built against
// an outdated stdlib would keep replaying that result after the stdlib was
// repaired, and a check that had been silently disabled would stay disabled.
func TestComputeSourceHash_TracksStdlibFingerprint(t *testing.T) {
	files := writeSourceTree(t)

	base := computeSourceHash(files, "dev", "fingerprint-a")
	require.NotEmpty(t, base)

	t.Run("stable for an unchanged stdlib", func(t *testing.T) {
		require.Equal(t, base, computeSourceHash(files, "dev", "fingerprint-a"),
			"identical inputs must not force a needless re-transpile")
	})

	t.Run("changes when the stdlib changes", func(t *testing.T) {
		require.NotEqual(t, base, computeSourceHash(files, "dev", "fingerprint-b"),
			"a different stdlib snapshot must invalidate the cached transpile")
	})

	t.Run("changes for an unstamped build too", func(t *testing.T) {
		// "dev" == "dev" for every unstamped revision, so the version string
		// carries no information here — the fingerprint is the only thing that
		// can distinguish the two.
		require.NotEqual(t,
			computeSourceHash(files, "dev", "fingerprint-a"),
			computeSourceHash(files, "dev", "fingerprint-b"))
	})

	t.Run("still tracks the version and the sources", func(t *testing.T) {
		require.NotEqual(t, base, computeSourceHash(files, "0.71.0", "fingerprint-a"))

		require.NoError(t, os.WriteFile(files[0], []byte("fun main(): Unit = Println(\"bye\")\n"), 0644))
		require.NotEqual(t, base, computeSourceHash(files, "dev", "fingerprint-a"))
	})
}

// TestComputeSourceHash_UsesRealEmbeddedFingerprint verifies the production call
// site is wired to a non-empty fingerprint. An empty one would silently degrade
// the key back to sources-plus-version.
func TestComputeSourceHash_UsesRealEmbeddedFingerprint(t *testing.T) {
	files := writeSourceTree(t)

	require.NotEmpty(t, stdlib.Fingerprint())
	require.NotEqual(t,
		computeSourceHash(files, "dev", stdlib.Fingerprint()),
		computeSourceHash(files, "dev", ""))
}

// TestComputeSourceHash_MissingFileForcesRetranspile documents the existing
// contract: an unreadable input yields an empty hash, which never compares
// equal to a recorded one, so the build re-transpiles rather than trusting a
// stale result.
func TestComputeSourceHash_MissingFileForcesRetranspile(t *testing.T) {
	files := writeSourceTree(t)
	require.Empty(t, computeSourceHash(append(files, filepath.Join(t.TempDir(), "absent.gala")),
		"dev", stdlib.Fingerprint()))
}

// TestComputeDepsHash_TracksStdlibFingerprint verifies the sibling cache key
// guarding transpiled GALA dependencies moves with the stdlib as well.
// Dependency sources are transpiled against the same stdlib, so a repair that
// re-enables a check must re-run it over dependency code too — the requirement
// list on its own cannot express that.
func TestComputeDepsHash_TracksStdlibFingerprint(t *testing.T) {
	requires := []mod.Require{{Path: "github.com/example/lib", Version: "1.2.3"}}
	replaces := []mod.Replace{{
		Old: mod.ModuleVersion{Path: "github.com/example/lib"},
		New: mod.ModuleVersion{Path: "../lib"},
	}}

	base := computeDepsHash(requires, replaces, "fingerprint-a")
	require.NotEmpty(t, base)

	require.Equal(t, base, computeDepsHash(requires, replaces, "fingerprint-a"))
	require.NotEqual(t, base, computeDepsHash(requires, replaces, "fingerprint-b"))

	// The pre-existing inputs must keep invalidating the key.
	other := []mod.Require{{Path: "github.com/example/lib", Version: "1.2.4"}}
	require.NotEqual(t, base, computeDepsHash(other, replaces, "fingerprint-a"))
	require.NotEqual(t, base, computeDepsHash(requires, nil, "fingerprint-a"))
}
