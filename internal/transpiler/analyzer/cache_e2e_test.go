package analyzer

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"martianoff/gala/internal/transpiler"
)

// The unit tests in cache_test.go cover the in-memory toCachedRichAST
// projection. The tests below complement those by driving the full
// analyzer pipeline (Analyze → analyzePackage → disk cache → second
// Analyze) so a regression that only manifests in the integration —
// e.g. a missing rehydrateImports call on a HIT, or a filter step that
// runs against the wrong RichAST — surfaces as a test failure rather
// than waiting for a downstream consumer build to break.

// TestCacheE2E_HitRecursionRebuildsTransitiveView fires a fresh
// analyzer instance against the populated disk cache. Because the new
// instance starts with an empty in-memory analyzedPkgs map, the
// transitive types from pkg_b → pkg_c can only reach the consumer view
// through the cache HIT recursion path. If rehydrateImports is ever
// dropped or its input goes empty, this test fails with a missing
// pkg_c.CType, well before any downstream build error.
func TestCacheE2E_HitRecursionRebuildsTransitiveView(t *testing.T) {
	tmp := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(tmp, "go.mod"), []byte("module testmod\ngo 1.25\n"), 0644))

	pkgC := filepath.Join(tmp, "pkg_c")
	require.NoError(t, os.MkdirAll(pkgC, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(pkgC, "c.gala"), []byte("package pkg_c\n\nstruct CType(V int)\n"), 0644))

	pkgB := filepath.Join(tmp, "pkg_b")
	require.NoError(t, os.MkdirAll(pkgB, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(pkgB, "b.gala"), []byte("package pkg_b\n\nimport \"martianoff/gala/pkg_c\"\n\nstruct BType(C pkg_c.CType)\n"), 0644))

	pkgA := filepath.Join(tmp, "pkg_a")
	require.NoError(t, os.MkdirAll(pkgA, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(pkgA, "a.gala"), []byte("package pkg_a\n\nimport \"martianoff/gala/pkg_b\"\n\nstruct AType(B pkg_b.BType)\n"), 0644))

	parser := transpiler.NewAntlrGalaParser()
	searchPaths := []string{tmp}
	if std := stdSearchPathForE2E(); std != "" {
		searchPaths = append(searchPaths, std)
	}

	aFile := filepath.Join(pkgA, "a.gala")
	body, err := os.ReadFile(aFile)
	require.NoError(t, err)

	// Cold pass — populates disk cache (no in-memory analyzedPkgs entries).
	cold := NewBatchAnalyzer(parser, searchPaths, tmp)
	coldTree, err := parser.Parse(string(body))
	require.NoError(t, err)
	_, err = cold.Analyze(coldTree, aFile)
	require.NoError(t, err)

	// Confirm pkg_b's cached entry is local-only (BType present, CType absent)
	// AND records pkg_c as a direct import — the prerequisites for HIT
	// recursion to work.
	bCached := readDiskCacheEntry(t, tmp, "pkg_b")
	require.NotNil(t, bCached.Types["pkg_b.BType"], "pkg_b's own type must be cached")
	assert.Nil(t, bCached.Types["pkg_c.CType"], "transitive pkg_c.CType must NOT be inlined into pkg_b's cache")
	assert.Contains(t, bCached.DirectImports, "martianoff/gala/pkg_c", "pkg_b cache must record pkg_c as a direct import")

	// Warm pass — fresh analyzer instance. Every transitive type must
	// arrive via the cache-HIT recursion since analyzedPkgs starts empty.
	warm := NewBatchAnalyzer(parser, searchPaths, tmp)
	warmTree, err := parser.Parse(string(body))
	require.NoError(t, err)
	res, err := warm.Analyze(warmTree, aFile)
	require.NoError(t, err)
	require.NotNil(t, res)

	require.NotNil(t, res.Types["pkg_a.AType"], "AType must reach the consumer after HIT path")
	require.NotNil(t, res.Types["pkg_b.BType"], "BType must reach the consumer via HIT recursion through pkg_b")
	require.NotNil(t, res.Types["pkg_c.CType"], "CType must reach the consumer via two-level HIT recursion (pkg_a → pkg_b → pkg_c)")
}

