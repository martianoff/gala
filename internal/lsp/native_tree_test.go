package lsp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/owenrumney/go-lsp/lsp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bazelbuild/rules_go/go/tools/bazel"

	"martianoff/gala/galaerr"
	"martianoff/gala/internal/parser"
	"martianoff/gala/internal/transpiler/analyzer"
)

// nativeCorpus is the GALA sources staged as the test's data, sorted, with
// their text as the handler holds it (BOM-stripped), and the search path that
// resolves their imports.
func nativeCorpus(t *testing.T) (map[string]string, []string, string) {
	t.Helper()
	stdFile, err := bazel.Runfile("std/option.gala")
	require.NoError(t, err, "the std sources are not staged")
	files, err := bazel.ListRunfiles()
	require.NoError(t, err)
	texts := map[string]string{}
	var paths []string
	for _, f := range files {
		if !strings.HasSuffix(f.ShortPath, ".gala") {
			continue
		}
		if info, err := os.Stat(f.Path); err != nil || !info.Mode().IsRegular() {
			continue
		}
		data, err := os.ReadFile(f.Path)
		require.NoError(t, err)
		texts[f.Path] = galaerr.StripBOM(string(data))
		paths = append(paths, f.Path)
	}
	sort.Strings(paths)
	require.Greater(t, len(paths), 300, "the corpus is not staged")
	return texts, paths, filepath.Dir(filepath.Dir(stdFile))
}

// callCursors is the byte offsets in text signature help is asked at: on and
// after every '(', ',' and ')', and every 61st byte.
func callCursors(text string) []int {
	var out []int
	for i := 0; i <= len(text); i++ {
		switch {
		case i < len(text) && strings.IndexByte("(,)", text[i]) >= 0:
			out = append(out, i, i+1)
		case i%61 == 0:
			out = append(out, i)
		}
	}
	return out
}

// TestSignatureCallTreesAgree finds the call at up to 150 cursors of callCursors in
// every clean file of the corpus on both trees: findCallAtOffset on ANTLR's
// and findCallAtOffsetNative on the native one must find the same call. Each
// file's native tree is the one callAtOffset reads (it bridges), so each case
// is a read of the native tree.
func TestSignatureCallTreesAgree(t *testing.T) {
	texts, paths, _ := nativeCorpus(t)
	p := parser.NewAntlrGalaParser()
	files, cases, calls := 0, 0, 0
	for _, path := range paths {
		text := texts[path]
		parsed := p.ParseFile(text)
		handle := parsed.CleanNative()
		if handle == nil {
			continue // a parse with errors (or none) is ANTLR's alone
		}
		require.True(t, handle.Bridged(), "%s: the native tree does not bridge", path)
		files++
		for _, off := range spread(callCursors(text), 150) {
			want := findCallAtOffset(parsed.Tree, text, off)
			got, onNative := callAtOffset(parsed.Tree, handle, text, off)
			require.True(t, onNative, "%s@%d: callAtOffset read ANTLR's tree", path, off)
			require.Equal(t, want, got, "%s@%d", path, off)
			cases++
			if want != nil {
				calls++
			}
		}
	}
	t.Logf("%d files, %d cursors, %d in a call", files, cases, calls)
	assert.Greater(t, files, 300)
	assert.Greater(t, calls, 10000)
}

// TestDotImportTreesAgree reads every clean file's dot imports on both trees.
func TestDotImportTreesAgree(t *testing.T) {
	texts, paths, _ := nativeCorpus(t)
	p := parser.NewAntlrGalaParser()
	files, dotting := 0, 0
	for _, path := range paths {
		parsed := p.ParseFile(texts[path])
		handle := parsed.CleanNative()
		if handle == nil {
			continue
		}
		// Hover reads the native tree only once it is converted.
		_, onNative := dotImportPathsOf(parsed.Tree, handle)
		require.False(t, onNative, "%s: read the native tree before it converted", path)
		_, err := handle.Tree()
		require.NoError(t, err)
		got, onNative := dotImportPathsOf(parsed.Tree, handle)
		require.True(t, onNative, "%s: read ANTLR's tree", path)
		want := analyzer.DotImportPaths(parsed.Tree)
		require.Equal(t, want, got, path)
		files++
		if len(want) > 0 {
			dotting++
		}
	}
	t.Logf("%d files, %d with dot imports", files, dotting)
	assert.Greater(t, files, 300)
	assert.Greater(t, dotting, 5)
}

// treeReads records a handler's reads of a document's tree (onTreeRead).
type treeReads struct{ native, antlr int }

func (r *treeReads) note(_ string, onNative bool) {
	if onNative {
		r.native++
	} else {
		r.antlr++
	}
}

