package analyzer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/antlr4-go/antlr/v4"

	"martianoff/gala/internal/parser"
	"martianoff/gala/internal/transpiler"
)

// analyzeSrc writes src to a temp .gala file, analyzes it, and returns the RichAST.
func analyzeSrc(t *testing.T, src string) *transpiler.RichAST {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "doc.gala")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	p := parser.NewAntlrGalaParser()
	tree, docs, err := p.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	a := NewGalaAnalyzer(p, nil)
	rich, err := a.Analyze(tree, docs, path)
	if err != nil {
		t.Fatalf("analyze: %v", err)
	}
	return rich
}

const docSrc = `package docpkg

// Box holds a single value and knows how to describe itself.
type Box struct {
    // value is the wrapped payload.
    val value int
    val label string
}

// Describe renders the box as text.
// sep: the separator placed between label and value.
func (b Box) Describe(sep string) string = b.label

// NewBox builds a Box around v.
func NewBox(v int) Box = Box(value = v, label = "b")

// Shape is a closed set of drawable things.
sealed type Shape {
    // Circle is round.
    case Circle(r float64)
    case Square(side float64)
}
`

func TestDocsReachMetadata(t *testing.T) {
	rich := analyzeSrc(t, docSrc)

	boxKey := "docpkg.Box"
	box, ok := rich.Types[boxKey]
	if !ok {
		t.Fatalf("type %s absent; have %v", boxKey, keysOf(rich.Types))
	}
	if got, want := box.Doc, "Box holds a single value and knows how to describe itself."; got != want {
		t.Errorf("Box.Doc = %q, want %q", got, want)
	}
	if got, want := box.FieldDocs["value"], "value is the wrapped payload."; got != want {
		t.Errorf("Box.FieldDocs[value] = %q, want %q", got, want)
	}
	if got := box.FieldDocs["label"]; got != "" {
		t.Errorf("Box.FieldDocs[label] = %q, want empty", got)
	}

	m, ok := box.Methods["Describe"]
	if !ok {
		t.Fatalf("method Describe absent")
	}
	want := "Describe renders the box as text.\nsep: the separator placed between label and value."
	if m.Doc != want {
		t.Errorf("Describe.Doc = %q, want %q", m.Doc, want)
	}

	fn, ok := rich.Functions["docpkg.NewBox"]
	if !ok {
		t.Fatalf("func NewBox absent; have %v", keysOf(rich.Functions))
	}
	if got, want := fn.Doc, "NewBox builds a Box around v."; got != want {
		t.Errorf("NewBox.Doc = %q, want %q", got, want)
	}

	shape, ok := rich.Types["docpkg.Shape"]
	if !ok {
		t.Fatalf("sealed type Shape absent")
	}
	if got, want := shape.Doc, "Shape is a closed set of drawable things."; got != want {
		t.Errorf("Shape.Doc = %q, want %q", got, want)
	}
	var circle, square *transpiler.SealedVariant
	for i := range shape.SealedVariants {
		switch shape.SealedVariants[i].Name {
		case "Circle":
			circle = &shape.SealedVariants[i]
		case "Square":
			square = &shape.SealedVariants[i]
		}
	}
	if circle == nil || square == nil {
		t.Fatalf("variants missing: %+v", shape.SealedVariants)
	}
	if got, want := circle.Doc, "Circle is round."; got != want {
		t.Errorf("Circle.Doc = %q, want %q", got, want)
	}
	if square.Doc != "" {
		t.Errorf("Square.Doc = %q, want empty", square.Doc)
	}

	if got, want := rich.PackageName, "docpkg"; got != want {
		t.Errorf("PackageName = %q, want %q", got, want)
	}
}

// Docs must survive the on-disk cache round-trip, or a cache hit silently
// serves documentation-free metadata.
func TestDocsSurviveCacheCodec(t *testing.T) {
	rich := analyzeSrc(t, docSrc)
	cached := toCachedRichAST(rich, "deps", nil)

	blob, err := encodeCachedRichAST(cached)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	back, err := decodeCachedRichAST(blob)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}

	box := back.Types["docpkg.Box"]
	if box == nil {
		t.Fatalf("Box lost across codec; have %v", keysOf(back.Types))
	}
	if box.Doc != rich.Types["docpkg.Box"].Doc {
		t.Errorf("Box.Doc lost: %q", box.Doc)
	}
	if box.FieldDocs["value"] != "value is the wrapped payload." {
		t.Errorf("Box.FieldDocs lost: %v", box.FieldDocs)
	}
	if box.Methods["Describe"].Doc == "" {
		t.Error("Describe.Doc lost across codec")
	}
	if back.Functions["docpkg.NewBox"].Doc == "" {
		t.Error("NewBox.Doc lost across codec")
	}
	shape := back.Types["docpkg.Shape"]
	if shape == nil || len(shape.SealedVariants) == 0 {
		t.Fatalf("Shape lost across codec")
	}
	for _, v := range shape.SealedVariants {
		if v.Name == "Circle" && v.Doc != "Circle is round." {
			t.Errorf("Circle.Doc lost across codec: %q", v.Doc)
		}
	}
}

