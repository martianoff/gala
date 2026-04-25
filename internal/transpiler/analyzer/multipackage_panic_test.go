package analyzer_test

import (
	"os"
	"path/filepath"
	"testing"

	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/analyzer"
)

// TestMultiPackageBatch_NoPanic reproduces the FIX-001 scenario where a
// BatchAnalyzer is reused across files that belong to different Go-level
// packages (e.g., root.gala in `package repro` and state/state.gala in
// `package state`). Previously this triggered an ANTLR nil-pointer
// dereference when sibling discovery mixed trees from different packages;
// we expect a clean error or successful analysis instead.
func TestMultiPackageBatch_NoPanic(t *testing.T) {
	tmp := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp, "gala.mod"), []byte("module example.com/repro\n\ngala dev\n"), 0644); err != nil {
		t.Fatal(err)
	}
	rootFile := filepath.Join(tmp, "root.gala")
	if err := os.WriteFile(rootFile, []byte("package repro\n\nfunc RootFn() string = \"hi\"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	stateDir := filepath.Join(tmp, "state")
	if err := os.MkdirAll(stateDir, 0755); err != nil {
		t.Fatal(err)
	}
	stateFile := filepath.Join(stateDir, "state.gala")
	if err := os.WriteFile(stateFile, []byte("package state\n\nfunc StateFn() string = \"world\"\n"), 0644); err != nil {
		t.Fatal(err)
	}

	p := transpiler.NewAntlrGalaParser()
	searchPaths := append([]string{tmp}, getStdSearchPath()...)
	batch := analyzer.NewBatchAnalyzer(p, searchPaths, tmp)

	files := []string{rootFile, stateFile}
	for _, fp := range files {
		content, err := os.ReadFile(fp)
		if err != nil {
			t.Fatal(err)
		}
		tree, err := p.Parse(string(content))
		if err != nil {
			t.Fatalf("parse %s: %v", fp, err)
		}
		// Simulate the builder: pass ALL files as siblings regardless of package,
		// mimicking the old (pre-FIX-007) code path.
		var siblings []string
		for _, other := range files {
			if other != fp {
				siblings = append(siblings, other)
			}
		}
		batch.SetPackageFiles(siblings)

		// The call must either succeed or return an error — never panic.
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("analyzer panicked on %s: %v", fp, r)
			}
		}()
		if _, err := batch.Analyze(tree, fp); err != nil {
			// A semantic error (e.g., package name mismatch) is acceptable —
			// we only care that no panic escapes.
			t.Logf("analyze %s returned error (expected for cross-package siblings): %v", fp, err)
		}
	}
}
