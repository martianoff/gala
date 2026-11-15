package inoutfix_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bazelbuild/rules_go/go/tools/bazel"
	"github.com/stretchr/testify/require"

	"martianoff/gala/internal/inoutfix"
)

// repoRoot finds the repository: in the runfiles, or above the working
// directory.
func repoRoot(t *testing.T) string {
	t.Helper()
	if p, err := bazel.Runfile("std/option.gala"); err == nil {
		return filepath.Dir(filepath.Dir(p))
	}
	dir, err := os.Getwd()
	require.NoError(t, err)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		require.NotEqual(t, parent, dir, "repository not found")
		dir = parent
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	require.NoError(t, err)
	return strings.ReplaceAll(string(b), "\r\n", "\n")
}

// TestGolden rewrites examples/inout_fix_before.gala and compares the
// result with examples/inout_fix_after.gala, byte for byte. Both examples
// run as gala_exec_tests with the same expected output, so the rewrite
// compiles and behaves the same.
func TestGolden(t *testing.T) {
	root := repoRoot(t)
	before := read(t, filepath.Join(root, "examples", "inout_fix_before.gala"))
	want := read(t, filepath.Join(root, "examples", "inout_fix_after.gala"))

	dir := t.TempDir()
	path := filepath.Join(dir, "main.gala")
	require.NoError(t, os.WriteFile(path, []byte(before), 0o644))
	cfg := inoutfix.Config{SearchPaths: []string{root}}

	res, err := inoutfix.Files([]string{path}, cfg)
	require.NoError(t, err)
	require.NoError(t, res.Write())
	require.Equal(t, want, read(t, path))

	// What it keeps, and why.
	kept := map[string]string{}
	for _, f := range res.Plan.Funcs {
		for _, b := range f.Bindings {
			if !b.Converted {
				kept[f.Name] = b.Blocked
			}
		}
	}
	require.Len(t, kept, 4)
	require.Contains(t, kept["remember"], "escapes: stored")
	require.Contains(t, kept["double"], "used as a function value")
	require.Contains(t, kept["applyTo"], "unknown-callee")
	require.Contains(t, kept["addHundred"], "GALA-E0111")
	require.Contains(t, res.Excluded, "addHundred")

	// Idempotent: a second run changes nothing.
	again, err := inoutfix.Files([]string{path}, cfg)
	require.NoError(t, err)
	require.Empty(t, again.Changes)
	require.Empty(t, again.Diff(dir))
}

// TestDiffAndPublic checks the dry-run diff, and that a public function of
// a library keeps its Ptr unless Public is set.
func TestDiffAndPublic(t *testing.T) {
	root := repoRoot(t)
	dir := filepath.Join(t.TempDir(), "lib")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	src := `package lib

struct Acc(var total int)

func add(a Ptr[Acc], n int) {
    a.total = a.total + n
}

func Fill(a Ptr[Acc]) {
    add(a, 1)
}
`
	path := filepath.Join(dir, "lib.gala")
	require.NoError(t, os.WriteFile(path, []byte(src), 0o644))

	results, err := inoutfix.Package(dir, inoutfix.Config{SearchPaths: []string{root}})
	require.Len(t, results, 1)
	res := results[0]
	require.NoError(t, err)
	require.Equal(t, "--- a/lib.gala\n+++ b/lib.gala\n@@ -5 +5 @@\n-func add(a Ptr[Acc], n int) {\n+func add(inout a Acc, n int) {\n",
		res.Diff(dir))
	require.Equal(t, src, read(t, path), "Package writes nothing")
	sigs := res.Signatures(false)
	require.Len(t, sigs, 1)
	require.False(t, sigs[0].Func.Public)

	pubs, err := inoutfix.Package(dir, inoutfix.Config{SearchPaths: []string{root}, Public: true})
	pub := pubs[0]
	require.NoError(t, err)
	sigs = pub.Signatures(true)
	require.Len(t, sigs, 1)
	require.Equal(t, "func Fill(a Ptr[Acc])", sigs[0].Before)
	require.Equal(t, "func Fill(inout a Acc)", sigs[0].After)
	require.Contains(t, pub.Diff(dir), "+    add(&a, 1)")
}

// TestPackageOfPrograms: a directory of standalone programs, as examples/
// is, is one package per file.
func TestPackageOfPrograms(t *testing.T) {
	root := repoRoot(t)
	dir := t.TempDir()
	src := "package main\n\nstruct C(var n int)\n\nfunc inc(c Ptr[C]) {\n    c.n = c.n + 1\n}\n\nfunc main() {\n    var c = C(0)\n    inc(&c)\n    Println(c.n)\n}\n"
	for _, name := range []string{"a.gala", "b.gala"} {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644))
	}
	results, err := inoutfix.Package(dir, inoutfix.Config{SearchPaths: []string{root}})
	require.NoError(t, err)
	require.Len(t, results, 2)
	for _, res := range results {
		require.Len(t, res.Changes, 1)
	}
}
