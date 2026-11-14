package difftest

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"martianoff/gala/internal/formal/gala2lean"
)

// fakeLean is a LeanRunner that returns a fixed result.
type fakeLean struct{ out gala2lean.RunResult }

func (f fakeLean) Run(*gala2lean.Result, time.Duration) (gala2lean.RunResult, error) {
	return f.out, nil
}

func TestCompareLean(t *testing.T) {
	ok := Outcome{Kind: KindOK}
	goRun := Run{Stdout: "1\n", Outcome: ok}
	tests := []struct {
		name      string
		out       gala2lean.RunResult
		spec      Run
		want      Verdict
		reason    string
		construct string
	}{
		{name: "agree", out: gala2lean.RunResult{Kind: gala2lean.RanOK, Stdout: "1\n", Outcome: "ok"}, want: Agree},
		{
			name: "disagree, galaspec with go", out: gala2lean.RunResult{Kind: gala2lean.RanOK, Stdout: "2\n", Outcome: "ok"},
			spec: goRun, want: Disagree, reason: `stdout: line 1: gala2lean="2", go="1" [galaspec agrees with go]`,
		},
		{
			name: "disagree, galaspec with gala2lean", out: gala2lean.RunResult{Kind: gala2lean.RanOK, Stdout: "1\n", Outcome: "panic:divide"},
			spec: Run{Stdout: "1\n", Outcome: Outcome{Kind: KindPanic, Detail: ClassDivide}},
			want: Disagree, reason: "outcome: gala2lean=panic:divide, go=ok [galaspec agrees with gala2lean]",
		},
		{
			name: "disagree, galaspec out of its subset", out: gala2lean.RunResult{Kind: gala2lean.RanOK, Stdout: "", Outcome: "ok"},
			spec: Run{Outcome: Outcome{Kind: KindUnsupported, Detail: "lambda"}},
			want: Disagree, reason: `stdout: line 1: gala2lean="", go="1"`,
		},
		{name: "timed out", out: gala2lean.RunResult{Kind: gala2lean.TimedOut}, want: Inconclusive, reason: "did not finish"},
		{name: "diverged", out: gala2lean.RunResult{Kind: gala2lean.Diverged}, want: Inconclusive, reason: "diverged"},
		{name: "crashed", out: gala2lean.RunResult{Kind: gala2lean.Crashed, Message: "Stack overflow"}, want: Inconclusive, reason: "crashed: Stack overflow"},
		{
			name: "not monotone", out: gala2lean.RunResult{Kind: gala2lean.NotMonotone, Decl: "walk", Message: "Could not prove 'walk' to be monotone"},
			want: Unsupported, reason: "partial_fixpoint cannot define walk", construct: "monotonicity:walk",
		},
		{
			name: "rejected by Lean", out: gala2lean.RunResult{Kind: gala2lean.LeanRejected, Decl: "f", Message: "type mismatch"},
			want: Disagree, reason: "Lean rejected the translation (in f): type mismatch",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := compareLean(fakeLean{tt.out}, &gala2lean.Result{}, tt.spec, goRun)
			require.NoError(t, err)
			require.Equal(t, tt.want, got.Verdict)
			require.Contains(t, got.Reason, tt.reason)
			require.Equal(t, tt.construct, got.Construct)
		})
	}
	rejected := Run{Outcome: Outcome{Kind: KindRejected, Detail: "go build: x"}}
	got, err := compareLean(fakeLean{gala2lean.RunResult{Kind: gala2lean.RanOK, Stdout: "1\n", Outcome: "ok"}}, &gala2lean.Result{}, Run{}, rejected)
	require.NoError(t, err)
	require.Equal(t, Inconclusive, got.Verdict, "a Go run that never reached an outcome says nothing about the translation")
	require.Contains(t, got.Reason, "the Go run ended rejected:")

	_, err = compareLean(fakeLean{gala2lean.RunResult{Kind: gala2lean.RanOK, Outcome: "weird"}}, &gala2lean.Result{}, Run{}, goRun)
	require.Error(t, err, "an outcome the prelude never prints is a broken run, not a verdict")
}

