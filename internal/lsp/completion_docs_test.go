package lsp_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/owenrumney/go-lsp/lsp"
)

const docsSrc = `package main

import "martianoff/gala/std"

// Greeter greets people politely.
type Greeter struct {
    // prefix is placed before the name.
    val prefix string
}

// Greet builds a greeting for name.
// name: who to greet.
func (g Greeter) Greet(name string) string = s"${g.prefix} ${name}"

// makeGreeter builds a Greeter with the default prefix.
// style: which greeting style to use.
func makeGreeter(style string) Greeter = Greeter(prefix = style)

func main() {
    val gr = makeGreeter("Hello")
    val opt = std.Some(5)
    Println(gr.Greet("world"), opt.GetOrElse(0))
}
`

// resolveItem sends completionItem/resolve and returns the resolved item.
func resolveItem(t *testing.T, h hoverHarness, item lsp.CompletionItem) lsp.CompletionItem {
	t.Helper()
	raw, err := h.Call("completionItem/resolve", item)
	if err != nil {
		t.Fatalf("completionItem/resolve: %v", err)
	}
	var out lsp.CompletionItem
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decoding resolved item: %v", err)
	}
	return out
}

func findItem(list *lsp.CompletionList, label string) (lsp.CompletionItem, bool) {
	if list == nil {
		return lsp.CompletionItem{}, false
	}
	for _, it := range list.Items {
		if it.Label == label || strings.HasPrefix(it.Label, label+"(") {
			return it, true
		}
	}
	return lsp.CompletionItem{}, false
}

// Completion lists stay lean: documentation arrives via completionItem/resolve,
// so a dot-completion over a large type does not put every doc comment on the
// wire for every keystroke.
func TestCompletionDocsArriveOnResolve(t *testing.T) {
	h := newHarness(t)
	uri := openFileOnDisk(t, h, docsSrc)
	settle(t, h, uri, docsSrc, "type Greeter", "Greeter")

	line, col := locate(t, docsSrc, "gr.Greet(", "Greet")
	list, err := h.Completion(uri, line, col-1)
	if err != nil {
		t.Fatal(err)
	}
	item, ok := findItem(list, "Greet")
	if !ok {
		t.Fatalf("Greet absent from dot completion; got %v", labelSlice(list))
	}
	if item.Documentation != nil {
		t.Errorf("documentation was sent eagerly in the list: %q", item.Documentation.Value)
	}
	if item.Data == nil {
		t.Fatal("item carries no Data, so it can never be resolved")
	}

	resolved := resolveItem(t, h, item)
	if resolved.Documentation == nil {
		t.Fatal("resolve returned no documentation")
	}
	if !strings.Contains(resolved.Documentation.Value, "Greet builds a greeting for name.") {
		t.Errorf("resolved documentation missing the doc comment\n--- got ---\n%s", resolved.Documentation.Value)
	}
}

// A stdlib method resolves the standard library's own prose.
func TestCompletionResolveStdlibMethod(t *testing.T) {
	h := newHarness(t)
	uri := openFileOnDisk(t, h, docsSrc)
	settle(t, h, uri, docsSrc, "type Greeter", "Greeter")

	line, col := locate(t, docsSrc, "opt.GetOrElse(", "GetOrElse")
	list, err := h.Completion(uri, line, col-1)
	if err != nil {
		t.Fatal(err)
	}
	item, ok := findItem(list, "GetOrElse")
	if !ok {
		t.Skipf("GetOrElse absent from completion; got %v", labelSlice(list))
	}
	resolved := resolveItem(t, h, item)
	if resolved.Documentation == nil || !strings.Contains(resolved.Documentation.Value, "returns the option's value") {
		got := ""
		if resolved.Documentation != nil {
			got = resolved.Documentation.Value
		}
		t.Errorf("stdlib doc did not resolve\n--- got ---\n%s", got)
	}
}

// An item the server did not tag — a keyword, a builtin — must round-trip
// unchanged rather than error.
func TestCompletionResolveUntaggedItemIsInert(t *testing.T) {
	h := newHarness(t)
	uri := openFileOnDisk(t, h, docsSrc)
	settle(t, h, uri, docsSrc, "type Greeter", "Greeter")

	item := lsp.CompletionItem{Label: "match", Detail: "keyword"}
	resolved := resolveItem(t, h, item)
	if resolved.Label != "match" {
		t.Errorf("untagged item was altered: %+v", resolved)
	}
	if resolved.Documentation != nil {
		t.Errorf("untagged item gained documentation: %q", resolved.Documentation.Value)
	}
}

// Signature help carries the callee's summary, and each parameter carries its
// own line from the doc comment — the `name: description` convention the
// standard library already writes.
func TestSignatureHelpCarriesDocumentation(t *testing.T) {
	h := newHarness(t)
	uri := openFileOnDisk(t, h, docsSrc)
	settle(t, h, uri, docsSrc, "type Greeter", "Greeter")

	line, col := locate(t, docsSrc, "makeGreeter(\"Hello\")", "(")
	raw, err := h.Call("textDocument/signatureHelp", lsp.SignatureHelpParams{
		TextDocumentPositionParams: lsp.TextDocumentPositionParams{
			TextDocument: lsp.TextDocumentIdentifier{URI: uri},
			Position:     lsp.Position{Line: line, Character: col},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	var help lsp.SignatureHelp
	if err := json.Unmarshal(raw, &help); err != nil {
		t.Fatal(err)
	}
	if len(help.Signatures) == 0 {
		t.Fatal("no signature returned")
	}
	sig := help.Signatures[0]
	if sig.Documentation == nil || !strings.Contains(sig.Documentation.Value, "makeGreeter builds a Greeter") {
		got := ""
		if sig.Documentation != nil {
			got = sig.Documentation.Value
		}
		t.Errorf("signature carries no summary\n--- got ---\n%s", got)
	}
	// The `style:` line documents the parameter, and must not be left in the
	// summary as though it were prose.
	if sig.Documentation != nil && strings.Contains(sig.Documentation.Value, "style:") {
		t.Errorf("parameter line leaked into the summary\n--- got ---\n%s", sig.Documentation.Value)
	}
	if len(sig.Parameters) == 0 {
		t.Fatal("signature has no parameters")
	}
	p := sig.Parameters[0]
	if p.Documentation == nil || !strings.Contains(p.Documentation.Value, "which greeting style to use") {
		got := ""
		if p.Documentation != nil {
			got = p.Documentation.Value
		}
		t.Errorf("parameter carries no documentation\n--- got ---\n%s", got)
	}
}
