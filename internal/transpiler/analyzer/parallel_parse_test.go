package analyzer_test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/antlr4-go/antlr/v4"
	"github.com/stretchr/testify/require"

	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/analyzer"
)

// TestParseFilesConcurrent_ResultMatchesSequential locks in that the
// parallel sibling-parse path produces the same per-file metadata as
// the previous sequential loop. Regression guard for the cold-start
// optimization that turns the per-package sibling-discovery loop from
// O(N) wall to ~O(N/GOMAXPROCS): a future refactor that drops the
// parsedFileCacheMu, mis-orders the result slice, or silently swallows
// a parse error from one worker would break batch-mode transpilation.
//
// Strategy: build a 12-file package (more than GOMAXPROCS on most
// runners so the worker pool actually rotates), drive Analyze through
// the explicit-package-files branch, and assert that every sibling's
// types appear in the result for at least one Analyze call. We don't
// pin Parse counts here — TestParsedFileCache_AmortizesAcrossBatch
// already covers cache-hit semantics; this test just covers parallel
// completeness.
func TestParseFilesConcurrent_ResultMatchesSequential(t *testing.T) {
	dir := t.TempDir()
	const n = 12
	paths := make([]string, n)
	for i := 0; i < n; i++ {
		name := filepath.Join(dir, fmt.Sprintf("file_%02d.gala", i))
		body := fmt.Sprintf("package pkg\n\ntype T%d struct {\n    x int\n}\n", i)
		require.NoError(t, os.WriteFile(name, []byte(body), 0644))
		paths[i] = name
	}

	innerParser := transpiler.NewAntlrGalaParser()
	batch := analyzer.NewBatchAnalyzer(innerParser, getStdSearchPath(), dir)

	parse := func(p string) antlr.Tree {
		body, err := os.ReadFile(p)
		require.NoError(t, err)
		tree, err := innerParser.Parse(string(body))
		require.NoError(t, err)
		return tree
	}

	// Drive Analyze for the FIRST file with all others as explicit
	// package files. The parallel sibling parser kicks in for the 11
	// siblings; their type metadata must end up in the merged RichAST.
	siblings := append([]string(nil), paths[1:]...)
	batch.SetPackageFiles(siblings)
	rich, err := batch.Analyze(parse(paths[0]), paths[0])
	require.NoError(t, err)

	for i := 0; i < n; i++ {
		want := fmt.Sprintf("pkg.T%d", i)
		require.Containsf(t, rich.Types, want,
			"expected type %s to be present after parallel sibling parse", want)
	}
}

// TestParseFilesConcurrent_RaceFree exercises parseFilesConcurrent's
// goroutine pool inside a single Analyze call under -race. The inner
// pool spawns workers that all hit parsedFileCache through the same
// parsedFileCacheMu; this test forces that path to fire with a
// many-sibling package so any future regression that drops the lock
// (or adds a non-locked write to the cache) trips the race detector.
//
// We do NOT exercise concurrent Analyze calls against the same
// BatchAnalyzer — that's an explicitly unsupported pattern (most of
// the analyzer's per-call state — analyzedPkgs, siblingTreeCache,
// pkgResultCache, currentRichAST — is single-threaded). PR #319's
// initial version of this test did fire concurrent Analyze and
// passed on Windows by scheduling luck; the same code surfaced a
// `concurrent map writes` fatal on Linux CI. The fix is to scope
// the test to what we actually claim to be safe.
func TestParseFilesConcurrent_RaceFree(t *testing.T) {
	dir := t.TempDir()
	const n = 12
	paths := make([]string, n)
	for i := 0; i < n; i++ {
		name := filepath.Join(dir, fmt.Sprintf("rf_%02d.gala", i))
		body := fmt.Sprintf("package pkg\n\nval v%d = %d\n", i, i)
		require.NoError(t, os.WriteFile(name, []byte(body), 0644))
		paths[i] = name
	}

	innerParser := transpiler.NewAntlrGalaParser()
	batch := analyzer.NewBatchAnalyzer(innerParser, getStdSearchPath(), dir)

	// One Analyze call with N-1 siblings → parseFilesConcurrent
	// fires its full goroutine pool. Under -race, any unprotected
	// write to parsedFileCache from the worker goroutines fails.
	body, err := os.ReadFile(paths[0])
	require.NoError(t, err)
	tree, err := innerParser.Parse(string(body))
	require.NoError(t, err)

	siblings := append([]string(nil), paths[1:]...)
	batch.SetPackageFiles(siblings)
	_, err = batch.Analyze(tree, paths[0])
	require.NoError(t, err)
}