func keysOf[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// TestDocCacheGrowth measures what carrying doc comments costs the on-disk
// cache, on a real stdlib package rather than a synthetic one. Reported, not
// asserted beyond a generous ceiling: the point is that the number is visible
// in CI output when someone changes the codec.
func TestDocCacheGrowth(t *testing.T) {
	root := findRepoRoot(t)
	pkgDir := filepath.Join(root, "collection_immutable")
	entries, err := os.ReadDir(pkgDir)
	if err != nil {
		t.Skipf("collection_immutable not staged: %v", err)
	}

	p := parser.NewAntlrGalaParser()
	merged := &transpiler.RichAST{
		Types:     map[string]*transpiler.TypeMetadata{},
		Functions: map[string]*transpiler.FunctionMetadata{},
	}
	files := 0
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".gala" || strings.HasSuffix(e.Name(), "_test.gala") {
			continue
		}
		path := filepath.Join(pkgDir, e.Name())
		src, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		tree, docs, perr := p.Parse(string(src))
		if perr != nil {
			continue
		}
		a := NewGalaAnalyzer(p, []string{root})
		rich, aerr := a.Analyze(tree, docs, path)
		if aerr != nil || rich == nil {
			continue
		}
		files++
		merged.PackageName = rich.PackageName
		merged.Merge(rich)
	}
	if files == 0 {
		t.Skip("no analyzable files")
	}

	withDocs, err := encodeCachedRichAST(toCachedRichAST(merged, "d", nil))
	if err != nil {
		t.Fatalf("encode: %v", err)
	}

	documented := 0
	for _, tm := range merged.Types {
		if tm.Doc != "" {
			documented++
		}
		tm.Doc, tm.FieldDocs = "", nil
		for _, m := range tm.Methods {
			if m.Doc != "" {
				documented++
			}
			m.Doc = ""
		}
		for i := range tm.SealedVariants {
			tm.SealedVariants[i].Doc = ""
		}
	}
	for _, fm := range merged.Functions {
		if fm.Doc != "" {
			documented++
		}
		fm.Doc = ""
	}
	without, err := encodeCachedRichAST(toCachedRichAST(merged, "d", nil))
	if err != nil {
		t.Fatalf("encode: %v", err)
	}

	grow := len(withDocs) - len(without)
	pct := 100 * float64(grow) / float64(len(without))
	t.Logf("collection_immutable (%d files, %d documented declarations): cache entry %d -> %d bytes (+%d, +%.1f%%)",
		files, documented, len(without), len(withDocs), grow, pct)

	if documented == 0 {
		t.Error("no documented declarations found — doc capture is not reaching stdlib metadata")
	}
}

func findRepoRoot(t *testing.T) string {
	t.Helper()
	dir, _ := os.Getwd()
	for i := 0; i < 8; i++ {
		if _, err := os.Stat(filepath.Join(dir, "collection_immutable")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	t.Skip("repo root not found")
	return ""
}

// Re-analysis must not resurrect documentation for fields that no longer exist,
// and must not wipe documentation when the caller threads no docs map.
func TestDocsAcrossReanalysis(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "r.gala")
	const v1 = `package rpkg

// Holder holds things.
type Holder struct {
    // gone is documented in v1 only.
    val gone int
    val kept string
}
`
	const v2 = `package rpkg

// Holder holds things.
type Holder struct {
    val kept string
}
`
	p := parser.NewAntlrGalaParser()
	a := NewGalaAnalyzer(p, nil)

	tree1, docs1, err := p.Parse(v1)
	if err != nil {
		t.Fatalf("parse v1: %v", err)
	}
	if err := os.WriteFile(path, []byte(v1), 0o644); err != nil {
		t.Fatal(err)
	}
	rich, err := a.Analyze(tree1, docs1, path)
	if err != nil {
		t.Fatalf("analyze v1: %v", err)
	}
	if got := rich.Types["rpkg.Holder"].FieldDocs["gone"]; got != "gone is documented in v1 only." {
		t.Fatalf("v1 FieldDocs[gone] = %q", got)
	}

	// Re-analyze the edited source through the SAME analyzer, so the existing
	// TypeMetadata is reused rather than rebuilt.
	tree2, docs2, err := p.Parse(v2)
	if err != nil {
		t.Fatalf("parse v2: %v", err)
	}
	if err := os.WriteFile(path, []byte(v2), 0o644); err != nil {
		t.Fatal(err)
	}
	rich2, err := a.Analyze(tree2, docs2, path)
	if err != nil {
		t.Fatalf("analyze v2: %v", err)
	}
	holder := rich2.Types["rpkg.Holder"]
	if got, ok := holder.FieldDocs["gone"]; ok {
		t.Errorf("FieldDocs kept a doc for the removed field %q: %q", "gone", got)
	}
	if holder.Doc != "Holder holds things." {
		t.Errorf("Holder.Doc = %q after re-analysis", holder.Doc)
	}

}

// setDoc backs the re-analysis and sibling-merge paths, where a TypeMetadata
// may already carry documentation from a cache load or an earlier pass. An
// absent doc must leave that value alone; a real one must replace it.
func TestSetDocKeepsExistingWhenAbsent(t *testing.T) {
	docs := map[int]string{10: "fresh doc"}
	tok := &stubToken{start: 10}
	missing := &stubToken{start: 99}

	cases := []struct {
		name  string
		start antlr.Token
		docs  map[int]string
		in    string
		want  string
	}{
		{"absent leaves existing", missing, docs, "existing", "existing"},
		{"nil map leaves existing", tok, nil, "existing", "existing"},
		{"present replaces existing", tok, docs, "existing", "fresh doc"},
		{"present fills empty", tok, docs, "", "fresh doc"},
		{"absent leaves empty", missing, docs, "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.in
			setDoc(&got, tc.docs, tc.start)
			if got != tc.want {
				t.Errorf("setDoc = %q, want %q", got, tc.want)
			}
		})
	}
}

// stubToken is the minimal antlr.Token surface setDoc/docAt touch.
type stubToken struct {
	antlr.Token
	start int
}

func (s *stubToken) GetStart() int { return s.start }
