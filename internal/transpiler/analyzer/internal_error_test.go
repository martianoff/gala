package analyzer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	galaparser "martianoff/gala/internal/parser"
	"martianoff/gala/internal/transpiler"
)

// convertFailMark marks a source whose parse convertFails leaves without a
// native tree, as a clean parse galop's converter failed on.
const convertFailMark = "// the converter fails on this file"

// convertFails parses as p does, except that a source holding
// convertFailMark parses clean with no native tree.
type convertFails struct{ p transpiler.GalaParser }

func (c convertFails) ParseFile(input string) *galaparser.Parsed {
	parsed := c.p.ParseFile(input)
	if !strings.Contains(input, convertFailMark) || parsed.Err() != nil {
		return parsed
	}
	failed := *parsed
	failed.Native = nil
	return &failed
}

// internalErrorProject writes files under a module root (example.com/repro)
// and returns the root and an analyzer of it parsing with convertFails.
func internalErrorProject(t *testing.T, files map[string]string) (string, *galaAnalyzer) {
	t.Helper()
	parseWithGalop(t)
	root := t.TempDir()
	files["gala.mod"] = "module example.com/repro\n\ngala dev\n"
	for name, text := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	_, std := collectionCorpus(t)
	a := NewBatchAnalyzer(convertFails{galaparser.NewAntlrGalaParser()}, append([]string{root}, std...), t.TempDir()).inner
	a.cache = nil
	return root, a
}

// requireInternal fails t unless err is an InternalError naming file.
func requireInternal(t *testing.T, err error, file string) {
	t.Helper()
	ie := asInternal(err)
	if ie == nil {
		t.Fatalf("got %v (%T), want an internal error naming %s", err, err, file)
	}
	if filepath.Clean(ie.File) != filepath.Clean(file) {
		t.Fatalf("the internal error names %s, want %s", ie.File, file)
	}
}

func (a *galaAnalyzer) analyzeText(t *testing.T, path string, packageFiles []string) error {
	t.Helper()
	text, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	a.packageFiles = packageFiles
	_, err = a.AnalyzeParsed(a.parser.ParseFile(string(text)), path)
	return err
}

// TestInternalErrorOfTheFile: a file whose clean parse did not convert fails
// its analysis with an internal error naming it.
func TestInternalErrorOfTheFile(t *testing.T) {
	root, a := internalErrorProject(t, map[string]string{
		"main.gala": "package main\n\n" + convertFailMark + "\nfunc main() {}\n",
	})
	requireInternal(t, a.analyzeText(t, filepath.Join(root, "main.gala"), nil), filepath.Join(root, "main.gala"))
}

// TestInternalErrorOfAPackageFile: a package file given to the analysis
// whose parse did not convert fails it, and is not reported as the file's
// syntax error (as a sibling that does not parse is).
func TestInternalErrorOfAPackageFile(t *testing.T) {
	root, a := internalErrorProject(t, map[string]string{
		"a.gala": "package main\n\nfunc main() {\n    f()\n}\n",
		"b.gala": "package main\n\n" + convertFailMark + "\nfunc f() {}\n",
	})
	b := filepath.Join(root, "b.gala")
	requireInternal(t, a.analyzeText(t, filepath.Join(root, "a.gala"), []string{b}), b)
}

// TestInternalErrorOfALibrarySibling: so does a sibling the analysis finds in
// a library's directory.
func TestInternalErrorOfALibrarySibling(t *testing.T) {
	root, a := internalErrorProject(t, map[string]string{
		"lib/a.gala": "package lib\n\nfunc A() int = B()\n",
		"lib/b.gala": "package lib\n\n" + convertFailMark + "\nfunc B() int = 1\n",
	})
	requireInternal(t, a.analyzeText(t, filepath.Join(root, "lib", "a.gala"), nil), filepath.Join(root, "lib", "b.gala"))
}

// TestInternalErrorOfAnImportedPackage: a file of an imported package whose
// parse did not convert fails the importing analysis, though a package that
// does not load is otherwise a warning.
func TestInternalErrorOfAnImportedPackage(t *testing.T) {
	root, a := internalErrorProject(t, map[string]string{
		"main.gala":   "package main\n\nimport \"example.com/repro/dep\"\n\nfunc main() {\n    dep.F()\n}\n",
		"dep/a.gala":  "package dep\n\nfunc F() {}\n",
		"dep/b.gala":  "package dep\n\n" + convertFailMark + "\nfunc G() {}\n",
		"dep2/a.gala": "package dep2\n\nfunc H() {}\n",
	})
	main := filepath.Join(root, "main.gala")
	requireInternal(t, a.analyzeText(t, main, nil), filepath.Join(root, "dep", "b.gala"))
	// Every analysis of the batch that imports the package fails, not only
	// the one that loaded it first.
	requireInternal(t, a.analyzeText(t, main, nil), filepath.Join(root, "dep", "b.gala"))
	// The failure is the analysis's own: the next one of the batch, which
	// imports a package that loads, succeeds.
	other := filepath.Join(root, "other.gala")
	if err := os.WriteFile(other, []byte("package main\n\nimport \"example.com/repro/dep2\"\n\nfunc main() {\n    dep2.H()\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := a.analyzeText(t, other, nil); err != nil {
		t.Fatalf("the next analysis failed: %v", err)
	}
}

// TestPackageTopLevelNamesInternalError: the source scan of a package's
// declarations returns the internal error of a file whose parse did not
// convert, rather than leaving the file's names out.
func TestPackageTopLevelNamesInternalError(t *testing.T) {
	root, a := internalErrorProject(t, map[string]string{
		"dep/a.gala": "package dep\n\nfunc F() {}\n",
		"dep/b.gala": "package dep\n\n" + convertFailMark + "\nfunc G() {}\n",
	})
	names, err := a.packageTopLevelNames("example.com/repro/dep")
	if names != nil {
		t.Errorf("got names %v with the error", names)
	}
	requireInternal(t, err, filepath.Join(root, "dep", "b.gala"))
}
