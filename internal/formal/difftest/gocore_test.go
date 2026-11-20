package difftest

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseGoCore(t *testing.T) {
	tests := []struct {
		name    string
		stderr  string
		code    int
		want    GoCoreRun
		wantErr bool
	}{
		{name: "ok", stderr: "gocore: outcome=ok\n", code: 0, want: GoCoreRun{Stdout: "x\n", Outcome: "ok"}},
		{name: "panic", stderr: "gocore: panic: runtime error: integer divide by zero\ngocore: outcome=panic\n", code: 1,
			want: GoCoreRun{Stdout: "x\n", Outcome: "panic", Detail: "runtime error: integer divide by zero"}},
		{name: "unsupported", stderr: "gocore: outcome=unsupported:map-type\r\n", code: 3,
			want: GoCoreRun{Stdout: "x\n", Outcome: "unsupported:map-type"}},
		{name: "stuck", stderr: "gocore: stuck: no such variable\ngocore: outcome=stuck\n", code: 5,
			want: GoCoreRun{Stdout: "x\n", Outcome: "stuck", Detail: "no such variable"}},
		{name: "exit status disagrees", stderr: "gocore: outcome=ok\n", code: 1, wantErr: true},
		{name: "no outcome line", stderr: "crashed\n", code: 1, wantErr: true},
		{name: "unknown outcome", stderr: "gocore: outcome=maybe\n", code: 0, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseGoCore("x\n", tt.stderr, tt.code)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestCompareGoCore(t *testing.T) {
	ok := Run{Stdout: "1\n", Outcome: Outcome{Kind: KindOK}}
	divide := Run{Stdout: "1\n", Outcome: Outcome{Kind: KindPanic, Detail: ClassDivide}}
	tests := []struct {
		name    string
		g       GoCoreRun
		real    Run
		goPanic string
		want    Verdict
	}{
		{"agree", GoCoreRun{Stdout: "1\n", Outcome: "ok"}, ok, "", Agree},
		{"agree on a panic", GoCoreRun{Stdout: "1\n", Outcome: "panic", Detail: "runtime error: integer divide by zero"},
			divide, "runtime error: integer divide by zero", Agree},
		{"panic text differs", GoCoreRun{Stdout: "1\n", Outcome: "panic", Detail: "runtime error: x"},
			divide, "runtime error: integer divide by zero", Disagree},
		{"stdout differs", GoCoreRun{Stdout: "2\n", Outcome: "ok"}, ok, "", Disagree},
		{"outcome differs", GoCoreRun{Stdout: "1\n", Outcome: "ok"}, divide, "runtime error: integer divide by zero", Disagree},
		{"unsupported", GoCoreRun{Outcome: "unsupported:map-type"}, ok, "", Unsupported},
		{"unsupported after a prefix of Go's output", GoCoreRun{Stdout: "1\n", Outcome: "unsupported:map-type"}, ok, "", Unsupported},
		{"unsupported after a prefix of a panicking Go run", GoCoreRun{Stdout: "1", Outcome: "unsupported:map-type"},
			divide, "runtime error: integer divide by zero", Unsupported},
		{"fuel", GoCoreRun{Outcome: "fuel"}, ok, "", Inconclusive},
		{"stuck is a bug", GoCoreRun{Outcome: "stuck", Detail: "x"}, ok, "", Disagree},
		{"go ran out of stack", GoCoreRun{Outcome: "fuel"},
			Run{Outcome: Outcome{Kind: KindCrash, Detail: "stack overflow"}}, "", Resource},
		{"go timed out", GoCoreRun{Outcome: "ok"}, Run{Outcome: Outcome{Kind: KindTimeout}}, "", Resource},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CompareGoCore(tt.g, tt.real, tt.goPanic)
			require.Equal(t, tt.want, got.Verdict, got.Reason)
			if tt.want == Unsupported {
				require.Equal(t, "map-type", got.Construct)
			}
		})
	}
}

// TestCompareGoCoreStoppedEarly: a GoCore run that stops unsupported after
// printing a line Go did not is a disagreement, not UNSUPPORTED.
func TestCompareGoCoreStoppedEarly(t *testing.T) {
	ok := Run{Stdout: "1\n", Outcome: Outcome{Kind: KindOK}}
	tests := []struct {
		name, stdout, reason string
	}{
		{"a line Go did not print", "2\n",
			`gocore stopped (unsupported:map-type) after printing what Go did not: stdout: line 1: gocore="2", go="1"`},
		{"more than Go printed", "1\n2\n",
			`gocore stopped (unsupported:map-type) after printing what Go did not: stdout: line 2: gocore="2", go=""`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CompareGoCore(GoCoreRun{Stdout: tt.stdout, Outcome: "unsupported:map-type"}, ok, "")
			require.Equal(t, GoCoreRecord{Comparison: Comparison{Disagree, tt.reason}}, got)
		})
	}
}

