package difftest

import (
	"cmp"
	"fmt"
	"maps"
	"slices"
	"strings"

	"martianoff/gala/internal/formal/coreexport"
)

// Source is where a program comes from.
type Source string

const (
	// FromExample is an examples/*.gala program with a .out file.
	FromExample Source = "example"
	// FromGenerator is a program from FuzzPrograms' generator.
	FromGenerator Source = "generated"
)

// Record is the result for one program.
type Record struct {
	ID     string
	Source Source
	Comparison
	// Triage is the three-way triage against the program's oracle.
	Triage Triage
	// Constructs is how often each construct occurs in the program's Core
	// JSON: the kind of each supported node, and the name of each
	// unsupported one.
	Constructs map[string]int
	// Spec is galaspec's outcome.
	Spec Outcome
	// Lean is the third oracle's result, gala2lean's translation run with
	// Lean; nil when the run did not use it.
	Lean *LeanRecord
	// GoCore is how GoCore fared on the Go the transformer emitted, compared
	// with `go run`; nil when the run did not use it or the program did not
	// compile.
	GoCore *GoCoreRecord
}

// Baseline is the ratchet the coverage may not fall below.
type Baseline struct {
	// AgreeingExamples is how many examples run wholly inside the subset
	// and agree with the spec.
	AgreeingExamples int `json:"agreeing_examples"`
	// Gala2LeanAgreeingExamples is how many examples gala2lean translates
	// whose Lean run agrees with Go.
	Gala2LeanAgreeingExamples int `json:"gala2lean_agreeing_examples"`
	// GoCoreAgreeingExamples is how many examples agree three ways: GoCore's
	// run of the emitted Go agrees with `go run` of it, and galaspec agrees
	// with `go run` (docs/FORMAL_SEMANTICS.MD, M4).
	GoCoreAgreeingExamples int `json:"gocore_agreeing_examples"`
	// GoCoreFidelityAgreeing is how many programs, examples and generated,
	// GoCore runs with the same stdout and ending as `go run`: so GoCore cannot
	// quietly turn agreeing programs into unsupported ones.
	GoCoreFidelityAgreeing int `json:"gocore_fidelity_agreeing"`
}

// GoCoreFidelityAgreeing is how many records GoCore agrees with `go run` on.
func GoCoreFidelityAgreeing(records []Record) int {
	n := 0
	for _, r := range records {
		if r.GoCore != nil && r.GoCore.Verdict == Agree {
			n++
		}
	}
	return n
}

// GoCoreAgreeingExamples is how many example records agree three ways: GoCore
// with `go run`, and galaspec with `go run`.
func GoCoreAgreeingExamples(records []Record) int {
	n := 0
	for _, r := range records {
		if agreesThreeWays(r) {
			n++
		}
	}
	return n
}

// agreesThreeWays reports whether r is an example on which GoCore agrees
// with `go run` and galaspec agrees with `go run`.
func agreesThreeWays(r Record) bool {
	return r.Source == FromExample && r.Verdict == Agree && r.GoCore != nil && r.GoCore.Verdict == Agree
}

// usesGoCore reports whether the run compared GoCore's runs.
func usesGoCore(records []Record) bool {
	for _, r := range records {
		if r.GoCore != nil {
			return true
		}
	}
	return false
}

// CheckGoCore names every GoCore disagreement. Each is a bug in GoCore or the
// Go AST exporter, never the transpiler's, so none may be listed as known: it
// fails the run until it is fixed, or the construct is reported as
// unsupported.
func CheckGoCore(records []Record) []string {
	var problems []string
	for _, r := range records {
		if r.GoCore != nil && r.GoCore.Verdict == Disagree {
			problems = append(problems, fmt.Sprintf("%s: GoCore DISAGREEs with go run: %s\n  a GoCore or exporter bug: fix it, or report the construct as unsupported", r.ID, r.GoCore.Reason))
		}
	}
	return problems
}

// Gala2LeanAgreeingExamples is how many example records gala2lean
// translated and whose Lean run agrees with the real one.
func Gala2LeanAgreeingExamples(records []Record) int {
	n := 0
	for _, r := range records {
		if r.Source == FromExample && r.Lean != nil && r.Lean.Verdict == Agree {
			n++
		}
	}
	return n
}

