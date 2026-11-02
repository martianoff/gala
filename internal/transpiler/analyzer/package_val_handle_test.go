package analyzer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"martianoff/gala/internal/parser"
	"martianoff/gala/internal/transpiler"
)

// genericValSrc declares a package-level val whose initializer is a generic
// call, a shape only the transformer's inference types
// (inferPackageValTypes).
const genericValSrc = "package pv\n\nstruct Box[T any](V T)\n\nfunc Wrap[T any](v T) Box[T] = Box(v)\n\nval W = Wrap(\"a\")\n"

// writeGalaFile writes src to name in dir and returns its path.
func writeGalaFile(t *testing.T, dir, name, src string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// valType is the type the analysis recorded for the package-level binding
// name, "" when it has none.
func valType(t *testing.T, rich *transpiler.RichAST, name string) string {
	t.Helper()
	pv := rich.PackageVals[name]
	if pv == nil {
		t.Fatalf("PackageVals missing %s", name)
	}
	if pv.Type == nil || pv.Type.IsNil() {
		return ""
	}
	return pv.Type.String()
}

// TestAnalyzedFileWithoutHandleTypesItsVals: the analyzed file itself, given
// with no native-tree handle (as an imported package's file is when the
// parsed-file cache does not pair it with its tree), takes the handle the
// cache holds for its tree's parse, so its own generic-call vals are typed.
func TestAnalyzedFileWithoutHandleTypesItsVals(t *testing.T) {
	path := writeGalaFile(t, t.TempDir(), "pv.gala", genericValSrc)
	a := NewGalaAnalyzer(parser.NewAntlrGalaParser(), nil).(*galaAnalyzer)
	tree, _, err := a.parseFileCached(path)
	if err != nil || tree == nil {
		t.Fatalf("parseFileCached: %v", err)
	}
	rich, err := a.analyze(tree, nil, nil, path)
	if err != nil {
		t.Fatalf("analyze: %v", err)
	}
	if got := valType(t, rich, "W"); got != "pv.Box[string]" {
		t.Errorf("W: type = %q, want pv.Box[string]", got)
	}
}

// TestStaleSiblingTreeTakesNoNewerTypes: a sibling edited in place after its
// tree was cached (the directory's file count unchanged, so the sibling-tree
// cache still serves the old tree) has no handle for that tree once the
// parsed-file cache holds the new parse. Its vals, collected from the old
// tree, must not take the types of the new file's bindings of the same name.
func TestStaleSiblingTreeTakesNoNewerTypes(t *testing.T) {
	dir := t.TempDir()
	mainPath := writeGalaFile(t, dir, "a.gala", "package pv\n\nfunc Use() string = W.V\n")
	sibPath := writeGalaFile(t, dir, "b.gala", genericValSrc)
	p := parser.NewAntlrGalaParser()
	a := NewGalaAnalyzer(p, nil).(*galaAnalyzer)
	analyzeMain := func() *transpiler.RichAST {
		t.Helper()
		body, err := os.ReadFile(mainPath)
		if err != nil {
			t.Fatal(err)
		}
		rich, err := a.AnalyzeParsed(p.ParseFile(string(body)), mainPath)
		if err != nil {
			t.Fatalf("analyze: %v", err)
		}
		return rich
	}
	if got := valType(t, analyzeMain(), "W"); got != "pv.Box[string]" {
		t.Fatalf("W before the edit: type = %q, want pv.Box[string]", got)
	}

	writeGalaFile(t, dir, "b.gala", strings.Replace(genericValSrc, `Wrap("a")`, "Wrap(12345)", 1))
	if tree, _, err := a.parseFileCached(sibPath); err != nil || tree == nil {
		t.Fatalf("parseFileCached: %v", err)
	}
	if got := valType(t, analyzeMain(), "W"); got == "pv.Box[int]" {
		t.Errorf("W, collected from the sibling's old tree, took the new file's type %q", got)
	}
}
