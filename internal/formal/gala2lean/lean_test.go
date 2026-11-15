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

	"martianoff/gala/internal/formal/coreexport"
	"martianoff/gala/internal/formal/coreexport/analyzerhints"
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
		// gala2lean's own cases must translate, and end ok unless a .outcome
		// says how Go's run of them ends (a panic class, `panic:other`).
		own := dir == testdata
		wantOutcome, wantOut := "ok", files[testdata+"/"+name+".out"]
		if o, ok := files[testdata+"/"+name+".outcome"]; own && ok {
			wantOutcome = strings.TrimSpace(o)
		}
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

	// gala2lean's multi-package cases: a directory with a gala.mod, its
	// main package's files, the packages of the module below it, and the
	// main.out the gala CLI prints for it.
	for _, mod := range slices.Sorted(maps.Keys(files)) {
		if path.Base(mod) != "gala.mod" || path.Dir(path.Dir(mod)) != testdata {
			continue
		}
		dir := path.Dir(mod)
		t.Run(path.Base(dir), func(t *testing.T) {
			p, deps := exportModule(t, files, dir)
			res, err := gala2lean.Translate(p, gala2lean.Options{Namespace: "GalaProgram", Main: true, Deps: deps})
			require.NoError(t, err)
			r, err := runner.Run(res, 2*time.Minute)
			require.NoError(t, err)
			require.Equal(t, gala2lean.RanOK, r.Kind, "%+v", r)
			require.Equal(t, "ok", r.Outcome)
			require.Equal(t, files[dir+"/main.out"], r.Stdout)
		})
	}
}

// exportModule exports the program of the module in staged directory dir:
// its main package (the .gala files directly in dir) and the packages of the
// module it imports, each a directory below dir, files in path order.
func exportModule(t *testing.T, files map[string]string, dir string) (*coreexport.Program, []gala2lean.Package) {
	t.Helper()
	pkg := func(d string) *coreexport.Program {
		var srcs []coreexport.Source
		for _, f := range slices.Sorted(maps.Keys(files)) {
			if path.Dir(f) == d && strings.HasSuffix(f, ".gala") && !strings.HasSuffix(f, "_test.gala") {
				srcs = append(srcs, coreexport.Source{Path: strings.TrimPrefix(f, dir+"/"), Text: files[f]})
			}
		}
		require.NotEmpty(t, srcs, "no .gala files in %s", d)
		p, err := coreexport.Export(srcs, coreexport.Options{LiteralType: analyzerhints.LiteralType})
		require.NoError(t, err, d)
		return p
	}
	modPath := gala2lean.ModulePath(files[dir+"/gala.mod"])
	require.NotEmpty(t, modPath)
	p := pkg(dir)
	deps, err := gala2lean.ModuleDeps(p, modPath, func(imp string) (*coreexport.Program, error) {
		return pkg(dir + "/" + strings.TrimPrefix(imp, modPath+"/")), nil
	})
	require.NoError(t, err)
	return p, deps
}
