package lsp

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/owenrumney/go-lsp/lsp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// utf8BOM is the UTF-8 encoding of U+FEFF. Written as explicit bytes because a
// literal BOM inside a Go source file is itself rejected by the Go parser
// ("illegal byte order mark") when it appears anywhere but the very first
// position.
const utf8BOM = "\xef\xbb\xbf"

// TestPackageDeclLocationWithLeadingBOM pins go-to-definition on a package
// clause for a BOM'd file. strings.TrimSpace does not remove U+FEFF, so without
// stripping the scan misses line 1 and the jump lands on the file's start by
// the fallback path — which happens to be the same line here, so the test uses
// a leading comment to make line 1 and the package line distinct.
func TestPackageDeclLocationWithLeadingBOM(t *testing.T) {
	const src = "// a leading comment\npackage mypkg\n\nval x = 10\n"

	write := func(content string) string {
		path := filepath.Join(t.TempDir(), "src.gala")
		require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
		return path
	}

	plain := packageDeclLocation(write(src))
	bommed := packageDeclLocation(write(utf8BOM + src))

	require.NotNil(t, plain)
	require.NotNil(t, bommed)
	assert.Equal(t, 1, plain.Range.Start.Line, "package clause sits on the second line")
	assert.Equal(t, plain.Range.Start.Line, bommed.Range.Start.Line,
		"a leading BOM must not move the package clause")
}

// TestDidOpenStripsBOM pins the document-sync normalization: the cached text
// must match what the parser sees, or anything correlating the two by offset
// (signatureHelp) is three bytes out on a BOM-preserving client.
func TestDidOpenStripsBOM(t *testing.T) {
	const src = "package mypkg\n\nval x = 10\n"

	path := filepath.Join(t.TempDir(), "src.gala")
	require.NoError(t, os.WriteFile(path, []byte(utf8BOM+src), 0o644))
	uri := pathToURI(path)

	h := NewGalaHandler()
	require.NoError(t, h.DidOpen(context.Background(), &lsp.DidOpenTextDocumentParams{
		TextDocument: lsp.TextDocumentItem{
			URI:        lsp.DocumentURI(uri),
			LanguageID: "gala",
			Text:       utf8BOM + src,
		},
	}))

	h.mu.Lock()
	got := h.documents[uri]
	h.mu.Unlock()

	assert.Equal(t, src, got, "the cached document must match the text the parser sees")
}
