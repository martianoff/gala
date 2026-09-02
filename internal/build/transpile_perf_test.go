package build

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/antlr4-go/antlr/v4"
	"github.com/bazelbuild/rules_go/go/tools/bazel"
	"github.com/stretchr/testify/require"

	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/analyzer"
	"martianoff/gala/internal/transpiler/generator"
	"martianoff/gala/internal/transpiler/transformer"
)

// TestTranspile_ParallelSiblingPerf_ColdWarm is the gala-build counterpart
// of the bazel perf-real-build workflow. It runs the same transpile
// pipeline that `gala build` invokes (BatchAnalyzer.Analyze →
// parseFilesConcurrent), measures cold and warm wall times against a
// synthetic many-sibling package, logs both numbers, and fails the
// build if either exceeds a loose budget.
//
// Why a Go-level perf test
// ------------------------
// The bazel perf workflow only runs nightly + on workflow_dispatch.
// Local development needs a faster signal: a regression that silently
// re-serializes the sibling-parse loop, drops the parsedFileCache
// mutex, or makes pkgResultCache hits re-walk the import closure
// should fail at `bazel test //...` time, not the next nightly cron.
// This test is the in-process equivalent — it exercises the exact
// code path the persistent worker uses (one BatchAnalyzer reused
// across files), so the numbers it produces correlate directly with
// the apex-transpile budget on CI.
//
// Methodology
// -----------
// Synthesize a single GALA package with N=8 sibling files, each
// containing transpilePerfTypesPerFile struct types so per-file ANTLR
// parse time is meaningful (tens of milliseconds, not microseconds).
// Then time two passes of single-file transpile against the FIRST
// sibling, with the OTHER seven files set as packageFiles — that's
// the path where parseFilesConcurrent is called against a non-trivial
// candidate list, exactly as it fires in the apex transpile.
//
//   - Cold pass: fresh BatchAnalyzer + fresh on-disk cache. The
//     analyzer must do the full ANTLR parse for every sibling, plus
//     the std closure walk.
//   - Warm pass: reuse the same BatchAnalyzer (matches the worker's
//     in-process amortization), so parsedFileCache + analyzedPkgs
//     short-circuit re-parse and re-analyze.
//
// Numbers
// -------
// On the maintainer's Windows box the optimization brings cold from
// ~2.6 s to ~0.6 s and warm from ~2.4 s to ~0.5 s for a comparable
// shape (collection_immutable/list.gala, 5-file sibling set).
// Synthetic numbers are smaller because the test files don't import
// go_interop (which costs ~500 ms by itself).
//
// Budgets are loose enough for a 4-vCPU CI runner (~3-5x slower than
// the maintainer's box) — bumping them needs a documented reason in
// the PR description, mirroring the bazel-perf budget convention.
const (
	transpilePerfFileCount    = 8
	transpilePerfTypesPerFile = 30
	transpilePerfColdBudget   = 8 * time.Second
	transpilePerfWarmBudget   = 4 * time.Second
)

