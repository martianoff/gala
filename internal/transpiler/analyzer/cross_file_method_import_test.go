package analyzer_test

import (
	"errors"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"martianoff/gala/galaerr"
	galaparser "martianoff/gala/internal/parser"
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
	analyzeEachFile(t, map[string]string{
		"box.gala": "package repro\n\nstruct Box(name string)\n",
		"dur.gala": "package repro\n\nimport . \"martianoff/gala/time_utils\"\n\nfunc (b Box) Span() Duration = Seconds(1)\n",
	}, func(t *testing.T, errs map[string]error) {
		requireNoErrors(t, errs, "a cross-file method may use its own file's imports")
	})
}

// analyzeEachFile writes files (name -> source) as the package repro in a
// fresh module and analyzes each of them in turn, in name order, with the
// others as its package files, as a build does. It does so once per twin of
// the collection, as a subtest, and gives check each file's error, or nil.
// The analyzed file is read on its native tree when its parse comes with
// one, and on ANTLR's tree when the analysis gets no native handle.
func analyzeEachFile(t *testing.T, files map[string]string, check func(t *testing.T, errs map[string]error)) {
	t.Helper()
	for _, twin := range []string{"antlr", "native"} {
		t.Run(twin, func(t *testing.T) {
			galaparser.RequestEngine(string(galaparser.EngineGalop))
			t.Cleanup(func() { galaparser.RequestEngine("") })
			tmp := t.TempDir()
			if err := os.WriteFile(filepath.Join(tmp, "gala.mod"), []byte("module example.com/repro\n\ngala dev\n"), 0644); err != nil {
				t.Fatal(err)
			}
			paths := writePackage(t, tmp, files)
			p := transpiler.NewAntlrGalaParser()
			batch := analyzer.NewBatchAnalyzer(p, append([]string{tmp}, getStdSearchPath()...), tmp)
			errs := make(map[string]error, len(paths))
			for _, name := range slices.Sorted(maps.Keys(paths)) {
				parsed := p.ParseFile(files[name])
				if err := parsed.Err(); err != nil {
					t.Fatalf("parse %s: %v", name, err)
				}
				batch.SetPackageFiles(siblingsOf(paths, name))
				if twin == "native" {
					_, errs[name] = batch.AnalyzeParsed(parsed, paths[name])
				} else {
					_, errs[name] = transpiler.Analyze(batch, parsed.Tree, parsed.Docs, paths[name])
				}
			}
			check(t, errs)
		})
	}
}

// requireNoErrors fails t for each file of errs whose analysis failed; why
// says why none should.
func requireNoErrors(t *testing.T, errs map[string]error, why string) {
	t.Helper()
	for _, name := range slices.Sorted(maps.Keys(errs)) {
		if errs[name] != nil {
			t.Errorf("%s: unexpected error (%s): %v", name, why, errs[name])
		}
	}
}

// requireCode fails t unless err, the analysis of file, is a semantic error
// coded code, and returns it.
func requireCode(t *testing.T, file string, err error, code galaerr.ErrorCode) *galaerr.SemanticError {
	t.Helper()
	var se *galaerr.SemanticError
	if !errors.As(err, &se) || se.Code != code {
		t.Fatalf("%s: got %v, want %s", file, err, code)
	}
	return se
}

// TestCrossFileMethodImport_Unimported: a method declared in another file
// than its receiver type is held to GALA-E0025 in its own file's analysis. Its
// signature names Array, which neither its file nor the type's file imports;
// a third file's dot import is no allowance.
func TestCrossFileMethodImport_Unimported(t *testing.T) {
	analyzeEachFile(t, map[string]string{
		"box.gala":   "package repro\n\nstruct Box(name string)\n",
		"items.gala": "package repro\n\nfunc (b Box) Items(xs Array[int]) Array[int] = xs\n",
		"other.gala": "package repro\n\nimport . \"martianoff/gala/collection_immutable\"\n\nstruct Holder(xs Array[int])\n",
	}, func(t *testing.T, errs map[string]error) {
		requireCode(t, "items.gala", errs["items.gala"], galaerr.CodeUnresolvedCrossPackageSymbol)
		delete(errs, "items.gala")
		requireNoErrors(t, errs, "only the method's file names Array without importing it")
	})
}

// TestCrossFileMethodImport_ReceiverFileImport: a method's signature may name
// a type its receiver type's file imports, though its own file does not.
func TestCrossFileMethodImport_ReceiverFileImport(t *testing.T) {
	analyzeEachFile(t, map[string]string{
		"box.gala":   "package repro\n\nimport . \"martianoff/gala/collection_immutable\"\n\nstruct Box(items Array[int])\n",
		"items.gala": "package repro\n\nfunc (b Box) Items() Array[int] = b.items\n",
	}, func(t *testing.T, errs map[string]error) {
		requireNoErrors(t, errs, "a method may use its receiver file's imports")
	})
}

// TestCrossFileFieldNamedLikeMethod: a method declared in another file than
// its struct, named like one of the struct's fields, is GALA-E0081 at the
// method, which Go would otherwise reject.
func TestCrossFileFieldNamedLikeMethod(t *testing.T) {
	analyzeEachFile(t, map[string]string{
		"box.gala":  "package repro\n\nstruct Box(Name string)\n",
		"name.gala": "package repro\n\nfunc (b Box) Name() string = \"box\"\n",
	}, func(t *testing.T, errs map[string]error) {
		if se := requireCode(t, "name.gala", errs["name.gala"], galaerr.CodeFieldNamedLikeMethod); se.Line != 3 || se.Column != 13 {
			t.Errorf("reported at %d:%d, want the method's name at 3:13", se.Line, se.Column)
		}
		delete(errs, "name.gala")
		requireNoErrors(t, errs, "the clash is reported at the method")
	})
}
