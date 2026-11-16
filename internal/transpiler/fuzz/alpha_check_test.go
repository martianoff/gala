package fuzz_test

import (
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bazelbuild/rules_go/go/runfiles"

	"martianoff/gala/internal/formal/coreexport"
	"martianoff/gala/internal/formal/coreexport/analyzerhints"
)

// alpha_check_test.go checks the metamorphic renaming generator against the
// formal alpha-invariance theorem (T2, docs/FORMAL_SEMANTICS.MD, M2): each
// renaming it makes of local binders is one T2 covers. It renders generated
// programs with plain names and with hostile local names (metaConfig.localOnly:
// the same code path as FuzzAlphaRenaming, keeping the top-level functions'
// names, whose renaming T2 does not cover), exports both to Core JSON and
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

// specRlocationEnv names //formal:galaspec in the runfiles.
const specRlocationEnv = "GALA_SPEC_RLOCATION"

func galaspecBin(t *testing.T) string {
	t.Helper()
	loc := os.Getenv(specRlocationEnv)
	if loc == "" {
		t.Skipf("no galaspec: run with --config=formal, which builds //formal:galaspec and sets %s", specRlocationEnv)
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

// exportCore writes src's Core JSON to dir/name.json and returns its path.
func exportCore(t *testing.T, dir, name, src string) string {
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
	return path
}

// alphaVerdict runs `galaspec alpha a b` and returns its verdict line's value.
func alphaVerdict(t *testing.T, bin, a, b string) string {
	t.Helper()
	out, err := exec.Command(bin, "alpha", a, b).CombinedOutput()
	text := strings.TrimSpace(string(out))
	if v, ok := strings.CutPrefix(text, "galaspec: alpha="); ok {
		return v
	}
	t.Fatalf("galaspec alpha: %v\n%s", err, text)
	return ""
}

func TestGeneratedRenamingsAreCoveredByT2(t *testing.T) {
	bin := galaspecBin(t)
	dir := t.TempDir()
	rng := rand.New(rand.NewSource(7))
	renamed := 0
	for i := range alphaSeeds {
		data := make([]byte, 160)
		rng.Read(data)
		p := generateMeta(data, metaConfig{hostile: true, localOnly: true, traceOdds: 6})
		if p.src != p.plain {
			renamed++
		}
		name := "seed" + string(rune('a'+i%26)) + string(rune('a'+i/26))
		a := exportCore(t, dir, name+"_plain", p.plain)
		b := exportCore(t, dir, name+"_renamed", p.src)
		if v := alphaVerdict(t, bin, a, b); v != "related" {
			t.Errorf("the generator made a renaming T2 does not cover (galaspec alpha: %s):\n--- plain\n%s\n--- renamed\n%s", v, p.plain, p.src)
		}
	}
	if renamed < alphaSeeds/2 {
		t.Fatalf("only %d of %d generated programs had a binder renamed: the check would be vacuous", renamed, alphaSeeds)
	}
}

// TestAlphaCheckRejectsCapture keeps the check honest: a renaming that makes a
// reference resolve to another binder is not related.
func TestAlphaCheckRejectsCapture(t *testing.T) {
	bin := galaspecBin(t)
	dir := t.TempDir()
	const plain = "package main\n\nfunc main() {\n    val x = 1\n    val y = 2\n    Println(x + y)\n}\n"
	const captured = "package main\n\nfunc main() {\n    val x = 1\n    val x = 2\n    Println(x + x)\n}\n"
	a := exportCore(t, dir, "plain", plain)
	b := exportCore(t, dir, "captured", captured)
	if v := alphaVerdict(t, bin, a, b); v != "unrelated" {
		t.Fatalf("galaspec alpha accepted a capturing renaming: %s", v)
	}
	if v := alphaVerdict(t, bin, a, a); v != "related" {
		t.Fatalf("galaspec alpha rejected a program against itself: %s", v)
	}
}
