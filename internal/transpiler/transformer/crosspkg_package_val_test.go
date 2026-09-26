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

// crossPkgValFixture is a module with one library package declaring
// package-level vals and a var, read from another package in the tests below.
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

val Green = NamedColor(2)
val Default = Theme("classic", Red())
val Greeting = "hello"
val Limit = 50
val secret = 7
var Hits = 0

func ToSgr(c Color) string = c match {
    case Red() => "31"
    case NamedColor(i) => s"3$i"
}
`)
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

// TestCrossPackageValAssignmentRejected: reassigning another package's `val`
// is as immutable as reassigning a same-package one.
func TestCrossPackageValAssignmentRejected(t *testing.T) {
	root := crossPkgValFixture(t)
	tests := []struct {
		name    string
		stmt    string
		wantErr string
	}{
		{"assignment", "colors.Green = colors.NamedColor(9)", "cannot assign to immutable variable colors.Green"},
		{"increment", "colors.Limit++", "cannot increment/decrement immutable variable colors.Limit"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := transpileCrossPkg(t, root, `package main

import "example.com/xpkg/colors"

func main() {
    `+tc.stmt+`
}`)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.wantErr)
		})
	}
}

// TestCrossPackageValMetadata: the analyzer surfaces an imported package's
// exported bindings package-qualified, with the element types their
// initializers settle, and keeps them out of the importer's own PackageVals.
func TestCrossPackageValMetadata(t *testing.T) {
	root := crossPkgValFixture(t)
	const src = `package main

import "example.com/xpkg/colors"

func main() {
    Println(colors.ToSgr(colors.Green))
}`
	p := transpiler.NewAntlrGalaParser()
	tree, _, err := p.Parse(src)
	require.NoError(t, err)
	a := analyzer.NewGalaAnalyzer(p, append([]string{root}, getStdSearchPath()...), root)
	richAST, err := a.Analyze(tree, nil, filepath.Join(root, "main.gala"))
	require.NoError(t, err)

	assert.Empty(t, richAST.PackageVals, "imported bindings must not become the importer's own")

	want := map[string]struct {
		typ   string
		isVal bool
	}{
		"colors.Green":    {"colors.Color", true},
		"colors.Default":  {"colors.Theme", true},
		"colors.Greeting": {"string", true},
		"colors.Hits":     {"int", false},
	}
	for key, w := range want {
		pv := richAST.ImportedVals[key]
		require.NotNil(t, pv, "ImportedVals missing %s", key)
		assert.Equal(t, w.typ, pv.Type.String(), "type of %s", key)
		assert.Equal(t, w.isVal, pv.IsVal, "val/var classification of %s", key)
	}
	assert.NotContains(t, richAST.ImportedVals, "colors.secret", "unexported bindings are not importable")
}
