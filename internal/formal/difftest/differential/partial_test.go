package differential_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"martianoff/gala/internal/formal/difftest"
	"martianoff/gala/internal/formal/gocoreexport"
)

// TestPartialPackageUnsupported pins what GoCore does with a name a partial
// package lacks (gocoreexport.RunnableStdlib): the run prints what comes before
// it and stops unsupported:<package>.<name>, never stuck, and the verdict
// against go run is UNSUPPORTED.
//   - errors.Is lives in Go's errors/wrap.go, which the document leaves out:
//     a call of it, and it as a function value;
//   - math/bits.Div, in bits.go, which the document holds, panics with
//     divideError, declared in bits_errors.go, which it leaves out.
//
// Each program runs through the harness's own Go run and GoCore lane
// (difftest.RunGo, difftest.GoCoreLane), the document built from the pinned
// GOROOT; only the programs' text is in the repository. Like TestDifferential
// it needs gocore and the pinned Go, and fails without them.
//
// GoCore's other stops for a partial package's missing name, a type or an
// address taken of it, have no case: no runnable package lacks an exported type
// or variable worth aiming at. Tested on these programs, not proved.
func TestPartialPackageUnsupported(t *testing.T) {
	gc := difftest.GoCore{Bin: gocoreBin(t)}
	requirePinnedGo(t)
	tests := []struct {
		name string
		src  string
		// pkg is the partial package; construct is where GoCore stops.
		pkg, construct string
		// goOut is what go run prints, all of it.
		goOut string
	}{
		{"errors.Is call", `package main

import (
	"errors"
	"fmt"
)

func main() {
	fmt.Println("before")
	e := errors.New("x")
	fmt.Println(errors.Is(e, e))
}
`, "errors", "errors.Is", "before\ntrue\n"},
		{"errors.Is value", `package main

import (
	"errors"
	"fmt"
)

func main() {
	fmt.Println("before")
	e := errors.New("x")
	f := errors.Is
	fmt.Println(f(e, e))
}
`, "errors", "errors.Is", "before\ntrue\n"},
		{"math/bits sibling var", `package main

import (
	"fmt"
	"math/bits"
)

func main() {
	fmt.Println("before")
	var y uint
	q, r := bits.Div(0, 1, y)
	fmt.Println(q, r)
}
`, "math/bits", "math/bits.divideError", "before\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			real, stderr, prog := difftest.RunGo(t, "partial", tt.src, true)
			if real.Stdout != tt.goOut || prog == nil {
				t.Fatalf("go run: %s, stdout %q\n%s", real.Outcome, real.Stdout, stderr)
			}
			dir := t.TempDir()
			rec, run, err := difftest.GoCoreLane(gc, dir, "partial", *prog, real, stderr)
			if err != nil {
				t.Fatal(err)
			}
			requirePartial(t, dir, tt.pkg)
			if run.Stdout != "before\n" || rec.Verdict != difftest.Unsupported || rec.Construct != tt.construct {
				t.Fatalf("gocore: outcome %q, stdout %q, verdict %s (%s); want unsupported:%s after %q "+
					"(it must stay out of gocoreexport.RunnableStdlib's files)",
					run.Outcome, run.Stdout, rec.Verdict, rec.Reason, tt.construct, "before\n")
			}
		})
	}
}

// requirePartial fails the test unless the one document GoCoreLane wrote under
// dir holds package pkg marked partial, with exactly its runnable files. GoCore
// stops the same way at a name of a package the document lacks altogether, so
// this is what makes the stop one at a partial package's missing name.
func requirePartial(t *testing.T, dir, pkg string) {
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
		if p.Path != pkg {
			continue
		}
		var names []string
		for _, f := range p.Files {
			names = append(names, f.Name)
		}
		if want := gocoreexport.RunnableStdlib[pkg]; !p.Partial || !slices.Equal(names, want) {
			t.Fatalf("package %s in the document: partial %v, files %v; want partial, files %v", pkg, p.Partial, names, want)
		}
		return
	}
	t.Fatalf("the document holds no package %s: the stop would not be at a partial package", pkg)
}
