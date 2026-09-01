package analyzer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

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
