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

// parsedFileCounter wraps a GalaParser and counts Parse invocations.
type parsedFileCounter struct {
	inner transpiler.GalaParser
	calls int64
}

func (c *parsedFileCounter) Parse(input string) (antlr.Tree, map[int]string, error) {
	atomic.AddInt64(&c.calls, 1)
	return c.inner.Parse(input)
}

func (c *parsedFileCounter) ParseLenient(input string) (antlr.Tree, map[int]string, []error) {
	atomic.AddInt64(&c.calls, 1)
	return c.inner.ParseLenient(input)
}

// TestParsedFileCache_AmortizesAcrossBatch locks in the parsedFileCache
// optimization. Without it, every Analyze call in the explicit-package-
// files branch re-reads and re-parses every sibling — turning a 37-file
// package's sibling-discovery cost from O(N) into O(N²). With the cache,
// re-running the same set of Analyze calls is essentially free at the
// parser level.
//
// Strategy: warm the cache by transpiling all files once (parses fire,
// fill cache and analyzedPkgs). Reset the parse counter. Run the same
// loop again. The second pass must produce ~zero parses; allow up to 1
// for any analyzer-internal noise we don't control.
func TestParsedFileCache_AmortizesAcrossBatch(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"a.gala": "package pkg\n\nval a = 1\n",
		"b.gala": "package pkg\n\nval b = 2\n",
		"c.gala": "package pkg\n\nval c = 3\n",
		"d.gala": "package pkg\n\nval d = 4\n",
	}
	paths := make([]string, 0, len(files))
	for name, body := range files {
		p := filepath.Join(dir, name)
		require.NoError(t, os.WriteFile(p, []byte(body), 0644))
		paths = append(paths, p)
	}

	innerParser := transpiler.NewAntlrGalaParser()
	counter := &parsedFileCounter{inner: innerParser}
	batch := analyzer.NewBatchAnalyzer(counter, getStdSearchPath(), dir)

	bodies := make(map[string]antlr.Tree)
	for _, p := range paths {
		body, err := os.ReadFile(p)
		require.NoError(t, err)
		tree, _, err := innerParser.Parse(string(body))
		require.NoError(t, err)
		bodies[p] = tree
	}

	transpileAll := func() {
		for _, current := range paths {
			var siblings []string
			for _, other := range paths {
				if other != current {
					siblings = append(siblings, other)
				}
			}
			batch.SetPackageFiles(siblings)
			_, err := batch.Analyze(bodies[current], nil, current)
			require.NoError(t, err)
		}
	}

	transpileAll() // warm
	atomic.StoreInt64(&counter.calls, 0)
	transpileAll() // measure

	got := atomic.LoadInt64(&counter.calls)
	if got > 1 {
		t.Fatalf("parsedFileCache regression: %d Parse calls on warm pass across %d files; expected ~0 (sibling caches must hit on warm pass)",
			got, len(paths))
	}
}

// TestParsedFileCache_InvalidatesOnSizeChange proves the (mtime, size)
// invalidation triggers when a file changes. Otherwise the cache could
// mask edits in long-lived processes (LSP, watch mode).
func TestParsedFileCache_InvalidatesOnSizeChange(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.gala")
	b := filepath.Join(dir, "b.gala")
	require.NoError(t, os.WriteFile(a, []byte("package pkg\n\nval a = 1\n"), 0644))
	require.NoError(t, os.WriteFile(b, []byte("package pkg\n\nval b = 2\n"), 0644))

	innerParser := transpiler.NewAntlrGalaParser()
	counter := &parsedFileCounter{inner: innerParser}
	batch := analyzer.NewBatchAnalyzer(counter, getStdSearchPath(), dir)

	parse := func(p string) antlr.Tree {
		body, err := os.ReadFile(p)
		require.NoError(t, err)
		tree, _, err := innerParser.Parse(string(body))
		require.NoError(t, err)
		return tree
	}

	batch.SetPackageFiles([]string{b})
	_, err := batch.Analyze(parse(a), nil, a)
	require.NoError(t, err)
	first := atomic.LoadInt64(&counter.calls)

	batch.SetPackageFiles([]string{b})
	_, err = batch.Analyze(parse(a), nil, a)
	require.NoError(t, err)
	require.Equal(t, first, atomic.LoadInt64(&counter.calls), "expected cache hit on unchanged file")

	// Rewrite b with different size — invalidation must fire.
	require.NoError(t, os.WriteFile(b, []byte("package pkg\n\nval b2 = 99\nval bExtra = 42\n"), 0644))

	batch.SetPackageFiles([]string{b})
	_, err = batch.Analyze(parse(a), nil, a)
	require.NoError(t, err)
	if atomic.LoadInt64(&counter.calls) <= first {
		t.Fatalf("expected re-parse after b.gala size changed; counter still at %d", atomic.LoadInt64(&counter.calls))
	}
}