// TestHandlerTreesAgree serves the same requests from two handlers, one that
// keeps each parse's native tree and one on ANTLR's tree alone: signature help
// at the cursors of callCursors and hover at the start of every identifier,
// on a sample of the corpus. The responses, positions in UTF-16 columns
// included, must be the same, and every read of the first handler must take
// the native tree and every read of the second ANTLR's.
func TestHandlerTreesAgree(t *testing.T) {
	texts, paths, root := nativeCorpus(t)
	sample := handlerSample(t, texts, paths)
	var nativeReads, antlrReads treeReads
	newHandler := func(antlrOnly bool, reads *treeReads) *GalaHandler {
		h := NewGalaHandler()
		h.SetSearchPaths([]string{root})
		h.antlrTreeOnly = antlrOnly
		h.onTreeRead = reads.note
		return h
	}
	onNative, onANTLR := newHandler(false, &nativeReads), newHandler(true, &antlrReads)
	ctx := context.Background()
	sigCases, hoverCases, answered := 0, 0, 0
	for _, path := range sample {
		text := texts[path]
		uri := lsp.DocumentURI("file:///" + filepath.ToSlash(path))
		doc := lsp.TextDocumentIdentifier{URI: uri}
		for _, h := range []*GalaHandler{onNative, onANTLR} {
			require.NoError(t, h.DidOpen(ctx, &lsp.DidOpenTextDocumentParams{TextDocument: lsp.TextDocumentItem{URI: uri, LanguageID: "gala", Text: text}}))
		}
		// ask serves one request from both handlers and checks that they
		// answer the same, and that each read the tree it should.
		ask := func(what string, off int, serve func(h *GalaHandler, at lsp.Position) (any, error)) {
			at := wirePosition(text, off)
			n0, a0 := nativeReads, antlrReads
			got, gotErr := serve(onNative, at)
			want, wantErr := serve(onANTLR, at)
			where := fmt.Sprintf("%s %s@%d:%d", what, path, at.Line, at.Character)
			require.Equal(t, wantErr, gotErr, where)
			require.Equal(t, jsonOf(t, want), jsonOf(t, got), where)
			require.Zero(t, nativeReads.antlr-n0.antlr, "%s: the native handler read ANTLR's tree", where)
			require.Zero(t, antlrReads.native-a0.native, "%s: the ANTLR handler read the native tree", where)
			require.Equal(t, antlrReads.antlr-a0.antlr, nativeReads.native-n0.native, "%s: the handlers read the tree a different number of times", where)
			if jsonOf(t, want) != "null" {
				answered++
			}
		}
		for _, off := range spread(callCursors(text), 40) {
			ask("signature help", off, func(h *GalaHandler, at lsp.Position) (any, error) {
				return h.SignatureHelp(ctx, &lsp.SignatureHelpParams{TextDocumentPositionParams: lsp.TextDocumentPositionParams{TextDocument: doc, Position: at}})
			})
			sigCases++
		}
		for _, off := range spread(identifierStarts(text), 60) {
			ask("hover", off, func(h *GalaHandler, at lsp.Position) (any, error) {
				return h.Hover(ctx, &lsp.HoverParams{TextDocumentPositionParams: lsp.TextDocumentPositionParams{TextDocument: doc, Position: at}})
			})
			hoverCases++
		}
	}
	t.Logf("%d files, %d signature-help and %d hover requests, %d answered; native handler: %+v", len(sample), sigCases, hoverCases, answered, nativeReads)
	assert.Greater(t, answered, 200)
	assert.Greater(t, nativeReads.native, 300)
}

// handlerSample is the files TestHandlerTreesAgree opens: the largest clean
// files, and the files with a dot import or non-ASCII text, a few of each.
func handlerSample(t *testing.T, texts map[string]string, paths []string) []string {
	t.Helper()
	p := parser.NewAntlrGalaParser()
	var clean, dotting, wide []string
	for _, path := range paths {
		parsed := p.ParseFile(texts[path])
		if parsed.CleanNative() == nil || !strings.Contains(texts[path], "(") {
			continue
		}
		clean = append(clean, path)
		if len(analyzer.DotImportPaths(parsed.Tree)) > 0 {
			dotting = append(dotting, path)
		}
		if strings.ContainsFunc(texts[path], func(r rune) bool { return r > 0x7F }) {
			wide = append(wide, path)
		}
	}
	sort.SliceStable(clean, func(i, j int) bool { return len(texts[clean[i]]) > len(texts[clean[j]]) })
	seen := map[string]bool{}
	var out []string
	for _, group := range [][]string{clean[:min(6, len(clean))], spread(dotting, 4), spread(wide, 4)} {
		for _, path := range group {
			if !seen[path] {
				seen[path] = true
				out = append(out, path)
			}
		}
	}
	require.NotEmpty(t, dotting, "no file of the corpus dot-imports")
	require.NotEmpty(t, wide, "no file of the corpus has non-ASCII text")
	return out
}

// spread is at most n of xs, evenly spaced, in order.
func spread[T any](xs []T, n int) []T {
	if len(xs) <= n {
		return xs
	}
	out := make([]T, 0, n)
	for i := range n {
		out = append(out, xs[i*len(xs)/n])
	}
	return out
}

// identifierStarts is the byte offset of every identifier's first byte in
// text.
func identifierStarts(text string) []int {
	var out []int
	for i := 0; i < len(text); i++ {
		c := text[i]
		isStart := c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
		if isStart && (i == 0 || !isIdentChar(text[i-1])) {
			out = append(out, i)
		}
	}
	return out
}

// wirePosition is byte offset off of text as an LSP position, its column in
// UTF-16 code units (the default encoding).
func wirePosition(text string, off int) lsp.Position {
	line := strings.Count(text[:off], "\n")
	start := strings.LastIndexByte(text[:off], '\n') + 1
	end := strings.IndexByte(text[start:], '\n')
	if end < 0 {
		end = len(text) - start
	}
	return lsp.Position{Line: line, Character: byteToWire(text[start:start+end], off-start, lsp.PositionEncodingUTF16)}
}

func jsonOf(t *testing.T, v any) string {
	t.Helper()
	data, err := json.Marshal(v)
	require.NoError(t, err)
	return string(data)
}
