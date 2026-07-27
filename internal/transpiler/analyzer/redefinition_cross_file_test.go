package analyzer_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/analyzer"
)

// writePackage writes each name→source pair into dir and returns the absolute
// path of every file, keyed by name.
func writePackage(t *testing.T, dir string, files map[string]string) map[string]string {
	t.Helper()
	paths := make(map[string]string, len(files))
	for name, src := range files {
		p := filepath.Join(dir, name)
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0755))
		require.NoError(t, os.WriteFile(p, []byte(src), 0644))
		paths[name] = p
	}
	return paths
}

// siblingsOf returns every path in paths except the one named self, which is
// what the transpiler passes as --package-files when compiling self.
func siblingsOf(paths map[string]string, self string) []string {
	var out []string
	for name, p := range paths {
		if name != self {
			out = append(out, p)
		}
	}
	return out
}

// TestRedefinitionAcrossFiles covers duplicate declarations spread over the
// files of a single package. A duplicate method used to be reported only when
// both definitions sat in the same file: the sibling-metadata pass, which is
// what pulls in the other files of the package, dropped a colliding method
// without a word, so the duplicate reached the generated Go and surfaced as
// the Go compiler's "method X.Y already declared".
func TestRedefinitionAcrossFiles(t *testing.T) {
	p := transpiler.NewAntlrGalaParser()
	searchPaths := getStdSearchPath()

	tests := []struct {
		name string
		// files of the package, by file name.
		files map[string]string
		// analyze names the file to compile; the others are its siblings.
		analyze string
		// wantCode is the error code the analysis must report; empty means
		// the analysis must succeed.
		wantCode string
		// wantContains are substrings the message must carry.
		wantContains []string
		// wantNotContains are substrings the message must not carry — used to
		// pin down that a message never points at the file it is reported
		// against as if that were a different declaration.
		wantNotContains []string
	}{
		{
			name: "duplicate method across two files, compiling the first",
			files: map[string]string{
				"a.gala": "package dup\n\nstruct User(Name string)\n\nfunc (u User) Greet() string = \"hi\"\n",
				"b.gala": "package dup\n\nfunc (u User) Greet() string = \"hello\"\n",
			},
			analyze:         "a.gala",
			wantCode:        "GALA-E0012",
			wantContains:    []string{`method "Greet" on type "User" in package "dup" redefined`, "also declared at", "b.gala:3"},
			wantNotContains: []string{"at a.gala"},
		},
		{
			name: "duplicate method across two files, compiling the second",
			files: map[string]string{
				"a.gala": "package dup\n\nstruct User(Name string)\n\nfunc (u User) Greet() string = \"hi\"\n",
				"b.gala": "package dup\n\nfunc (u User) Greet() string = \"hello\"\n",
			},
			analyze:      "b.gala",
			wantCode:     "GALA-E0012",
			wantContains: []string{`method "Greet" on type "User"`, "also declared at", "a.gala:5"},
		},
		{
			name: "duplicate method in one file still reported",
			files: map[string]string{
				"a.gala": "package dup\n\nstruct User(Name string)\n\nfunc (u User) Greet() string = \"hi\"\n\nfunc (u User) Greet() string = \"hello\"\n",
			},
			analyze:      "a.gala",
			wantCode:     "GALA-E0012",
			wantContains: []string{`method "Greet" on type "User"`, "also declared at line 5"},
		},
		{
			name: "duplicate method on a generic type across two files",
			files: map[string]string{
				"a.gala": "package dup\n\ntype Box[T any] struct {\n    value T\n}\n\nfunc (b Box[T]) Get() T = b.value\n",
				"b.gala": "package dup\n\nfunc (b Box[T]) Get() T = b.value\n",
			},
			analyze:      "a.gala",
			wantCode:     "GALA-E0012",
			wantContains: []string{`method "Get" on type "Box"`, "also declared at", "b.gala:3"},
		},
		{
			name: "duplicate generic method across two files",
			files: map[string]string{
				"a.gala": "package dup\n\ntype Box[T any] struct {\n    value T\n}\n\nfunc (b Box[T]) To[U any](u U) U = u\n",
				"b.gala": "package dup\n\nfunc (b Box[T]) To[U any](u U) U = u\n",
			},
			analyze:      "a.gala",
			wantCode:     "GALA-E0012",
			wantContains: []string{`method "To" on type "Box"`},
		},
		{
			name: "duplicate top-level function across two files",
			files: map[string]string{
				"a.gala": "package dup\n\nfunc Greet() string = \"hi\"\n",
				"b.gala": "package dup\n\nfunc Greet() string = \"hello\"\n",
			},
			analyze:         "a.gala",
			wantCode:        "GALA-E0027",
			wantContains:    []string{`function "Greet" in package "dup" redeclared`, "also declared at", "b.gala:3"},
			wantNotContains: []string{"at a.gala"},
		},
		{
			name: "duplicate type across two files names the sibling, not itself",
			files: map[string]string{
				"a.gala": "package dup\n\nstruct Item(Id int)\n",
				"b.gala": "package dup\n\nstruct Item(Name string)\n",
			},
			analyze:         "a.gala",
			wantCode:        "GALA-E0011",
			wantContains:    []string{`type "Item" in package "dup" redefined`, "also declared at", "b.gala:3"},
			wantNotContains: []string{"at a.gala"},
		},
		{
			name: "duplicate sealed type across two files names the sibling",
			files: map[string]string{
				"a.gala": "package dup\n\nsealed type Color {\n    case Red()\n    case Blue()\n}\n",
				"b.gala": "package dup\n\nsealed type Color {\n    case Green()\n}\n",
			},
			analyze:         "a.gala",
			wantCode:        "GALA-E0011",
			wantContains:    []string{`type "Color" in package "dup" redefined`, "also declared at", "b.gala:3"},
			wantNotContains: []string{"at a.gala"},
		},

		// --- negative cases: none of these may report a redefinition ---
		{
			name: "same method name on different types",
			files: map[string]string{
				"a.gala": "package ok\n\nstruct User(Name string)\n\nfunc (u User) Label() string = u.Name\n",
				"b.gala": "package ok\n\nstruct Item(Id int)\n\nfunc (i Item) Label() string = \"item\"\n",
			},
			analyze: "a.gala",
		},
		{
			name: "method and a same-named top-level function",
			files: map[string]string{
				"a.gala": "package ok\n\nstruct User(Name string)\n\nfunc (u User) Label() string = u.Name\n",
				"b.gala": "package ok\n\nfunc Label() string = \"free function\"\n",
			},
			analyze: "a.gala",
		},
		{
			name: "methods on a sibling-declared type",
			files: map[string]string{
				"a.gala": "package ok\n\nstruct User(Name string)\n",
				"b.gala": "package ok\n\nfunc (u User) Greet() string = u.Name\n\nfunc (u User) Shout() string = u.Name\n",
			},
			analyze: "a.gala",
		},
		{
			name: "one type with methods spread over three files",
			files: map[string]string{
				"a.gala": "package ok\n\nstruct User(Name string)\n\nfunc (u User) First() string = u.Name\n",
				"b.gala": "package ok\n\nfunc (u User) Second() string = u.Name\n",
				"c.gala": "package ok\n\nfunc (u User) Third() string = u.Name\n",
			},
			analyze: "b.gala",
		},
		{
			name: "methods on companion-object variants of a sibling sealed type",
			files: map[string]string{
				"a.gala": "package ok\n\nsealed type Shape {\n    case Circle(r float64)\n    case Square(s float64)\n}\n",
				"b.gala": "package ok\n\nfunc (c Circle) Area() float64 = c.r * c.r\n\nfunc (s Square) Area() float64 = s.s * s.s\n",
			},
			analyze: "a.gala",
		},
		{
			name: "generic methods with distinct names on a sibling generic type",
			files: map[string]string{
				"a.gala": "package ok\n\ntype Box[T any] struct {\n    value T\n}\n",
				"b.gala": "package ok\n\nfunc (b Box[T]) Get() T = b.value\n\nfunc (b Box[T]) To[U any](u U) U = u\n",
			},
			analyze: "a.gala",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			paths := writePackage(t, dir, tc.files)

			src, err := os.ReadFile(paths[tc.analyze])
			require.NoError(t, err)
			tree, err := p.Parse(string(src))
			require.NoError(t, err)

			a := analyzer.NewGalaAnalyzerWithPackageFiles(p, searchPaths, siblingsOf(paths, tc.analyze))
			_, err = a.Analyze(tree, paths[tc.analyze])

			if tc.wantCode == "" {
				assert.NoError(t, err, "legitimate cross-file declarations must analyze cleanly")
				return
			}
			require.Error(t, err, "duplicate declaration must be rejected by the analyzer")
			msg := err.Error()
			assert.Contains(t, msg, tc.wantCode)
			for _, want := range tc.wantContains {
				assert.Contains(t, msg, want)
			}
			for _, notWant := range tc.wantNotContains {
				assert.NotContains(t, msg, notWant,
					"the message must point at the other declaration, not at the file it is reported against")
			}
		})
	}
}

