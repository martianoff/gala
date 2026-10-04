package commands

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

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

// referenceSingleScan transpiles srcPath the way the single-file path always
// does: the analyzer discovers siblings by scanning the input's directory.
func referenceSingleScan(t *testing.T, srcPath string) string {
	t.Helper()
	p := transpiler.NewAntlrGalaParser()
	tr := transpiler.NewGalaToGoTranspiler(
		p,
		analyzer.NewGalaAnalyzer(p, []string{filepath.Dir(srcPath)}),
		transformer.NewGalaASTTransformer(),
		generator.NewGoCodeGenerator(),
	)
	content, err := os.ReadFile(srcPath)
	if err != nil {
		t.Fatal(err)
	}
	goCode, err := tr.Transpile(string(content), srcPath)
	if err != nil {
		t.Fatalf("reference single-file transpile of %s: %v", srcPath, err)
	}
	return goCode
}

// setupSiblingProject writes a gala.mod and two packages. In each package
// a.gala uses a type defined in that same package's c.gala, which is
// deliberately left out of the transpile inputs. Both packages define a type
// with the same name so a cross-package sibling mix-up fails loudly.
func setupSiblingProject(t *testing.T) (root, pkg1, pkg2 string) {
	t.Helper()
	root = t.TempDir()
	writeFile(t, filepath.Join(root, "gala.mod"), "module example.com/repro\n\ngala dev\n")

	for _, p := range []string{"pkg1", "pkg2"} {
		dir := filepath.Join(root, p)
		writeFile(t, filepath.Join(dir, "a.gala"),
			"package "+p+"\n\nfunc NameOf() string = NewThing().name\n")
		writeFile(t, filepath.Join(dir, "b.gala"),
			"package "+p+"\n\nfunc Other() int = 1\n")
		writeFile(t, filepath.Join(dir, "c.gala"),
			"package "+p+"\n\nstruct Thing(name string)\n\nfunc NewThing() Thing = Thing(\"x\")\n")
	}
	return root, filepath.Join(root, "pkg1"), filepath.Join(root, "pkg2")
}

// TestTranspilePackageScanFalseIsExplicitSiblings documents the limitation of
// the default mode: only the listed inputs are siblings, so a same-directory
// .gala file that is not in --inputs is invisible (here: a.gala cannot resolve
// the NewThing defined in c.gala).
func TestTranspilePackageScanFalseIsExplicitSiblings(t *testing.T) {
	root, pkg, _ := setupSiblingProject(t)
	out := t.TempDir()

	inA := filepath.Join(pkg, "a.gala")
	inB := filepath.Join(pkg, "b.gala")
	outA := filepath.Join(out, "a.gen.go")
	outB := filepath.Join(out, "b.gen.go")

	err := transpilePackage([]string{inA, inB}, []string{outA, outB}, root, "", false)
	if err == nil {
		t.Fatal("scan=false with c.gala omitted from inputs: expected a failure")
	}
	if _, statErr := os.Stat(outA); statErr == nil {
		t.Error("a.gen.go should not have been written: c.gala was not a listed sibling")
	}
	if _, statErr := os.Stat(outB); statErr != nil {
		t.Errorf("b.gen.go should still have been written: %v", statErr)
	}
}

// TestTranspilePackageScanSeesUnlistedSiblings pins the --scan fix: the
// directory scan sees same-directory files outside --inputs, matching
// gala_bootstrap's batch mode and the single-file path, byte for byte.
func TestTranspilePackageScanSeesUnlistedSiblings(t *testing.T) {
	root, pkg, _ := setupSiblingProject(t)
	out := t.TempDir()

	inA := filepath.Join(pkg, "a.gala")
	inB := filepath.Join(pkg, "b.gala")
	outA := filepath.Join(out, "a.gen.go")
	outB := filepath.Join(out, "b.gen.go")

	if err := transpilePackage([]string{inA, inB}, []string{outA, outB}, root, "", true); err != nil {
		t.Fatalf("scan=true: %v", err)
	}

	got, err := os.ReadFile(outA)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "NewThing().name.Get()") {
		t.Errorf("a.gala did not see unlisted sibling c.gala; got:\n%s", got)
	}
	if want := referenceSingleScan(t, inA); string(got) != want {
		t.Errorf("scan output differs from directory-scanning single-file output:\ngot:\n%s\nwant:\n%s", got, want)
	}
}

// TestTranspilePackageScanResolvesTestPackageSiblings pins that --scan gives a
// package named test its siblings. The analyzer's own directory scan skips
// main and test packages, so relying on it rejected uses.gala's reference to
// T (declared in decl.gala) with GALA-E0023.
func TestTranspilePackageScanResolvesTestPackageSiblings(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "gala.mod"), "module example.com/repro\n\ngala dev\n")
	pkg := filepath.Join(root, "test")
	writeFile(t, filepath.Join(pkg, "uses.gala"), "package test\n\nfunc Describe(t T) string = t.Name\n")
	writeFile(t, filepath.Join(pkg, "decl.gala"), "package test\n\nstruct T(Name string)\n")
	out := t.TempDir()

	in := []string{filepath.Join(pkg, "uses.gala"), filepath.Join(pkg, "decl.gala")}
	outs := []string{filepath.Join(out, "uses.gen.go"), filepath.Join(out, "decl.gen.go")}
	if err := transpilePackage(in, outs, root, "", true); err != nil {
		t.Fatalf("scan=true over a package named test: %v", err)
	}
}

// TestTranspilePackageScanDoesNotMixPackages runs one --scan invocation over
// files from two packages that both define Thing. Per-file directory scanning
// must keep each package's siblings separate; a shared sibling set would make
// the duplicate type clash (or resolve across packages).
func TestTranspilePackageScanDoesNotMixPackages(t *testing.T) {
	root, pkg1, pkg2 := setupSiblingProject(t)
	out := t.TempDir()

	in1 := filepath.Join(pkg1, "a.gala")
	in2 := filepath.Join(pkg2, "a.gala")
	out1 := filepath.Join(out, "pkg1", "a.gen.go")
	out2 := filepath.Join(out, "pkg2", "a.gen.go")

	if err := transpilePackage([]string{in1, in2}, []string{out1, out2}, root, "", true); err != nil {
		t.Fatalf("scan=true across two packages: %v", err)
	}
	for in, outPath := range map[string]string{in1: out1, in2: out2} {
		got, err := os.ReadFile(outPath)
		if err != nil {
			t.Fatal(err)
		}
		if want := referenceSingleScan(t, in); string(got) != want {
			t.Errorf("%s output differs from directory-scanning single-file output:\ngot:\n%s\nwant:\n%s", in, got, want)
		}
	}
}
