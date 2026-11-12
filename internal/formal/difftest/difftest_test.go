package difftest

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"martianoff/gala/internal/formal/coreexport"
)

func TestParseSpecOutcome(t *testing.T) {
	tests := []struct {
		name    string
		stderr  string
		code    int
		want    Outcome
		wantErr string
	}{
		{"ok", "galaspec: outcome=ok\n", 0, Outcome{Kind: KindOK}, ""},
		{"ok after other lines", "note: something\ngalaspec: outcome=ok\n\n", 0, Outcome{Kind: KindOK}, ""},
		{"crlf", "galaspec: outcome=fuel\r\n", 2, Outcome{Kind: KindFuel}, ""},
		{"panic match", "galaspec: outcome=panic:match", 1, Outcome{Kind: KindPanic, Detail: ClassMatch}, ""},
		{"panic divide", "galaspec: outcome=panic:divide\n", 1, Outcome{Kind: KindPanic, Detail: ClassDivide}, ""},
		{"panic index", "galaspec: outcome=panic:index\n", 1, Outcome{Kind: KindPanic, Detail: ClassIndex}, ""},
		{"panic other", "galaspec: outcome=panic:other\n", 1, Outcome{Kind: KindPanic, Detail: ClassOther}, ""},
		{"unsupported", "galaspec: outcome=unsupported:lambda\n", 3, Outcome{Kind: KindUnsupported, Detail: "lambda"}, ""},
		{"malformed", "galaspec: outcome=malformed\n", 4, Outcome{Kind: KindMalformed}, ""},
		{"no outcome line", "boom\n", 1, Outcome{}, "the last stderr line"},
		{"outcome not last", "galaspec: outcome=ok\nmore\n", 0, Outcome{}, "the last stderr line"},
		{"empty stderr", "", 0, Outcome{}, "the last stderr line"},
		{"exit status disagrees", "galaspec: outcome=ok\n", 1, Outcome{}, "must exit with status 0"},
		{"panic with exit 0", "galaspec: outcome=panic:match\n", 0, Outcome{}, "must exit with status 1"},
		{"unknown class", "galaspec: outcome=panic:overflow\n", 1, Outcome{}, "unknown panic class"},
		{"unsupported without kind", "galaspec: outcome=unsupported:\n", 3, Outcome{}, "unknown outcome"},
		{"unknown outcome", "galaspec: outcome=done\n", 0, Outcome{}, "unknown outcome"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseSpecOutcome(tt.stderr, tt.code)
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestOutcomeString(t *testing.T) {
	require.Equal(t, "ok", Outcome{Kind: KindOK}.String())
	require.Equal(t, "panic:match", Outcome{Kind: KindPanic, Detail: ClassMatch}.String())
	require.Equal(t, "unsupported:lambda", Outcome{Kind: KindUnsupported, Detail: "lambda"}.String())
	require.Equal(t, "exit:3", Outcome{Kind: KindExit, Detail: "3"}.String())
	require.Equal(t, "timeout", Outcome{Kind: KindTimeout}.String())
}

func TestGoOutcome(t *testing.T) {
	const trace = "\n\ngoroutine 1 [running]:\nmain.main()\n\t/tmp/main.go:7 +0x1d\nexit status 2\n"
	tests := []struct {
		name     string
		code     int
		stderr   string
		timedOut bool
		want     Outcome
	}{
		{"ok", 0, "", false, Outcome{Kind: KindOK}},
		{"ok with stderr", 0, "panic: not really\n", false, Outcome{Kind: KindOK}},
		{"sealed zero value", 2, "panic: gala: a Shape matched none of its cases: it is a zero value, which a sealed type without a default case does not have" + trace, false, Outcome{Kind: KindPanic, Detail: ClassMatch}},
		{"qualified sealed zero value", 2, "panic: gala: a lib.Shape matched none of its cases: it is a zero value" + trace, false, Outcome{Kind: KindPanic, Detail: ClassMatch}},
		{"unreachable arm", 2, "panic: unreachable" + trace, false, Outcome{Kind: KindPanic, Detail: ClassMatch}},
		{"divide", 2, "panic: runtime error: integer divide by zero" + trace, false, Outcome{Kind: KindPanic, Detail: ClassDivide}},
		{"divide re-panicked", 2, "panic: runtime error: integer divide by zero [recovered]\n\tpanic: again" + trace, false, Outcome{Kind: KindPanic, Detail: ClassDivide}},
		{"unreachable repanicked", 2, "panic: unreachable [recovered, repanicked]" + trace, false, Outcome{Kind: KindPanic, Detail: ClassMatch}},
		{"index", 2, "panic: runtime error: index out of range [5] with length 3" + trace, false, Outcome{Kind: KindPanic, Detail: ClassIndex}},
		{"slice bounds", 2, "panic: runtime error: slice bounds out of range [:4] with capacity 3" + trace, false, Outcome{Kind: KindPanic, Detail: ClassIndex}},
		{"user panic", 2, "panic: boom" + trace, false, Outcome{Kind: KindPanic, Detail: ClassOther}},
		{"nil dereference", 2, "panic: runtime error: invalid memory address or nil pointer dereference\n[signal SIGSEGV]" + trace, false, Outcome{Kind: KindPanic, Detail: ClassOther}},
		{"match text without the gala prefix", 2, "panic: my value matched none of its cases" + trace, false, Outcome{Kind: KindPanic, Detail: ClassOther}},
		{"crlf", 2, "panic: runtime error: integer divide by zero\r\n", false, Outcome{Kind: KindPanic, Detail: ClassDivide}},
		{"stack overflow", 2, "runtime: goroutine stack exceeds 1000000000-byte limit\nfatal error: stack overflow\n", false, Outcome{Kind: KindCrash, Detail: "stack overflow"}},
		{"plain exit", 3, "", false, Outcome{Kind: KindExit, Detail: "3"}},
		{"timeout", -1, "", true, Outcome{Kind: KindTimeout}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, GoOutcome(tt.code, tt.stderr, tt.timedOut))
		})
	}
}

func ok(stdout string) Run { return Run{Stdout: stdout, Outcome: Outcome{Kind: KindOK}} }

func panicked(stdout, class string) Run {
	return Run{Stdout: stdout, Outcome: Outcome{Kind: KindPanic, Detail: class}}
}

func TestCompare(t *testing.T) {
	tests := []struct {
		name       string
		spec, real Run
		want       Verdict
		reason     string
	}{
		{"same output", ok("1\n2\n"), ok("1\n2\n"), Agree, ""},
		{"same panic", panicked("1\n", ClassMatch), panicked("1\n", ClassMatch), Agree, ""},
		{"other line", ok("1\n2\n"), ok("1\n3\n"), Disagree, `stdout: line 2: spec="2", go="3"`},
		{"spec longer", ok("1\n2\n"), ok("1\n"), Disagree, `stdout: line 2: spec="2", go=""`},
		{"go longer", ok("1\n"), ok("1\n2\n"), Disagree, `line 2: spec="", go="2"`},
		{"missing newline", ok("1\n"), ok("1"), Disagree, `line 2: spec="", go ends`},
		{"other panic class", panicked("", ClassMatch), panicked("", ClassOther), Disagree, "outcome: spec=panic:match, go=panic:other"},
		{"panic and ok", panicked("", ClassDivide), ok(""), Disagree, "outcome: spec=panic:divide, go=ok"},
		{"both differ", ok("a\n"), panicked("b\n", ClassIndex), Disagree, "outcome: spec=ok, go=panic:index; stdout: line 1"},
		{"go rejected", ok("a\n"), Run{Outcome: Outcome{Kind: KindRejected, Detail: "go build: x"}}, Disagree, "go=rejected:go build: x"},
		{"unsupported", Run{Outcome: Outcome{Kind: KindUnsupported, Detail: "lambda"}}, Run{}, Unsupported, "lambda"},
		{"fuel", Run{Outcome: Outcome{Kind: KindFuel}}, ok("x"), Inconclusive, "fuel"},
		{"malformed", Run{Outcome: Outcome{Kind: KindMalformed}}, ok(""), Disagree, "malformed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Compare(tt.spec, tt.real)
			require.Equal(t, tt.want, got.Verdict)
			require.Contains(t, got.Reason, tt.reason)
			if tt.want == Agree {
				require.Empty(t, got.Reason)
			}
		})
	}
}

