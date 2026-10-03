package stdlib

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestFingerprint_StableAcrossCalls verifies the embedded snapshot digest does
// not change between calls — the cache marker must be comparable over time.
func TestFingerprint_StableAcrossCalls(t *testing.T) {
	first := Fingerprint()
	require.NotEmpty(t, first)
	require.Equal(t, first, Fingerprint())
	require.Equal(t, first, fingerprintPackages(EmbeddedPackages, PackageImportPaths))
}

// TestFingerprintPackages_DetectsContentChanges verifies that every kind of
// change to the snapshot produces a different digest.
func TestFingerprintPackages_DetectsContentChanges(t *testing.T) {
	base := map[string]map[string]string{
		"alpha": {"a.go": "package alpha\n"},
		"beta":  {"b.go": "package beta\n"},
	}
	imports := map[string]string{
		"alpha": "example.com/alpha",
		"beta":  "example.com/beta",
	}
	baseline := fingerprintPackages(base, imports)
	require.NotEmpty(t, baseline)

	tests := []struct {
		name     string
		packages map[string]map[string]string
		imports  map[string]string
	}{
		{
			name: "file content changed",
			packages: map[string]map[string]string{
				"alpha": {"a.go": "package alpha // edited\n"},
				"beta":  {"b.go": "package beta\n"},
			},
			imports: imports,
		},
		{
			name: "file added",
			packages: map[string]map[string]string{
				"alpha": {"a.go": "package alpha\n", "extra.go": "package alpha\n"},
				"beta":  {"b.go": "package beta\n"},
			},
			imports: imports,
		},
		{
			name: "file removed",
			packages: map[string]map[string]string{
				"alpha": {"a.go": "package alpha\n"},
				"beta":  {},
			},
			imports: imports,
		},
		{
			name: "package removed",
			packages: map[string]map[string]string{
				"alpha": {"a.go": "package alpha\n"},
			},
			imports: imports,
		},
		{
			name:     "import path changed",
			packages: base,
			imports: map[string]string{
				"alpha": "example.com/alpha/v2",
				"beta":  "example.com/beta",
			},
		},
		{
			name: "content moved between files",
			packages: map[string]map[string]string{
				"alpha": {"a.go": "package beta\n"},
				"beta":  {"b.go": "package alpha\n"},
			},
			imports: imports,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.NotEqual(t, baseline, fingerprintPackages(tt.packages, tt.imports))
		})
	}
}

// TestFingerprintPackages_IndependentOfMapOrder verifies the digest is derived
// from sorted names, not Go's randomized map iteration order.
func TestFingerprintPackages_IndependentOfMapOrder(t *testing.T) {
	packages := map[string]map[string]string{
		"alpha": {"a.go": "1", "b.go": "2", "c.go": "3"},
		"beta":  {"d.go": "4", "e.go": "5"},
		"gamma": {"f.go": "6"},
	}
	imports := map[string]string{"alpha": "x/alpha", "beta": "x/beta", "gamma": "x/gamma"}

	want := fingerprintPackages(packages, imports)
	for i := 0; i < 20; i++ {
		require.Equal(t, want, fingerprintPackages(packages, imports))
	}
}
