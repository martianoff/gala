package differential_test

import (
	"go/ast"
	"go/parser"
	"go/token"
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
// before it and ends unsupported:errors.Is, never stuck, and the harness's
// verdict is UNSUPPORTED. The document is built here from the pinned GOROOT by
// the harness's own export (difftest.ExportGoCore); only the programs' text is
// in the repository. Like TestDifferential it needs gocore and the pinned Go,
// and fails without them.
//
// It covers the two value sites (a call and a function value). GoCore's other
// stops for a partial package's missing name, a type or an address taken of it,
// have no case: no runnable package lacks an exported type or variable worth
// aiming at. Tested on these programs, not proved.
func TestPartialPackageUnsupported(t *testing.T) {
	gc := difftest.GoCore{Bin: gocoreBin(t)}
	requirePinnedGo(t)
	goroot := progrun.GoRoot(t)
	requireLacks(t, goroot, "errors", "Is")
	imp := progrun.OracleImporter(t)
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
			if tc := imp.TypeCheck("main.go", src); tc.Skipped != "" || len(tc.Errors) > 0 {
				t.Fatalf("go type-check: %s%s", tc.Skipped, strings.Join(tc.Errors, "\n"))
			}
			res, err := progrun.Run(t, imp, src)
			if err != nil {
				t.Fatalf("go run: %v", err)
			}
			real := difftest.Run{Stdout: res.Stdout, Outcome: difftest.GoOutcome(res.ExitCode, res.Stderr, res.TimedOut)}
			if real.Outcome.Kind != difftest.KindOK {
				t.Fatalf("go run: %s\n%s", real.Outcome, res.Stderr)
			}
			path, err := difftest.ExportGoCore(t.TempDir(), "partial", difftest.GoProgram{Main: src, GoRoot: goroot})
			if err != nil {
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
			// What Go prints goes on past the stop: the prefix is tied to it.
			if real.Stdout == got.Stdout {
				t.Fatalf("go run printed %q, no more than gocore did before it stopped", real.Stdout)
			}
			if r := difftest.CompareGoCore(got, real, ""); r.Verdict != difftest.Unsupported || r.Construct != "errors.Is" {
				t.Fatalf("verdict %s (%s), construct %q; want UNSUPPORTED at errors.Is", r.Verdict, r.Reason, r.Construct)
			}
		})
	}
}

// requireLacks fails the test unless the runnable files of the package at ip,
// read from goroot, declare no function name: a stop at it then comes from
// the package being partial.
func requireLacks(t *testing.T, goroot, ip, name string) {
	t.Helper()
	files := gocoreexport.RunnableStdlib[ip]
	if len(files) == 0 {
		t.Fatalf("%s has no runnable files (gocoreexport.RunnableStdlib)", ip)
	}
	for _, f := range files {
		file, err := parser.ParseFile(token.NewFileSet(), filepath.Join(goroot, "src", filepath.FromSlash(ip), f), nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range file.Decls {
			if fn, ok := d.(*ast.FuncDecl); ok && fn.Recv == nil && fn.Name.Name == name {
				t.Fatalf("%s/%s declares %s: the test needs a name the runnable files lack", ip, f, name)
			}
		}
	}
}