// AgreeingExamples is how many example records agree: each one ran wholly
// inside the subset, since galaspec reports any construct outside it.
func AgreeingExamples(records []Record) int {
	n := 0
	for _, r := range records {
		if r.Source == FromExample && r.Verdict == Agree {
			n++
		}
	}
	return n
}

// CheckBaseline fails when the agreeing examples fell below the baseline.
// A count above it is reported, so the baseline can be raised.
// The gala2lean count is checked only for a run that used gala2lean.
func CheckBaseline(b Baseline, records []Record) (problem, note string) {
	var problems, notes []string
	check := func(n, base int, what, field string) {
		switch {
		case n < base:
			problems = append(problems, fmt.Sprintf("only %d %s, down from the baseline %d: the coverage may not go down", n, what, base))
		case n > base:
			notes = append(notes, fmt.Sprintf("%d %s, up from the baseline %d: raise %s in baseline.json", n, what, base, field))
		}
	}
	check(AgreeingExamples(records), b.AgreeingExamples, "examples agree with the spec", "agreeing_examples")
	if usesLean(records) {
		check(Gala2LeanAgreeingExamples(records), b.Gala2LeanAgreeingExamples,
			"examples gala2lean translates agree with Go", "gala2lean_agreeing_examples")
	}
	if usesGoCore(records) {
		check(GoCoreAgreeingExamples(records), b.GoCoreAgreeingExamples,
			"examples agree three ways (GoCore, galaspec and go run)", "gocore_agreeing_examples")
		check(GoCoreFidelityAgreeing(records), b.GoCoreFidelityAgreeing,
			"programs GoCore agrees with go run on", "gocore_fidelity_agreeing")
	}
	return strings.Join(problems, "; "), strings.Join(notes, "; ")
}

// usesLean reports whether the run compared gala2lean's translations.
func usesLean(records []Record) bool {
	for _, r := range records {
		if r.Lean != nil {
			return true
		}
	}
	return false
}

// MinAgreeing is how many agreeing programs a construct needs, with none
// disagreeing or inconclusive, to count as tested against the spec.
const MinAgreeing = 3

// The markers around the generated section of formal/COVERAGE.md.
const (
	BeginMarker = "<!-- difftest:begin -->"
	EndMarker   = "<!-- difftest:end -->"
)

// Coverage is what the report is rendered from.
type Coverage struct {
	Records  []Record
	Known    []Known
	Baseline Baseline
	// Proved maps a construct to the theorems that cover it; see
	// proved_constructs.json.
	Proved map[string][]string
}

// Render is the generated section of formal/COVERAGE.md, without the
// markers. The wording follows docs/FORMAL_SEMANTICS.MD: "tested against the
// spec" only for agreeing programs, "proved" only for theorems, and nothing
// about the transpiler being verified.
func Render(c Coverage) string {
	var b strings.Builder
	b.WriteString("## Differential testing\n\n")
	b.WriteString("Generated by `internal/formal/difftest` from one run; do not edit by hand.\n")
	b.WriteString("Each program runs through galaspec and through the transpiler and Go; the two\n")
	b.WriteString("runs are compared on stdout and outcome. These are test results, not proofs: they\n")
	b.WriteString("say the programs below behave as the spec says, not that the transpiler is correct.\n\n")
	lean := usesLean(c.Records)
	if lean {
		b.WriteString("Each program gala2lean translates is also run as Lean, a third account of what\n")
		b.WriteString("it means, and compared with the Go run (the gala2lean columns). A program it\n")
		b.WriteString("translates and that agrees says that the translation and Go print the same\n")
		b.WriteString("thing for it, nothing more.\n\n")
	}

	renderVerdicts(&b, c.Records, lean)
	fmt.Fprintf(&b, "Examples that run wholly inside the subset and agree with the spec: **%d** (ratchet baseline %d).\n",
		AgreeingExamples(c.Records), c.Baseline.AgreeingExamples)
	if lean {
		fmt.Fprintf(&b, "Examples gala2lean translates whose Lean run agrees with Go: **%d** (ratchet baseline %d).\n",
			Gala2LeanAgreeingExamples(c.Records), c.Baseline.Gala2LeanAgreeingExamples)
	}
	b.WriteString("\n")
	renderConstructs(&b, c, lean)
	renderUnsupported(&b, c.Records)
	if lean {
		renderLeanUnsupported(&b, c.Records)
	}
	renderKnown(&b, c.Known)
	if usesGoCore(c.Records) {
		renderGoCore(&b, c)
	}
	return b.String()
}

