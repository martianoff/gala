package difftest

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"martianoff/gala/internal/formal/coreexport"
	"martianoff/gala/internal/formal/coreexport/analyzerhints"
	"martianoff/gala/internal/transpiler/fuzz/progrun"
)

// Program is one program to compare.
type Program struct {
	// ID names the program in reports and in known_disagreements.json.
	ID     string
	Source Source
	// Path is the file name the program is exported and transpiled as.
	Path string
	Text string
	// Lib and LibPath are a library package the program imports (its
	// source and import path), as FuzzPrograms generates; "" for none.
	Lib, LibPath string
	// Oracle is what the program must do by a third account — an
	// example's .out file, or the generator's model — or nil for none.
	Oracle *Run
}

// Harness compares programs.
type Harness struct {
	Spec Spec
	// Dir is where the Core JSON documents are written.
	Dir string
}

// Check compares one program, and fails tb only for a broken setup: a
// program the exporter cannot export, or a galaspec that breaks its
// contract.
func (h Harness) Check(tb testing.TB, p Program) Record {
	tb.Helper()
	exported, err := coreexport.Export([]coreexport.Source{{Path: p.Path, Text: p.Text}},
		coreexport.Options{LiteralType: analyzerhints.LiteralType})
	if err != nil {
		tb.Fatalf("%s: export: %v", p.ID, err)
	}
	doc, err := coreexport.Marshal(exported)
	if err != nil {
		tb.Fatalf("%s: %v", p.ID, err)
	}
	constructs, err := coreexport.ConstructCounts(exported)
	if err != nil {
		tb.Fatalf("%s: %v", p.ID, err)
	}
	file := filepath.Join(h.Dir, strings.NewReplacer("/", "_", "\\", "_", ":", "_").Replace(p.ID)+".json")
	if err := os.WriteFile(file, doc, 0o644); err != nil {
		tb.Fatal(err)
	}
	spec, err := h.Spec.Run(file)
	if err != nil {
		tb.Fatalf("%s: %v", p.ID, err)
	}
	rec := Record{ID: p.ID, Source: p.Source, Constructs: constructs, Spec: spec.Outcome}
	var (
		real   Run
		stderr string
	)
	if NeedsReal(spec.Outcome) {
		real, stderr = realRun(tb, p)
	}
	rec.Comparison = Compare(spec, real)
	// An example's .out holds stdout and stderr together (the example test
	// compares combined output), so it is no oracle for the stdout of a
	// program that also wrote to stderr: its builtin println, say.
	if p.Oracle != nil && (stderr == "" || real.Outcome.Kind != KindOK) {
		rec.Triage = ThreeWay(spec, real, *p.Oracle)
		if rec.Verdict == Disagree && rec.Triage != TriageNone {
			rec.Reason += " [triage: " + string(rec.Triage) + "]"
		}
	}
	return rec
}

// realRun transpiles, builds and runs p with the real toolchain, and returns
// the run and what it wrote to stderr. A program the transpiler or the Go
// compiler rejects is a KindRejected outcome.
func realRun(tb testing.TB, p Program) (Run, string) {
	tb.Helper()
	rejected := func(detail string) (Run, string) {
		return Run{Outcome: Outcome{Kind: KindRejected, Detail: detail}}, ""
	}
	if p.Lib != "" {
		progrun.WriteLibrary(tb, p.LibPath, p.Lib)
	}
	res := progrun.Transpile(tb, p.Text, p.Path)
	switch {
	case res.Panic != nil:
		return rejected("the transpiler panicked")
	case res.Err != nil:
		return rejected("the transpiler rejected it: " + firstLine(res.Err.Error()))
	}
	// Type-checking loads every GALA package the program imports into the
	// importer, which progrun.Run then writes out as Go.
	imp := progrun.OracleImporter(tb)
	if tc := imp.TypeCheck("main.go", res.Go); tc.Skipped != "" || len(tc.Errors) > 0 {
		return rejected("go type-check: " + firstLine(tc.Skipped+strings.Join(tc.Errors, "\n")))
	}
	out, err := progrun.Run(tb, imp, res.Go)
	if err != nil {
		var build *progrun.Error
		if errors.As(err, &build) {
			return rejected(build.Step + ": " + firstLine(build.Out))
		}
		tb.Fatalf("%s: %v", p.ID, err)
	}
	return Run{Stdout: out.Stdout, Outcome: GoOutcome(out.ExitCode, out.Stderr, out.TimedOut)}, out.Stderr
}

// firstLine is the first line of a diagnostic, past the "# package" header
// go build prints.
func firstLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		if line = strings.TrimSpace(line); line != "" && !strings.HasPrefix(line, "# ") {
			return line
		}
	}
	return ""
}
