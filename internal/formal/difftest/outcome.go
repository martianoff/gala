// Package difftest compares GALA's reference semantics, the Lean interpreter
// galaspec, with the real transpiler and Go toolchain (docs/FORMAL_SEMANTICS.MD,
// "Differential harness"). For each program it exports Core JSON, runs
// galaspec on it, transpiles, builds and runs the program, and compares the
// two runs' stdout and outcome. Each comparison is one verdict: AGREE,
// DISAGREE, UNSUPPORTED or INCONCLUSIVE.
//
// The package also keeps the list of known disagreements and renders the
// coverage report that formal/COVERAGE.md carries.
//
// It is a test-support library: no part of the gala CLI links it.
package difftest

import (
	"fmt"
	"strconv"
	"strings"
)

// Kind is how a run ended.
type Kind int

const (
	// KindOK: the program finished normally.
	KindOK Kind = iota
	// KindPanic: the program panicked; the outcome's Detail is a panic class.
	KindPanic
	// KindFuel: galaspec ran out of steps. Never agreement, never disagreement.
	KindFuel
	// KindUnsupported: the program is outside the spec's subset; Detail names
	// the construct.
	KindUnsupported
	// KindMalformed: galaspec rejected the document.
	KindMalformed
	// KindExit: the Go program exited with a non-zero status without a panic;
	// Detail is the status. The spec has no such outcome.
	KindExit
	// KindCrash: the Go runtime died with a fatal error (a stack overflow, say);
	// Detail is its message. The spec has no such outcome.
	KindCrash
	// KindTimeout: the Go program ran past its time limit.
	KindTimeout
	// KindRejected: the transpiler or the Go compiler rejected the program;
	// Detail says which. The spec has no such outcome.
	KindRejected
)

// The panic classes both sides report.
const (
	ClassMatch  = "match"  // no match arm matched
	ClassDivide = "divide" // integer division by zero
	ClassIndex  = "index"  // an index out of range
	ClassOther  = "other"  // any other panic
)

var panicClasses = []string{ClassMatch, ClassDivide, ClassIndex, ClassOther}

// Outcome is how a run ended: a Kind and, for some kinds, a detail.
type Outcome struct {
	Kind   Kind
	Detail string
}

// String is the outcome as galaspec spells it ("ok", "panic:match", "fuel",
// "unsupported:lambda", "malformed"), and in the same style for the outcomes
// only a Go run has ("exit:3", "crash:...", "timeout", "rejected:...").
func (o Outcome) String() string {
	switch o.Kind {
	case KindOK:
		return "ok"
	case KindPanic:
		return "panic:" + o.Detail
	case KindFuel:
		return "fuel"
	case KindUnsupported:
		return "unsupported:" + o.Detail
	case KindMalformed:
		return "malformed"
	case KindExit:
		return "exit:" + o.Detail
	case KindCrash:
		return "crash:" + o.Detail
	case KindTimeout:
		return "timeout"
	case KindRejected:
		return "rejected:" + o.Detail
	}
	return fmt.Sprintf("kind(%d)", int(o.Kind))
}

// specExitCodes is galaspec's exit status for each outcome it reports.
var specExitCodes = map[Kind]int{KindOK: 0, KindPanic: 1, KindFuel: 2, KindUnsupported: 3, KindMalformed: 4}

// outcomePrefix starts the last line of galaspec's stderr.
const outcomePrefix = "galaspec: outcome="

// ParseSpecOutcome reads galaspec's outcome from its stderr and exit status.
// The last non-empty stderr line must be the outcome line, and the outcome
// must be the one the exit status stands for: a run where they disagree is a
// broken galaspec, never a verdict.
func ParseSpecOutcome(stderr string, exitCode int) (Outcome, error) {
	lines := strings.Split(strings.TrimRight(stderr, "\r\n"), "\n")
	last := strings.TrimRight(lines[len(lines)-1], "\r")
	if !strings.HasPrefix(last, outcomePrefix) {
		return Outcome{}, fmt.Errorf("galaspec: the last stderr line is %q, not %q<outcome> (exit status %d)", last, outcomePrefix, exitCode)
	}
	o, err := parseSpecOutcome(strings.TrimPrefix(last, outcomePrefix))
	if err != nil {
		return Outcome{}, err
	}
	if want := specExitCodes[o.Kind]; exitCode != want {
		return Outcome{}, fmt.Errorf("galaspec: outcome %s must exit with status %d, got %d", o, want, exitCode)
	}
	return o, nil
}

func parseSpecOutcome(s string) (Outcome, error) {
	name, detail, hasDetail := strings.Cut(s, ":")
	switch {
	case s == "ok":
		return Outcome{Kind: KindOK}, nil
	case s == "fuel":
		return Outcome{Kind: KindFuel}, nil
	case s == "malformed":
		return Outcome{Kind: KindMalformed}, nil
	case name == "panic" && hasDetail:
		for _, c := range panicClasses {
			if detail == c {
				return Outcome{Kind: KindPanic, Detail: c}, nil
			}
		}
		return Outcome{}, fmt.Errorf("galaspec: unknown panic class %q (want one of %s)", detail, strings.Join(panicClasses, ", "))
	case name == "unsupported" && hasDetail && detail != "":
		return Outcome{Kind: KindUnsupported, Detail: detail}, nil
	}
	return Outcome{}, fmt.Errorf("galaspec: unknown outcome %q", s)
}

// matchFailure is the message of the panic GALA emits when no arm of a match
// matches (internal/transpiler/transformer/match.go).
const matchFailure = "matched none of its cases"

// GoOutcome classifies how a Go run ended from its exit status and stderr.
// A Go panic prints "panic: <value>" and exits with status 2. The class comes
// from the message: GALA's match failure is "match"; Go's runtime errors give
// "divide" and "index"; everything else is "other".
func GoOutcome(exitCode int, stderr string, timedOut bool) Outcome {
	if timedOut {
		return Outcome{Kind: KindTimeout}
	}
	if exitCode == 0 {
		return Outcome{Kind: KindOK}
	}
	for _, line := range strings.Split(stderr, "\n") {
		line = strings.TrimRight(line, "\r")
		if msg, ok := strings.CutPrefix(line, "panic: "); ok {
			return Outcome{Kind: KindPanic, Detail: panicClass(msg)}
		}
		if msg, ok := strings.CutPrefix(line, "fatal error: "); ok {
			return Outcome{Kind: KindCrash, Detail: msg}
		}
	}
	return Outcome{Kind: KindExit, Detail: strconv.Itoa(exitCode)}
}

// panicClass is the class of a Go panic, from the text after "panic: ".
func panicClass(msg string) string {
	// A re-panic prints the first value followed by " [recovered]" or
	// " [recovered, repanicked]".
	msg, _, _ = strings.Cut(msg, " [recovered")
	// A string or error value is printed bare, so GALA's own messages and
	// Go's runtime errors start the text.
	switch {
	case strings.HasPrefix(msg, "gala: ") && strings.Contains(msg, matchFailure),
		// The closing branch of a match whose arms cover every value.
		msg == "unreachable":
		return ClassMatch
	case strings.HasPrefix(msg, "runtime error: integer divide by zero"):
		return ClassDivide
	case strings.HasPrefix(msg, "runtime error: index out of range"),
		strings.HasPrefix(msg, "runtime error: slice bounds out of range"):
		return ClassIndex
	}
	return ClassOther
}
