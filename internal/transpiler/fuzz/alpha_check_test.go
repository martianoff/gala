package fuzz_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bazelbuild/rules_go/go/runfiles"

	"martianoff/gala/internal/formal/coreexport"
	"martianoff/gala/internal/formal/coreexport/analyzerhints"
)

// alpha_check_test.go checks the metamorphic renaming generator against the
// formal alpha-invariance theorem (T2, docs/FORMAL_SEMANTICS.MD, M2): each
// renaming it makes of local binders is one T2 covers. It renders generated
// programs with plain names and with hostile local names (metaConfig.localOnly:
// FuzzAlphaRenaming's generator and naming code, but keeping the top-level
// functions' names, whose renaming T2 does not cover), exports both to Core JSON and
// asks `galaspec alpha` whether they are related. galaspec's answer is
// alphaCheck, proved sound in formal/Gala/AlphaProg.lean (alphaCheck_sound),
// so "related" means the two programs print the same output and end the same
// way in galaspec, whatever they do.
//
// It needs galaspec, built with Lean, which //formal:galaspec provides only
// under --config=formal; without it the two tests skip. The formal CI job runs
// them with the config (.github/workflows/formal.yml):
//
//	bazel test --config=formal //internal/transpiler/fuzz:fuzz_test \
//	    --test_filter='TestGeneratedRenamingsAreCoveredByT2|TestAlphaCheckRejectsCapture'

// alphaSeeds is how many generated programs the test checks.
const alphaSeeds = 60

// specRlocationEnv names //formal:galaspec in the runfiles; specBinEnv names
// another galaspec (a Lake build), as for the differential run.
const (
	specRlocationEnv = "GALA_SPEC_RLOCATION"
	specBinEnv       = "GALA_SPEC_BIN"
)

func galaspecBin(t *testing.T) string {
	t.Helper()
	if bin := os.Getenv(specBinEnv); bin != "" {
		if _, err := os.Stat(bin); err != nil {
			t.Fatalf("%s=%s: %v", specBinEnv, bin, err)
		}
		return bin
	}
	loc := os.Getenv(specRlocationEnv)
	if loc == "" {
		t.Skipf("no galaspec: run with --config=formal, which builds //formal:galaspec and sets %s, "+
			"or set %s (with --test_env)", specRlocationEnv, specBinEnv)
	}
	bin, err := runfiles.Rlocation(loc)
	if err == nil {
		_, err = os.Stat(bin)
	}
	if err != nil {
		t.Fatalf("finding //formal:galaspec (%s) in the runfiles: %v", loc, err)
	}
	return bin
}

// exportCore writes src's Core JSON to dir/name.json. It returns the path and
// the document without source positions, which is what alphaCheck compares.
func exportCore(t *testing.T, dir, name, src string) (string, string) {
	t.Helper()
	p, err := coreexport.Export([]coreexport.Source{{Path: "main.gala", Text: src}},
		coreexport.Options{LiteralType: analyzerhints.LiteralType})
	if err != nil {
		t.Fatalf("exporting %s: %v\n%s", name, err, src)
	}
	doc, err := coreexport.Marshal(p)
	if err != nil {
		t.Fatalf("encoding %s: %v", name, err)
	}
	path := filepath.Join(dir, name+".json")
	if err := os.WriteFile(path, doc, 0o644); err != nil {
		t.Fatal(err)
	}
	var v any
	if err := json.Unmarshal(doc, &v); err != nil {
		t.Fatal(err)
	}
	stripped, err := json.Marshal(dropPos(v))
	if err != nil {
		t.Fatal(err)
	}
	return path, string(stripped)
}

// dropPos removes every "pos" member of a decoded JSON document.
func dropPos(v any) any {
	switch x := v.(type) {
	case map[string]any:
		delete(x, "pos")
		for k, e := range x {
			x[k] = dropPos(e)
		}
	case []any:
		for i, e := range x {
			x[i] = dropPos(e)
		}
	}
	return v
}

// alphaVerdict runs `galaspec alpha a b` and returns its verdict, checking
// that the exit code agrees with it (0 related, 1 unrelated).
func alphaVerdict(t *testing.T, bin, a, b string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, bin, "alpha", a, b)
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	code := cmd.ProcessState.ExitCode()
	v, ok := strings.CutPrefix(strings.TrimSpace(stdout.String()), "galaspec: alpha=")
	switch {
	case ok && v == "related" && code == 0, ok && v == "unrelated" && code == 1:
		return v
	}
	t.Fatalf("galaspec alpha %s %s: %v (exit %d)\nstdout: %s\nstderr: %s", a, b, err, code, stdout.String(), stderr.String())
	return ""
}

func TestGeneratedRenamingsAreCoveredByT2(t *testing.T) {
	bin := galaspecBin(t)
	dir := t.TempDir()
	rng := rand.New(rand.NewSource(7))
	// renamed counts the programs whose Core JSON, positions aside, differs:
	// a binder renamed where alphaCheck sees it, not only inside a construct
	// the exporter reports as unsupported.
	renamed := 0
	for i := range alphaSeeds {
		data := make([]byte, 160)
		rng.Read(data)
		p := generateMeta(data, metaConfig{hostile: true, localOnly: true, traceOdds: 6})
		name := fmt.Sprintf("seed%02d", i)
		a, da := exportCore(t, dir, name+"_plain", p.plain)
		b, db := exportCore(t, dir, name+"_renamed", p.src)
		if da != db {
			renamed++
		}
		if v := alphaVerdict(t, bin, a, b); v != "related" {
			t.Errorf("the generator made a renaming T2 does not cover (galaspec alpha: %s):\n--- plain\n%s\n--- renamed\n%s", v, p.plain, p.src)
		}
	}
	if renamed < alphaSeeds/2 {
		t.Fatalf("only %d of %d generated programs had a binder renamed where alphaCheck sees it: the check would be vacuous", renamed, alphaSeeds)
	}
	t.Logf("%d of %d programs had binders renamed in their Core JSON", renamed, alphaSeeds)
}

// TestAlphaCheckRejectsCapture keeps the check honest: a renaming that makes a
// reference resolve to another binder is not related. Here the outer y is
// renamed to the name of an inner binder that shadows it, so the inner
// reference to y would read the inner value.
func TestAlphaCheckRejectsCapture(t *testing.T) {
	bin := galaspecBin(t)
	dir := t.TempDir()
	const plain = "package main\n\nfunc main() {\n    val y = 2\n    val f = (x int) => x + y\n    Println(f(3))\n}\n"
	const captured = "package main\n\nfunc main() {\n    val x = 2\n    val f = (x int) => x + x\n    Println(f(3))\n}\n"
	const renamed = "package main\n\nfunc main() {\n    val z = 2\n    val f = (x int) => x + z\n    Println(f(3))\n}\n"
	a, _ := exportCore(t, dir, "plain", plain)
	b, _ := exportCore(t, dir, "captured", captured)
	c, _ := exportCore(t, dir, "renamed", renamed)
	if v := alphaVerdict(t, bin, a, b); v != "unrelated" {
		t.Fatalf("galaspec alpha accepted a capturing renaming: %s", v)
	}
	if v := alphaVerdict(t, bin, a, c); v != "related" {
		t.Fatalf("galaspec alpha rejected a capture-avoiding renaming: %s", v)
	}
}
