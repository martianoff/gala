package gala2lean_test

import (
	"maps"
	"os"
	"path"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/bazelbuild/rules_go/go/tools/bazel"
	"github.com/stretchr/testify/require"

	"martianoff/gala/internal/formal/gala2lean"
)

// LeanEnv names a lean executable to run the translations with; under
// --config=formal the test gets the toolchain's (leanRlocationEnv).
const (
	LeanEnv          = "GALA_LEAN_BIN"
	leanRlocationEnv = "GALA_LEAN_RLOCATION"
)

// leanBin is the lean to run translations with, or "" for none.
func leanBin(t *testing.T) string {
	if bin := os.Getenv(LeanEnv); bin != "" {
		return bin
	}
	if loc := os.Getenv(leanRlocationEnv); loc != "" {
		bin, err := gala2lean.LeanFromRunfiles(loc)
		require.NoError(t, err, "finding //formal:lean_runtime (%s) in the runfiles", loc)
		return bin
	}
	return ""
}

// testdata holds gala2lean's own runtime cases, each a program and the .out
// the gala CLI prints for it.
const testdata = "internal/formal/gala2lean/testdata"

// TestFixturesRunInLean translates each galaspec fixture source
// (formal/tests/src) and each case in testdata, runs it with Lean, and
// compares what it prints and how it ends with the expected output and
// outcome (for a fixture, the ones galaspec is held to). A fixture galaspec runs out of fuel on must not finish in Lean
// either; one outside the translated subset is logged.
//
// It needs Lean, so its target is manual: run it with --config=formal, or
// with GALA_LEAN_BIN naming a lean (with --test_env=GALA_LEAN_BIN).
func TestFixturesRunInLean(t *testing.T) {
	bin := leanBin(t)
	if bin == "" {
		t.Fatalf("no lean: run with --config=formal, or set %s to a lean executable (--test_env=%s)", LeanEnv, LeanEnv)
	}
	prelude, err := bazel.Runfile("formal/Gala/Gala2Lean/Prelude.lean")
	require.NoError(t, err)
	runner, err := gala2lean.NewRunner(bin, prelude, t.TempDir())
	require.NoError(t, err)

	files := stagedFiles(t, "formal/tests/", testdata)
	ran := 0
	for _, p := range slices.Sorted(maps.Keys(files)) {
		dir := path.Dir(p)
		if (dir != "formal/tests/src" && dir != testdata) || !strings.HasSuffix(p, ".gala") {
			continue
		}
		name := strings.TrimSuffix(path.Base(p), ".gala")
		// gala2lean's own cases must translate and end ok.
		own := dir == testdata
		wantOutcome, wantOut := "ok", files[testdata+"/"+name+".out"]
		if !own {
			wantOutcome = strings.TrimSpace(files["formal/tests/"+name+".outcome"])
			wantOut = files["formal/tests/"+name+".out"]
		}
		t.Run(name, func(t *testing.T) {
			res, err := gala2lean.Translate(export(t, path.Base(p), files[p]), gala2lean.Options{Namespace: "GalaProgram", Main: true})
			if err != nil {
				require.False(t, own, "%v", err)
				t.Logf("not translated: %v", err)
				return
			}
			ran++
			timeout := 2 * time.Minute
			if wantOutcome == "fuel" {
				timeout = 20 * time.Second
			}
			r, err := runner.Run(res, timeout)
			require.NoError(t, err)
			if wantOutcome == "fuel" {
				require.Contains(t, []gala2lean.RunKind{gala2lean.TimedOut, gala2lean.Diverged}, r.Kind, "%+v", r)
				return
			}
			require.Equal(t, gala2lean.RanOK, r.Kind, "%+v", r)
			require.Equal(t, wantOutcome, r.Outcome)
			require.Equal(t, wantOut, r.Stdout)
		})
	}
	require.Greater(t, ran, 10, "too few fixtures translated")
}
