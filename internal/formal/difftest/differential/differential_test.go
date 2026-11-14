// Package differential_test is the differential run of the formal semantics
// (docs/FORMAL_SEMANTICS.MD): every example with a .out file and a sample
// of FuzzPrograms' generated programs, each compared three ways — galaspec,
// the real transpiler and Go, and the program's own oracle.
//
// It needs galaspec, so its target is manual and the formal CI job runs it.
// --config=formal builds //formal:galaspec with rules_lean and uses it:
//
//	bazel test --config=formal //internal/formal/difftest/differential:differential_test
//
// GALA_SPEC_BIN, when set, names another galaspec instead (e.g. a Lake build):
//
//	GALA_SPEC_BIN=/path/to/galaspec bazel test //internal/formal/difftest/differential:differential_test \
//	    --test_env=GALA_SPEC_BIN
//
// With neither it fails; it never passes without comparing anything.
//
// Each program gala2lean translates is also run as Lean, the third oracle,
// and compared with Go. --config=formal gives it the toolchain's lean;
// without the config GALA_LEAN_BIN must name one (e.g. elan's), with
// --test_env=GALA_LEAN_BIN. It needs one either way. It
// writes the coverage section of formal/COVERAGE.md and a summary to its
// undeclared outputs (see difftest.Render).
package differential_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/bazelbuild/rules_go/go/runfiles"
	"github.com/bazelbuild/rules_go/go/tools/bazel"

	"martianoff/gala/internal/formal/difftest"
	"martianoff/gala/internal/formal/gala2lean"
)

// defaultGenerated is how many generated programs a run compares;
// GALA_DIFFTEST_GENERATED overrides it.
const defaultGenerated = 40

// generatorSeed seeds the generated programs, so their IDs name the same
// programs from run to run.
const generatorSeed = 961

// specRlocationEnv names //formal:galaspec in the runfiles; the target sets it
// under --config=formal.
const specRlocationEnv = "GALA_SPEC_RLOCATION"

// specBin is the galaspec to compare with: GALA_SPEC_BIN when set, otherwise
// the //formal:galaspec that --config=formal puts in the runfiles.
func specBin(t *testing.T) string {
	if bin := os.Getenv(difftest.SpecEnv); bin != "" {
		if _, err := os.Stat(bin); err != nil {
			t.Fatalf("%s=%s: %v", difftest.SpecEnv, bin, err)
		}
		return bin
	}
	if loc := os.Getenv(specRlocationEnv); loc != "" {
		bin, err := runfiles.Rlocation(loc)
		if err == nil {
			_, err = os.Stat(bin)
		}
		if err != nil {
			t.Fatalf("finding //formal:galaspec (%s) in the runfiles: %v", loc, err)
		}
		return bin
	}
	t.Fatalf("no galaspec: the differential run needs one and passes nothing without it. Either\n"+
		"  bazel test --config=formal //internal/formal/difftest/differential:differential_test\n"+
		"(builds //formal:galaspec with rules_lean), or point %s at a galaspec, e.g. one built with\n"+
		"`lake build galaspec` in formal/:\n"+
		"  %s=/path/to/galaspec bazel test //internal/formal/difftest/differential:differential_test --test_env=%s",
		difftest.SpecEnv, difftest.SpecEnv, difftest.SpecEnv)
	return ""
}

// leanEnv names a lean executable for gala2lean's translations; under
// --config=formal the test gets the toolchain's (leanRlocationEnv).
const (
	leanEnv          = "GALA_LEAN_BIN"
	leanRlocationEnv = "GALA_LEAN_RLOCATION"
)

// leanBin is the lean that runs gala2lean's translations, the third oracle:
// GALA_LEAN_BIN when set, otherwise the toolchain's that --config=formal puts
// in the runfiles.
func leanBin(t *testing.T) string {
	if bin := os.Getenv(leanEnv); bin != "" {
		if _, err := os.Stat(bin); err != nil {
			t.Fatalf("%s=%s: %v", leanEnv, bin, err)
		}
		return bin
	}
	if loc := os.Getenv(leanRlocationEnv); loc != "" {
		bin, err := gala2lean.LeanFromRunfiles(loc)
		if err != nil {
			t.Fatalf("finding //formal:lean_runtime (%s) in the runfiles: %v", loc, err)
		}
		return bin
	}
	t.Fatalf("no lean for gala2lean's translations: run with --config=formal, or point %s at a lean\n"+
		"(elan's, the version formal/lean-toolchain pins) and pass --test_env=%s", leanEnv, leanEnv)
	return ""
}

