package analyzer

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"martianoff/gala/internal/parser/grammar"
	"martianoff/gala/internal/transpiler"
)

// TestSpelledName: the duplicate-import check keys on the alias, else the
// path's last segment as written.
func TestSpelledName(t *testing.T) {
	assert.Equal(t, "v1", fileImport{Path: "k8s.io/api/core/v1"}.SpelledName())
	assert.Equal(t, "corev1", fileImport{Path: "k8s.io/api/core/v1", Alias: "corev1"}.SpelledName())
	assert.Equal(t, "yaml.v3", fileImport{Path: "gopkg.in/yaml.v3"}.SpelledName())
}

// TestQualifiersForFile covers the per-file import table: which import each
// qualifier means.
func TestQualifiersForFile(t *testing.T) {
	const src = `package repro

import (
    "k8s.io/api/core/v1"
    "martianoff/gala/core"
    "math/rand/v2"
    "gopkg.in/yaml.v3"
    "github.com/mattn/go-sqlite3"
)
`
	tree, _, err := transpiler.NewAntlrGalaParser().Parse(src)
	require.NoError(t, err)
	sf := tree.(*grammar.SourceFileContext)
	a := &galaAnalyzer{}

	t.Run("real Go package names win when known", func(t *testing.T) {
		rich := &transpiler.RichAST{
			Packages: map[string]string{"martianoff/gala/core": "core"},
			GoImportNames: map[string]string{
				"k8s.io/api/core/v1": "v1", "math/rand/v2": "rand",
				"gopkg.in/yaml.v3": "yaml", "github.com/mattn/go-sqlite3": "sqlite3",
			},
		}
		q := a.qualifiersForFile(sf, rich)
		for qualifier, path := range map[string]string{
			"v1": "k8s.io/api/core/v1", "core": "martianoff/gala/core", "rand": "math/rand/v2",
			"yaml": "gopkg.in/yaml.v3", "sqlite3": "github.com/mattn/go-sqlite3",
		} {
			assert.Equal(t, path, q.named[qualifier].Path, qualifier)
		}
		assert.NotContains(t, q.named, "v2", "math/rand/v2 binds rand only")
	})

	t.Run("a guessed name never shadows a surer one", func(t *testing.T) {
		// No Go type info: k8s.io/api/core/v1 may bind v1 (its last segment)
		// or core (derived). The GALA package really is core, so it keeps it.
		rich := &transpiler.RichAST{Packages: map[string]string{"martianoff/gala/core": "core"}}
		q := a.qualifiersForFile(sf, rich)
		assert.Equal(t, "martianoff/gala/core", q.named["core"].Path)
		assert.True(t, q.named["core"].IsGala)
		assert.Equal(t, "k8s.io/api/core/v1", q.named["v1"].Path)
		assert.Equal(t, "math/rand/v2", q.named["rand"].Path)
		assert.Equal(t, "gopkg.in/yaml.v3", q.named["yaml"].Path)
		assert.Equal(t, "github.com/mattn/go-sqlite3", q.named["sqlite3"].Path)
		_, isGo := q.goImport("core")
		assert.False(t, isGo, "core.X in this file is the GALA package's")
	})
}

// TestWithGoImportPath: a type written against a Go import is tied to it only
// when the Go package declares the type, or when its type info is missing.
func TestWithGoImportPath(t *testing.T) {
	const src = `package repro

import (
    "strings"
    "k8s.io/api/core/v1"
)
`
	tree, _, err := transpiler.NewAntlrGalaParser().Parse(src)
	require.NoError(t, err)
	sf := tree.(*grammar.SourceFileContext)
	a := &galaAnalyzer{}
	named := func(pkg, name string) transpiler.NamedType { return transpiler.NamedType{Package: pkg, Name: name} }

	gi := transpiler.NewGoTypeInfo()
	gi.Types["strings.Builder"] = &transpiler.GoTypeData{}
	gi.Types["v1.Pod"] = &transpiler.GoTypeData{}
	withInfo := a.qualifiersForFile(sf, &transpiler.RichAST{
		GoImportNames: map[string]string{"strings": "strings", "k8s.io/api/core/v1": "v1"},
	})
	cases := []struct {
		typ      transpiler.NamedType
		wantPath string
	}{
		{named("strings", "Builder"), "strings"},
		{named("strings", "Str"), ""}, // Go's strings declares no Str
		{named("v1", "Pod"), "k8s.io/api/core/v1"},
		{named("core", "Widget"), ""}, // the k8s package is v1; core is not bound to it
	}
	for _, tc := range cases {
		got := withInfo.withGoImportPath(tc.typ, gi).(transpiler.NamedType)
		assert.Equal(t, tc.wantPath, got.ImportPath, tc.typ.String())
	}

	// Without type info the Go import is taken at its word.
	noInfo := a.qualifiersForFile(sf, &transpiler.RichAST{})
	got := noInfo.withGoImportPath(named("strings", "Str"), nil).(transpiler.NamedType)
	assert.Equal(t, "strings", got.ImportPath)
}
