package transformer_test

import (
	"os"
	"path/filepath"
	"testing"

	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/analyzer"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// crossPkgValFixture is a module whose library packages declare package-level
// vals and vars, read from another package in the tests below.
func crossPkgValFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	write := func(rel, content string) {
		t.Helper()
		full := filepath.Join(root, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(full), 0755))
		require.NoError(t, os.WriteFile(full, []byte(content), 0644))
	}
	write("gala.mod", "module example.com/xpkg\n\ngala dev\n")
	write("colors/colors.gala", `package colors

sealed type Color {
    case Red()
    case NamedColor(Idx int)
}

struct Theme(Name string, Accent Color)

struct Adder(N int)

func (a Adder) Apply(x int) int = a.N + x

struct Halver()

func (h Halver) Unapply(n int) Option[int] = When(n % 2 == 0, n / 2)

val Green = NamedColor(2)
val Default = Theme("classic", Red())
val Greeting = "hello"
val Silent = s"$Greeting" == ""
val Limit = 50
val AddTen = Adder(10)
val Halve = Halver()
val secret = 7
var Hits = 0

func ToSgr(c Color) string = c match {
    case Red() => "31"
    case NamedColor(i) => s"3$i"
}
`)
	// Builds a colors struct through an import alias.
	write("theme/theme.gala", `package theme

import c "example.com/xpkg/colors"

val Dark = c.Theme("dark", c.Red())
`)
	// Two packages that share a name: their bindings must not collide.
	write("a/util/util.gala", "package util\n\nval Limit = 3\n")
	write("b/util/util.gala", "package util\n\nvar Limit = 4\n")
	// A val whose initializer reads a sibling file's val: its type is settled
	// by its own file's pass, whichever file is analyzed first.
	write("split/a.gala", "package split\n\nval Base = 3\n")
	write("split/b.gala", "package split\n\nval Derived = Base\n")
	// A void initializer has no type, and must not record a nil one.
	write("voids/voids.gala", "package voids\n\nfunc setup() {\n}\n\nval Done = setup()\n")
	return root
}

// TestCrossPackageValUnwrap: a package-level `val` read from another package
// lowers to std.Immutable[T] there, so every read must go through .Get() — the
// same unwrap a same-package reference gets. A `var` is a plain Go variable
// and must be left as written.
func TestCrossPackageValUnwrap(t *testing.T) {
	root := crossPkgValFixture(t)
	tests := []struct {
		name        string
		src         string
		contains    []string
		notContains []string
	}{
		{
			name: "qualified import",
			src: `package main

import "example.com/xpkg/colors"

func show(c colors.Color) string = colors.ToSgr(c)

func main() {
    Println(show(colors.Green))
    val c colors.Color = colors.Green
    Println(show(c))
    Println(s"${colors.Greeting} ${colors.Default.Name}")
    colors.Hits = colors.Hits + 1
}`,
			contains: []string{
				"show(colors.Green.Get())",
				"std.NewImmutable[colors.Color](colors.Green.Get())",
				"colors.Greeting.Get()",
				"colors.Default.Get().Name.Get()",
				"colors.Hits = colors.Hits + 1",
			},
			notContains: []string{"colors.Hits.Get()"},
		},
		{
			name: "aliased import",
			src: `package main

import c "example.com/xpkg/colors"

func main() {
    Println(c.ToSgr(c.Green))
}`,
			contains: []string{"c.ToSgr(c.Green.Get())"},
		},
		{
			name: "dot import",
			src: `package main

import . "example.com/xpkg/colors"

func main() {
    Println(ToSgr(Green))
    Println(Hits)
}`,
			contains:    []string{"ToSgr(Green.Get())", "fmt.Println(Hits)"},
			notContains: []string{"Hits.Get()"},
		},
		{
			name: "local binding shadows the package",
			src: `package main

import "example.com/xpkg/colors"

struct Box(Green int)

func main() {
    val colors = Box(5)
    Println(colors.Green)
}`,
			notContains: []string{"colors.Green.Get().Get()"},
		},
		{
			name: "calling an imported val whose type has Apply",
			src: `package main

import "example.com/xpkg/colors"

func main() {
    Println(colors.AddTen(5))
}`,
			contains: []string{"colors.AddTen.Get().Apply(5)"},
		},
		{
			name: "an imported val as an extractor",
			src: `package main

import "example.com/xpkg/colors"

func half(n int) int = n match {
    case colors.Halve(h) => h
    case _ => -1
}

func main() {
    Println(half(8))
}`,
			contains: []string{"colors.Halve.Get().Unapply("},
		},
		{
			name: "same-named packages keep their own bindings",
			src: `package main

import (
    ua "example.com/xpkg/a/util"
    ub "example.com/xpkg/b/util"
)

func main() {
    Println(ua.Limit + ub.Limit)
}`,
			contains:    []string{"ua.Limit.Get() + ub.Limit"},
			notContains: []string{"ub.Limit.Get()"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			out, err := transpileCrossPkg(t, root, tc.src)
			require.NoError(t, err)
			for _, want := range tc.contains {
				assert.Contains(t, out, want, "generated:\n%s", out)
			}
			for _, bad := range tc.notContains {
				assert.NotContains(t, out, bad, "generated:\n%s", out)
			}
		})
	}
}