// GoCoreVerdicts is every GoCore verdict, in report order.
var GoCoreVerdicts = []Verdict{Agree, Disagree, Unsupported, Inconclusive, Resource}

// renderGoCore is the GoCore section (docs/FORMAL_SEMANTICS.MD, M4): its
// fidelity to `go run` on the Go the transformer emitted, apart from the
// spec's verdicts above, and the three-way count.
func renderGoCore(b *strings.Builder, c Coverage) {
	b.WriteString("### GoCore: the Go the transformer emitted\n\n")
	b.WriteString("GoCore (`formal/Gala/GoCore/`), the Lean model of the Go the transpiler emits, runs\n")
	b.WriteString("each compiled program's actual Go output, read back by the Go AST exporter, and is\n")
	b.WriteString("compared with `go run` of the same Go: stdout, and how it ends, a panic by the text Go\n")
	b.WriteString("prints. These measure GoCore's fidelity to Go, tested, not proved. RESOURCE is a run\n")
	b.WriteString("Go aborted for lack of stack or memory, or that ran past its time limit: reported,\n")
	b.WriteString("never compared. A GoCore DISAGREE is a GoCore or exporter bug and fails the run.\n\n")
	counts := map[Source]map[Verdict]int{FromExample: {}, FromGenerator: {}}
	for _, r := range c.Records {
		if r.GoCore != nil {
			counts[r.Source][r.GoCore.Verdict]++
		}
	}
	b.WriteString("| GoCore against go run | Examples | Generated programs |\n|---|---|---|\n")
	for _, v := range GoCoreVerdicts {
		fmt.Fprintf(b, "| %s | %d | %d |\n", v, counts[FromExample][v], counts[FromGenerator][v])
	}
	fmt.Fprintf(b, "\nPrograms GoCore agrees with go run on: **%d** (ratchet baseline %d).\n",
		GoCoreFidelityAgreeing(c.Records), c.Baseline.GoCoreFidelityAgreeing)
	fmt.Fprintf(b, "Examples where GoCore agrees with go run and galaspec agrees with go run: **%d** (ratchet baseline %d).\n\n",
		GoCoreAgreeingExamples(c.Records), c.Baseline.GoCoreAgreeingExamples)
	var agree []string
	for _, r := range c.Records {
		if agreesThreeWays(r) {
			agree = append(agree, "`"+r.ID+"`")
		}
	}
	if len(agree) > 0 {
		b.WriteString("They are: " + strings.Join(agree, ", ") + ".\n\n")
	}
	unsupported := map[string]int{}
	var resource []string
	for _, r := range c.Records {
		switch {
		case r.GoCore == nil:
		case r.GoCore.Verdict == Unsupported:
			unsupported[r.GoCore.Construct]++
		case r.GoCore.Verdict == Resource:
			resource = append(resource, fmt.Sprintf("- `%s`: %s", r.ID, r.GoCore.Reason))
		}
	}
	if len(unsupported) > 0 {
		renderCounts(b, unsupported, "The construct GoCore stopped at in each program it did not run to the end, by how many programs")
	}
	if len(resource) > 0 {
		b.WriteString("Runs Go aborted, not compared:\n\n" + strings.Join(resource, "\n") + "\n\n")
	}
}