// TestCacheE2E_DiskFootprintBounded is the on-disk equivalent of
// TestCachedRichAST_GobSizeBounded — it drives the full Analyze
// pipeline, lets it write a real cache file, and asserts the file size.
// The unit test covers the projection logic in isolation; this one
// catches a Put path that bypasses the projection (e.g. a future
// "atomic two-step" cache write that forgets to filter, or a different
// cache backend that writes RichAST directly).
func TestCacheE2E_DiskFootprintBounded(t *testing.T) {
	tmp := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(tmp, "go.mod"), []byte("module testmod\ngo 1.25\n"), 0644))

	// fanout — leaf with many declarations.
	const fanoutTypes = 80
	fanout := filepath.Join(tmp, "fanout")
	require.NoError(t, os.MkdirAll(fanout, 0755))
	var fanoutSrc strings.Builder
	fanoutSrc.WriteString("package fanout\n\n")
	for i := 0; i < fanoutTypes; i++ {
		fmt.Fprintf(&fanoutSrc, "struct T%d(V int)\n", i)
	}
	require.NoError(t, os.WriteFile(filepath.Join(fanout, "fanout.gala"), []byte(fanoutSrc.String()), 0644))

	// consumer imports fanout but declares only one type.
	consumer := filepath.Join(tmp, "consumer")
	require.NoError(t, os.MkdirAll(consumer, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(consumer, "consumer.gala"), []byte(
		"package consumer\n\nimport \"martianoff/gala/fanout\"\n\nstruct Wrapper(F fanout.T0)\n"), 0644))

	// top — ensures consumer is reached via analyzePackage (top-level
	// Analyze does not write to disk cache).
	top := filepath.Join(tmp, "top")
	require.NoError(t, os.MkdirAll(top, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(top, "top.gala"), []byte(
		"package top\n\nimport \"martianoff/gala/consumer\"\n\nstruct Use(W consumer.Wrapper)\n"), 0644))

	parser := transpiler.NewAntlrGalaParser()
	searchPaths := []string{tmp}
	if std := stdSearchPathForE2E(); std != "" {
		searchPaths = append(searchPaths, std)
	}

	a := NewBatchAnalyzer(parser, searchPaths, tmp)
	topFile := filepath.Join(top, "top.gala")
	body, err := os.ReadFile(topFile)
	require.NoError(t, err)
	tree, err := parser.Parse(string(body))
	require.NoError(t, err)
	_, err = a.Analyze(tree, topFile)
	require.NoError(t, err)

	// 64 KiB matches the unit test's budget. With 80 fanout types
	// inlined, a regression would push consumer's entry well past
	// 100 KiB; comfortably above the budget.
	const maxConsumerBytes = 64 * 1024
	consumerBytes := readDiskCacheEntryRaw(t, tmp, "consumer")
	if len(consumerBytes) > maxConsumerBytes {
		t.Fatalf("consumer cache entry is %d bytes (>%d) — transitive metadata likely leaked through the disk Put path. "+
			"v1 (pre-fix) baseline for fanout(N=%d) approaches %d bytes per consumer.",
			len(consumerBytes), maxConsumerBytes, fanoutTypes, fanoutTypes*1500)
	}

	// Warm-Analyze wall budget. Each cache HIT decode + recursion
	// should finish in well under a second on this fixture; the 5 s
	// cap traps the symptom of a 100-MB-class entry's gob decode.
	const maxWarmAnalyzeWall = 5 * time.Second
	warm := NewBatchAnalyzer(parser, searchPaths, tmp)
	warmTree, err := parser.Parse(string(body))
	require.NoError(t, err)
	start := time.Now()
	_, err = warm.Analyze(warmTree, topFile)
	require.NoError(t, err)
	elapsed := time.Since(start)
	if elapsed > maxWarmAnalyzeWall {
		t.Fatalf("warm Analyze took %s (>%s); rerun with GALA_PROFILE=1 to identify the slow [cache] HIT line.",
			elapsed, maxWarmAnalyzeWall)
	}
	t.Logf("consumer entry: %d bytes; warm Analyze wall: %s", len(consumerBytes), elapsed)
}

// readDiskCacheEntry decodes the on-disk cache file for the given
// package name and returns the typed CachedRichAST. Fails the test on
// any I/O or decode error so callers can use the result directly.
func readDiskCacheEntry(t *testing.T, projectRoot, pkgRelPath string) *CachedRichAST {
	t.Helper()
	data := readDiskCacheEntryRaw(t, projectRoot, pkgRelPath)
	cached, err := decodeCachedRichAST(data)
	require.NoError(t, err)
	return cached
}

// readDiskCacheEntryRaw returns the raw on-disk bytes for the entry —
// the size is itself the regression signal for the bounded-footprint
// test, independent of any specific bytes-format layout.
func readDiskCacheEntryRaw(t *testing.T, projectRoot, pkgRelPath string) []byte {
	t.Helper()
	cacheDirs, err := filepath.Glob(filepath.Join(projectRoot, ".gala", "cache", "v*"))
	require.NoError(t, err)
	require.NotEmpty(t, cacheDirs, "expected at least one cache directory under %s", projectRoot)
	prefix := strings.ReplaceAll(pkgRelPath, "/", "_") + "_"
	for _, d := range cacheDirs {
		entries, err := ioutil.ReadDir(d)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			if !strings.HasPrefix(e.Name(), prefix) {
				continue
			}
			// Accept either v1's .gob or v2's .bin so a transitional run
			// (mixed versions in stale dirs) doesn't trip the test. The
			// active version's directory always wins because the analyzer
			// only writes there.
			if !strings.HasSuffix(e.Name(), ".bin") && !strings.HasSuffix(e.Name(), ".gob") {
				continue
			}
			data, err := os.ReadFile(filepath.Join(d, e.Name()))
			require.NoError(t, err)
			return data
		}
	}
	t.Fatalf("no cache entry found for pkg %q under %s", pkgRelPath, projectRoot)
	return nil
}

// stdSearchPathForE2E walks up from the working directory looking for
// std/option.gala so tests can import std without pre-staging it under
// each tempdir. Named with the E2E suffix to avoid colliding with
// helpers in sibling test files.
func stdSearchPathForE2E() string {
	cwd, err := os.Getwd()
	if err != nil {
		return ""
	}
	dir := cwd
	for i := 0; i < 8; i++ {
		if _, err := os.Stat(filepath.Join(dir, "std", "option.gala")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
	return ""
}
