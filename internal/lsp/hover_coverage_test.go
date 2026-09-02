package lsp_test

import (
	"strings"
	"testing"
	"time"

	"github.com/owenrumney/go-lsp/lsp"
	"github.com/owenrumney/go-lsp/servertest"
)

const hoverSrc = `package main

import "martianoff/gala/std"
import im "martianoff/gala/collection_immutable"

// Greeter greets people politely.
type Greeter struct {
    // prefix is placed before the name.
    val prefix string
}

// Greet builds a greeting for name.
// name: who to greet.
func (g Greeter) Greet(name string) string = s"${g.prefix} ${name}"

// makeGreeter builds a Greeter with the default prefix.
func makeGreeter() Greeter = Greeter(prefix = "Hello")

// Shape is a closed set of drawable things.
sealed type Shape {
    // Circle is round.
    case Circle(radius float64)
    case Square(side float64)
}

func main() {
    val gr = makeGreeter()
    val opt = std.Some(5)
    val arr = im.ArrayOf(1, 2, 3)
    val c = Circle(radius = 1.0)
    Println(gr.Greet("world"), gr.prefix, opt.GetOrElse(0), arr.Size(), c)
}
`

// hoverAt hovers on `word` inside the first line containing `anchor`.
//
// Anchors rather than line numbers: hardcoded line numbers silently drift as the
// fixture is edited, and a stale one either panics or, worse, quietly hovers
// somewhere else and still passes.
func hoverAt(t *testing.T, h hoverHarness, uri lsp.DocumentURI, anchor, word string) string {
	t.Helper()
	line, ai := -1, -1
	for i, l := range strings.Split(hoverSrc, "\n") {
		if idx := strings.Index(l, anchor); idx >= 0 {
			line, ai = i, idx
			break
		}
	}
	if line < 0 {
		t.Fatalf("anchor %q not found in fixture", anchor)
	}
	wi := strings.Index(anchor, word)
	if wi < 0 {
		t.Fatalf("word %q not inside anchor %q", word, anchor)
	}
	col := ai + wi + 1

	hv, err := h.Hover(uri, line, col)
	if err != nil {
		t.Fatalf("hover: %v", err)
	}
	if hv == nil {
		return ""
	}
	return hv.Contents.Value
}

type hoverHarness interface {
	Hover(uri lsp.DocumentURI, line, char int) (*lsp.Hover, error)
}

func TestHoverCoverage(t *testing.T) {
	h := newHarness(t)
	uri := openFileOnDisk(t, h, hoverSrc)
	settle(t, h, uri, hoverSrc, "type Greeter", "Greeter")

	tests := []struct {
		name   string
		anchor string
		word   string
		want   []string // all must appear
	}{
		{"type declaration carries its doc", "type Greeter", "Greeter",
			[]string{"Greeter", "Greeter greets people politely."}},
		{"function declaration carries its doc", "func makeGreeter", "makeGreeter",
			[]string{"makeGreeter", "makeGreeter builds a Greeter with the default prefix."}},
		{"method declaration", ") Greet(", "Greet",
			[]string{"Greet", "string", "Greet builds a greeting for name."}},
		{"method call on a local", "gr.Greet", "Greet",
			[]string{"Greet", "Greet builds a greeting for name."}},
		{"field declaration", "val prefix", "prefix",
			[]string{"prefix", "string", "prefix is placed before the name."}},
		{"field access", "gr.prefix", "prefix",
			[]string{"prefix", "string"}},
		{"local val shows inferred type", "val gr =", "gr",
			[]string{"gr", "Greeter"}},
		{"stdlib method carries stdlib doc", "opt.GetOrElse", "GetOrElse",
			[]string{"GetOrElse", "returns the option's value"}},
		{"stdlib collection method", "arr.Size", "Size",
			[]string{"Size"}},
		{"package alias", "= im.", "im",
			[]string{"collection_immutable"}},
		{"sealed variant reads as a case, not a type", "= Circle", "Circle",
			[]string{"Circle", "radius", "Shape"}},
		{"sealed type declaration", "sealed type Shape", "Shape",
			[]string{"Shape", "Shape is a closed set of drawable things."}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := hoverAt(t, h, uri, tt.anchor, tt.word)
			if got == "" {
				t.Fatalf("hover returned nothing")
			}
			for _, want := range tt.want {
				if !strings.Contains(got, want) {
					t.Errorf("hover missing %q\n--- got ---\n%s", want, got)
				}
			}
		})
	}
}

