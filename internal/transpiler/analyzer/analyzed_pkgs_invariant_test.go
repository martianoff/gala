package analyzer

import (
	"bytes"
	"encoding/gob"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"martianoff/gala/internal/transpiler"
)

// TestBatchAnalyzer_AnalyzedPkgsStoresOnlyOwnTypes locks in the in-memory
// projection invariant introduced in commit 4b99e60 (PR #316, Bug 15(a)).
//
// Before the fix, BatchAnalyzer.analyzedPkgs[P] held the post-Merge full
// transitive type closure of P — every package's entry had inlined copies
// of every type reachable through its import graph. On gala_team
// (~96 packages including transitive deps) this drove peak RSS past
// 16 GB before the OOM killer fired; a single app/cli entry alone reached
// 4.6 GB. The fix replaces the eager `analyzedPkgs[X] = importedAST` writes
// with `storeAnalyzedPkg(X, importedAST)`, which projects to own-types only
// (via projectOwnRichAST in cache.go) and rebuilds the closure on demand
// (via mergeAnalyzedClosureAt) using the recorded analyzedPkgImports.
//
// This test catches a regression that re-introduces eager-merge into
// analyzedPkgs — either by direct reassignment or by a future refactor
// that bypasses storeAnalyzedPkg's projection step. The shape mirrors
// PR #308's on-disk equivalent (TestCachedRichAST_GobSizeBounded /
// TestCacheE2E_DiskFootprintBounded) so the in-memory and on-disk
// projections share a regression net.
//
// Fixture: three packages in a chain. C imports A and B; B imports A.
// Analysing C populates analyzedPkgs entries for A, B, and (via Analyze)
// nothing for C itself (top-level Analyze does not store). For each entry
// we assert (a) the entry's own type is present, and (b) types defined in
// any other gala package are absent. Then we gob-serialize each entry and
// assert the byte size is bounded — a soft floor that catches the
// "Merge slipped back in" failure mode even if the field layout changes.
func TestBatchAnalyzer_AnalyzedPkgsStoresOnlyOwnTypes(t *testing.T) {
	tmp := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(tmp, "go.mod"), []byte("module testmod\ngo 1.25\n"), 0644))

	// Package A — sealed type with two variants. Sealed types fan out
	// methods/companion-objects so a regression that re-inlines A into
	// downstream packages would carry visible weight.
	pkgA := filepath.Join(tmp, "pkg_a")
	require.NoError(t, os.MkdirAll(pkgA, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(pkgA, "a.gala"), []byte(
		"package pkg_a\n\nsealed type TypeA {\n    case VariantA1(X int)\n    case VariantA2(Y string)\n}\n"), 0644))

	// Package B — imports A, defines its own struct that references A.
	pkgB := filepath.Join(tmp, "pkg_b")
	require.NoError(t, os.MkdirAll(pkgB, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(pkgB, "b.gala"), []byte(
		"package pkg_b\n\nimport \"martianoff/gala/pkg_a\"\n\nstruct TypeB(A pkg_a.TypeA)\n"), 0644))

	// Package C (leaf) — imports both A and B, defines TypeC.
	pkgC := filepath.Join(tmp, "pkg_c")
	require.NoError(t, os.MkdirAll(pkgC, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(pkgC, "c.gala"), []byte(
		"package pkg_c\n\nimport \"martianoff/gala/pkg_a\"\nimport \"martianoff/gala/pkg_b\"\n\nstruct TypeC(A pkg_a.TypeA, B pkg_b.TypeB)\n"), 0644))

	parser := transpiler.NewAntlrGalaParser()
	searchPaths := []string{tmp}
	if std := stdSearchPathForE2E(); std != "" {
		searchPaths = append(searchPaths, std)
	}

	batch := NewBatchAnalyzer(parser, searchPaths, tmp)
	cFile := filepath.Join(pkgC, "c.gala")
	body, err := os.ReadFile(cFile)
	require.NoError(t, err)
	tree, _, err := parser.Parse(string(body))
	require.NoError(t, err)
	_, err = batch.Analyze(tree, nil, cFile)
	require.NoError(t, err)

	// After analysing the leaf, both transitive dependencies must have
	// been recorded. (C itself is the top-level Analyze target and is not
	// stored in analyzedPkgs.)
	const (
		pathA = "martianoff/gala/pkg_a"
		pathB = "martianoff/gala/pkg_b"
	)
	entryA, hasA := batch.inner.analyzedPkgs[pathA]
	entryB, hasB := batch.inner.analyzedPkgs[pathB]
	require.True(t, hasA, "expected analyzedPkgs entry for %s after analysing leaf C; entries: %v",
		pathA, keysOfAnalyzedPkgs(batch.inner.analyzedPkgs))
	require.True(t, hasB, "expected analyzedPkgs entry for %s after analysing leaf C; entries: %v",
		pathB, keysOfAnalyzedPkgs(batch.inner.analyzedPkgs))
	require.NotNil(t, entryA, "pkg_a entry must not be the in-progress nil placeholder after Analyze returned")
	require.NotNil(t, entryB, "pkg_b entry must not be the in-progress nil placeholder after Analyze returned")

	// Core invariant — each entry holds ONLY the package's own types.
	// pkg_a's entry must have TypeA but NOT pkg_b's TypeB or pkg_c's TypeC.
	// pkg_b's entry must have TypeB but NOT pkg_a's TypeA (despite import).
	t.Run("pkg_a entry has own type", func(t *testing.T) {
		assert.Contains(t, entryA.Types, "pkg_a.TypeA",
			"pkg_a's analyzedPkgs entry should contain its own sealed type pkg_a.TypeA")
	})
	t.Run("pkg_a entry has NO foreign types", func(t *testing.T) {
		for k := range entryA.Types {
			if !strings.HasPrefix(k, "pkg_a.") {
				t.Errorf("pkg_a's analyzedPkgs entry leaks foreign type key %q — projectOwnRichAST should have stripped it. "+
					"This is the regression that caused gala_team to OOM at 16+ GB RSS (Bug 15(a)).", k)
			}
		}
		// pkg_b's TypeB and pkg_c's TypeC must be absent specifically.
		assert.NotContains(t, entryA.Types, "pkg_b.TypeB",
			"pkg_b.TypeB must NOT appear in pkg_a's entry — pkg_a does not import pkg_b")
		assert.NotContains(t, entryA.Types, "pkg_c.TypeC",
			"pkg_c.TypeC must NOT appear in pkg_a's entry — pkg_c is downstream of pkg_a")
	})
	t.Run("pkg_b entry has own type", func(t *testing.T) {
		assert.Contains(t, entryB.Types, "pkg_b.TypeB",
			"pkg_b's analyzedPkgs entry should contain its own struct pkg_b.TypeB")
	})
	t.Run("pkg_b entry has NO transitive types from pkg_a", func(t *testing.T) {
		// This is the key regression — pre-fix, pkg_b's entry would have
		// pkg_a.TypeA inlined because Merge folded it in via the import
		// edge. Post-fix, only pkg_b's own keys remain; pkg_a's types are
		// reconstituted at read time through mergeAnalyzedClosureAt.
		for k := range entryB.Types {
			if !strings.HasPrefix(k, "pkg_b.") {
				t.Errorf("pkg_b's analyzedPkgs entry leaks foreign type key %q — eager Merge regression. "+
					"projectOwnRichAST should have filtered to own package only.", k)
			}
		}
		assert.NotContains(t, entryB.Types, "pkg_a.TypeA",
			"pkg_a.TypeA must NOT be inlined into pkg_b's entry — that is the eager-merge bug fixed in 4b99e60")
		assert.NotContains(t, entryB.Types, "pkg_a.VariantA1",
			"pkg_a.VariantA1 (sealed companion) must NOT be inlined into pkg_b's entry")
		assert.NotContains(t, entryB.Types, "pkg_a.VariantA2",
			"pkg_a.VariantA2 (sealed companion) must NOT be inlined into pkg_b's entry")
	})

	// Closure walk must still reconstruct the full transitive type set
	// for pkg_b on demand — this proves the projection didn't break the
	// downstream consumer's view.
	t.Run("mergeAnalyzedClosureAt reconstructs transitive types on read", func(t *testing.T) {
		target := &transpiler.RichAST{
			Types:    make(map[string]*transpiler.TypeMetadata),
			Packages: make(map[string]string),
		}
		batch.inner.mergeAnalyzedClosureAt(target, pathB, make(map[string]bool))
		assert.Contains(t, target.Types, "pkg_b.TypeB",
			"closure walker should pull pkg_b's own type into target")
		assert.Contains(t, target.Types, "pkg_a.TypeA",
			"closure walker should pull pkg_a.TypeA into target via the recorded import edge — "+
				"if absent, mergeAnalyzedClosureAt is broken")
	})

	// Bounded-size sanity: gob each entry and assert it fits well under
	// 1 MiB. This is the "if the structure changes, still catch the
	// regression" net. Pre-fix, app/cli's gala_team entry was 4.6 GB; any
	// reasonable bound rules it out. We use 256 KiB headroom, which is
	// double the on-disk cache budget (TestCachedRichAST_GobSizeBounded
	// uses 64 KiB) — the in-memory entry is allowed to carry slightly
	// more incidental fields than the gob-only CachedRichAST view.
	const maxEntryBytes = 256 * 1024
	t.Run("entry sizes are bounded", func(t *testing.T) {
		for _, c := range []struct {
			path  string
			entry *transpiler.RichAST
		}{
			{pathA, entryA},
			{pathB, entryB},
		} {
			cached := toCachedRichAST(c.entry, "h", nil)
			var buf bytes.Buffer
			require.NoError(t, gob.NewEncoder(&buf).Encode(cached))
			if buf.Len() > maxEntryBytes {
				t.Errorf("analyzedPkgs[%s] gob-serializes to %d bytes (>%d) — eager-merge regression suspected. "+
					"Pre-fix baseline on gala_team was 4.6 GB per app/cli-class entry.",
					c.path, buf.Len(), maxEntryBytes)
			} else {
				t.Logf("analyzedPkgs[%s]: %d bytes (well under %d budget)", c.path, buf.Len(), maxEntryBytes)
			}
		}
	})
}

// keysOfAnalyzedPkgs returns the set of paths recorded in analyzedPkgs.
// Used only for failure-message context so a regression's diagnostic
// shows what *did* get stored, not just what was expected.
func keysOfAnalyzedPkgs(m map[string]*transpiler.RichAST) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
