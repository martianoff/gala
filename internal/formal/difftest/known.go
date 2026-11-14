package difftest

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"
)

// Known is a known disagreement: a program whose DISAGREE verdict is tracked
// by an open issue. The fix for the issue removes the entry: a listed program
// that no longer disagrees fails the run, so the list never outlives a fix.
// The issue being open is checked in CI against the issue tracker
// (.github/workflows/formal.yml), since a test has no network.
type Known struct {
	// Program is the program's ID: an example's path in the repository
	// (examples/x.gala), or a generated program's ID (fuzzprograms/seed-007).
	Program string `json:"program"`
	// Issue is the open issue that tracks the disagreement.
	Issue int `json:"issue"`
	// Symptom is a substring of the disagreement's reason, so a program
	// that starts disagreeing for another reason is not taken for the
	// issue.
	Symptom string `json:"symptom"`
	// Oracle is whose disagreement with Go it is: "" for galaspec,
	// OracleGala2Lean for gala2lean's translation.
	Oracle string `json:"oracle,omitempty"`
}

// OracleGala2Lean marks a known disagreement of gala2lean's translation.
const OracleGala2Lean = "gala2lean"

// Key is the entry's key in the results CheckKnown compares: the program's ID
// for galaspec, LeanKey(ID) for gala2lean.
func (k Known) Key() string {
	if k.Oracle == OracleGala2Lean {
		return LeanKey(k.Program)
	}
	return k.Program
}

// LeanKey is the key of gala2lean's comparison of program id.
func LeanKey(id string) string { return OracleGala2Lean + ":" + id }

// ParseKnown reads the known-disagreements file: a JSON list of entries,
// each with a program, an issue and a symptom, no program twice. Unknown
// fields are an error, so a misspelt field is not silently empty.
func ParseKnown(data []byte) ([]Known, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var list []Known
	if err := dec.Decode(&list); err != nil {
		return nil, fmt.Errorf("known disagreements: %w", err)
	}
	seen := map[string]bool{}
	for i, k := range list {
		switch {
		case k.Program == "":
			return nil, fmt.Errorf("known disagreements: entry %d has no program", i)
		case k.Issue <= 0:
			return nil, fmt.Errorf("known disagreements: %s has no issue number", k.Program)
		case strings.TrimSpace(k.Symptom) == "":
			return nil, fmt.Errorf("known disagreements: %s has no symptom", k.Program)
		case k.Oracle != "" && k.Oracle != OracleGala2Lean:
			return nil, fmt.Errorf("known disagreements: %s has an unknown oracle %q (want none, or %q)", k.Program, k.Oracle, OracleGala2Lean)
		case seen[k.Key()]:
			return nil, fmt.Errorf("known disagreements: %s is listed twice", k.Program)
		}
		seen[k.Key()] = true
	}
	return list, nil
}

// CheckKnown compares the verdicts of a run, by Known.Key (the program ID, or
// LeanKey of it for gala2lean's comparisons), with the known
// disagreements, and lists every problem:
//
//   - a DISAGREE that is not listed;
//   - a listed program that disagrees for another reason than its symptom;
//   - a listed program that no longer disagrees: the entry must go;
//   - a listed program the run did not compare: the entry is stale.
//
// No problems means the run passes.
func CheckKnown(known []Known, results map[string]Comparison) []string {
	listed := map[string]Known{}
	for _, k := range known {
		listed[k.Key()] = k
	}
	var problems []string
	for _, id := range slices.Sorted(maps.Keys(results)) {
		c := results[id]
		k, ok := listed[id]
		switch {
		case c.Verdict == Disagree && !ok:
			problems = append(problems, fmt.Sprintf("%s: DISAGREE: %s\n  triage it (docs/FORMAL_SEMANTICS.MD): fix the transpiler or the spec, or list it in known_disagreements.json with an open issue", id, c.Reason))
		case ok && c.Verdict == Disagree && !strings.Contains(c.Reason, k.Symptom):
			problems = append(problems, fmt.Sprintf("%s: disagrees, but not with #%d's symptom %q: %s", id, k.Issue, k.Symptom, c.Reason))
		case ok && c.Verdict != Disagree:
			problems = append(problems, fmt.Sprintf("%s: listed for #%d but now %s: remove it from known_disagreements.json", id, k.Issue, c.Verdict))
		}
	}
	for _, k := range known {
		if _, ran := results[k.Key()]; !ran {
			problems = append(problems, fmt.Sprintf("%s: listed for #%d but not compared in this run: remove the stale entry", k.Key(), k.Issue))
		}
	}
	return problems
}
