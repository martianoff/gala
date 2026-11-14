package difftest

import (
	"errors"
	"fmt"
	"time"

	"martianoff/gala/internal/formal/coreexport"
	"martianoff/gala/internal/formal/gala2lean"
)

// LeanRunner elaborates and runs a gala2lean translation;
// *gala2lean.Runner is the real one.
type LeanRunner interface {
	Run(res *gala2lean.Result, timeout time.Duration) (gala2lean.RunResult, error)
}

// LeanTimeout bounds one run of a translated program. Lean elaborates the
// program first, which takes a few seconds.
const LeanTimeout = 2 * time.Minute

// leanNamespace is the Lean namespace a program is translated into.
const leanNamespace = "GalaProgram"

// LeanRecord is how gala2lean, the third oracle, fared on one program: the
// program translated to Lean and run, compared with the real run.
type LeanRecord struct {
	Comparison
	// Construct is what gala2lean did not translate, for an UNSUPPORTED
	// verdict.
	Construct string
}

// translate translates p for the third oracle, or names the construct that
// stops it.
func translate(p *coreexport.Program) (*gala2lean.Result, string, error) {
	res, err := gala2lean.Translate(p, gala2lean.Options{Namespace: leanNamespace, Main: true})
	var u *gala2lean.UnsupportedError
	if errors.As(err, &u) {
		return nil, u.Construct(), nil
	}
	return res, "", err
}

// compareLean runs a translation and compares it with the real run, and, as a
// hint for triage, with galaspec's when galaspec ran the program to the end.
// A translation Lean rejects is a disagreement: gala2lean must write Lean that
// compiles, except where partial_fixpoint cannot prove a recursive function
// monotone, which is reported, by function, as outside what gala2lean
// translates.
func compareLean(r LeanRunner, res *gala2lean.Result, spec, real Run) (LeanRecord, error) {
	out, err := r.Run(res, LeanTimeout)
	if err != nil {
		return LeanRecord{}, err
	}
	switch out.Kind {
	case gala2lean.TimedOut:
		return LeanRecord{Comparison: Comparison{Inconclusive, fmt.Sprintf("the Lean run did not finish within %s", LeanTimeout)}}, nil
	case gala2lean.Diverged:
		return LeanRecord{Comparison: Comparison{Inconclusive, "the Lean run diverged"}}, nil
	case gala2lean.Crashed:
		return LeanRecord{Comparison: Comparison{Inconclusive, "the Lean run crashed: " + out.Message}}, nil
	case gala2lean.NotMonotone:
		c := "monotonicity:" + out.Decl
		return LeanRecord{Comparison: Comparison{Unsupported, "partial_fixpoint cannot define " + out.Decl + ": " + out.Message}, Construct: c}, nil
	case gala2lean.LeanRejected:
		where := ""
		if out.Decl != "" {
			where = " (in " + out.Decl + ")"
		}
		return LeanRecord{Comparison: Comparison{Disagree, "Lean rejected the translation" + where + ": " + out.Message}}, nil
	}
	o, err := parseSpecOutcome(out.Outcome)
	if err != nil {
		return LeanRecord{}, fmt.Errorf("gala2lean run: %w", err)
	}
	lean := Run{Stdout: out.Stdout, Outcome: o}
	d := diff("gala2lean", lean, "go", real)
	if d == "" {
		return LeanRecord{Comparison: Comparison{Verdict: Agree}}, nil
	}
	if k := spec.Outcome.Kind; k == KindOK || k == KindPanic {
		switch {
		case spec == lean:
			d += " [galaspec agrees with gala2lean]"
		case spec == real:
			d += " [galaspec agrees with go]"
		default:
			d += " [galaspec differs from both]"
		}
	}
	return LeanRecord{Comparison: Comparison{Disagree, d}}, nil
}
