package transformer_test

import (
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// unresolvedBudget is the number of distinct expressions in the example corpus
// whose type the transformer cannot determine.
//
// It is a ratchet, and a regression detector rather than a list of defects to
// burn down.
//
// Every one of the current entries sits in a program that compiles and runs
// correctly. The examples corpus is covered by gala_exec_test targets, which
// build the generated Go and diff its stdout, and they all pass. That is not a
// coincidence: GALA emits Go, and where the transformer gives up, Go's own type
// checker finishes the job. `b.value = b.thunk()` inside a method on `Box[T]`
// is the shape — the transformer cannot say what `T` is, the emitted Go needs
// no help saying it.
//
// So a high number is not a backlog, and driving it to zero would mean teaching
// the transformer to re-derive types Go already derives: no user-visible
// payoff, and every added inference route is a speculative fallback of the kind
// this project has reverted before for lacking a guarding test.
//
// What the number is good for:
//
//   - A rise means something that used to resolve stopped resolving. That is
//     worth investigating even when the output is still correct, because the
//     next fallback along may not be as lucky.
//   - When a type bug *is* reported, the inventory says where inference gave up
//     nearby, which is usually where the fix goes.
//
// The guard that catches actually-wrong output is the type-erasure check in
// type_erasure_corpus_test.go, which asserts no value position widened to
// `any`. That one finding zero is the meaningful "no defects" signal; this one
// finding 541 is not a contradiction of it.
//
// When a change legitimately adds a construct the transformer cannot type yet,
// raise it deliberately and say why. When a change resolves types that were
// previously unresolved, lower it in the same commit so the slack is not
// silently available to the next regression.
//
// One shape to leave alone rather than filter: a generic function passed as a
// value (tickerAdvance, splitGeneric), about a dozen entries. Those were
// briefly excluded as "a generic function has no standalone type", which is
// true, and it hid exactly the reports that would show an instantiation
// failing. See hasNoTypeByConstruction.
//
// This is a ceiling rather than an equality check on purpose. Go type info is
// unavailable when the Go SDK is not on the path, and without it some
// expressions that would otherwise resolve do not. A run with more type
// information available resolves more and comes in under the ceiling; it
// should not have to move the number to stay green.
const unresolvedBudget = 541

// TestUnresolvedTypeInventory transpiles the single-file example corpus with
// the unresolved-type inventory enabled and holds the total to a budget.
//
// Examples are the corpus because they are the broadest body of GALA that is
// known to compile: whatever they fail to type, they fail to type while
// otherwise succeeding, which is exactly the hidden case.
func TestUnresolvedTypeInventory(t *testing.T) {
	files := exampleCorpus(t)

	var sites []string
	var skipped int

	for _, f := range files {
		if f.Err != nil {
			// Some examples are intentional negative cases, and some need
			// sibling files this single-file harness does not stage. Neither
			// tells us anything about unresolved types, so they are skipped —
			// and counted, so a harness that silently stops covering the corpus
			// is visible rather than looking like an improvement.
			skipped++
			continue
		}
		for _, u := range f.Unresolved {
			sites = append(sites, fmt.Sprintf("%s:%d\t%s", f.Name, u.Line, u.Expr))
		}
	}
	total := len(sites)

	t.Logf("corpus: %d files, %d transpiled, %d skipped", len(files), len(files)-skipped, skipped)
	require.Less(t, skipped, len(files),
		"every example failed to transpile; the inventory is measuring nothing")

	if total > unresolvedBudget {
		sort.Strings(sites)
		var b strings.Builder
		for i, s := range sites {
			if i == 40 {
				fmt.Fprintf(&b, "  ... and %d more\n", len(sites)-i)
				break
			}
			fmt.Fprintf(&b, "  %s\n", s)
		}
		t.Fatalf("unresolved-type inventory is %d, budget is %d.\n"+
			"Each line is an expression whose type could not be determined, "+
			"in source order:\n%s"+
			"If this change legitimately introduces a construct that cannot be typed yet, "+
			"raise unresolvedBudget and say why. Otherwise this is a type-resolution "+
			"regression — see unresolved_types.go.",
			total, unresolvedBudget, b.String())
	}
}
