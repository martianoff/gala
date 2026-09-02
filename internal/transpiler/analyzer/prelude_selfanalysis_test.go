package analyzer_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bazelbuild/rules_go/go/tools/bazel"

	"martianoff/gala/internal/parser"
	"martianoff/gala/internal/transpiler/analyzer"
)

// analyzePkgFile analyzes one file of a package with its siblings registered.
func analyzePkgFile(t *testing.T, searchPaths []string, dir, primary string) error {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(dir, "*.gala"))
	if err != nil {
		t.Fatal(err)
	}
	p := parser.NewAntlrGalaParser()
	a := analyzer.NewGalaAnalyzerWithPackageFiles(p, searchPaths, files)

	path := filepath.Join(dir, primary)
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	tree, docs, perr := p.Parse(string(src))
	if perr != nil {
		t.Fatalf("parse %s: %v", primary, perr)
	}
	_, aerr := a.Analyze(tree, docs, path)
	return aerr
}

// Analyzing a file that belongs to a package the analyzer ALSO loads
// implicitly — which is every file in `std` — merges a prebuilt copy of that
// package into the RichAST before the file's own declarations are walked. Each
// declaration then meets itself, and was reported as a redefinition of itself:
//
//	[GALA-E0011] type "ConstPtr" in package "std" redefined (also declared at line 6)
//
// This is the only shape that reproduces it, because `std` is the package the
// analyzer loads for every file. A synthetic package cannot stand in.
func TestStdFileIsNotARedefinitionOfItself(t *testing.T) {
	stdFile, err := bazel.Runfile("std/option.gala")
	if err != nil {
		t.Skipf("std sources not staged: %v", err)
	}
	stdDir := filepath.Dir(stdFile)

	for _, primary := range []string{"option.gala", "constptr.gala", "try.gala"} {
		t.Run(primary, func(t *testing.T) {
			if _, err := os.Stat(filepath.Join(stdDir, primary)); err != nil {
				t.Skipf("%s not staged", primary)
			}
			if err := analyzePkgFile(t, getStdSearchPath(), stdDir, primary); err != nil {
				t.Errorf("std file reported as a duplicate of itself: %v", err)
			}
		})
	}
}

func writeDupPkg(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "dup")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, src := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// Forgiving a package's merged copy of itself must not cost duplicate
// detection. These six shapes have to keep failing.
func TestDuplicateDeclarationsStillRejected(t *testing.T) {
	cases := []struct {
		name  string
		files map[string]string
		want  string
	}{
		{
			name:  "type twice in one file",
			files: map[string]string{"a.gala": "package dup\n\ntype Thing struct {\n    val A int\n}\n\ntype Thing struct {\n    val B int\n}\n"},
			want:  "redefined",
		},
		{
			name:  "function twice in one file",
			files: map[string]string{"a.gala": "package dup\n\nfunc Twice() int = 1\n\nfunc Twice() int = 2\n"},
			want:  "redeclared",
		},
		{
			name:  "method twice in one file",
			files: map[string]string{"a.gala": "package dup\n\ntype Box struct {\n    val V int\n}\n\nfunc (b Box) Get() int = b.V\n\nfunc (b Box) Get() int = 0\n"},
			want:  "redefined",
		},
		{
			name: "type across two files",
			files: map[string]string{
				"a.gala": "package dup\n\ntype Thing struct {\n    val A int\n}\n",
				"b.gala": "package dup\n\ntype Thing struct {\n    val B int\n}\n",
			},
			want: "redefined",
		},
		{
			name: "function across two files",
			files: map[string]string{
				"a.gala": "package dup\n\nfunc Once() int = 1\n",
				"b.gala": "package dup\n\nfunc Once() int = 2\n",
			},
			want: "redeclared",
		},
		{
			name: "method across two files",
			files: map[string]string{
				"a.gala": "package dup\n\ntype Thing struct {\n    val A int\n}\n\nfunc (t Thing) Get() int = t.A\n",
				"b.gala": "package dup\n\nfunc (t Thing) Get() int = 0\n",
			},
			want: "redefined",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := writeDupPkg(t, tc.files)
			err := analyzePkgFile(t, getStdSearchPath(), dir, "a.gala")
			if err == nil {
				t.Fatal("duplicate declaration was accepted")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error does not report a duplicate: %v", err)
			}
		})
	}
}
