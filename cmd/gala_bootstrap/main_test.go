package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/analyzer"
	"martianoff/gala/internal/transpiler/generator"
	"martianoff/gala/internal/transpiler/transformer"
)

// TestRunBatchSeesUnlistedSiblings pins the fix for the batch mode hiding
// same-directory files that are not listed in inList. The analyzer's explicit
// package-file list replaces its directory scan rather than adding to it, so
// deriving siblings from inList alone made any .gala file outside the list
// invisible to its neighbours (e.g. stdlib concurrent/retry.gala). The output
// still exited 0 — it was just wrong.
func TestRunBatchSeesUnlistedSiblings(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "gala.mod"), []byte("module example.com/repro\n\ngala dev\n"), 0644); err != nil {
		t.Fatal(err)
	}
	pkgDir := filepath.Join(dir, "pkg")
	if err := os.MkdirAll(pkgDir, 0755); err != nil {
		t.Fatal(err)
	}

	// c.gala defines the type a.gala uses but is deliberately absent from the
	// batch inputs; b.gala only exists so the sibling list is non-empty.
	sources := map[string]string{
		"a.gala": "package pkg\n\nfunc NameOf() string = NewThing().name\n",
		"b.gala": "package pkg\n\nfunc Other() int = 1\n",
		"c.gala": "package pkg\n\nstruct Thing(name string)\n\nfunc NewThing() Thing = Thing(\"x\")\n",
	}
	for name, src := range sources {
		if err := os.WriteFile(filepath.Join(pkgDir, name), []byte(src), 0644); err != nil {
			t.Fatal(err)
		}
	}

	inA := filepath.Join(pkgDir, "a.gala")
	inB := filepath.Join(pkgDir, "b.gala")
	outA := filepath.Join(dir, "a.gen.go")
	outB := filepath.Join(dir, "b.gen.go")
	if err := runBatch([]string{inA, inB}, []string{outA, outB}, []string{dir}); err != nil {
		t.Fatalf("runBatch: %v", err)
	}

	got, err := os.ReadFile(outA)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "NewThing().name.Get()") {
		t.Errorf("a.gala did not see unlisted sibling c.gala; got:\n%s", got)
	}

	// The single-file path always scans the directory; batch output must match.
	p := transpiler.NewAntlrGalaParser()
	single := transpiler.NewGalaToGoTranspiler(
		p,
		analyzer.NewGalaAnalyzer(p, []string{dir}),
		transformer.NewGalaASTTransformer(),
		generator.NewGoCodeGenerator(),
	)
	srcA, err := os.ReadFile(inA)
	if err != nil {
		t.Fatal(err)
	}
	want, err := single.Transpile(string(srcA), inA)
	if err != nil {
		t.Fatalf("single-file transpile: %v", err)
	}
	if string(got) != want {
		t.Errorf("batch output differs from directory-scanning single-file output:\ngot:\n%s\nwant:\n%s", got, want)
	}
}

// TestRunBatchResolvesSiblingTypesInTestPackage pins the fix for a library
// package named `test` (the shipped test framework): its files are compiled
// together, so a type declared in one of them must resolve from another.
// Relying on the analyzer's directory scan left the sibling invisible because
// that scan is skipped for `main`/`test` packages, and the batch then failed
// on `T` or emitted Go that could not compile.
func TestRunBatchResolvesSiblingTypesInTestPackage(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "gala.mod"), []byte("module example.com/repro\n\ngala dev\n"), 0644); err != nil {
		t.Fatal(err)
	}
	pkgDir := filepath.Join(dir, "test")
	if err := os.MkdirAll(pkgDir, 0755); err != nil {
		t.Fatal(err)
	}

	sources := map[string]string{
		"framework.gala": "package test\n\nstruct T(name string, count int)\n",
		// Constructing the sibling's struct with named arguments only type
		// checks when T's declaration is in scope.
		"assertions.gala": "package test\n\nfunc Make() T = T(name = \"x\", count = 1)\n",
		// A same-directory test file may declare a different package; the
		// sibling list must not pick it up.
		"framework_test.gala": "package main\n\nfunc main() {}\n",
	}
	for name, src := range sources {
		if err := os.WriteFile(filepath.Join(pkgDir, name), []byte(src), 0644); err != nil {
			t.Fatal(err)
		}
	}

	inA := filepath.Join(pkgDir, "assertions.gala")
	inF := filepath.Join(pkgDir, "framework.gala")
	outA := filepath.Join(dir, "assertions.gen.go")
	outF := filepath.Join(dir, "framework.gen.go")
	if err := runBatch([]string{inA, inF}, []string{outA, outF}, []string{dir}); err != nil {
		t.Fatalf("runBatch: %v", err)
	}

	got, err := os.ReadFile(outA)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "T{") {
		t.Errorf("assertions.gala did not resolve its sibling's struct T; got:\n%s", got)
	}
}