// TestCrossPackageValAssignmentRejected: reassigning a `val` is rejected
// whether it is another package's or this one's, and whichever assignment form
// is used.
func TestCrossPackageValAssignmentRejected(t *testing.T) {
	root := crossPkgValFixture(t)
	tests := []struct {
		name    string
		stmt    string
		wantErr string
	}{
		{"assignment", "colors.Green = colors.NamedColor(9)", "cannot assign to immutable variable colors.Green"},
		{"compound assignment", "colors.Limit += 1", "cannot assign to immutable variable colors.Limit"},
		{"increment", "colors.Limit++", "cannot increment/decrement immutable variable colors.Limit"},
		{"same-package increment", "local++", "cannot increment/decrement immutable variable local"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := transpileCrossPkg(t, root, `package main

import "example.com/xpkg/colors"

val local = 1

func main() {
    `+tc.stmt+`
    Println(colors.Limit, local)
}`)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.wantErr)
		})
	}
}

// TestCrossPackageValMetadata: the analyzer surfaces an imported package's
// exported bindings keyed by import path, with the element types their
// initializers settle, and keeps them out of the importer's own PackageVals.
func TestCrossPackageValMetadata(t *testing.T) {
	root := crossPkgValFixture(t)
	const src = `package main

import (
    "example.com/xpkg/colors"
    "example.com/xpkg/split"
    "example.com/xpkg/theme"
    "example.com/xpkg/voids"
)

func main() {
    Println(colors.ToSgr(colors.Green), theme.Dark.Name, split.Derived, voids.Done)
}`
	p := transpiler.NewAntlrGalaParser()
	tree, _, err := p.Parse(src)
	require.NoError(t, err)
	a := analyzer.NewGalaAnalyzer(p, append([]string{root}, getStdSearchPath()...), root)
	richAST, err := a.Analyze(tree, nil, filepath.Join(root, "main.gala"))
	require.NoError(t, err)

	assert.Empty(t, richAST.PackageVals, "imported bindings must not become the importer's own")

	const colorsPath, themePath = "example.com/xpkg/colors", "example.com/xpkg/theme"
	want := []struct {
		path, name, typ string
		isVal           bool
	}{
		{colorsPath, "Green", "colors.Color", true},
		{colorsPath, "Default", "colors.Theme", true},
		{colorsPath, "Greeting", "string", true},
		{colorsPath, "AddTen", "colors.Adder", true},
		{colorsPath, "Halve", "colors.Halver", true},
		{colorsPath, "Hits", "int", false},
		// Built through the alias `c`: the type names the package, not the alias.
		{themePath, "Dark", "colors.Theme", true},
		{"example.com/xpkg/split", "Derived", "int", true},
	}
	for _, w := range want {
		pv := richAST.ImportedVals[w.path][w.name]
		require.NotNil(t, pv, "ImportedVals missing %s %s", w.path, w.name)
		assert.Equal(t, w.typ, pv.Type.String(), "type of %s", w.name)
		assert.Equal(t, w.isVal, pv.IsVal, "val/var classification of %s", w.name)
	}
	// `s"$Greeting" == ""` is a comparison, not a string literal.
	require.NotNil(t, richAST.ImportedVals[colorsPath]["Silent"])
	assert.NotEqual(t, "string", richAST.ImportedVals[colorsPath]["Silent"].Type.String())
	assert.NotContains(t, richAST.ImportedVals[colorsPath], "secret", "unexported bindings are not importable")
	done := richAST.ImportedVals["example.com/xpkg/voids"]["Done"]
	require.NotNil(t, done)
	require.NotNil(t, done.Type, "a void initializer must record NilType, not a nil interface")
	assert.True(t, done.Type.IsNil())
}
