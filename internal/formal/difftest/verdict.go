package difftest

import (
	"fmt"
	"strings"
)

// Verdict is the result of comparing the spec's run with the real one.
type Verdict string

const (
	// Agree: same stdout and the same outcome.
	Agree Verdict = "AGREE"
	// Disagree: the stdout or the outcome differ. It fails the build unless
	// the program is a known disagreement.
	Disagree Verdict = "DISAGREE"
	// Unsupported: the program is outside the spec's subset. Counted in
	// the coverage report, never passed silently.
	Unsupported Verdict = "UNSUPPORTED"
	// Inconclusive: galaspec ran out of fuel. Never counted as agreement.
	Inconclusive Verdict = "INCONCLUSIVE"
)

// Verdicts is every verdict, in report order.
var Verdicts = []Verdict{Agree, Disagree, Unsupported, Inconclusive}

// Run is what one side did: its stdout and how it ended.
type Run struct {
	Stdout  string
	Outcome Outcome
}

// Comparison is a verdict and, for anything but agreement, why.
type Comparison struct {
	Verdict Verdict
	Reason  string
}

// NeedsReal reports whether the verdict for spec's run needs the real one: a
// run out of fuel is decided by the spec alone, and so is an unsupported one
// that printed nothing, so the program need not be built. An unsupported run
// that printed must have printed a prefix of what Go did (Compare).
func NeedsReal(spec Run) bool {
	switch spec.Outcome.Kind {
	case KindFuel:
		return false
	case KindUnsupported:
		return spec.Stdout != ""
	}
	return true
}

// Compare is the verdict for the spec's run and the real one. real is not
// looked at when NeedsReal(spec) is false. An unsupported run is UNSUPPORTED
// once what it printed before stopping is a prefix of what Go printed
// (stoppedEarlyDiff), and a disagreement otherwise. A malformed document is a
// disagreement: the exporter and the spec must agree on Core JSON.
func Compare(spec, real Run) Comparison {
	switch spec.Outcome.Kind {
	case KindUnsupported:
		if d := stoppedEarlyDiff("galaspec", spec.Outcome.String(), spec.Stdout, real); d != "" {
			return Comparison{Disagree, d}
		}
		return Comparison{Unsupported, "outside the spec's subset: " + spec.Outcome.Detail}
	case KindFuel:
		return Comparison{Inconclusive, "galaspec ran out of fuel"}
	case KindMalformed:
		return Comparison{Disagree, "galaspec rejected the Core JSON document as malformed"}
	}
	if d := diff("spec", spec, "go", real); d != "" {
		return Comparison{Disagree, d}
	}
	return Comparison{Verdict: Agree}
}

// stoppedEarlyDiff checks a run that stopped with outcome (an `unsupported:`
// one) after printing stdout against Go's: what it printed must be a prefix of
// what Go printed, or the stop would hide a wrong line. name is the run's
// (galaspec, gala2lean or gocore). The comparison is exact, as the comparison
// of a whole run is. It is "" when it is a prefix, and when Go did not end ok
// or with a panic: a rejected program has no output, and an exit, crash or
// time-out may have cut Go's short (a process killed from outside is an
// exit). Otherwise it is the disagreement, with the first line that differs.
func stoppedEarlyDiff(name, outcome, stdout string, real Run) string {
	if real.Outcome.Kind != KindOK && real.Outcome.Kind != KindPanic {
		return ""
	}
	if strings.HasPrefix(real.Stdout, stdout) {
		return ""
	}
	return name + " stopped (" + outcome + ") after printing what Go did not: stdout: " +
		firstDifference(name, stdout, "go", real.Stdout)
}

// diff describes how run a (named na) differs from run b (named nb): the
// outcomes, then the first stdout line that differs. It is "" when they are
// the same.
func diff(na string, a Run, nb string, b Run) string {
	var parts []string
	if a.Outcome != b.Outcome {
		parts = append(parts, fmt.Sprintf("outcome: %s=%s, %s=%s", na, a.Outcome, nb, b.Outcome))
	}
	if a.Stdout != b.Stdout {
		parts = append(parts, "stdout: "+firstDifference(na, a.Stdout, nb, b.Stdout))
	}
	return strings.Join(parts, "; ")
}

// firstDifference names the first line where a and b differ.
func firstDifference(na, a, nb, b string) string {
	la, lb := strings.Split(a, "\n"), strings.Split(b, "\n")
	for i := 0; ; i++ {
		switch {
		case i >= len(la):
			return fmt.Sprintf("line %d: %s ends, %s=%q", i+1, na, nb, lb[i])
		case i >= len(lb):
			return fmt.Sprintf("line %d: %s=%q, %s ends", i+1, na, la[i], nb)
		case la[i] != lb[i]:
			return fmt.Sprintf("line %d: %s=%q, %s=%q", i+1, na, la[i], nb, lb[i])
		}
	}
}

// Triage is where a three-way comparison points: the spec, the real run and
// a third oracle that says what the program must print — an example's .out
// file, or FuzzPrograms' model of its generated program.
type Triage string

const (
	// TriageNone: all three agree.
	TriageNone Triage = ""
	// TriageTranspiler: the spec and the oracle agree, the real run does
	// not. A transpiler bug: file an issue with a regression example.
	TriageTranspiler Triage = "transpiler bug"
	// TriageSpecOrOracle: the real run and the oracle agree, the spec does
	// not. Either the spec is wrong, or the oracle enshrines a transpiler
	// bug. Check the spec against docs/GALA.MD: if the spec matches it,
	// the oracle is wrong; if not, fix the spec; if the docs are silent, it
	// is a language decision for the maintainer.
	TriageSpecOrOracle Triage = "spec bug, or the oracle enshrines a transpiler bug"
	// TriageOracle: the spec and the real run agree, the oracle does not.
	// The oracle is wrong.
	TriageOracle Triage = "the oracle is wrong"
	// TriageAll: all three differ.
	TriageAll Triage = "all three differ"
)

// ThreeWay triages the spec's run, the real run and the oracle's. It is
// TriageNone when galaspec did not run the program to an end (it ran out of
// fuel or stopped unsupported) or rejected the document. The spec and the real
// run are compared exactly; each is compared with the oracle as an example's
// test compares a run with its .out file, leading and trailing white space
// aside.
func ThreeWay(spec, real, oracle Run) Triage {
	if k := spec.Outcome.Kind; k == KindFuel || k == KindUnsupported || k == KindMalformed {
		return TriageNone
	}
	specGo, specOracle, goOracle := spec == real, matchesOracle(spec, oracle), matchesOracle(real, oracle)
	switch {
	case specGo && specOracle:
		return TriageNone
	case specOracle:
		return TriageTranspiler
	case goOracle:
		return TriageSpecOrOracle
	case specGo:
		return TriageOracle
	}
	return TriageAll
}

// matchesOracle reports whether r ends as oracle says and prints what it
// says, leading and trailing white space aside.
func matchesOracle(r, oracle Run) bool {
	return r.Outcome == oracle.Outcome && strings.TrimSpace(r.Stdout) == strings.TrimSpace(oracle.Stdout)
}
