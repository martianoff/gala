package ownershipcov

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"martianoff/gala/internal/transpiler/ownership"
)

func TestSplice(t *testing.T) {
	doc := "head\n" + BeginMarker + "\nold\n" + EndMarker + "\ntail\n"
	out, err := Splice(doc, "new\n")
	require.NoError(t, err)
	require.Equal(t, "head\n"+BeginMarker+"\nnew\n"+EndMarker+"\ntail\n", out)
	_, err = Splice("no markers", "x")
	require.Error(t, err)
}

func TestRender(t *testing.T) {
	at := func(k ownership.ReasonKind, detail string, line int) ownership.Reason {
		return ownership.Reason{Kind: k, Detail: detail, File: "lib/a.gala", Line: line, Col: 1}
	}
	lib := &Tree{Name: "Libraries", Reports: []*ownership.Report{{Package: "lib", Funcs: []ownership.Func{
		{Name: "pure"},
		{Name: "lex", Bindings: []ownership.Binding{{Name: "l", Role: ownership.RoleLocal, Unique: true}}},
		{Name: "keep", Bindings: []ownership.Binding{{Name: "p", Role: ownership.RoleParam, Reasons: []ownership.Reason{
			at(ownership.Stored, "", 3), at(ownership.PassedToGo, "fmt.Sprint", 4), at(ownership.PassedToGo, "fmt.Sprint", 5),
		}}}},
	}}}}
	ex := &Tree{Name: "Examples", Failed: []string{"examples/broken.gala"}}
	out := Render([]*Tree{lib, ex})
	require.Equal(t, out, Render([]*Tree{lib, ex}), "deterministic")
	for _, want := range []string{
		"| Functions and methods | 3 | 0 | ",
		"| …whose pointer bindings are all unique | 1 (50.0%) | 0 (0.0%) | ",
		"| Packages that did not analyze | 0 | 1 | ",
		"| `stored` | " + ownership.Stored.Describe() + " | 1 | 0 | `lib/a.gala:3` |",
		"| `passed-to-go` | ",
		"| `passed-to-go:fmt.Sprint` | 1 |",
	} {
		require.True(t, strings.Contains(out, want), "missing %q in\n%s", want, out)
	}
}
