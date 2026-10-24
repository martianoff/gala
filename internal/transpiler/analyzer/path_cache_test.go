package analyzer

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Each case is asked twice of one cache, so the second answer comes from it:
// the cache must never change which files count as the same.
func TestPathCache_SameFile(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.gala")
	b := filepath.Join(dir, "sub", "a.gala")
	require.NoError(t, os.MkdirAll(filepath.Dir(b), 0o755))
	require.NoError(t, os.WriteFile(a, []byte("package a\n"), 0o644))
	require.NoError(t, os.WriteFile(b, []byte("package a\n"), 0o644))
	missing := filepath.Join(dir, "missing.gala")

	tests := []struct {
		name         string
		pathA, pathB string
		want         bool
	}{
		{"same path", a, a, true},
		{"same file through a dot segment", a, filepath.Join(dir, ".", "a.gala"), true},
		{"different directories, same name", a, b, false},
		{"different names", a, filepath.Join(dir, "b.gala"), false},
		{"missing file against itself", missing, missing, true},
		{"missing against existing", missing, a, false},
		{"empty path", "", a, false},
	}
	cache := newPathCache()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for i := 0; i < 2; i++ {
				assert.Equal(t, tt.want, cache.sameFile(tt.pathA, tt.pathB))
			}
		})
	}
}

func TestPathCache_Canonical(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "a.gala")
	require.NoError(t, os.WriteFile(file, []byte("package a\n"), 0o644))
	resolved, err := filepath.EvalSymlinks(file)
	require.NoError(t, err)
	missing := filepath.Join(dir, "missing.gala")

	cache := newPathCache()
	for i := 0; i < 2; i++ {
		assert.Equal(t, resolved, cache.canonical(file))
		assert.Equal(t, resolved, cache.canonical(filepath.Join(dir, ".", "a.gala")))
		// An unresolvable path falls back to its absolute form.
		assert.Equal(t, missing, cache.canonical(missing))
	}
}

// A symlink and its target are the same file, whatever their names, and
// canonicalize to the target. Skipped where the OS will not create symlinks
// (Windows without developer mode).
func TestPathCache_Symlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "real", "a.gala")
	require.NoError(t, os.MkdirAll(filepath.Dir(target), 0o755))
	require.NoError(t, os.WriteFile(target, []byte("package a\n"), 0o644))
	sameName := filepath.Join(dir, "a.gala")
	if err := os.Symlink(target, sameName); err != nil {
		t.Skipf("cannot create symlinks here: %v", err)
	}
	resolved, err := filepath.EvalSymlinks(target)
	require.NoError(t, err)

	cache := newPathCache()
	for i := 0; i < 2; i++ {
		assert.True(t, cache.sameFile(sameName, target))
		assert.Equal(t, resolved, cache.canonical(sameName))
	}
}

// A path that does not resolve is not remembered, so a file created later is
// found once it exists. A long-lived analyzer (the LSP) depends on this.
func TestPathCache_DoesNotCacheFailures(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "later.gala")
	cache := newPathCache()

	_, err := cache.evalSymlinks(path)
	require.Error(t, err)
	assert.NotContains(t, cache.real, path)

	require.NoError(t, os.WriteFile(path, []byte("package later\n"), 0o644))
	want, err := filepath.EvalSymlinks(path)
	require.NoError(t, err)
	got, err := cache.evalSymlinks(path)
	require.NoError(t, err)
	assert.Equal(t, want, got)
	assert.Equal(t, want, cache.real[path])
}

// A nil cache resolves without caching, so an analyzer built without one
// (as some tests do) behaves exactly as before.
func TestPathCache_NilResolvesUncached(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.gala")
	require.NoError(t, os.WriteFile(path, []byte("package a\n"), 0o644))

	var cache *pathCache
	want, err := filepath.EvalSymlinks(path)
	require.NoError(t, err)
	got, err := cache.evalSymlinks(path)
	require.NoError(t, err)
	assert.Equal(t, want, got)
	assert.True(t, cache.sameFile(path, path))
	assert.Equal(t, canonicalPath(path), cache.canonical(path))
}
