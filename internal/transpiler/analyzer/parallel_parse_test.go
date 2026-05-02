package analyzer_test

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
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

// TestParseFilesConcurrent_RaceFree exercises the parsedFileCacheMu
// path under concurrent load by running many Analyze calls against the
// same BatchAnalyzer in parallel goroutines. Designed for `go test
// -race` to catch any future regression that adds a non-locked write
// to parsedFileCache (or any other map the parallel parser brushes
// against).
//
// The contention surface here is small in practice — Bazel runs one
// request per worker process at a time — but multiplexed workers
// (`supports-multiplex-workers`) can fire concurrent requests against
// the same process, and the LSP fans out parallel analyses across
// requests. Both rely on the cache being race-safe.
func TestParseFilesConcurrent_RaceFree(t *testing.T) {
	dir := t.TempDir()
	const n = 8
	paths := make([]string, n)
	for i := 0; i < n; i++ {
		name := filepath.Join(dir, fmt.Sprintf("rf_%02d.gala", i))
		body := fmt.Sprintf("package pkg\n\nval v%d = %d\n", i, i)
		require.NoError(t, os.WriteFile(name, []byte(body), 0644))
		paths[i] = name
	}

	innerParser := transpiler.NewAntlrGalaParser()
	batch := analyzer.NewBatchAnalyzer(innerParser, getStdSearchPath(), dir)

	bodies := make([]antlr.Tree, n)
	for i, p := range paths {
		body, err := os.ReadFile(p)
		require.NoError(t, err)
		tree, err := innerParser.Parse(string(body))
		require.NoError(t, err)
		bodies[i] = tree
	}

	const goroutines = 4
	const iterations = 5
	var wg sync.WaitGroup
	var fail atomic.Bool
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(seed int) {
			defer wg.Done()
			for it := 0; it < iterations; it++ {
				idx := (seed + it) % n
				siblings := append([]string(nil), paths[:idx]...)
				siblings = append(siblings, paths[idx+1:]...)
				batch.SetPackageFiles(siblings)
				if _, err := batch.Analyze(bodies[idx], paths[idx]); err != nil {
					fail.Store(true)
					t.Errorf("goroutine %d iter %d Analyze: %v", seed, it, err)
					return
				}
			}
		}(g)
	}
	wg.Wait()
	if fail.Load() {
		t.FailNow()
	}
}