// TestRedefinitionSameFileListedTwice pins down that presenting one file to the
// analyzer under two spellings of its path is not a redefinition. The sibling
// pass records a declaration per file, and a package file list that repeats a
// file — or reaches it through a path that only cleans down to the same file —
// must resolve to a single declaration site.
func TestRedefinitionSameFileListedTwice(t *testing.T) {
	p := transpiler.NewAntlrGalaParser()
	searchPaths := getStdSearchPath()
	dir := t.TempDir()

	paths := writePackage(t, dir, map[string]string{
		"a.gala": "package ok\n\nstruct User(Name string)\n\nfunc (u User) Greet() string = u.Name\n\nfunc Helper() string = \"h\"\n",
		"c.gala": "package ok\n\nfunc Other() string = \"o\"\n",
	})

	// Same file, two spellings: the plain path and one routed through a
	// directory that is immediately backed out of.
	alias := filepath.Join(dir, "sub", "..", "a.gala")

	src, err := os.ReadFile(paths["c.gala"])
	require.NoError(t, err)
	tree, err := p.Parse(string(src))
	require.NoError(t, err)

	a := analyzer.NewGalaAnalyzerWithPackageFiles(p, searchPaths, []string{paths["a.gala"], alias})
	_, err = a.Analyze(tree, paths["c.gala"])
	assert.NoError(t, err, "one file listed twice must not look like two declarations")
}

