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
// This is a ratchet, not a target. It may only go down. Lowering it is the
// point: each unresolved site is a place inference gives up and something
// downstream falls back, and the fallback is what turns into a wrong type, an
// `any`, or a bug report — depending on which caller reached it first. Driving
// this number down closes those before anyone trips over them.
//
// When a change legitimately adds a construct the transformer cannot type yet,
// raise it deliberately and say why. When a change resolves types that were
// previously unresolved, lower it in the same commit so the slack is not
// silently available to the next regression.
//
// What is left is dominated by four shapes, which is what makes it a worklist
// rather than a wall:
//
//   - a val that loses its type, and every read through it
//     (arr, arr.Get(), copied, copied.Get())
//   - an Option or Immutable chain that stops resolving partway
//     (result.Get().Get, m.Get().Get, opt.Get().Map, ptr.Get().Deref)
//   - a method on a Go value, named through an interface
//     (err.Error(), e.Error(), node.Ptr)
//   - a generic function passed as a value (tickerAdvance, splitGeneric),
//     which has to be instantiated against the parameter it is passed to
//
// The first is the one to pull on: an unresolved val makes every later use of
// it unresolved too, so these counts are not independent.
//
// The fourth is small — about a dozen — but it is the one to leave alone in the
// count. Those idents were briefly filtered out as "a generic function has no
// standalone type", which is true, and it hid the reports that would show an
// instantiation failing. See hasNoTypeByConstruction.
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
