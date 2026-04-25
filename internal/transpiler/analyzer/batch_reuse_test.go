package analyzer_test

import (
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/antlr4-go/antlr/v4"
	"github.com/stretchr/testify/require"

	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/analyzer"
)

// countingParser wraps a GalaParser and counts how many times Parse fires.
// Used by TestBatchAnalyzer_StdAnalyzedOnce to prove the BatchAnalyzer's
// analyzedPkgs cache survives across SetPackageFiles calls — the property
// that powers the gala_tui demo build going from ~67s to ~18s cold.
type countingParser struct {
	inner transpiler.GalaParser
	calls int64
}

func (c *countingParser) Parse(input string) (antlr.Tree, error) {
	atomic.AddInt64(&c.calls, 1)
	return c.inner.Parse(input)
}

func (c *countingParser) ParseLenient(input string) (antlr.Tree, []error) {
	atomic.AddInt64(&c.calls, 1)
	return c.inner.ParseLenient(input)
}

// TestBatchAnalyzer_StdAnalyzedOnce locks in the invariant that powered
// the perf fix in builder.go (transpileWithSourceDir): when one
// BatchAnalyzer is reused across multiple Analyze calls, the std package
// (and any other previously-loaded import) is analyzed exactly once. A
// regression that resets analyzedPkgs per call — e.g. inside
// SetPackageFiles, or by allocating a fresh galaAnalyzer per file —
// would re-parse the entire std library on every Analyze and the per-file
// time blows up by 1+ seconds.
//
// Strategy: wrap the parser with a counter, run two Analyze calls on
// different files in different packages, snapshot the call count after
// the first, and assert the second call does not re-parse std.
func TestBatchAnalyzer_StdAnalyzedOnce(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.gala")
	b := filepath.Join(dir, "b.gala")
	require.NoError(t, os.WriteFile(a, []byte("package pkg\n\nval x = 1\n"), 0644))
	require.NoError(t, os.WriteFile(b, []byte("package pkg\n\nval y = 2\n"), 0644))

	innerParser := transpiler.NewAntlrGalaParser()
	counter := &countingParser{inner: innerParser}
	batch := analyzer.NewBatchAnalyzer(counter, getStdSearchPath(), dir)

	parse := func(p string) antlr.Tree {
		body, err := os.ReadFile(p)
		require.NoError(t, err)
		tree, err := innerParser.Parse(string(body))
		require.NoError(t, err)
		return tree
	}

	// First Analyze: std is loaded cold. Counter snapshots a high number.
	batch.SetPackageFiles([]string{b})
	_, err := batch.Analyze(parse(a), a)
	require.NoError(t, err)
	first := atomic.LoadInt64(&counter.calls)

	// Second Analyze on a different file in the same package: std must
	// already be cached in analyzedPkgs, so its many .gala files must NOT
	// be re-parsed. A reasonable bound is "no more parses than there are
	// sibling files in the test package" — i.e. 1 (just b.gala on the
	// previous call's parsedFileCache, or 0 if cached).
	atomic.StoreInt64(&counter.calls, 0)
	batch.SetPackageFiles([]string{a})
	_, err = batch.Analyze(parse(b), b)
	require.NoError(t, err)
	second := atomic.LoadInt64(&counter.calls)

	// We measure: first must include std parses (>>2). Second must be
	// drastically smaller — std is fully cached, only siblings might
	// trigger re-parse. Use a generous bound: second must be < first/2
	// so any regression that re-analyzes std (always >>10 .gala files)
	// is caught.
	if first <= 2 {
		t.Fatalf("expected first Analyze to parse many std files (sanity check); got only %d", first)
	}
	if second >= first/2 {
		t.Fatalf("BatchAnalyzer regression: second Analyze parsed %d files (first parsed %d); std should be cached after first call",
			second, first)
	}
}