func TestThreeWay(t *testing.T) {
	a, b, c := ok("a\n"), ok("b\n"), ok("c\n")
	tests := []struct {
		name               string
		spec, real, oracle Run
		want               Triage
	}{
		{"all agree", a, a, a, TriageNone},
		{"go differs", a, b, a, TriageTranspiler},
		{"spec differs", b, a, a, TriageSpecOrOracle},
		{"oracle differs", a, a, b, TriageOracle},
		{"all differ", a, b, c, TriageAll},
		{"white space around the oracle", a, a, ok("\na\n\n"), TriageNone},
		{"go panics where both others print", a, panicked("a\n", ClassOther), a, TriageTranspiler},
		{"unsupported is not triaged", Run{Outcome: Outcome{Kind: KindUnsupported, Detail: "x"}}, a, b, TriageNone},
		{"fuel is not triaged", Run{Outcome: Outcome{Kind: KindFuel}}, a, b, TriageNone},
		{"malformed is not triaged", Run{Outcome: Outcome{Kind: KindMalformed}}, a, a, TriageNone},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, ThreeWay(tt.spec, tt.real, tt.oracle))
		})
	}
}

func TestParseKnown(t *testing.T) {
	list, err := ParseKnown([]byte(`[{"program": "examples/a.gala", "issue": 12, "symptom": "line 2"}]`))
	require.NoError(t, err)
	require.Equal(t, []Known{{Program: "examples/a.gala", Issue: 12, Symptom: "line 2"}}, list)

	empty, err := ParseKnown([]byte(`[]`))
	require.NoError(t, err)
	require.Empty(t, empty)

	for doc, want := range map[string]string{
		`[{"program": "", "issue": 1, "symptom": "s"}]`:                                                "has no program",
		`[{"program": "p", "issue": 0, "symptom": "s"}]`:                                               "has no issue number",
		`[{"program": "p", "issue": 1, "symptom": " "}]`:                                               "has no symptom",
		`[{"program": "p", "issue": 1, "symptom": "s"}, {"program": "p", "issue": 2, "symptom": "t"}]`: "listed twice",
		`[{"program": "p", "issue": 1, "symptom": "s", "note": "x"}]`:                                  "unknown field",
		`{"program": "p"}`: "known disagreements",
	} {
		_, err := ParseKnown([]byte(doc))
		require.ErrorContains(t, err, want, doc)
	}
}

