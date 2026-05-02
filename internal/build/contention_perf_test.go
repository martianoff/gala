package build

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/analyzer"
	"martianoff/gala/internal/transpiler/generator"
	"martianoff/gala/internal/transpiler/transformer"
)

// TestTranspile_BazelLikeContention simulates the bazel CI scenario:
// multiple GalaTranspile actions running in parallel on a 4-vCPU
// runner, each running the parallel-sibling-parse pool internally.
// Goal: measure whether the inner pool's contention with the outer
// (Bazel-orchestrated) parallelism actually wins or loses.
//
// Spawns N=4 parallel "actions", each invoking BatchAnalyzer.Transpile
// on a single sibling target with N-1 packageFiles. Times the total
// wall and reports it. Run with -cpuprofile to get a flame graph of
// where time goes:
//
//   bazel test //internal/build:build_test --test_output=all \
//     --test_arg=-test.run=TestTranspile_BazelLikeContention \
//     --test_arg=-test.cpuprofile=/tmp/contention.pprof \
//     --test_arg=-test.v --cache_test_results=no
//
// then `go tool pprof -top /tmp/contention.pprof`.
//
// The test logs but does not assert — it's diagnostic, not a
// regression guard. Use it to compare serial-vs-parallel behavior
// under the same workload shape Bazel produces.
func TestTranspile_BazelLikeContention(t *testing.T) {
	if testing.Short() {
		t.Skip("contention diagnostic skipped in -short mode")
	}

	// Pin GOMAXPROCS=4 to mirror ubuntu-latest CI runners. Without
	// the pin, the test runs at the developer box's NumCPU (often
	// 8-32) which is not the contention regime we care about.
	prev := runtime.GOMAXPROCS(4)
	defer runtime.GOMAXPROCS(prev)

	projectDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(projectDir, "gala.mod"),
		[]byte("module example.com/contend\n\ngala 0.0.0\n"), 0644))

	const filesPerAction = 8
	files := writeContentionPackage(t, projectDir, filesPerAction)
	siblings := append([]string(nil), files[1:]...)
	target := files[0]
	content, err := os.ReadFile(target)
	require.NoError(t, err)

	stdDir := findStdDir(t.Fatalf)

	runOnce := func(parallelActions int, label string) time.Duration {
		t.Helper()
		// Wipe cache to make every action genuinely cold at the
		// disk-cache layer. Otherwise the second iteration would
		// pull populated cache and the comparison would conflate
		// parallel-parse with warm disk cache.
		_ = os.RemoveAll(filepath.Join(projectDir, ".gala"))
		runtime.GC()

		var wg sync.WaitGroup
		start := time.Now()
		for i := 0; i < parallelActions; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				p := transpiler.NewAntlrGalaParser()
				tr := transformer.NewGalaASTTransformer()
				g := generator.NewGoCodeGenerator()
				batch := analyzer.NewBatchAnalyzer(p, []string{projectDir, stdDir}, projectDir)
				batch.SetPackageFiles(siblings)
				txp := transpiler.NewGalaToGoTranspiler(p, batch, tr, g)
				_, err := txp.Transpile(string(content), target)
				if err != nil {
					t.Errorf("%s action %d: %v", label, i, err)
				}
			}()
		}
		wg.Wait()
		return time.Since(start)
	}

	// Single-action baseline: 1 transpile at a time, parallel-parse
	// pool sized to GOMAXPROCS=4 → all 4 cores available to the pool.
	one := runOnce(1, "1-action")
	t.Logf("1 action  / GOMAXPROCS=4: %s", one)

	// Bazel-like: 4 transpiles in parallel, each spawning its own
	// 4-goroutine pool → 16 ANTLR goroutines fighting for 4 vCPUs.
	// This is the contention scenario.
	four := runOnce(4, "4-actions")
	t.Logf("4 actions / GOMAXPROCS=4: %s (per-action avg %s)", four, four/4)

	// Per-action time should not balloon under 4-action contention.
	// If `four/4` is close to or higher than `one`, the inner pool
	// is hurting more than helping. A healthy ratio is < 2x (some
	// contention is unavoidable; pathological cases land at 4x+).
	ratio := float64(four/4) / float64(one)
	t.Logf("per-action ratio (4-action / 1-action): %.2fx", ratio)
	t.Logf("If ratio >> 1.0 the inner parallel-parse pool is oversubscribing the runner")
}

func writeContentionPackage(t *testing.T, projectDir string, n int) []string {
	t.Helper()
	files := make([]string, n)
	for i := 0; i < n; i++ {
		path := filepath.Join(projectDir, fmt.Sprintf("file_%02d.gala", i))
		body := siblingFileBody(i, n) // reuses the helper from transpile_perf_test.go
		require.NoError(t, os.WriteFile(path, []byte(body), 0644))
		files[i] = path
	}
	return files
}