func TestTranspile_ParallelSiblingPerf_ColdWarm(t *testing.T) {
	if testing.Short() {
		t.Skip("perf test skipped in -short mode")
	}

	projectDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(projectDir, "gala.mod"),
		[]byte("module example.com/perf\n\ngala 0.0.0\n"), 0644))
	files := writeSiblingPackage(t, projectDir, transpilePerfFileCount)

	// Pick the first file as the transpile target; the rest become
	// packageFiles. This exercises parseFilesConcurrent against a
	// realistic candidate list (N-1 siblings) instead of the sparse
	// edge cases.
	target := files[0]
	siblings := append([]string(nil), files[1:]...)
	content, err := os.ReadFile(target)
	require.NoError(t, err)

	// Wipe any prior .gala/cache so the cold pass is genuinely cold.
	_ = os.RemoveAll(filepath.Join(projectDir, ".gala"))

	// Build a transpiler pipeline once. Reusing the same
	// BatchAnalyzer across the cold/warm passes is exactly what the
	// persistent worker does, and it's what makes the warm pass
	// meaningfully faster than the cold one — the analyzedPkgs cache
	// for std and the parsedFileCache for the siblings both survive
	// the first pass.
	p := transpiler.NewAntlrGalaParser()
	tr := transformer.NewGalaASTTransformer()
	g := generator.NewGoCodeGenerator()
	searchPaths := []string{projectDir, builderStdDir(t)}
	batch := analyzer.NewBatchAnalyzer(p, searchPaths, projectDir)

	timeOnce := func(label string) time.Duration {
		t.Helper()
		batch.SetPackageFiles(siblings)
		txp := transpiler.NewGalaToGoTranspiler(p, batch, tr, g)
		start := time.Now()
		_, err := txp.Transpile(string(content), target)
		require.NoErrorf(t, err, "%s: Transpile", label)
		return time.Since(start)
	}

	cold := timeOnce("cold")
	t.Logf("cold transpile (1 file + %d siblings, GOMAXPROCS=%d): %s (budget %s)",
		len(siblings), runtime.GOMAXPROCS(0), cold, transpilePerfColdBudget)

	warm := timeOnce("warm")
	t.Logf("warm transpile (1 file + %d siblings, GOMAXPROCS=%d): %s (budget %s)",
		len(siblings), runtime.GOMAXPROCS(0), warm, transpilePerfWarmBudget)

	if cold > transpilePerfColdBudget {
		t.Errorf("cold transpile exceeded budget: %s > %s — likely a regression in parallel-sibling-parse or analyzer-cache paths",
			cold, transpilePerfColdBudget)
	}
	if warm > transpilePerfWarmBudget {
		t.Errorf("warm transpile exceeded budget: %s > %s — likely a regression in parsedFileCache or pkgResultCache hit paths",
			warm, transpilePerfWarmBudget)
	}

	// Warm should be substantially faster than cold (parsedFileCache
	// hits skip the ANTLR re-parse entirely, analyzedPkgs hits skip
	// the import closure walk). If the ratio creeps above 0.7, one
	// of those caches probably stopped being consulted — log a hint
	// rather than fail, since this is a softer signal than the
	// budget assertions.
	if cold > 0 {
		ratio := float64(warm) / float64(cold)
		t.Logf("warm/cold ratio: %.2f (cold=%s warm=%s)", ratio, cold, warm)
		if ratio > 0.7 {
			t.Logf("WARN: warm/cold ratio %.2f is suspiciously high — check that parsedFileCache and analyzedPkgs survive across SetPackageFiles + Analyze pairs",
				ratio)
		}
	}
}

// concurrencyProbeParser wraps a GalaParser and records the high-water
// mark of Parse calls that were inside the parser at the same instant.
//
// This is what lets the test below assert on parallelism without timing
// anything. A serialized implementation peaks at 1 whether the machine
// is idle or oversubscribed, so the signal survives a loaded shared CI
// runner — unlike a wall-clock speedup ratio, which shrinks toward 1.0
// purely because the runner is busy and reports that as a regression.
//
// Two atomic adds per parse are immeasurable next to an ANTLR parse.
type concurrencyProbeParser struct {
	inner    transpiler.GalaParser
	inFlight atomic.Int64
	peak     atomic.Int64
	calls    atomic.Int64
}

var _ transpiler.GalaParser = (*concurrencyProbeParser)(nil)

func (p *concurrencyProbeParser) Parse(input string) (antlr.Tree, map[int]string, error) {
	p.enter()
	defer p.leave()
	return p.inner.Parse(input)
}

func (p *concurrencyProbeParser) ParseLenient(input string) (antlr.Tree, map[int]string, []error) {
	p.enter()
	defer p.leave()
	return p.inner.ParseLenient(input)
}

func (p *concurrencyProbeParser) enter() {
	p.calls.Add(1)
	n := p.inFlight.Add(1)
	for {
		peak := p.peak.Load()
		if n <= peak || p.peak.CompareAndSwap(peak, n) {
			return
		}
	}
}

func (p *concurrencyProbeParser) leave() { p.inFlight.Add(-1) }