func TestCheckKnown(t *testing.T) {
	known := []Known{
		{Program: "listed-disagrees", Issue: 10, Symptom: "line 2"},
		{Program: "listed-other-symptom", Issue: 11, Symptom: "line 9"},
		{Program: "listed-agrees", Issue: 12, Symptom: "line 1"},
		{Program: "listed-unsupported", Issue: 13, Symptom: "line 1"},
		{Program: "listed-not-run", Issue: 14, Symptom: "line 1"},
	}
	results := map[string]Comparison{
		"agrees":               {Verdict: Agree},
		"unsupported":          {Verdict: Unsupported, Reason: "lambda"},
		"inconclusive":         {Verdict: Inconclusive},
		"unlisted":             {Verdict: Disagree, Reason: "stdout: line 3"},
		"listed-disagrees":     {Verdict: Disagree, Reason: "stdout: line 2: spec=..."},
		"listed-other-symptom": {Verdict: Disagree, Reason: "outcome: spec=ok, go=panic:other"},
		"listed-agrees":        {Verdict: Agree},
		"listed-unsupported":   {Verdict: Unsupported},
	}
	problems := CheckKnown(known, results)
	joined := strings.Join(problems, "\n")
	require.Len(t, problems, 5, joined)
	require.Contains(t, joined, "unlisted: DISAGREE: stdout: line 3")
	require.Contains(t, joined, `listed-other-symptom: disagrees, but not with #11's symptom "line 9"`)
	require.Contains(t, joined, "listed-agrees: listed for #12 but now AGREE: remove it from known_disagreements.json")
	require.Contains(t, joined, "listed-unsupported: listed for #13 but now UNSUPPORTED")
	require.Contains(t, joined, "listed-not-run: listed for #14 but not compared in this run")
	require.NotContains(t, joined, "listed-disagrees")

	require.Empty(t, CheckKnown(nil, map[string]Comparison{"a": {Verdict: Agree}, "u": {Verdict: Unsupported}}))
}

func TestConstructCounts(t *testing.T) {
	p, err := coreexport.Export([]coreexport.Source{{Path: "main.gala", Text: "package main\n\n" +
		"import \"strings\"\n\n" +
		"func main() {\n    val f = (x int) => x\n    val g = (x int) => x\n    Println(1)\n}\n"}}, coreexport.Options{})
	require.NoError(t, err)
	counts, err := coreexport.ConstructCounts(p)
	require.NoError(t, err)
	for what, n := range map[string]int{"file": 1, "func": 1, "block": 1, "val": 2, "call": 1, "lambda": 2, "import": 1} {
		require.Equal(t, n, counts[what], what)
	}
}

func TestCheckBaseline(t *testing.T) {
	records := []Record{
		{Source: FromExample, Comparison: Comparison{Verdict: Agree}},
		{Source: FromExample, Comparison: Comparison{Verdict: Agree}},
		{Source: FromExample, Comparison: Comparison{Verdict: Unsupported}},
		{Source: FromGenerator, Comparison: Comparison{Verdict: Agree}},
	}
	require.Equal(t, 2, AgreeingExamples(records))
	problem, note := CheckBaseline(Baseline{AgreeingExamples: 2}, records)
	require.Empty(t, problem)
	require.Empty(t, note)
	problem, _ = CheckBaseline(Baseline{AgreeingExamples: 3}, records)
	require.Contains(t, problem, "only 2 examples agree with the spec, down from the baseline 3")
	problem, note = CheckBaseline(Baseline{AgreeingExamples: 1}, records)
	require.Empty(t, problem)
	require.Contains(t, note, "raise agreeing_examples")
}