func renderVerdicts(b *strings.Builder, records []Record, lean bool) {
	counts := map[Source]map[Verdict]int{FromExample: {}, FromGenerator: {}}
	leanCounts := map[Source]map[Verdict]int{FromExample: {}, FromGenerator: {}}
	for _, r := range records {
		counts[r.Source][r.Verdict]++
		if r.Lean != nil {
			leanCounts[r.Source][r.Lean.Verdict]++
		}
	}
	b.WriteString("### Verdicts\n\n")
	if !lean {
		b.WriteString("| Verdict | Examples | Generated programs |\n|---|---|---|\n")
		for _, v := range Verdicts {
			fmt.Fprintf(b, "| %s | %d | %d |\n", v, counts[FromExample][v], counts[FromGenerator][v])
		}
		b.WriteString("\n")
		return
	}
	b.WriteString("| Verdict | Examples | Generated programs | gala2lean: examples | gala2lean: generated programs |\n|---|---|---|---|---|\n")
	for _, v := range Verdicts {
		fmt.Fprintf(b, "| %s | %d | %d | %d | %d |\n", v, counts[FromExample][v], counts[FromGenerator][v],
			leanCounts[FromExample][v], leanCounts[FromGenerator][v])
	}
	b.WriteString("\n")
}

// renderLeanUnsupported lists what gala2lean did not translate.
func renderLeanUnsupported(b *strings.Builder, records []Record) {
	programs := map[string]int{}
	for _, r := range records {
		if r.Lean != nil && r.Lean.Verdict == Unsupported {
			programs[r.Lean.Construct]++
		}
	}
	b.WriteString("### Outside what gala2lean translates\n\n")
	if len(programs) == 0 {
		b.WriteString("gala2lean translated every program.\n\n")
		return
	}
	renderCounts(b, programs, "The first construct gala2lean did not translate in each program, by how many programs")
}

// renderCounts writes counts as a table, most frequent first, at most
// maxUnsupportedRows rows, after the sentence intro.
func renderCounts(b *strings.Builder, counts map[string]int, intro string) {
	kinds := slices.SortedFunc(maps.Keys(counts), func(x, y string) int {
		return cmp.Or(cmp.Compare(counts[y], counts[x]), cmp.Compare(x, y))
	})
	fmt.Fprintf(b, "%s (%d kinds", intro, len(kinds))
	if len(kinds) > maxUnsupportedRows {
		fmt.Fprintf(b, ", the top %d shown", maxUnsupportedRows)
		kinds = kinds[:maxUnsupportedRows]
	}
	b.WriteString("):\n\n| Construct | Programs |\n|---|---|\n")
	for _, k := range kinds {
		fmt.Fprintf(b, "| `%s` | %d |\n", k, counts[k])
	}
	b.WriteString("\n")
}

// constructTally is how the programs using a construct fared.
type constructTally struct {
	ran, agree, disagree, inconclusive int
}

// add counts one program's verdict.
func (t *constructTally) add(v Verdict) {
	switch v {
	case Agree:
		t.agree++
	case Disagree:
		t.disagree++
	case Inconclusive:
		t.inconclusive++
	}
}

