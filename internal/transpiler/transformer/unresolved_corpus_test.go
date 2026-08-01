package transformer_test

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/analyzer"
	"martianoff/gala/internal/transpiler/generator"
	"martianoff/gala/internal/transpiler/transformer"

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
// The current inventory is dominated by three shapes, which is what makes it a
// worklist rather than a wall:
//
//   - bare references to a function passed as a value (tickerAdvance,
//     splitGeneric, consList, FutureOf, New)
//   - vals and Option chains that lose their type partway along
//     (arr, arr.Get(), d1.Get().Age.Get())
//   - a method named but not called, as the receiver of a further call
//     (.FlatMap, .GetOrElse, .Get)
//
// This is a ceiling rather than an equality check on purpose. Go type info is
// unavailable when the Go SDK is not on the path, and without it some
// expressions that would otherwise resolve do not. A run with more type
// information available resolves more and comes in under the ceiling; it
// should not have to move the number to stay green.
const unresolvedBudget = 735

// TestUnresolvedTypeInventory transpiles the single-file example corpus with
// the unresolved-type inventory enabled and holds the total to a budget.
//
// Examples are the corpus because they are the broadest body of GALA that is
// known to compile: whatever they fail to type, they fail to type while
// otherwise succeeding, which is exactly the hidden case.
func TestUnresolvedTypeInventory(t *testing.T) {
	t.Setenv("GALA_WARN_TYPES", "1")

	files := findExampleSources(t)
	require.NotEmpty(t, files, "no example sources found; the corpus harness would pass vacuously")

	type siteCount struct {
		key   string
		count int
	}
	bySite := map[string]int{}
	total := 0
	var skipped int

	for _, path := range files {
		src, err := os.ReadFile(path)
		require.NoError(t, err)

		p := transpiler.NewAntlrGalaParser()
		a := analyzer.NewGalaAnalyzer(p, getStdSearchPath())
		tr := transformer.NewGalaASTTransformer()
		g := generator.NewGoCodeGenerator()
		trans := transpiler.NewGalaToGoTranspiler(p, a, tr, g)

		if _, err := trans.Transpile(string(src), filepath.Base(path)); err != nil {
			// Some examples are intentional negative cases, and some need
			// sibling files this single-file harness does not stage. Neither
			// tells us anything about unresolved types, so they are skipped —
			// and counted, so a harness that silently stops covering the corpus
			// is visible rather than looking like an improvement.
			skipped++
			continue
		}

		for _, u := range transformer.UnresolvedTypes(tr) {
			total++
			bySite[fmt.Sprintf("%s\t%s:%d\t%s", u.Site, filepath.Base(path), u.Line, u.Expr)]++
		}
	}

	t.Logf("corpus: %d files, %d transpiled, %d skipped", len(files), len(files)-skipped, skipped)
	require.Less(t, skipped, len(files),
		"every example failed to transpile; the inventory is measuring nothing")

	if total > unresolvedBudget {
		sites := make([]siteCount, 0, len(bySite))
		for k, c := range bySite {
			sites = append(sites, siteCount{k, c})
		}
		sort.Slice(sites, func(i, j int) bool {
			if sites[i].count != sites[j].count {
				return sites[i].count > sites[j].count
			}
			return sites[i].key < sites[j].key
		})
		var b strings.Builder
		for i, s := range sites {
			if i == 40 {
				fmt.Fprintf(&b, "  ... and %d more\n", len(sites)-i)
				break
			}
			fmt.Fprintf(&b, "  %3d  %s\n", s.count, s.key)
		}
		t.Fatalf("unresolved-type inventory is %d, budget is %d.\n"+
			"Each line is an expression whose type could not be determined, "+
			"most frequent first:\n%s"+
			"If this change legitimately introduces a construct that cannot be typed yet, "+
			"raise unresolvedBudget and say why. Otherwise this is a type-resolution "+
			"regression — see unresolved_types.go.",
			total, unresolvedBudget, b.String())
	}
}

// findExampleSources returns the single-file example sources staged for this
// test target.
func findExampleSources(t *testing.T) []string {
	t.Helper()
	roots := getStdSearchPath()
	require.NotEmpty(t, roots, "cannot locate the repository root")

	dir := filepath.Join(roots[0], "examples")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Skipf("examples directory not staged for this target: %v", err)
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".gala") {
			continue
		}
		out = append(out, filepath.Join(dir, e.Name()))
	}
	sort.Strings(out)
	return out
}
