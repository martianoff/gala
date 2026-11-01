package analyzer_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"martianoff/gala/galaerr"
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
		tree, _, err := p.ParseFile(string(content)).Strict()
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
		if _, err := transpiler.Analyze(batch, tree, nil, fp); err != nil {
			t.Fatalf("analyze %s: unexpected error (a cross-file method may use its own file's imports): %v", fp, err)
		}
	}
}

// analyzeEachFile writes files (name -> source) as the package repro in a
// fresh module and analyzes each of them in turn with the others as its
// package files, as a build does. It returns each file's error, or nil.
func analyzeEachFile(t *testing.T, files map[string]string) map[string]error {
	t.Helper()
	tmp := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp, "gala.mod"), []byte("module example.com/repro\n\ngala dev\n"), 0644); err != nil {
		t.Fatal(err)
	}
	var paths []string
	for name, src := range files {
		p := filepath.Join(tmp, name)
		if err := os.WriteFile(p, []byte(src), 0644); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, p)
	}
	p := transpiler.NewAntlrGalaParser()
	batch := analyzer.NewBatchAnalyzer(p, append([]string{tmp}, getStdSearchPath()...), tmp)
	errs := make(map[string]error)
	for _, fp := range paths {
		tree, _, err := p.ParseFile(files[filepath.Base(fp)]).Strict()
		if err != nil {
			t.Fatalf("parse %s: %v", fp, err)
		}
		var siblings []string
		for _, other := range paths {
			if other != fp {
				siblings = append(siblings, other)
			}
		}
		batch.SetPackageFiles(siblings)
		_, errs[filepath.Base(fp)] = transpiler.Analyze(batch, tree, nil, fp)
	}
	return errs
}

// errorCode is the code of err when it is a coded semantic error, "" when it
// is not, and "none" when err is nil.
func errorCode(err error) galaerr.ErrorCode {
	if err == nil {
		return "none"
	}
	var se *galaerr.SemanticError
	if errors.As(err, &se) {
		return se.Code
	}
	return ""
}

// TestCrossFileMethodImport_Unimported: a method declared in another file
// than its receiver type is held to GALA-E0025 in its own file's analysis. Its
// signature names Array, which neither its file nor the type's file imports;
// a third file's dot import is no allowance.
func TestCrossFileMethodImport_Unimported(t *testing.T) {
	errs := analyzeEachFile(t, map[string]string{
		"box.gala":   "package repro\n\nstruct Box(name string)\n",
		"items.gala": "package repro\n\nfunc (b Box) Items(xs Array[int]) Array[int] = xs\n",
		"other.gala": "package repro\n\nimport . \"martianoff/gala/collection_immutable\"\n\nstruct Holder(xs Array[int])\n",
	})
	if got := errorCode(errs["items.gala"]); got != galaerr.CodeUnresolvedCrossPackageSymbol {
		t.Errorf("items.gala: got %q (%v), want %s", got, errs["items.gala"], galaerr.CodeUnresolvedCrossPackageSymbol)
	}
	for _, name := range []string{"box.gala", "other.gala"} {
		if errs[name] != nil {
			t.Errorf("%s: unexpected error: %v", name, errs[name])
		}
	}
}

// TestCrossFileMethodImport_ReceiverFileImport: a method's signature may name
// a type its receiver type's file imports, though its own file does not.
func TestCrossFileMethodImport_ReceiverFileImport(t *testing.T) {
	errs := analyzeEachFile(t, map[string]string{
		"box.gala":   "package repro\n\nimport . \"martianoff/gala/collection_immutable\"\n\nstruct Box(items Array[int])\n",
		"items.gala": "package repro\n\nfunc (b Box) Items() Array[int] = b.items\n",
	})
	for name, err := range errs {
		if err != nil {
			t.Errorf("%s: unexpected error (a method may use its receiver file's imports): %v", name, err)
		}
	}
}

// TestCrossFileFieldNamedLikeMethod: a method declared in another file than
// its struct, named like one of the struct's fields, is GALA-E0081 at the
// method, which Go would otherwise reject.
func TestCrossFileFieldNamedLikeMethod(t *testing.T) {
	errs := analyzeEachFile(t, map[string]string{
		"box.gala":  "package repro\n\nstruct Box(Name string)\n",
		"name.gala": "package repro\n\nfunc (b Box) Name() string = \"box\"\n",
	})
	err := errs["name.gala"]
	if got := errorCode(err); got != galaerr.CodeFieldNamedLikeMethod {
		t.Fatalf("name.gala: got %q (%v), want %s", got, err, galaerr.CodeFieldNamedLikeMethod)
	}
	var se *galaerr.SemanticError
	if errors.As(err, &se) && (se.Line != 3 || se.Column != 13) {
		t.Errorf("reported at %d:%d, want the method's name at 3:13", se.Line, se.Column)
	}
}
