package analyzer_test

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"martianoff/gala/internal/transpiler/analyzer"
)

// PackageSiblings keeps only the other non-test .gala files: not the file
// itself, not _test.gala files, not other extensions, not directories.
func TestPackageSiblings(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"a.gala", "b.gala", "c.gala", "a_test.gala", "notes.txt"} {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(dir, "sub.gala"), 0755); err != nil {
		t.Fatal(err)
	}

	got, err := analyzer.PackageSiblings(filepath.Join(dir, "a.gala"))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{filepath.Join(dir, "b.gala"), filepath.Join(dir, "c.gala")}
	if !slices.Equal(got, want) {
		t.Errorf("PackageSiblings = %v, want %v", got, want)
	}
}
