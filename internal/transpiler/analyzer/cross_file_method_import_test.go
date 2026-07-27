package analyzer_test

import (
	"os"
	"path/filepath"
	"testing"

	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/analyzer"
)

// TestCrossFileMethodImport_MethodFileImport verifies that a method declared in
// a DIFFERENT file from its receiver type may reference a type imported ONLY in
// the method's own file, without tripping the GALA-E0025 explicit-import check.
// The receiver type's file need not import that package.
//
// Repro: type Box lives in box.gala (which does not import time_utils); a method
// Span on Box lives in dur.gala (which imports time_utils and returns Duration).
// Before the fix, analyzing box.gala validated Box's method set against box.gala's
// imports and reported "undefined: Duration — 'time_utils' is not imported in this
// file", even though the method that uses Duration is written in dur.gala, which
// does import it. The check now accepts the union of the method's-file and the
// receiver-type's-file imports.
func TestCrossFileMethodImport_MethodFileImport(t *testing.T) {
	tmp := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp, "gala.mod"), []byte("module example.com/repro\n\ngala dev\n"), 0644); err != nil {
		t.Fatal(err)
	}
	boxFile := filepath.Join(tmp, "box.gala")
	if err := os.WriteFile(boxFile, []byte("package repro\n\nstruct Box(name string)\n"), 0644); err != nil {
		t.Fatal(err)
	}
	durFile := filepath.Join(tmp, "dur.gala")
	durSrc := "package repro\n\nimport . \"martianoff/gala/time_utils\"\n\nfunc (b Box) Span() Duration = Seconds(1)\n"
	if err := os.WriteFile(durFile, []byte(durSrc), 0644); err != nil {
		t.Fatal(err)
	}

	p := transpiler.NewAntlrGalaParser()
	searchPaths := append([]string{tmp}, getStdSearchPath()...)
	batch := analyzer.NewBatchAnalyzer(p, searchPaths, tmp)

	files := []string{boxFile, durFile}
	for _, fp := range files {
		content, err := os.ReadFile(fp)
		if err != nil {
			t.Fatal(err)
		}
		tree, err := p.Parse(string(content))
		if err != nil {
			t.Fatalf("parse %s: %v", fp, err)
		}
		var siblings []string
		for _, other := range files {
			if other != fp {
				siblings = append(siblings, other)
			}
		}
		batch.SetPackageFiles(siblings)
		if _, err := batch.Analyze(tree, fp); err != nil {
			t.Fatalf("analyze %s: unexpected error (a cross-file method may use its own file's imports): %v", fp, err)
		}
	}
}