// TestTranspile_SiblingParseIsConcurrent proves that
// parseFilesConcurrent really parses siblings in parallel, by watching
// how many parses are in flight simultaneously rather than by timing
// two passes and dividing.
//
// Catches the regressions the previous timing assertion targeted — the
// pool replaced with a for-each parseFileCached, or its sizing logic
// reverting to 1 — plus one it could not see: the pool losing its cap
// and oversubscribing a 4-vCPU runner.
//
// The GOMAXPROCS=1 control pass is what makes the parallel assertion
// mean something. parseFilesConcurrent sizes its pool from GOMAXPROCS
// and is the only place the transpiler spawns goroutines, so at 1 the
// observed peak must be exactly 1. A probe that had quietly stopped
// observing anything would report 1 there too — but so would a pool
// that had reverted to serial, and the control tells the two apart.
func TestTranspile_SiblingParseIsConcurrent(t *testing.T) {
	if testing.Short() {
		t.Skip("perf test skipped in -short mode")
	}
	if runtime.NumCPU() < 2 {
		t.Skip("concurrency test requires NumCPU >= 2")
	}

	projectDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(projectDir, "gala.mod"),
		[]byte("module example.com/perf\n\ngala 0.0.0\n"), 0644))
	files := writeSiblingPackage(t, projectDir, transpilePerfFileCount)
	target := files[0]
	siblings := append([]string(nil), files[1:]...)
	content, err := os.ReadFile(target)
	require.NoError(t, err)

	// One full transpile at the given GOMAXPROCS, reporting the peak
	// number of simultaneous parses and how many parses ran at all.
	// Fresh BatchAnalyzer and wiped on-disk cache per run, so every
	// sibling is genuinely re-parsed instead of served from a cache.
	peakAt := func(maxprocs int) (peak, calls int64) {
		t.Helper()
		prev := runtime.GOMAXPROCS(maxprocs)
		defer runtime.GOMAXPROCS(prev)

		_ = os.RemoveAll(filepath.Join(projectDir, ".gala"))
		p := &concurrencyProbeParser{inner: transpiler.NewAntlrGalaParser()}
		tr := transformer.NewGalaASTTransformer()
		g := generator.NewGoCodeGenerator()
		batch := analyzer.NewBatchAnalyzer(p, []string{projectDir, builderStdDir(t)}, projectDir)
		batch.SetPackageFiles(siblings)
		txp := transpiler.NewGalaToGoTranspiler(p, batch, tr, g)

		_, err := txp.Transpile(string(content), target)
		require.NoError(t, err, "Transpile at GOMAXPROCS=%d", maxprocs)
		return p.peak.Load(), p.calls.Load()
	}

	serialPeak, serialCalls := peakAt(1)
	t.Logf("GOMAXPROCS=1: peak %d simultaneous parses over %d total", serialPeak, serialCalls)
	require.Greater(t, serialCalls, int64(1),
		"expected the transpile to parse the target plus its siblings; with one parse there is nothing to parallelize and the assertions below are vacuous")
	if serialPeak != 1 {
		t.Errorf("GOMAXPROCS=1 produced a peak of %d simultaneous parses, want exactly 1 — the worker pool no longer sizes itself from GOMAXPROCS, so the parallel check below proves nothing",
			serialPeak)
	}

	// Two goroutines being inside Parse at the same instant is a
	// scheduling outcome, not a guarantee. It is overwhelmingly likely
	// for parses this long, but retry before failing: three runs that
	// never once overlap mean the pool is serial in fact, not unlucky.
	maxprocs := runtime.GOMAXPROCS(0)
	const attempts = 3
	var peak, calls int64
	for attempt := 1; attempt <= attempts; attempt++ {
		peak, calls = peakAt(maxprocs)
		t.Logf("GOMAXPROCS=%d attempt %d/%d: peak %d simultaneous parses over %d total",
			maxprocs, attempt, attempts, peak, calls)
		if peak > 1 {
			break
		}
	}

	if peak < 2 {
		t.Errorf("sibling parses never overlapped in %d runs at GOMAXPROCS=%d (peak %d) — parseFilesConcurrent may have been silently serialized; check that analyzer.parseFilesConcurrent still spawns a goroutine pool of size GOMAXPROCS, and that Analyze + analyzePackage still route through it",
			attempts, maxprocs, peak)
	}
	if peak > int64(maxprocs) {
		t.Errorf("peak %d simultaneous parses exceeds GOMAXPROCS=%d — parseFilesConcurrent has stopped capping its worker count and will oversubscribe small CI runners",
			peak, maxprocs)
	}
}

// BenchmarkTranspile_ParallelSibling measures the cost of a single
// transpile pass over a synthetic 8-file package with N-1 siblings.
// Use it for local before/after comparisons:
//
//	bazel test //internal/build:build_test --test_arg=-test.bench=Transpile_ParallelSibling --test_arg=-test.benchtime=5x
//
// or, outside bazel, `go test -bench Transpile_ParallelSibling -benchtime=5x ./internal/build`.
//
// Allocates a fresh BatchAnalyzer per iteration so each measurement
// pays the full cold-start cost (no in-process cache reuse). The
// per-op number is directly comparable to the cold-pass time logged
// by TestTranspile_ParallelSiblingPerf_ColdWarm.
func BenchmarkTranspile_ParallelSibling(b *testing.B) {
	projectDir := b.TempDir()
	require.NoError(b, os.WriteFile(filepath.Join(projectDir, "gala.mod"),
		[]byte("module example.com/perf-bench\n\ngala 0.0.0\n"), 0644))
	files := writeSiblingPackageB(b, projectDir, transpilePerfFileCount)
	target := files[0]
	siblings := append([]string(nil), files[1:]...)
	content, err := os.ReadFile(target)
	require.NoError(b, err)

	stdDir := builderStdDirB(b)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = os.RemoveAll(filepath.Join(projectDir, ".gala"))
		p := transpiler.NewAntlrGalaParser()
		tr := transformer.NewGalaASTTransformer()
		g := generator.NewGoCodeGenerator()
		batch := analyzer.NewBatchAnalyzer(p, []string{projectDir, stdDir}, projectDir)
		batch.SetPackageFiles(siblings)
		txp := transpiler.NewGalaToGoTranspiler(p, batch, tr, g)
		_, err := txp.Transpile(string(content), target)
		require.NoError(b, err)
	}
}

