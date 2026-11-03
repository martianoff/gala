package lsp

import (
	"encoding/json"
	"maps"
	"slices"
	"testing"

	"github.com/owenrumney/go-lsp/lsp"

	"martianoff/gala/internal/transpiler"
)

// sortedTypeCompletions is typeCompletions' rule stated plainly: every type
// walked in key order, each name offered by the first entry that spells it,
// and a type not offered offering none of its cases.
func sortedTypeCompletions(richAST *transpiler.RichAST) []lsp.CompletionItem {
	items := make([]lsp.CompletionItem, 0)
	seen := make(map[string]bool)
	for _, key := range slices.Sorted(maps.Keys(richAST.Types)) {
		tm := richAST.Types[key]
		if name := typeItemName(key, tm); !seen[name] && isExported(name) {
			items = appendTypeItems(items, seen, key, tm, name)
		}
	}
	return items
}

// TestTypeCompletionsPickTheSmallestKey: typeCompletions offers what a walk
// of every type in key order offers, where entries share a name: a sealed
// case and the case's own type entry, either way round; two types of a name;
// and a type that loses its name to a case, and with it its own cases.
func TestTypeCompletionsPickTheSmallestKey(t *testing.T) {
	variants := func(names ...string) []transpiler.SealedVariant {
		var out []transpiler.SealedVariant
		for _, n := range names {
			out = append(out, transpiler.SealedVariant{Name: n})
		}
		return out
	}
	richAST := &transpiler.RichAST{Types: map[string]*transpiler.TypeMetadata{
		"std.Option":  {Name: "Option", IsSealed: true, SealedVariants: variants("Some", "None")},
		"std.None":    {Name: "None"},
		"std.Some":    {Name: "Some"},
		"a.Shape":     {Name: "Shape", IsSealed: true, SealedVariants: variants("Circle", "Square")},
		"b.Shape":     {Name: "Shape", IsSealed: true, SealedVariants: variants("Triangle")},
		"c.Circle":    {Name: "Circle", IsSealed: true, SealedVariants: variants("Dot")},
		"d.Dot":       {Name: "Dot"},
		"e.Plain":     {Name: "Plain"},
		"f.lower":     {Name: "lower", IsSealed: true, SealedVariants: variants("Hidden")},
		"g.Hidden":    {Name: "Hidden"},
		"h.Opaque":    {Name: "Opaque", IsOpaque: true},
		"pkg.Unnamed": {},
	}}
	sortItems := func(items []lsp.CompletionItem) []string {
		var out []string
		for _, it := range items {
			js, err := json.Marshal(it)
			if err != nil {
				t.Fatal(err)
			}
			out = append(out, string(js))
		}
		slices.Sort(out)
		return out
	}
	want := sortItems(sortedTypeCompletions(richAST))
	for range 20 { // map order varies between runs
		if got := sortItems(typeCompletions(richAST)); !slices.Equal(got, want) {
			t.Fatalf("got\n%v\nwant\n%v", got, want)
		}
	}
	labels := map[string]string{}
	for _, it := range typeCompletions(richAST) {
		labels[it.Label] = it.Detail
	}
	for label, detail := range map[string]string{
		"Some":   "case of Option", // std.Option < std.Some
		"None":   "type",           // std.None < std.Option
		"Shape":  "sealed type",    // a.Shape, not b.Shape: no Triangle
		"Circle": "case of Shape",  // a.Shape < c.Circle, so c.Circle's Dot is d.Dot's
		"Dot":    "type",
		"Hidden": "type",
	} {
		if labels[label] != detail {
			t.Errorf("%s: offered as %q, want %q", label, labels[label], detail)
		}
	}
	if _, ok := labels["Triangle"]; ok {
		t.Error("Triangle is offered, though its type is not")
	}
}
