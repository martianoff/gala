package differential_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"martianoff/gala/internal/formal/difftest"
	"martianoff/gala/internal/formal/gocoreexport"
)

// partialProgram prints "before", then uses errors.Is where USE stands.
const partialProgram = `package main

import (
	"errors"
	"fmt"
)

func main() {
	fmt.Println("before")
	e := errors.New("x")
	USE
}
`

// TestPartialPackageUnsupported pins what GoCore does with a name a partial
// package lacks (gocoreexport.RunnableStdlib): errors.Is lives in Go's
// errors/wrap.go, which the document leaves out, so GoCore prints what comes
// before it and stops unsupported:errors.Is, never stuck, and the verdict
// against go run is UNSUPPORTED. Each program runs through the harness's own
// Go run and GoCore lane (difftest.RunGo, difftest.GoCoreLane), the document
// built from the pinned GOROOT; only the programs' text is in the repository.
// Like TestDifferential it needs gocore and the pinned Go, and fails without
// them.
//
// It covers the two value sites (a call and a function value). GoCore's other
// stops for a partial package's missing name, a type or an address taken of it,
// have no case: no runnable package lacks an exported type or variable worth
// aiming at. Tested on these programs, not proved.
func TestPartialPackageUnsupported(t *testing.T) {
	gc := difftest.GoCore{Bin: gocoreBin(t)}
	requirePinnedGo(t)
	for name, use := range map[string]string{
		"call":       "fmt.Println(errors.Is(e, e))",
		"func value": "f := errors.Is\n\tfmt.Println(f(e, e))",
	} {
		t.Run(name, func(t *testing.T) {
			src := strings.Replace(partialProgram, "USE", use, 1)
			real, stderr, prog := difftest.RunGo(t, "partial", src, true)
			if real.Outcome.Kind != difftest.KindOK || real.Stdout != "before\ntrue\n" || prog == nil {
				t.Fatalf("go run: %s, stdout %q\n%s", real.Outcome, real.Stdout, stderr)
			}
			dir := t.TempDir()
			rec, run, err := difftest.GoCoreLane(gc, dir, "partial", *prog, real, stderr)
			if err != nil {
				t.Fatal(err)
			}
			requirePartialErrors(t, dir)
			if run.Stdout != "before\n" || rec.Verdict != difftest.Unsupported || rec.Construct != "errors.Is" {
				t.Fatalf("gocore: outcome %q, stdout %q, verdict %s (%s); want unsupported:errors.Is after %q "+
					"(errors.Is must stay out of gocoreexport.RunnableStdlib's files)",
					run.Outcome, run.Stdout, rec.Verdict, rec.Reason, "before\n")
			}
		})
	}
}

// requirePartialErrors fails the test unless the one document GoCoreLane
// wrote under dir holds package errors marked partial, with exactly its
// runnable files. GoCore stops the same way at a name of a package the
// document lacks altogether, so this is what makes the stop one at a partial
// package's missing name.
func requirePartialErrors(t *testing.T, dir string) {
	t.Helper()
	docs, err := filepath.Glob(filepath.Join(dir, "*.gocore.json"))
	if err != nil || len(docs) != 1 {
		t.Fatalf("want one GoCore document under %s, found %v (%v)", dir, docs, err)
	}
	data, err := os.ReadFile(docs[0])
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Packages []struct {
			Path    string `json:"path"`
			Partial bool   `json:"partial"`
			Files   []struct {
				Name string `json:"name"`
			} `json:"files"`
		} `json:"packages"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	for _, p := range doc.Packages {
		if p.Path != "errors" {
			continue
		}
		var names []string
		for _, f := range p.Files {
			names = append(names, f.Name)
		}
		if want := gocoreexport.RunnableStdlib["errors"]; !p.Partial || !slices.Equal(names, want) {
			t.Fatalf("package errors in the document: partial %v, files %v; want partial, files %v", p.Partial, names, want)
		}
		return
	}
	t.Fatal("the document holds no package errors: the stop would not be at a partial package")
}
