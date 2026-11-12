package lsp

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bazelbuild/rules_go/go/tools/bazel"
	lsp "github.com/owenrumney/go-lsp/lsp"
)

// BenchmarkBrokenKeystroke is what the server does on each keystroke in a
// file with a syntax error: analyzeFile of the edited text, which parses it
// (the errors and the error-recovered tree), publishes the diagnostics and
// analyzes the recovered tree for completion and hover. The file is
// collection_immutable's array.gala, about 1,200 lines, broken two ways in
// its middle: an unfinished declaration, and a call left unclosed as when
// one is being typed.
func BenchmarkBrokenKeystroke(b *testing.B) {
	path, err := bazel.Runfile("collection_immutable/array.gala")
	if err != nil {
		b.Skipf("collection_immutable/array.gala is not staged: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		b.Fatal(err)
	}
	src := strings.ReplaceAll(string(data), "\r\n", "\n")
	mid := strings.Index(src[len(src)/2:], "\nfunc ") + len(src)/2
	call := mid + strings.Index(src[mid:], "builder.Add(") + len("builder.Add(")
	end := call + strings.Index(src[call:], ")\n")
	for name, text := range map[string]string{
		"unfinished declaration": src[:mid] + "\nfunc broken(a int = a +\n" + src[mid:],
		"unclosed call":          src[:end] + src[end+1:],
	} {
		b.Run(name, func(b *testing.B) {
			h := NewGalaHandler()
			h.SetSearchPaths([]string{filepath.Dir(filepath.Dir(path))})
			uri := "file:///" + filepath.ToSlash(path)
			if err := h.DidOpen(context.Background(), &lsp.DidOpenTextDocumentParams{
				TextDocument: lsp.TextDocumentItem{URI: lsp.DocumentURI(uri), LanguageID: "gala", Text: text},
			}); err != nil {
				b.Fatal(err)
			}
			// The first analysis warms the parsed-file cache, as an open
			// document's does before the user types.
			if len(h.analyzeFile(uri, uriToPath(uri), text)) == 0 {
				b.Fatal("the broken text has no diagnostics")
			}
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				h.analyzeFile(uri, uriToPath(uri), text)
			}
		})
	}
}
