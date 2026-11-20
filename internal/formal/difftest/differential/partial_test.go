package differential_test

import (
	"strings"
	"testing"

	"martianoff/gala/internal/formal/difftest"
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
			rec, run, err := difftest.GoCoreLane(gc, t.TempDir(), "partial", *prog, real, stderr)
			if err != nil {
				t.Fatal(err)
			}
			if run.Stdout != "before\n" || rec.Verdict != difftest.Unsupported || rec.Construct != "errors.Is" {
				t.Fatalf("gocore: outcome %q, stdout %q, verdict %s (%s); want unsupported:errors.Is after %q "+
					"(errors.Is must stay out of gocoreexport.RunnableStdlib's files)",
					run.Outcome, run.Stdout, rec.Verdict, rec.Reason, "before\n")
			}
		})
	}
}
