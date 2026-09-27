package transpiler

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestAssumedPackageName pins the name an unaliased import binds when its
// package name differs from the last path segment, e.g. `yaml` for
// "gopkg.in/yaml.v3".
func TestAssumedPackageName(t *testing.T) {
	cases := []struct {
		path string
		want string
	}{
		{"strings", "strings"},
		{"encoding/json", "json"},
		{"martianoff/gala/collection_immutable", "collection_immutable"},
		{"gopkg.in/yaml.v3", "yaml"},
		{"github.com/example/foo/v2", "foo"},
		{"math/rand/v2", "rand"},
		{"github.com/mattn/go-sqlite3", "sqlite3"},
		{"github.com/example/v2", "example"},
		{"v2", "v2"}, // a bare vN has no parent segment to fall back to
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			assert.Equal(t, tc.want, AssumedPackageName(tc.path))
		})
	}
}

// TestPackageNameCandidates covers the ambiguity AssumedPackageName cannot
// settle alone: a trailing `/vN` is a module major version for `math/rand/v2`
// but the package name itself for `k8s.io/api/core/v1`.
func TestPackageNameCandidates(t *testing.T) {
	cases := []struct {
		path string
		want []string
	}{
		{"strings", []string{"strings"}},
		{"k8s.io/api/core/v1", []string{"core", "v1"}},
		{"math/rand/v2", []string{"rand", "v2"}},
		{"gopkg.in/yaml.v3", []string{"yaml", "yaml.v3"}},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			assert.Equal(t, tc.want, PackageNameCandidates(tc.path))
		})
	}
}
