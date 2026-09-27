package transformer_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAliasedGoImportTypeKeepsItsPackage covers a Go type written against an
// aliased import (`gostrings "strings"`) after it has flowed through a type
// argument — an Option element, a lambda parameter, a tuple slot. The type's
// qualifier is the alias, not the package name, so every lookup of its methods
// must go through the import path it carries; a lookup keyed by its printed
// form (`gostrings.Builder`) finds nothing and erased the method's result to
// `any`.
func TestAliasedGoImportTypeKeepsItsPackage(t *testing.T) {
	trans := newDefaultsTranspiler()

	cases := []struct {
		name     string
		imports  string
		contains []string
		absent   []string
	}{
		{
			name:    "aliased Go import alone",
			imports: `gostrings "strings"`,
			contains: []string{
				"func(b *gostrings.Builder) string {",
				"func(b *gostrings.Builder) int {",
			},
			absent: []string{"*gostrings.Builder) any {"},
		},
		{
			name: "aliased Go import beside the same-name GALA package",
			imports: `gostrings "strings"
    "martianoff/gala/strings"`,
			contains: []string{
				"func(b *gostrings.Builder) string {",
				"func(b *gostrings.Builder) int {",
			},
			absent: []string{"*gostrings.Builder) any {", "*strings.Builder"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := `package main

import (
    ` + tc.imports + `
)

func length(s string) int = s.Size()

func main() {
    var sb gostrings.Builder
    sb.WriteString("abc")
    val p = &sb
    val o = Some(p)
    val text = o.Map((b) => b.String()).GetOrElse("none")
    Println(length(text))
    val n = o.Map((b) => b.Len()).GetOrElse(0)
    Println(n + 1)
}
`
			out, err := trans.Transpile(src, "aliased_go_import_types_test.gala")
			require.NoError(t, err)
			for _, want := range tc.contains {
				assert.Contains(t, out, want)
			}
			for _, notWant := range tc.absent {
				assert.NotContains(t, out, notWant)
			}
		})
	}
}

// TestAliasedGoImportFunctionsAndNamedCollections covers package-level Go
// lookups through an aliased import (`gourl "net/url"`): Go type info records
// `url.ParseQuery` and `url.Values`, never `gourl.…`. Keyed by the alias, the
// (Values, error) call was not recognised as multi-return inside Try, and
// Size() on url.Values was not lowered to len().
func TestAliasedGoImportFunctionsAndNamedCollections(t *testing.T) {
	trans := newDefaultsTranspiler()
	src := `package main

import gourl "net/url"

func main() {
    Println(Try(gourl.ParseQuery("a=1&b=2")).Map((q) => q.Size()).GetOrElse(-1))
}
`
	out, err := trans.Transpile(src, "aliased_go_import_funcs_test.gala")
	require.NoError(t, err)
	assert.Contains(t, out, "len(q)")
	assert.NotContains(t, out, "q.Size()")
}
