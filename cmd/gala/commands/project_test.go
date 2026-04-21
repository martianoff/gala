package commands

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestResolveRunTarget_FindsExistingGalaMod ensures that `gala run` still
// resolves to the real project root when a gala.mod is present — the existing
// behavior is preserved and ephemeral mode is NOT entered.
func TestResolveRunTarget_FindsExistingGalaMod(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "gala.mod"), []byte("module example.com/test\n"), 0644); err != nil {
		t.Fatalf("write gala.mod: %v", err)
	}
	mainGala := filepath.Join(dir, "main.gala")
	if err := os.WriteFile(mainGala, []byte("package main\n"), 0644); err != nil {
		t.Fatalf("write main.gala: %v", err)
	}

	root, ephemeral, err := resolveRunTarget(mainGala)
	if err != nil {
		t.Fatalf("resolveRunTarget returned error: %v", err)
	}
	if ephemeral {
		t.Fatalf("expected ephemeral=false when gala.mod is present, got true")
	}
	// Resolve symlinks / evaluate for platform-consistent comparison on macOS/Windows.
	if !samePath(root, dir) {
		t.Fatalf("expected project root %q, got %q", dir, root)
	}
}

// TestResolveRunTarget_EphemeralSingleFile exercises the gap #2 fix: a fresh
// user writes main.gala in an empty directory with no gala.mod and runs
// `gala run main.gala`. We must return the file's directory as the project
// root and signal ephemeral mode.
func TestResolveRunTarget_EphemeralSingleFile(t *testing.T) {
	dir := t.TempDir()
	mainGala := filepath.Join(dir, "main.gala")
	if err := os.WriteFile(mainGala, []byte("package main\n\nfunc main() = Println(\"hi\")\n"), 0644); err != nil {
		t.Fatalf("write main.gala: %v", err)
	}

	root, ephemeral, err := resolveRunTarget(mainGala)
	if err != nil {
		t.Fatalf("resolveRunTarget returned error: %v", err)
	}
	if !ephemeral {
		t.Fatalf("expected ephemeral=true when no gala.mod and single .gala file, got false")
	}
	if !samePath(root, dir) {
		t.Fatalf("expected project root %q, got %q", dir, root)
	}
}

// TestResolveRunTarget_DirectoryStillErrors guards the scope of the fix: a
// directory argument with no gala.mod must still fail. Treating a whole
// directory as an ephemeral module would hide a forgotten `gala mod init`
// on a real project.
func TestResolveRunTarget_DirectoryStillErrors(t *testing.T) {
	// Use a tempdir that we know has no gala.mod in any ancestor. Skip if the
	// OS tempdir root happens to sit inside some parent module (unlikely on
	// CI but possible on developer machines).
	dir := t.TempDir()
	if _, err := findProjectRoot(dir); err == nil {
		t.Skipf("temp dir %s has a gala.mod ancestor; skipping negative test", dir)
	}

	_, ephemeral, err := resolveRunTarget(dir)
	if err == nil {
		t.Fatalf("expected error for directory without gala.mod, got success")
	}
	if ephemeral {
		t.Fatalf("expected ephemeral=false for directory arg, got true")
	}
}

// TestResolveRunTarget_NonGalaFileErrors ensures that a non-.gala file
// argument does not silently trigger ephemeral mode.
func TestResolveRunTarget_NonGalaFileErrors(t *testing.T) {
	dir := t.TempDir()
	if _, err := findProjectRoot(dir); err == nil {
		t.Skipf("temp dir %s has a gala.mod ancestor; skipping negative test", dir)
	}
	other := filepath.Join(dir, "main.go")
	if err := os.WriteFile(other, []byte("package main\n"), 0644); err != nil {
		t.Fatalf("write main.go: %v", err)
	}

	_, ephemeral, err := resolveRunTarget(other)
	if err == nil {
		t.Fatalf("expected error for non-.gala file without gala.mod, got success")
	}
	if ephemeral {
		t.Fatalf("expected ephemeral=false for .go file, got true")
	}
}

// samePath compares two filesystem paths in a platform-tolerant way. On
// Windows the comparison is case-insensitive because Go's t.TempDir() can
// round-trip paths through different case conventions (e.g. C:\Users vs
// c:\users).
func samePath(a, b string) bool {
	aa, _ := filepath.EvalSymlinks(a)
	bb, _ := filepath.EvalSymlinks(b)
	if aa == "" {
		aa = a
	}
	if bb == "" {
		bb = b
	}
	if runtime.GOOS == "windows" {
		return strings.EqualFold(aa, bb)
	}
	return aa == bb
}