// TestRedefinitionSelfListedAsPackageFile pins down that the file being
// analyzed may also appear in its own package-file list — the build driver
// passes the whole package in some modes — without its own declarations being
// counted twice.
func TestRedefinitionSelfListedAsPackageFile(t *testing.T) {
	p := transpiler.NewAntlrGalaParser()
	searchPaths := getStdSearchPath()
	dir := t.TempDir()

	paths := writePackage(t, dir, map[string]string{
		"a.gala": "package ok\n\nstruct User(Name string)\n\nfunc (u User) Greet() string = u.Name\n\nfunc Helper() string = \"h\"\n",
		"b.gala": "package ok\n\nfunc (u User) Shout() string = u.Name\n",
	})

	src, err := os.ReadFile(paths["a.gala"])
	require.NoError(t, err)
	tree, err := p.Parse(string(src))
	require.NoError(t, err)

	self := filepath.Join(dir, "sub", "..", "a.gala")
	a := analyzer.NewGalaAnalyzerWithPackageFiles(p, searchPaths, []string{self, paths["b.gala"]})
	_, err = a.Analyze(tree, paths["a.gala"])
	assert.NoError(t, err, "a file listed among its own package files must not redefine itself")
}

// TestRedefinitionAcrossPackages pins down that identical type and method
// names in two different packages never collide, including when both packages
// are analyzed through the same analyzer instance and therefore share its
// caches.
func TestRedefinitionAcrossPackages(t *testing.T) {
	p := transpiler.NewAntlrGalaParser()
	searchPaths := getStdSearchPath()
	root := t.TempDir()

	body := "struct Item(Id int)\n\nfunc (i Item) Label() string = \"item\"\n"
	pkgs := map[string]string{
		"pkga": "package pkga\n\n" + body,
		"pkgb": "package pkgb\n\n" + body,
	}

	a := analyzer.NewGalaAnalyzer(p, searchPaths)
	for pkg, src := range pkgs {
		dir := filepath.Join(root, pkg)
		require.NoError(t, os.MkdirAll(dir, 0755))
		file := filepath.Join(dir, "item.gala")
		require.NoError(t, os.WriteFile(file, []byte(src), 0644))

		tree, err := p.Parse(src)
		require.NoError(t, err)
		_, err = a.Analyze(tree, file)
		assert.NoError(t, err, "package %s must not collide with the identically shaped sibling package", pkg)
	}
}

// TestRedefinitionMessageNamesOtherFile is the message-shape guard for the
// three redefinition codes: the location a redefinition message names must be
// a declaration other than the one the caret is on. Naming the file under
// compilation as the place the symbol was "first defined" told the reader
// nothing.
func TestRedefinitionMessageNamesOtherFile(t *testing.T) {
	p := transpiler.NewAntlrGalaParser()
	searchPaths := getStdSearchPath()

	tests := []struct {
		name  string
		files map[string]string
		code  string
	}{
		{
			name: "type",
			files: map[string]string{
				"first.gala":  "package dup\n\nstruct Item(Id int)\n",
				"second.gala": "package dup\n\nstruct Item(Name string)\n",
			},
			code: "GALA-E0011",
		},
		{
			name: "method",
			files: map[string]string{
				"first.gala":  "package dup\n\nstruct Item(Id int)\n\nfunc (i Item) Label() string = \"a\"\n",
				"second.gala": "package dup\n\nfunc (i Item) Label() string = \"b\"\n",
			},
			code: "GALA-E0012",
		},
		{
			name: "function",
			files: map[string]string{
				"first.gala":  "package dup\n\nfunc Label() string = \"a\"\n",
				"second.gala": "package dup\n\nfunc Label() string = \"b\"\n",
			},
			code: "GALA-E0027",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			paths := writePackage(t, dir, tc.files)

			src, err := os.ReadFile(paths["first.gala"])
			require.NoError(t, err)
			tree, err := p.Parse(string(src))
			require.NoError(t, err)

			a := analyzer.NewGalaAnalyzerWithPackageFiles(p, searchPaths, siblingsOf(paths, "first.gala"))
			_, err = a.Analyze(tree, paths["first.gala"])
			require.Error(t, err)

			msg := err.Error()
			assert.Contains(t, msg, tc.code)
			// The named site is the sibling…
			assert.True(t, strings.Contains(msg, "second.gala"),
				"message must name the other declaration site, got: %s", msg)
			// …and never the file the diagnostic is reported against.
			assert.NotContains(t, msg, "first.gala",
				"message must not name the file it is reported against, got: %s", msg)
		})
	}
}