func TestGoPanicText(t *testing.T) {
	require.Equal(t, "runtime error: integer divide by zero",
		goPanicText("panic: runtime error: integer divide by zero\n[signal 0xc0000005]\n\ngoroutine 1 [running]:\n"))
	require.Equal(t, "code 7", goPanicText("panic: code 7 [recovered]\n"))
	// A panic raised while another unwinds ended the run: Go prints it last.
	require.Equal(t, "second", goPanicText("panic: first [recovered]\n\tpanic: second\n\ngoroutine 1 [running]:\n"))
	require.Equal(t, "real", goPanicText("panic: printed by the program\npanic: real\n"))
	require.Equal(t, "", goPanicText("fatal error: stack overflow\n"))
}

func TestExportGoCore(t *testing.T) {
	prog := GoProgram{
		Main: "package main\n\nimport \"martianoff/gala/std\"\n\nfunc main() { _ = std.X }\n",
		Packages: map[string]map[string]string{
			"martianoff/gala/std": {"b.go": "package std\n", "a.go": "package std\n\nvar X = 1\n"},
		},
	}
	path, err := ExportGoCore(t.TempDir(), "examples/x.gala", prog)
	require.NoError(t, err)
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	var doc struct {
		GoCore   int `json:"gocore"`
		Packages []struct {
			Path  string `json:"path"`
			Files []struct {
				Name string `json:"name"`
			} `json:"files"`
		} `json:"packages"`
	}
	require.NoError(t, json.Unmarshal(data, &doc))
	require.Equal(t, 1, doc.GoCore)
	require.Len(t, doc.Packages, 2)
	require.Equal(t, "main", doc.Packages[0].Path)
	require.Equal(t, "martianoff/gala/std", doc.Packages[1].Path)
	require.Equal(t, "a.go", doc.Packages[1].Files[0].Name, "files in name order")
}

func TestCheckGoCoreAndBaseline(t *testing.T) {
	records := []Record{
		{ID: "a", Source: FromExample, Comparison: Comparison{Verdict: Agree}, GoCore: &GoCoreRecord{Comparison: Comparison{Verdict: Agree}}},
		{ID: "b", Source: FromExample, Comparison: Comparison{Verdict: Agree}, GoCore: &GoCoreRecord{Comparison: Comparison{Disagree, "stdout"}}},
		{ID: "c", Source: FromExample, Comparison: Comparison{Verdict: Unsupported}, GoCore: &GoCoreRecord{Comparison: Comparison{Verdict: Agree}}},
	}
	require.Equal(t, 1, GoCoreAgreeingExamples(records))
	problems := CheckGoCore(records)
	require.Len(t, problems, 1)
	require.Contains(t, problems[0], "b: GoCore DISAGREEs")
	problem, _ := CheckBaseline(Baseline{GoCoreAgreeingExamples: 2}, records)
	require.Contains(t, problem, "three ways")
	_, note := CheckBaseline(Baseline{}, records)
	require.Contains(t, note, "gocore_agreeing_examples")
	require.Contains(t, note, "gocore_fidelity_agreeing")
	problem, _ = CheckBaseline(Baseline{GoCoreFidelityAgreeing: 3}, records)
	require.Contains(t, problem, "programs GoCore agrees with go run on")
	section := Render(Coverage{Records: records})
	require.Contains(t, section, "### GoCore: the Go the transformer emitted")
	require.Contains(t, section, "`a`")
}
