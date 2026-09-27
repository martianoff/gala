package analyzer

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestAssumedPackageName pins the name an unaliased import binds when its
// package name differs from the last path segment. The explicit-import check
// relies on it to recognise `yaml.Node` as the Go import "gopkg.in/yaml.v3"
// rather than a GALA package that happens to be called `yaml`.
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
			assert.Equal(t, tc.want, assumedPackageName(tc.path))
			assert.Equal(t, tc.want, fileImport{Path: tc.path}.LocalName())
		})
	}
	assert.Equal(t, "gy", fileImport{Path: "gopkg.in/yaml.v3", Alias: "gy"}.LocalName(), "an alias wins")
}

// TestLocalNames covers the ambiguity assumedPackageName cannot settle alone: a
// trailing `/vN` is a module major version for `math/rand/v2` but the package
// name itself for `k8s.io/api/core/v1`, so an unaliased import answers to both.
func TestLocalNames(t *testing.T) {
	cases := []struct {
		imp  fileImport
		want []string
	}{
		{fileImport{Path: "strings"}, []string{"strings"}},
		{fileImport{Path: "k8s.io/api/core/v1"}, []string{"core", "v1"}},
		{fileImport{Path: "math/rand/v2"}, []string{"rand", "v2"}},
		{fileImport{Path: "gopkg.in/yaml.v3"}, []string{"yaml", "yaml.v3"}},
		{fileImport{Path: "k8s.io/api/core/v1", Alias: "corev1"}, []string{"corev1"}},
	}
	for _, tc := range cases {
		t.Run(tc.imp.Path+"/"+tc.imp.Alias, func(t *testing.T) {
			assert.Equal(t, tc.want, tc.imp.LocalNames())
		})
	}
}
