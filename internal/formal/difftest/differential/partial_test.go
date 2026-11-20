package differential_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"martianoff/gala/internal/formal/difftest"
	"martianoff/gala/internal/formal/gocoreexport"
	"martianoff/gala/internal/transpiler/fuzz/progrun"
)

// TestPartialPackageUnsupported pins what GoCore does with a name a partial
// package lacks (gocoreexport.RunnableStdlib): errors.Is lives in Go's
// errors/wrap.go, which the document leaves out, so the run prints what comes
// before it and ends unsupported:errors.Is, never stuck. The document is built
// here from the pinned GOROOT, as the differential run builds it; only the
// programs' own text is in the repository. Like TestDifferential it needs gocore
// and the pinned Go, and fails without them.
//
// It covers the two value sites (a call and a function value). GoCore's other
// stops for a partial package's missing name, a type or an address taken of it,
// have no case: no runnable package lacks an exported type or variable worth
// aiming at. Tested on these programs, not proved.
func TestPartialPackageUnsupported(t *testing.T) {
	gc := difftest.GoCore{Bin: gocoreBin(t)}
	if got := progrun.GoVersion(t); got != difftest.GcRuntimeGoVersion {
		t.Fatalf("Go is %s, but GoCore models %s: run with the Go SDK MODULE.bazel pins (GOROOT)", got, difftest.GcRuntimeGoVersion)
	}
	goroot := progrun.GoRoot(t)
	tests := []struct {
		name string
		use  string
	}{
		{"call", "fmt.Println(errors.Is(e, e))"},
		{"func value", "f := errors.Is\n\tfmt.Println(f(e, e))"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			src := "package main\n\nimport (\n\t\"errors\"\n\t\"fmt\"\n)\n\nfunc main() {\n" +
				"\tfmt.Println(\"before\")\n\te := errors.New(\"x\")\n\t" + tt.use + "\n}\n"
			pkgs, err := gocoreexport.AddStdlib(goroot, []gocoreexport.PackageSource{
				{Path: "main", Files: []gocoreexport.Source{{Name: "main.go", Text: src}}}})
			if err != nil {
				t.Fatal(err)
			}
			// The stop must come from partiality: errors is in the document,
			// marked partial, with its runnable files only.
			var errorsPkg *gocoreexport.PackageSource
			for i := range pkgs {
				if pkgs[i].Path == "errors" {
					errorsPkg = &pkgs[i]
				}
			}
			if errorsPkg == nil || !errorsPkg.Partial || len(errorsPkg.Files) != len(gocoreexport.RunnableStdlib["errors"]) {
				t.Fatalf("errors must be in the document, partial, with only %v: %+v", gocoreexport.RunnableStdlib["errors"], errorsPkg)
			}
			doc, err := gocoreexport.Export(pkgs)
			if err != nil {
				t.Fatal(err)
			}
			data, err := gocoreexport.Marshal(doc)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(t.TempDir(), "prog.gocore.json")
			if err := os.WriteFile(path, data, 0o644); err != nil {
				t.Fatal(err)
			}
			got, err := gc.Run(path)
			if err != nil {
				t.Fatal(err)
			}
			if got.Outcome != "unsupported:errors.Is" || got.Stdout != "before\n" {
				t.Fatalf("gocore: outcome %q, stdout %q (detail %q); want unsupported:errors.Is after %q",
					got.Outcome, got.Stdout, got.Detail, "before\n")
			}
			// The prefix is what Go prints, not only what the test expects.
			out, err := progrun.RunOK(t, progrun.OracleImporter(t), src)
			if err != nil {
				t.Fatalf("go run: %v", err)
			}
			if !strings.HasPrefix(out, got.Stdout) || out == got.Stdout {
				t.Fatalf("go run printed %q: gocore's %q must be a proper prefix of it", out, got.Stdout)
			}
		})
	}
}
