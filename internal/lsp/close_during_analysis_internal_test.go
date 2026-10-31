package lsp

import (
	"context"
	"testing"

	lsp "github.com/owenrumney/go-lsp/lsp"
	"github.com/stretchr/testify/require"
)

// TestCloseDuringAnalysisLeavesNoEntries closes a document while an analysis of
// it is in flight: the analysis, which DidChange runs in the background and
// which looks at its context only after storing, reaches its stores after
// DidClose has dropped the document. Nothing may be stored for the closed URI.
func TestCloseDuringAnalysisLeavesNoEntries(t *testing.T) {
	tests := []struct {
		name string
		text string
	}{
		{"clean parse", "package main\n\nfunc add(a int, b int) int = a + b\n"},
		{"error-recovered parse", "package main\n\nfunc add(a int, b int) int = a +\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := NewGalaHandler()
			uri := "file:///close_during_analysis.gala"
			// stored is the stores holding an entry for uri.
			stored := func() []string {
				h.mu.Lock()
				defer h.mu.Unlock()
				var names []string
				note := func(name string, ok bool) {
					if ok {
						names = append(names, name)
					}
				}
				_, ok := h.richASTs[uri]
				note("richASTs", ok)
				_, ok = h.parseTrees[uri]
				note("parseTrees", ok)
				_, ok = h.parseNatives[uri]
				note("parseNatives", ok)
				_, ok = h.parseTexts[uri]
				note("parseTexts", ok)
				_, ok = h.varTypes[uri]
				note("varTypes", ok)
				_, ok = h.lambdaHints[uri]
				note("lambdaHints", ok)
				return names
			}

			// While the document is open, the analysis stores its results:
			// otherwise the check below would pass vacuously.
			require.NoError(t, h.DidOpen(context.Background(), &lsp.DidOpenTextDocumentParams{
				TextDocument: lsp.TextDocumentItem{URI: lsp.DocumentURI(uri), LanguageID: "gala", Text: tt.text},
			}))
			require.NotEmpty(t, stored(), "an open document's analysis stored nothing")

			require.NoError(t, h.DidClose(context.Background(), &lsp.DidCloseTextDocumentParams{
				TextDocument: lsp.TextDocumentIdentifier{URI: lsp.DocumentURI(uri)},
			}))
			// The in-flight analysis finishes after the close.
			h.analyzeFile(uri, uriToPath(uri), tt.text)
			require.Empty(t, stored(), "a closed document's entries came back")
		})
	}
}