// A sealed variant must not be presented as a bare type with the transpiler's
// Apply/Unapply plumbing on show.
func TestHoverVariantHidesInternals(t *testing.T) {
	h := newHarness(t)
	uri := openFileOnDisk(t, h, hoverSrc)
	settle(t, h, uri, hoverSrc, "type Greeter", "Greeter")

	got := hoverAt(t, h, uri, "= Circle", "Circle")
	for _, leak := range []string{"Unapply", "_variant"} {
		if strings.Contains(got, leak) {
			t.Errorf("hover leaks transpiler internal %q\n--- got ---\n%s", leak, got)
		}
	}
}

// A selector whose receiver resolves but whose member does not must render
// nothing, rather than falling through to a global name search and answering
// with an unrelated same-named type.
func TestHoverUnresolvedMemberDoesNotFallThrough(t *testing.T) {
	h := newHarness(t)
	src := `package main

// Body is a top-level type that must not be used to answer a member lookup.
type Body struct {
    val text string
}

type Resp struct {
    val code int
}

func main() {
    val r = Resp(code = 1)
    Println(r.Body, Body(text = "x"))
}
`
	uri := openFileOnDisk(t, h, src)
	settle(t, h, uri, src, "type Resp", "Resp")
	line, col := locate(t, src, "r.Body", "Body")
	hv, err := h.Hover(uri, line, col)
	if err != nil {
		t.Fatal(err)
	}
	if hv != nil && strings.Contains(hv.Contents.Value, "type Body") {
		t.Errorf("member lookup fell through to an unrelated top-level type\n--- got ---\n%s", hv.Contents.Value)
	}
}

// Mutability must not be guessed from the cursor's line: every reference to a
// `var` that is not its declaration would be reported as `val`.
func TestHoverLocalDoesNotClaimMutability(t *testing.T) {
	h := newHarness(t)
	src := `package main

func main() {
    var count = 1
    count = count + 1
    Println(count)
}
`
	uri := openFileOnDisk(t, h, src)
	line, col := locate(t, src, "count = count + 1", "count")
	settle(t, h, uri, src, "count = count + 1", "count")
	hv, err := h.Hover(uri, line, col)
	if err != nil {
		t.Fatal(err)
	}
	if hv == nil {
		t.Fatal("local did not resolve")
	}
	if strings.Contains(hv.Contents.Value, "val count") {
		t.Errorf("a var was reported as val at a non-declaration reference\n--- got ---\n%s", hv.Contents.Value)
	}
}

// Grouped imports must resolve; scanning only the cursor line for a quoted path
// went dead inside `import ( ... )`.
func TestHoverGroupedImportAlias(t *testing.T) {
	h := newHarness(t)
	src := `package main

import (
    im "martianoff/gala/collection_immutable"
)

func main() {
    val arr = im.ArrayOf(1, 2, 3)
    Println(arr)
}
`
	uri := openFileOnDisk(t, h, src)
	line, col := locate(t, src, "= im.", "im")
	settle(t, h, uri, src, "= im.", "im")
	hv, err := h.Hover(uri, line, col)
	if err != nil {
		t.Fatal(err)
	}
	if hv == nil || !strings.Contains(hv.Contents.Value, "collection_immutable") {
		got := ""
		if hv != nil {
			got = hv.Contents.Value
		}
		t.Errorf("grouped-import alias did not resolve\n--- got ---\n%s", got)
	}
}

// locate returns the LSP position of `word` inside the first line of src
// containing `anchor`.
func locate(t *testing.T, src, anchor, word string) (line, col int) {
	t.Helper()
	for i, l := range strings.Split(src, "\n") {
		if ai := strings.Index(l, anchor); ai >= 0 {
			return i, ai + strings.Index(anchor, word) + 1
		}
	}
	t.Fatalf("anchor %q not found", anchor)
	return 0, 0
}

// settle waits until the document has been analyzed, by polling the position of
// a symbol known to resolve.
//
// Analysis runs in a background goroutine, so a fixed sleep is wrong in both
// directions: it wastes wall clock on a fast machine and still races on a loaded
// one. Polling a known-good anchor is both faster in the common case and
// deterministic.
func settle(t *testing.T, h *servertest.Harness, uri lsp.DocumentURI, src, anchor, word string) {
	t.Helper()
	line, col := locate(t, src, anchor, word)
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if hv, err := h.Hover(uri, line, col); err == nil && hv != nil {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("document was not analyzed within the deadline (anchor %q)", anchor)
}
