package main

import (
	"os"
	"path/filepath"

	"strings"
	"testing"

	"github.com/bazelbuild/rules_go/go/tools/bazel"

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

// TestRunBatchResolvesSiblingTypesInEveryPackage pins that batch mode gives
// each file its package's siblings whatever the package is called. The
// analyzer's directory scan skips packages named main or test, so a batch
// relying on it alone rejected the stdlib test package: assertions.gala uses
// T, declared in framework.gala, and failed with GALA-E0023.
func TestRunBatchResolvesSiblingTypesInEveryPackage(t *testing.T) {
	const want = "func Describe(t T) string"
	for _, pkg := range []string{"test", "lib", "main"} {
		t.Run(pkg, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "gala.mod"), []byte("module example.com/repro\n\ngala dev\n"), 0644); err != nil {
				t.Fatal(err)
			}
			pkgDir := filepath.Join(dir, pkg)
			if err := os.MkdirAll(pkgDir, 0755); err != nil {
				t.Fatal(err)
			}
			// uses.gala names T in a type position; T lives only in decl.gala.
			// The _test.gala file declares T too and must not be a sibling,
			// or the package would hold two declarations of T.
			sources := map[string]string{
				"uses.gala":      "package " + pkg + "\n\nfunc Describe(t T) string = t.Name\n",
				"decl.gala":      "package " + pkg + "\n\nstruct T(Name string)\n",
				"decl_test.gala": "package " + pkg + "\n\nstruct T(Other int)\n",
			}
			if pkg == "main" {
				sources["main.gala"] = "package main\n\nfunc main() {\n    Println(Describe(T(\"x\")))\n}\n"
			}
			for name, src := range sources {
				if err := os.WriteFile(filepath.Join(pkgDir, name), []byte(src), 0644); err != nil {
					t.Fatal(err)
				}
			}

			in := []string{filepath.Join(pkgDir, "uses.gala"), filepath.Join(pkgDir, "decl.gala")}
			out := []string{filepath.Join(dir, "out", "uses.gen.go"), filepath.Join(dir, "out", "decl.gen.go")}
			// std must load, or the undefined-name check stands down and a
			// missing sibling goes unnoticed.
			if err := runBatch(in, out, []string{dir, stdlibRoot(t)}); err != nil {
				t.Fatalf("runBatch: %v", err)
			}
			got, err := os.ReadFile(out[0])
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(got), want) {
				t.Errorf("uses.gala output missing %q; got:\n%s", want, got)
			}
		})
	}
}

// TestRunBatchTranspilesStdlibTestPackage batch-transpiles the shipped test
// package the way nix/gala.nix's local bootstrap does: every non-test file at
// once, with the stdlib root as the search path.
func TestRunBatchTranspilesStdlibTestPackage(t *testing.T) {
	root := stdlibRoot(t)
	matches, err := filepath.Glob(filepath.Join(root, "test", "*.gala"))
	if err != nil {
		t.Fatal(err)
	}
	var in, out []string
	outDir := t.TempDir()
	for _, m := range matches {
		if strings.HasSuffix(m, "_test.gala") {
			continue
		}
		in = append(in, m)
		out = append(out, filepath.Join(outDir, strings.TrimSuffix(filepath.Base(m), ".gala")+".gen.go"))
	}
	if len(in) < 2 {
		t.Fatalf("expected the test package's sources in runfiles, found %v", in)
	}
	if err := runBatch(in, out, []string{root}); err != nil {
		t.Fatalf("runBatch: %v", err)
	}
}

// stdlibRoot returns the directory holding the stdlib packages staged as the
// test's data.
func stdlibRoot(t *testing.T) string {
	t.Helper()
	p, err := bazel.Runfile("std/option.gala")
	if err != nil {
		t.Fatalf("locating std in runfiles: %v", err)
	}
	return filepath.Dir(filepath.Dir(p))
}
