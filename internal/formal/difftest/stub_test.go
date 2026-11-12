package difftest

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/bazelbuild/rules_go/go/runfiles"
	"github.com/stretchr/testify/require"
)

// stubSpec is the stand-in galaspec (testdata/stubspec), which implements
// galaspec's contract for a tiny language, so the harness runs end to end
// without Lean.
func stubSpec(t *testing.T) string {
	t.Helper()
	p, err := runfiles.Rlocation(os.Getenv("STUB_SPEC"))
	require.NoError(t, err, "STUB_SPEC names the stub galaspec in the runfiles")
	return p
}

// TestHarnessWithStub runs programs through the harness with the stub
// galaspec and the real transpiler and Go, one per verdict.
func TestHarnessWithStub(t *testing.T) {
	tests := []struct {
		name   string
		body   string
		fuel   int
		oracle *Run
		want   Comparison
		triage Triage
	}{
		{
			name:   "agree",
			body:   "    val x = 6\n    Println(x * 7)\n    Println(\"hi\")\n    Println(true)\n",
			oracle: &Run{Stdout: "42\nhi\ntrue\n", Outcome: Outcome{Kind: KindOK}},
			want:   Comparison{Verdict: Agree},
		},
		{
			name: "agree on a divide panic",
			body: "    val z = 0\n    Println(1)\n    Println(1 / z)\n",
			want: Comparison{Verdict: Agree},
		},
		{
			name:   "disagree",
			body:   "    Println(\"stub:lie\")\n",
			oracle: &Run{Stdout: "stub:lie\n", Outcome: Outcome{Kind: KindOK}},
			want:   Comparison{Verdict: Disagree, Reason: `stdout: line 1: spec="truth", go="stub:lie" [triage: spec bug, or the oracle enshrines a transpiler bug]`},
			triage: TriageSpecOrOracle,
		},
		{
			name:   "the oracle is wrong",
			body:   "    Println(1)\n",
			oracle: &Run{Stdout: "2\n", Outcome: Outcome{Kind: KindOK}},
			want:   Comparison{Verdict: Agree},
			triage: TriageOracle,
		},
		{
			name: "unsupported by the exporter",
			body: "    val f = (x int) => x\n    Println(f(1))\n",
			want: Comparison{Verdict: Unsupported, Reason: "outside the spec's subset: lambda"},
		},
		{
			name: "unsupported by the spec",
			body: "    if (true) { Println(1) }\n",
			want: Comparison{Verdict: Unsupported, Reason: "outside the spec's subset: stub-if"},
		},
		{
			name: "out of fuel",
			body: "    Println(1)\n    Println(2)\n",
			fuel: 1,
			want: Comparison{Verdict: Inconclusive, Reason: "galaspec ran out of fuel"},
		},
	}
	h := Harness{Spec: Spec{Bin: stubSpec(t)}, Dir: t.TempDir()}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := h
			h.Spec.Fuel = tt.fuel
			rec := h.Check(t, Program{
				ID:     "stub/" + tt.name,
				Source: FromExample,
				Path:   "main.gala",
				Text:   "package main\n\nfunc main() {\n" + tt.body + "}\n",
				Oracle: tt.oracle,
			})
			require.Equal(t, tt.want, rec.Comparison)
			require.Equal(t, tt.triage, rec.Triage)
			require.NotEmpty(t, rec.Constructs)
		})
	}
}

// TestSpecRun checks that a document galaspec rejects is a KindMalformed
// outcome, and a galaspec that cannot run is an error, never a verdict.
func TestSpecRun(t *testing.T) {
	dir := t.TempDir()
	doc := filepath.Join(dir, "doc.json")
	require.NoError(t, os.WriteFile(doc, []byte(`{"core": 2, "files": []}`), 0o644))

	run, err := Spec{Bin: stubSpec(t)}.Run(doc)
	require.NoError(t, err)
	require.Equal(t, Outcome{Kind: KindMalformed}, run.Outcome)

	_, err = Spec{Bin: filepath.Join(dir, "no-such-galaspec")}.Run(doc)
	require.ErrorContains(t, err, "running galaspec")
}
