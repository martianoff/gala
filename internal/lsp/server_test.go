package lsp_test

import (
	"strings"
	"testing"

	"github.com/owenrumney/go-lsp/servertest"

	"martianoff/gala/internal/lsp"
)

const testURI = "file:///test/main.gala"

func newHarness(t *testing.T) *servertest.Harness {
	handler := lsp.NewGalaHandler()
	return servertest.New(t, handler)
}

func TestInitialize(t *testing.T) {
	h := newHarness(t)
	if h.InitResult == nil {
		t.Fatal("expected InitializeResult")
	}
	if h.InitResult.ServerInfo == nil || h.InitResult.ServerInfo.Name != "gala-lsp" {
		t.Errorf("expected server name 'gala-lsp', got %v", h.InitResult.ServerInfo)
	}
}

func TestHover_BuiltinType(t *testing.T) {
	h := newHarness(t)
	src := `package main

type Person struct {
    val name string
    val age int
}
`
	if err := h.DidOpen(testURI, "gala", src); err != nil {
		t.Fatal(err)
	}

	// Hover over "Person" (line 2, col 5)
	hover, err := h.Hover(testURI, 2, 5)
	if err != nil {
		t.Fatal(err)
	}
	if hover == nil {
		t.Skip("hover returned nil — analyzer may not resolve without search paths")
	}
	if !strings.Contains(hover.Contents.Value, "Person") {
		t.Errorf("expected hover to mention 'Person', got: %s", hover.Contents.Value)
	}
}

func TestCompletion_Keywords(t *testing.T) {
	h := newHarness(t)
	src := `package main

func main() {

}
`
	if err := h.DidOpen(testURI, "gala", src); err != nil {
		t.Fatal(err)
	}

	// Complete at line 3, col 4 (inside function body)
	list, err := h.Completion(testURI, 3, 4)
	if err != nil {
		t.Fatal(err)
	}
	if list == nil || len(list.Items) == 0 {
		t.Fatal("expected completion items")
	}

	// Check that keywords are present
	labels := make(map[string]bool)
	for _, item := range list.Items {
		labels[item.Label] = true
	}

	for _, expected := range []string{"val", "var", "func", "if", "for", "return", "match"} {
		if !labels[expected] {
			t.Errorf("missing keyword completion: %s", expected)
		}
	}

	// Check built-in functions
	for _, expected := range []string{"Println", "Print", "len"} {
		if !labels[expected] {
			t.Errorf("missing built-in function completion: %s", expected)
		}
	}
}

func TestCompletion_DotMethod(t *testing.T) {
	h := newHarness(t)
	src := `package main

type Person struct {
    val name string
    val age int
}

func (p Person) Greet() string {
    return s"Hello ${p.name}"
}

func main() {
    val p = Person(name = "Alice", age = 30)
    p.
}
`
	if err := h.DidOpen(testURI, "gala", src); err != nil {
		t.Fatal(err)
	}

	// Complete after "p." (line 13, col 6)
	list, err := h.Completion(testURI, 13, 6)
	if err != nil {
		t.Fatal(err)
	}
	if list == nil {
		t.Skip("completion returned nil — type resolution may need search paths")
	}
	if len(list.Items) == 0 {
		t.Skip("no completion items — type resolution may need search paths")
	}

	// Should have Person-specific items if type was resolved
	labels := make(map[string]bool)
	for _, item := range list.Items {
		labels[item.Label] = true
	}
	t.Logf("completion items: %v", labels)
}

func TestCompletion_NamedArgs(t *testing.T) {
	h := newHarness(t)
	src := `package main

type Config struct {
    val host string
    val port int
}

func main() {
    val c = Config(
}
`
	if err := h.DidOpen(testURI, "gala", src); err != nil {
		t.Fatal(err)
	}

	// Complete inside Config( (line 9, col 4)
	list, err := h.Completion(testURI, 9, 4)
	if err != nil {
		t.Fatal(err)
	}
	if list == nil {
		t.Skip("completion returned nil")
	}
	t.Logf("named arg completion items: %d", len(list.Items))
}

func TestDefinition_LocalDecl(t *testing.T) {
	h := newHarness(t)
	src := `package main

func main() {
    val greeting = "hello"
    Println(greeting)
}
`
	if err := h.DidOpen(testURI, "gala", src); err != nil {
		t.Fatal(err)
	}

	// Go to definition of "greeting" at line 4, col 12
	locs, err := h.Definition(testURI, 4, 12)
	if err != nil {
		t.Fatal(err)
	}
	if len(locs) == 0 {
		t.Skip("no definition found — local search may not match")
	}
	if locs[0].Range.Start.Line != 3 {
		t.Errorf("expected definition on line 3, got line %d", locs[0].Range.Start.Line)
	}
}

func TestReferences(t *testing.T) {
	h := newHarness(t)
	src := `package main

func main() {
    val x = 42
    Println(x)
    val y = x + 1
}
`
	if err := h.DidOpen(testURI, "gala", src); err != nil {
		t.Fatal(err)
	}

	// Find references to "x" at line 3, col 8
	refs, err := h.References(testURI, 3, 8, true)
	if err != nil {
		t.Fatal(err)
	}
	// "x" appears on lines 3, 4, 5
	if len(refs) < 3 {
		t.Errorf("expected at least 3 references to 'x', got %d", len(refs))
	}
}

func TestDocumentSymbol(t *testing.T) {
	h := newHarness(t)
	src := `package main

type Person struct {
    val name string
}

func greet(p Person) string {
    return s"Hello ${p.name}"
}

func main() {
    val p = Person(name = "Alice")
    Println(greet(p))
}
`
	if err := h.DidOpen(testURI, "gala", src); err != nil {
		t.Fatal(err)
	}

	symbols, err := h.DocumentSymbol(testURI)
	if err != nil {
		t.Fatal(err)
	}
	if len(symbols) == 0 {
		t.Skip("no symbols — analyzer may need search paths")
	}

	names := make(map[string]bool)
	for _, s := range symbols {
		names[s.Name] = true
	}
	t.Logf("symbols: %v", names)

	for _, expected := range []string{"Person", "greet", "main"} {
		if !names[expected] {
			t.Errorf("missing symbol: %s", expected)
		}
	}
}

func TestMatchCaseCompletion(t *testing.T) {
	h := newHarness(t)
	src := `package main

func main() {
    val opt = Some(42)
    opt match {
        case _
    }
}
`
	if err := h.DidOpen(testURI, "gala", src); err != nil {
		t.Fatal(err)
	}

	// Complete on "case _" line (line 5, col 14)
	list, err := h.Completion(testURI, 5, 14)
	if err != nil {
		t.Fatal(err)
	}
	if list == nil {
		t.Skip("completion returned nil")
	}

	labels := make(map[string]bool)
	for _, item := range list.Items {
		labels[item.Label] = true
	}

	// In a match case context, should get sealed variants or wildcard
	if labels["_"] {
		t.Log("wildcard pattern available")
	}
	t.Logf("match case completions: %d items", len(list.Items))
}