func TestKnownGala2Lean(t *testing.T) {
	list, err := ParseKnown([]byte(`[
		{"program": "examples/a.gala", "issue": 12, "symptom": "line 2"},
		{"program": "examples/a.gala", "issue": 13, "symptom": "line 3", "oracle": "gala2lean"}]`))
	require.NoError(t, err, "a program may be listed once for each oracle")
	require.Equal(t, "examples/a.gala", list[0].Key())
	require.Equal(t, "gala2lean:examples/a.gala", list[1].Key())

	_, err = ParseKnown([]byte(`[{"program": "p", "issue": 1, "symptom": "s", "oracle": "coq"}]`))
	require.ErrorContains(t, err, "unknown oracle")

	problems := CheckKnown(list, map[string]Comparison{
		"examples/a.gala":                 {Verdict: Disagree, Reason: "stdout: line 2"},
		LeanKey("examples/a.gala"):        {Verdict: Agree},
		LeanKey("examples/unlisted.gala"): {Verdict: Disagree, Reason: "x"},
	})
	joined := strings.Join(problems, "\n")
	require.Len(t, problems, 2, joined)
	require.Contains(t, joined, "gala2lean:examples/a.gala: listed for #13 but now AGREE")
	require.Contains(t, joined, "gala2lean:examples/unlisted.gala: DISAGREE: x")
}

func TestBaselineAndRenderWithGala2Lean(t *testing.T) {
	lean := func(v Verdict, construct string) *LeanRecord {
		return &LeanRecord{Comparison: Comparison{Verdict: v}, Construct: construct}
	}
	records := []Record{
		{ID: "e1", Source: FromExample, Comparison: Comparison{Verdict: Agree}, Lean: lean(Agree, ""), Constructs: map[string]int{"match": 1}},
		{ID: "e2", Source: FromExample, Comparison: Comparison{Verdict: Unsupported}, Spec: Outcome{Kind: KindUnsupported, Detail: "lambda"},
			Lean: lean(Agree, ""), Constructs: map[string]int{"match": 1, "int": 1}},
		{ID: "e3", Source: FromExample, Comparison: Comparison{Verdict: Agree}, Lean: lean(Unsupported, "interp"), Constructs: map[string]int{"interp": 1}},
		{ID: "g1", Source: FromGenerator, Comparison: Comparison{Verdict: Agree}, Lean: lean(Inconclusive, ""), Constructs: map[string]int{"int": 1}},
	}
	require.Equal(t, 2, Gala2LeanAgreeingExamples(records))
	problem, note := CheckBaseline(Baseline{AgreeingExamples: 2, Gala2LeanAgreeingExamples: 3}, records)
	require.Contains(t, problem, "only 2 examples gala2lean translates agree with Go, down from the baseline 3")
	require.Empty(t, note)
	_, note = CheckBaseline(Baseline{AgreeingExamples: 2, Gala2LeanAgreeingExamples: 1}, records)
	require.Contains(t, note, "raise gala2lean_agreeing_examples")

	out := Render(Coverage{Records: records, Baseline: Baseline{AgreeingExamples: 2, Gala2LeanAgreeingExamples: 2}})
	for _, want := range []string{
		"| AGREE | 2 | 1 | 2 | 0 |",
		"| UNSUPPORTED | 1 | 0 | 1 | 0 |",
		"| INCONCLUSIVE | 0 | 0 | 0 | 1 |",
		"Examples gala2lean translates whose Lean run agrees with Go: **2** (ratchet baseline 2).",
		"| `match` | yes | no: 1 agree | no | translated: 2 agree |",
		"| `int` | yes | no: 1 agree | no | translated: 1 agree, 0 disagree, 1 inconclusive |",
		"| `interp` | yes | no: 1 agree | no | not translated |",
		"### Outside what gala2lean translates",
		"| `interp` | 1 |",
	} {
		require.Contains(t, out, want)
	}
	lower := strings.ToLower(out)
	require.NotContains(t, lower, "verified")
	require.NotContains(t, lower, "proved by gala2lean")
}