// writeSiblingPackage drops n sibling .gala files into projectDir.
// Each file declares transpilePerfTypesPerFile struct types so the
// per-file ANTLR parse takes long enough for the parallel speedup
// to clear the noise floor.
func writeSiblingPackage(t *testing.T, projectDir string, n int) []string {
	t.Helper()
	files := make([]string, n)
	for i := 0; i < n; i++ {
		path := filepath.Join(projectDir, fmt.Sprintf("file_%02d.gala", i))
		body := siblingFileBody(i, n)
		require.NoError(t, os.WriteFile(path, []byte(body), 0644))
		files[i] = path
	}
	return files
}

// writeSiblingPackageB is the *testing.B variant.
func writeSiblingPackageB(b *testing.B, projectDir string, n int) []string {
	b.Helper()
	files := make([]string, n)
	for i := 0; i < n; i++ {
		path := filepath.Join(projectDir, fmt.Sprintf("file_%02d.gala", i))
		body := siblingFileBody(i, n)
		require.NoError(b, os.WriteFile(path, []byte(body), 0644))
		files[i] = path
	}
	return files
}

// siblingFileBody returns the source for the i-th sibling. Each file
// declares transpilePerfTypesPerFile struct types and a cross-file
// reference to the next sibling's first type — the analyzer has to
// walk the sibling AST set to resolve that reference, exercising
// the parallel-sibling-parse path.
//
// The body uses only constructs that survive the analyzer without
// dragging in the std closure too aggressively (no go_interop, no
// fmt) — the test's purpose is to measure parse + analyze, not
// stdlib loading.
func siblingFileBody(i, n int) string {
	var b strings.Builder
	b.WriteString("package perf\n\n")
	for k := 0; k < transpilePerfTypesPerFile; k++ {
		fmt.Fprintf(&b, "type T%d_%d struct {\n    a int\n    b int\n    c string\n    d string\n}\n\n", i, k)
		fmt.Fprintf(&b, "func make%d_%d() T%d_%d {\n    return T%d_%d{a: %d, b: %d, c: \"x\", d: \"y\"}\n}\n\n", i, k, i, k, i, k, k, k+1)
	}
	other := (i + 1) % n
	fmt.Fprintf(&b, "func cross%d() T%d_0 {\n    return T%d_0{a: 0, b: 0, c: \"\", d: \"\"}\n}\n", i, other, other)
	return b.String()
}

// builderStdDir returns the std/ directory that comes with the gala
// repository so the BatchAnalyzer can resolve the implicit `std`
// import. Reuses the same Bazel-runfile + walk-up logic the existing
// test_helper.go uses for the _test packages — duplicated here
// because the build package's tests live in `package build`, not
// `package build_test`, so they can't import the helper directly.
func builderStdDir(t *testing.T) string {
	t.Helper()
	return findStdDir(t.Fatalf)
}

func builderStdDirB(b *testing.B) string {
	b.Helper()
	return findStdDir(b.Fatalf)
}

func findStdDir(fail func(string, ...any)) string {
	// Under bazel, std sources are exposed via runfiles. Outside
	// bazel (e.g. `go test` from the repo root), walk up to find a
	// directory containing std/option.gala.
	if stdFilePath, err := bazel.Runfile("std/option.gala"); err == nil {
		stdDir := filepath.Dir(stdFilePath)
		return filepath.Dir(stdDir)
	}
	cwd, err := os.Getwd()
	if err != nil {
		fail("getwd: %v", err)
		return ""
	}
	dir := cwd
	for {
		candidate := filepath.Join(dir, "std", "option.gala")
		if _, err := os.Stat(candidate); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	fail("could not locate std/ on disk; tests run from %s", cwd)
	return ""
}