func renderConstructs(b *strings.Builder, c Coverage, lean bool) {
	tally := map[string]*constructTally{}
	leanTally := map[string]*constructTally{}
	for _, k := range coreexport.Kinds() {
		tally[k] = &constructTally{}
		leanTally[k] = &constructTally{}
	}
	for _, r := range c.Records {
		if r.Lean != nil && r.Lean.Verdict != Unsupported {
			for k := range r.Constructs {
				if t, ok := leanTally[k]; ok {
					t.add(r.Lean.Verdict)
				}
			}
		}
		if r.Verdict == Unsupported {
			continue
		}
		for k := range r.Constructs {
			t, ok := tally[k]
			if !ok {
				continue // an unsupported construct, reported below
			}
			// Modelled means galaspec ran the program to the end: not out
			// of fuel, not a document it rejected.
			if k := r.Spec.Kind; k == KindOK || k == KindPanic {
				t.ran++
			}
			t.add(r.Verdict)
		}
	}
	b.WriteString("### Constructs\n\n")
	b.WriteString("A construct is **modelled** when galaspec ran a program using it to the end, and\n")
	fmt.Fprintf(b, "**tested against the spec** when at least %d programs using it agree and none\n", MinAgreeing)
	b.WriteString("disagrees or is inconclusive. **Proved** lists the kernel-checked theorems about the\n")
	b.WriteString("spec that cover it. A construct no program reached is \"not seen\": the run says\n")
	b.WriteString("nothing about it.\n")
	if lean {
		b.WriteString("**gala2lean** says how the programs using it that gala2lean translated fared\n")
		b.WriteString("when run as Lean and compared with Go.\n")
	}
	b.WriteString("\n")
	if lean {
		b.WriteString("| Construct (Core JSON kind) | Modelled | Tested against the spec | Proved | gala2lean |\n|---|---|---|---|---|\n")
	} else {
		b.WriteString("| Construct (Core JSON kind) | Modelled | Tested against the spec | Proved |\n|---|---|---|---|\n")
	}
	for _, k := range coreexport.Kinds() {
		t := tally[k]
		modelled, tested := "not seen", "no"
		if t.ran > 0 {
			modelled = "yes"
		}
		switch {
		case t.disagree > 0 || t.inconclusive > 0:
			tested = fmt.Sprintf("no: %d agree, %d disagree, %d inconclusive", t.agree, t.disagree, t.inconclusive)
		case t.agree >= MinAgreeing:
			tested = fmt.Sprintf("yes: %d agree", t.agree)
		case t.agree > 0:
			tested = fmt.Sprintf("no: %d agree", t.agree)
		}
		proved := "no"
		if ts := c.Proved[k]; len(ts) > 0 {
			proved = "`" + strings.Join(ts, "`, `") + "`"
		}
		if !lean {
			fmt.Fprintf(b, "| `%s` | %s | %s | %s |\n", k, modelled, tested, proved)
			continue
		}
		lt := leanTally[k]
		translated := "not translated"
		switch {
		case lt.disagree > 0 || lt.inconclusive > 0:
			translated = fmt.Sprintf("translated: %d agree, %d disagree, %d inconclusive", lt.agree, lt.disagree, lt.inconclusive)
		case lt.agree > 0:
			translated = fmt.Sprintf("translated: %d agree", lt.agree)
		}
		fmt.Fprintf(b, "| `%s` | %s | %s | %s | %s |\n", k, modelled, tested, proved, translated)
	}
	b.WriteString("\n")
}

// maxUnsupportedRows caps the unsupported-constructs table.
const maxUnsupportedRows = 20

func renderUnsupported(b *strings.Builder, records []Record) {
	// Every program galaspec stopped at, by what it named: an UNSUPPORTED
	// one, or a DISAGREE for a wrong line printed before the stop.
	programs := map[string]int{}
	for _, r := range records {
		if r.Spec.Kind == KindUnsupported {
			programs[r.Spec.Detail]++
		}
	}
	b.WriteString("### Outside the subset\n\n")
	if len(programs) == 0 {
		b.WriteString("No program was outside the subset.\n\n")
		return
	}
	renderCounts(b, programs, "The constructs galaspec stopped at, by how many programs it stopped at each")
}

func renderKnown(b *strings.Builder, known []Known) {
	b.WriteString("### Known disagreements\n\n")
	if len(known) == 0 {
		b.WriteString("None.\n")
		return
	}
	b.WriteString("| Program | Issue | Symptom |\n|---|---|---|\n")
	for _, k := range known {
		fmt.Fprintf(b, "| `%s` | #%d | %s |\n", k.Key(), k.Issue, strings.ReplaceAll(k.Symptom, "|", `\|`))
	}
}

// Splice replaces the generated section of doc, between BeginMarker and
// EndMarker, with section. The rest of doc is hand-written and kept as it is;
// without both markers, in order, it is an error, never a rewrite.
func Splice(doc, section string) (string, error) {
	begin := strings.Index(doc, BeginMarker)
	end := strings.Index(doc, EndMarker)
	if strings.Count(doc, BeginMarker) != 1 || strings.Count(doc, EndMarker) != 1 || end < begin {
		return "", fmt.Errorf("the document must hold %s and then %s, once each, around the generated section", BeginMarker, EndMarker)
	}
	head := doc[:begin+len(BeginMarker)]
	return head + "\n" + strings.TrimRight(section, "\n") + "\n" + doc[end:], nil
}