func TestDifferential(t *testing.T) {
	bin := specBin(t)
	fuel := envInt(t, "GALA_SPEC_FUEL", 0)
	known, err := difftest.ParseKnown(readData(t, "known_disagreements.json"))
	if err != nil {
		t.Fatal(err)
	}
	var baseline difftest.Baseline
	unmarshal(t, "baseline.json", &baseline)
	var proved map[string][]string
	unmarshal(t, "proved_constructs.json", &proved)

	programs := difftest.ExamplePrograms(t)
	if len(programs) < 200 {
		t.Fatalf("found %d examples with a .out file in the runfiles; //examples:expected_outputs is not staged", len(programs))
	}
	generated, rejected := difftest.GeneratedPrograms(envInt(t, "GALA_DIFFTEST_GENERATED", defaultGenerated), generatorSeed)
	programs = append(programs, generated...)
	slices.SortFunc(programs, func(a, b difftest.Program) int { return strings.Compare(a.ID, b.ID) })

	prelude, err := bazel.Runfile("formal/Gala/Gala2Lean/Prelude.lean")
	if err != nil {
		t.Fatal(err)
	}
	lean, err := gala2lean.NewRunner(leanBin(t), prelude, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := difftest.Harness{Spec: difftest.Spec{Bin: bin, Fuel: fuel}, Dir: t.TempDir(), Lean: lean}
	var records []difftest.Record
	results := map[string]difftest.Comparison{}
	var problems []string
	for _, p := range programs {
		r := h.Check(t, p)
		records = append(records, r)
		results[r.ID] = r.Comparison
		results[difftest.LeanKey(r.ID)] = r.Lean.Comparison
		if r.Triage == difftest.TriageOracle {
			problems = append(problems, fmt.Sprintf("%s: the spec and Go agree, but the program's oracle (.out or the generator's model) says otherwise", r.ID))
		}
	}
	problems = append(problems, difftest.CheckKnown(known, results)...)
	problem, note := difftest.CheckBaseline(baseline, records)
	if problem != "" {
		problems = append(problems, problem)
	}

	section := difftest.Render(difftest.Coverage{Records: records, Known: known, Baseline: baseline, Proved: proved})
	summary := summarize(records, rejected, problems, note)
	writeOutput(t, "coverage_section.md", section)
	writeOutput(t, "summary.md", summary)
	t.Log("\n" + summary)
	if len(problems) > 0 {
		t.Fatalf("%d problems:\n%s", len(problems), strings.Join(problems, "\n"))
	}
}

// summarize is the run's report for the CI job summary.
func summarize(records []difftest.Record, rejected int, problems []string, note string) string {
	var b strings.Builder
	b.WriteString("## Differential run\n\n")
	counts := map[difftest.Verdict]int{}
	leanCounts := map[difftest.Verdict]int{}
	for _, r := range records {
		counts[r.Verdict]++
		leanCounts[r.Lean.Verdict]++
	}
	for _, v := range difftest.Verdicts {
		fmt.Fprintf(&b, "- %s: %d (gala2lean: %d)\n", v, counts[v], leanCounts[v])
	}
	fmt.Fprintf(&b, "- generated programs that must be rejected, left out: %d\n", rejected)
	if note != "" {
		fmt.Fprintf(&b, "\n%s\n", note)
	}
	if len(problems) > 0 {
		b.WriteString("\n### Problems\n\n")
		for _, p := range problems {
			fmt.Fprintf(&b, "- %s\n", strings.ReplaceAll(p, "\n", " "))
		}
	}
	var odd []string
	for _, r := range records {
		if r.Verdict == difftest.Disagree || r.Verdict == difftest.Inconclusive {
			odd = append(odd, fmt.Sprintf("- `%s` %s: %s", r.ID, r.Verdict, r.Reason))
		}
		if v := r.Lean.Verdict; v == difftest.Disagree || v == difftest.Inconclusive {
			odd = append(odd, fmt.Sprintf("- `%s` %s: %s", difftest.LeanKey(r.ID), v, r.Lean.Reason))
		}
	}
	if len(odd) > 0 {
		b.WriteString("\n### Disagreeing and inconclusive programs\n\n" + strings.Join(odd, "\n") + "\n")
	}
	return b.String()
}

func envInt(t *testing.T, name string, def int) int {
	t.Helper()
	s := os.Getenv(name)
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 0 {
		t.Fatalf("%s=%q is not a count", name, s)
	}
	return n
}

// readData reads one of the harness's data files from the runfiles.
func readData(t *testing.T, name string) []byte {
	t.Helper()
	p, err := bazel.Runfile("internal/formal/difftest/" + name)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func unmarshal(t *testing.T, name string, v any) {
	t.Helper()
	if err := json.Unmarshal(readData(t, name), v); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
}

// writeOutput writes an undeclared test output, which the CI job publishes.
func writeOutput(t *testing.T, name, content string) {
	t.Helper()
	dir := os.Getenv("TEST_UNDECLARED_OUTPUTS_DIR")
	if dir == "" {
		return
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
