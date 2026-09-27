package transpiler

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestAssumedPackageName pins the guess for an unaliased import whose real
// package name is unknown.
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

// TestImportNames: an alias or a known package name is definite; otherwise a
// `/vN` suffix does not settle the name, so both readings are listed.
func TestImportNames(t *testing.T) {
	cases := []struct {
		name                 string
		path, alias, pkgName string
		want                 []ImportName
	}{
		{"alias", "k8s.io/api/core/v1", "corev1", "v1", []ImportName{{"corev1", RankAlias}}},
		{"known name", "k8s.io/api/core/v1", "", "v1", []ImportName{{"v1", RankPackageName}}},
		{"k8s-style, unknown", "k8s.io/api/core/v1", "", "", []ImportName{{"v1", RankLastSegment}, {"core", RankDerived}}},
		{"major version, unknown", "math/rand/v2", "", "", []ImportName{{"v2", RankLastSegment}, {"rand", RankDerived}}},
		{"gopkg.in, unknown", "gopkg.in/yaml.v3", "", "", []ImportName{{"yaml.v3", RankLastSegment}, {"yaml", RankDerived}}},
		{"hyphenated, unknown", "github.com/mattn/go-sqlite3", "", "", []ImportName{{"go-sqlite3", RankLastSegment}, {"sqlite3", RankDerived}}},
		{"plain", "strings", "", "", []ImportName{{"strings", RankLastSegment}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, ImportNames(tc.path, tc.alias, tc.pkgName))
		})
	}
}

// TestRankedNames: a guessed name never shadows a surer binding, whatever the
// order the imports are declared in.
func TestRankedNames(t *testing.T) {
	r := NewRankedNames[string]()
	r.Bind("k8s.io/api/core/v1", "", "", "k8s")    // may bind v1 or core
	r.Bind("example.com/core", "", "core", "local") // is package core
	r.Bind("example.com/other", "v1", "", "aliased")
	got, _ := r.Get("core")
	assert.Equal(t, "local", got)
	got, _ = r.Get("v1")
	assert.Equal(t, "aliased", got)
	_, ok := r.Get("v2")
	assert.False(t, ok)
}

// TestLastPathSegment is the part after the last slash.
func TestLastPathSegment(t *testing.T) {
	assert.Equal(t, "v1", LastPathSegment("k8s.io/api/core/v1"))
	assert.Equal(t, "strings", LastPathSegment("strings"))
}

// TestIsValidGoImportPath accepts what a Go import declaration can name and
// rejects filesystem paths.
func TestIsValidGoImportPath(t *testing.T) {
	for _, p := range []string{
		"strings", "math/rand/v2", "gopkg.in/yaml.v3", "github.com/mattn/go-sqlite3",
		"example.com/gosubpkg/box", "martianoff/gala/std", "k8s.io/api/core/v1", "C",
		"example.com/a_b/c~d/e+f",
	} {
		assert.True(t, IsValidGoImportPath(p), p)
	}
	for _, p := range []string{
		"", `C:\Users\me\proj\box`, "C:/Users/me/proj/box", "/home/me/proj/box",
		"./box", "../box", "example.com//box", "example.com/box/", `example.com\box`,
		"example.com/b ox",
	} {
		assert.False(t, IsValidGoImportPath(p), p)
	}
}
