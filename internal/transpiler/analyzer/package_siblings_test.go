package analyzer_test

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"martianoff/gala/internal/transpiler/analyzer"
)

// PackageSiblings reads siblings from disk through ReadSource: it keeps the
// other same-package .gala files and drops the file itself, test files (for a
// non-test file), other packages, other extensions and directories. The
// package and main-program rules are covered in detail by the LSP's
// package_files_internal_test.go, which goes through the same function.
func TestPackageSiblings(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"a.gala":      "package test\n",
		"b.gala":      string([]byte{0xef, 0xbb, 0xbf}) + "package test\n", // BOM-prefixed
		"c.gala":      "package test\n",
		"a_test.gala": "package test\n",
		"other.gala":  "package elsewhere\n",
		"notes.txt":   "package test\n",
	}
	for name, src := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(dir, "sub.gala"), 0755); err != nil {
		t.Fatal(err)
	}

	got := analyzer.PackageSiblings(filepath.Join(dir, "a.gala"), files["a.gala"], analyzer.ReadSource)
	want := []string{filepath.Join(dir, "b.gala"), filepath.Join(dir, "c.gala")}
	if !slices.Equal(got, want) {
		t.Errorf("PackageSiblings = %v, want %v", got, want)
	}
}
