package analyzer_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"martianoff/gala/galaerr"
	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/analyzer"
)

// internalImportFixture builds a module rooted at tmp with the layout
//
//	gala.mod            module example.com/mod
//	internal/hidden/    package hidden        (private to the module)
//	sub/plain/          package plain         (public)
//	sub/internal/deep/  package deep          (private to sub/)
//
// and returns the module root. Callers add the importing file themselves.
func internalImportFixture(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()

	write := func(rel, content string) {
		t.Helper()
		full := filepath.Join(tmp, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}

	write("gala.mod", "module example.com/mod\n\ngala dev\n")
	write("internal/hidden/hidden.gala", "package hidden\n\nfunc Hidden() string = \"h\"\n")
	write("sub/plain/plain.gala", "package plain\n\nfunc Plain() string = \"p\"\n")
	write("sub/internal/deep/deep.gala", "package deep\n\nfunc Deep() string = \"d\"\n")

	return tmp
}

// analyzeFile writes src at relPath inside root and analyzes it, returning
// whatever error the analyzer produced.
func analyzeFile(t *testing.T, root, relPath, src string) error {
	t.Helper()

	full := filepath.Join(root, filepath.FromSlash(relPath))
	if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(src), 0644); err != nil {
		t.Fatal(err)
	}

	p := transpiler.NewAntlrGalaParser()
	searchPaths := append([]string{root}, getStdSearchPath()...)
	a := analyzer.NewGalaAnalyzer(p, searchPaths, root)

	tree, err := p.Parse(src)
	if err != nil {
		t.Fatalf("parse %s: %v", relPath, err)
	}
	_, analyzeErr := a.Analyze(tree, full)
	return analyzeErr
}

// requireE0041 asserts the error is a coded internal-import violation naming
// the expected offending path.
func requireE0041(t *testing.T, err error, wantPath string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected GALA-E0041 for import of %q, got no error", wantPath)
	}
	se, ok := err.(*galaerr.SemanticError)
	if !ok {
		t.Fatalf("expected *galaerr.SemanticError, got %T: %v", err, err)
	}
	if se.Code != galaerr.CodeInternalPackageImport {
		t.Fatalf("expected code %s, got %s: %v", galaerr.CodeInternalPackageImport, se.Code, err)
	}
	if !strings.Contains(se.Error(), wantPath) {
		t.Errorf("error should name the offending import %q, got: %v", wantPath, err)
	}
	if se.Line == 0 {
		t.Errorf("error must carry a source line, got 0: %v", err)
	}
	if se.FilePath == "" {
		t.Errorf("error must carry the .gala file path, got empty: %v", err)
	}
}

// A package outside the internal parent's tree cannot reach in, even though
// both live in the same module.
func TestInternalImport_RejectedFromSiblingSubtree(t *testing.T) {
	root := internalImportFixture(t)
	err := analyzeFile(t, root, "main.gala", `package main

import "example.com/mod/sub/internal/deep"

func main() {
    Println(deep.Deep())
}
`)
	requireE0041(t, err, "example.com/mod/sub/internal/deep")
}

// The module root may import its own top-level internal/ tree.
func TestInternalImport_AllowedFromModuleRoot(t *testing.T) {
	root := internalImportFixture(t)
	err := analyzeFile(t, root, "main.gala", `package main

import "example.com/mod/internal/hidden"

func main() {
    Println(hidden.Hidden())
}
`)
	if err != nil {
		t.Fatalf("importing the module's own internal/ package must be allowed, got: %v", err)
	}
}

// A package inside the internal parent's tree may reach in.
func TestInternalImport_AllowedFromWithinParentTree(t *testing.T) {
	root := internalImportFixture(t)
	err := analyzeFile(t, root, "sub/consumer/consumer.gala", `package consumer

import "example.com/mod/sub/internal/deep"

func Use() string = deep.Deep()
`)
	if err != nil {
		t.Fatalf("importing an internal package from inside its parent tree must be allowed, got: %v", err)
	}
}

// A non-internal import is never touched by the check.
func TestInternalImport_PublicPackageUnaffected(t *testing.T) {
	root := internalImportFixture(t)
	err := analyzeFile(t, root, "main.gala", `package main

import "example.com/mod/sub/plain"

func main() {
    Println(plain.Plain())
}
`)
	if err != nil {
		t.Fatalf("importing a public package must be allowed, got: %v", err)
	}
}

// "internalize" is not "internal" — only whole path elements count.
func TestInternalImport_ElementBoundaryNotSubstring(t *testing.T) {
	root := internalImportFixture(t)
	pkgDir := filepath.Join(root, "internalize", "thing")
	if err := os.MkdirAll(pkgDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkgDir, "thing.gala"),
		[]byte("package thing\n\nfunc Thing() string = \"t\"\n"), 0644); err != nil {
		t.Fatal(err)
	}

	err := analyzeFile(t, root, "main.gala", `package main

import "example.com/mod/internalize/thing"

func main() {
    Println(thing.Thing())
}
`)
	if err != nil {
		t.Fatalf("a path element merely starting with \"internal\" must not be restricted, got: %v", err)
	}
}

// The violation is reported at the import's own line and column, not at the
// top of the file — that position is the whole point of checking here rather
// than letting the Go compiler reject the generated code.
func TestInternalImport_ReportsImportPosition(t *testing.T) {
	root := internalImportFixture(t)
	err := analyzeFile(t, root, "main.gala", `package main

import "example.com/mod/sub/plain"
import "example.com/mod/sub/internal/deep"

func main() {
    Println(deep.Deep())
}
`)
	requireE0041(t, err, "example.com/mod/sub/internal/deep")

	se := err.(*galaerr.SemanticError)
	if se.Line != 4 {
		t.Errorf("expected the offending import's line 4, got %d", se.Line)
	}
	// Column points at the opening quote of the import string (0-based, as
	// ANTLR reports it): `import ` is 7 characters.
	if se.Column != 7 {
		t.Errorf("expected column 7 (the import path literal), got %d", se.Column)
	}
}
