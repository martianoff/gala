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

// NeedsReal reports whether the real run decides the verdict for spec's
// outcome: an unsupported program or a run out of fuel is decided by the
// spec alone, so the program need not be built.
func NeedsReal(spec Outcome) bool {
	return spec.Kind != KindUnsupported && spec.Kind != KindFuel
}

// Compare is the verdict for the spec's run and the real one. real is not
// looked at when NeedsReal(spec.Outcome) is false. A malformed document is a
// disagreement: the exporter and the spec must agree on Core JSON.
func Compare(spec, real Run) Comparison {
	switch spec.Outcome.Kind {
	case KindUnsupported:
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
// TriageNone when the spec's outcome does not decide a verdict (see
// NeedsReal) or galaspec rejected the document. The spec and the real run
// are compared exactly; each is compared with the oracle as an example's test
// compares a run with its .out file, leading and trailing white space aside.
func ThreeWay(spec, real, oracle Run) Triage {
	if !NeedsReal(spec.Outcome) || spec.Outcome.Kind == KindMalformed {
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
