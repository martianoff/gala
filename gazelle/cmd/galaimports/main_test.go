package main

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestScanFile(t *testing.T) {
	dir := t.TempDir()
	src := `package regex

import (
    "regexp"
    . "martianoff/gala/std"
    m "math"
    "martianoff/gala/go_interop"
)

type Regex struct {}
`
	path := filepath.Join(dir, "regex.gala")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	rf := scanFile(path)
	if rf.Error != "" {
		t.Fatalf("unexpected error: %s", rf.Error)
	}
	if rf.Package != "regex" {
		t.Errorf("package = %q, want regex", rf.Package)
	}
	want := []rawImport{
		{Path: "regexp"},
		{Path: "martianoff/gala/std", Dot: true},
		{Path: "math", Alias: "m"},
		{Path: "martianoff/gala/go_interop"},
	}
	if !reflect.DeepEqual(rf.Imports, want) {
		t.Errorf("imports = %+v, want %+v", rf.Imports, want)
	}
}

func TestScanSingleLineImport(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.gala")
	if err := os.WriteFile(path, []byte("package main\n\nimport . \"martianoff/gala/collection_immutable\"\n\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rf := scanFile(path)
	if rf.Package != "main" {
		t.Errorf("package = %q", rf.Package)
	}
	want := []rawImport{{Path: "martianoff/gala/collection_immutable", Dot: true}}
	if !reflect.DeepEqual(rf.Imports, want) {
		t.Errorf("imports = %+v, want %+v", rf.Imports, want)
	}
}

func TestScanMissingFile(t *testing.T) {
	rf := scanFile(filepath.Join(t.TempDir(), "nope.gala"))
	if rf.Error == "" {
		t.Errorf("expected error for missing file")
	}
}