func TestRender(t *testing.T) {
	agree := func(id string, src Source, kinds ...string) Record {
		r := Record{ID: id, Source: src, Comparison: Comparison{Verdict: Agree}, Constructs: map[string]int{}}
		for _, k := range kinds {
			r.Constructs[k]++
		}
		return r
	}
	records := []Record{
		agree("e1", FromExample, "int", "call", "match"),
		agree("e2", FromExample, "int", "call"),
		agree("g1", FromGenerator, "int", "call", "match"),
		{ID: "e3", Source: FromExample, Comparison: Comparison{Verdict: Disagree, Reason: "x"}, Constructs: map[string]int{"match": 1, "int": 1}},
		{ID: "e4", Source: FromExample, Comparison: Comparison{Verdict: Unsupported}, Spec: Outcome{Kind: KindUnsupported, Detail: "lambda"}, Constructs: map[string]int{"lambda": 1, "if": 1}},
		{ID: "e5", Source: FromExample, Comparison: Comparison{Verdict: Unsupported}, Spec: Outcome{Kind: KindUnsupported, Detail: "lambda"}},
		{ID: "g2", Source: FromGenerator, Comparison: Comparison{Verdict: Unsupported}, Spec: Outcome{Kind: KindUnsupported, Detail: "import"}},
		{ID: "e6", Source: FromExample, Comparison: Comparison{Verdict: Inconclusive}, Spec: Outcome{Kind: KindFuel}, Constructs: map[string]int{"call": 1}},
		// Only out of fuel: galaspec never ran a program using it to the end.
		{ID: "e7", Source: FromGenerator, Comparison: Comparison{Verdict: Inconclusive}, Spec: Outcome{Kind: KindFuel}, Constructs: map[string]int{"tuple": 1}},
	}
	out := Render(Coverage{
		Records:  records,
		Known:    []Known{{Program: "e3", Issue: 1234, Symptom: "a | b"}},
		Baseline: Baseline{AgreeingExamples: 2},
		Proved:   map[string][]string{"match": {"Gala.T5"}},
	})
	for _, want := range []string{
		"| AGREE | 2 | 1 |",
		"| DISAGREE | 1 | 0 |",
		"| UNSUPPORTED | 2 | 1 |",
		"| INCONCLUSIVE | 1 | 1 |",
		"agree with the spec: **2** (ratchet baseline 2)",
		"| `int` | yes | no: 3 agree, 1 disagree, 0 inconclusive | no |",
		"| `call` | yes | no: 3 agree, 0 disagree, 1 inconclusive | no |",
		"| `match` | yes | no: 2 agree, 1 disagree, 0 inconclusive | `Gala.T5` |",
		// Seen only in an unsupported program: the run says nothing about it.
		"| `if` | not seen | no | no |",
		"| `tuple` | not seen | no: 0 agree, 0 disagree, 1 inconclusive | no |",
		"| `lambda` | 2 |",
		"| `import` | 1 |",
		"| `e3` | #1234 | a \\| b |",
	} {
		require.Contains(t, out, want)
	}
	require.NotContains(t, strings.ToLower(out), "verified")
	require.Less(t, strings.Index(out, "`lambda` | 2"), strings.Index(out, "`import` | 1"), "most frequent first")

	tested := Render(Coverage{Records: []Record{
		agree("a", FromExample, "bool"), agree("b", FromExample, "bool"), agree("c", FromGenerator, "bool"),
	}})
	require.Contains(t, tested, "| `bool` | yes | yes: 3 agree | no |")
	require.Contains(t, tested, "No program was outside the subset.")
	require.Contains(t, tested, "### Known disagreements\n\nNone.")
}

func TestSplice(t *testing.T) {
	doc := "# Title\n\nhand-written\n\n" + BeginMarker + "\nold\n" + EndMarker + "\n\nmore hand-written\n"
	got, err := Splice(doc, "new section\n")
	require.NoError(t, err)
	require.Equal(t, "# Title\n\nhand-written\n\n"+BeginMarker+"\nnew section\n"+EndMarker+"\n\nmore hand-written\n", got)

	again, err := Splice(got, "new section\n")
	require.NoError(t, err)
	require.Equal(t, got, again, "splicing is idempotent")

	for _, bad := range []string{
		"no markers",
		BeginMarker + " only the start",
		EndMarker + " reversed " + BeginMarker,
		BeginMarker + EndMarker + BeginMarker + EndMarker,
	} {
		_, err := Splice(bad, "x")
		require.ErrorContains(t, err, "must hold", bad)
	}
}
