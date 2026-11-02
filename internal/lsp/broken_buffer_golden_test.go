package lsp

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/owenrumney/go-lsp/lsp"
	"github.com/stretchr/testify/require"

	"martianoff/gala/internal/digestgolden"
	"martianoff/gala/internal/parser"
	"martianoff/gala/internal/testcorpus"
	"martianoff/gala/internal/transpiler/analyzer"
)

// The broken-buffer golden holds what the language server answers on buffers
// with syntax errors to what it answered when it analyzed their
// error-recovered trees on ANTLR's tree: the diagnostics of each buffer, and
// hover, signature help and completion at cursors through it, in that order
// (a request may analyze a patched copy of the buffer and cache it, as in an
// editor, so the order is part of the answer).
//
// The buffers are files of the frozen corpus (internal/testcorpus), broken.
// Each line is `key<TAB>input<TAB>answers`: the buffer's name, a digest of
// its text and a digest of the answers, as JSON with the corpus root
// rewritten. A buffer the golden does not list, one whose text changed and a
// line no buffer has fail the test. To regenerate the golden, run the test with
// GALA_UPDATE_BROKEN_BUFFER_GOLDEN set to its path in the source tree:
//
//	bazel test //internal/lsp:lsp_native_test --test_filter=TestBrokenBufferGolden \
//	  --test_env=GALA_UPDATE_BROKEN_BUFFER_GOLDEN=$PWD/internal/lsp/testdata/broken_buffers.golden
//
// A change of the golden is a change of what an editor shows: review it as
// one.
const brokenBufferGolden = "internal/lsp/testdata/broken_buffers.golden"

// breakings are the ways a buffer is broken, as an edit in progress breaks
// it: each returns the broken text, or false when it does not apply.
var breakings = []struct {
	name  string
	apply func(text string) (string, bool)
}{
	{"unclosed call", func(text string) (string, bool) {
		// The middle ')' of the buffer is dropped.
		var at []int
		for i := range len(text) {
			if text[i] == ')' {
				at = append(at, i)
			}
		}
		if len(at) == 0 {
			return "", false
		}
		i := at[len(at)/2]
		return text[:i] + text[i+1:], true
	}},
	{"truncated", func(text string) (string, bool) {
		// Cut at the start of the line at 60% of the buffer: its blocks are
		// left open.
		i := strings.LastIndexByte(text[:len(text)*3/5], '\n')
		if i < 0 {
			return "", false
		}
		return text[:i+1], true
	}},
	{"half-typed declaration", func(text string) (string, bool) {
		// A statement being typed in the second function body.
		i := -1
		for range 2 {
			j := strings.Index(text[i+1:], "{\n")
			if j < 0 {
				return "", false
			}
			i += j + 1
		}
		return text[:i+2] + "    val = \n" + text[i+2:], true
	}},
	{"member being typed", func(text string) (string, bool) {
		// `xs.` with the member not typed yet, after the first identifier
		// of the line at 40% of the buffer.
		start := strings.LastIndexByte(text[:len(text)*2/5], '\n') + 1
		end := strings.IndexByte(text[start:], '\n')
		if end < 0 {
			return "", false
		}
		line := text[start : start+end]
		trimmed := strings.TrimLeft(line, " \t")
		ident := 0
		for ident < len(trimmed) && isIdentChar(trimmed[ident]) {
			ident++
		}
		if ident == 0 {
			return "", false
		}
		cut := start + len(line) - len(trimmed) + ident
		return text[:cut] + "." + text[cut:], true
	}},
}

// TestBrokenBufferGolden opens a sample of the corpus broken each way of
// breakings, and holds the server's answers on it to the golden.
func TestBrokenBufferGolden(t *testing.T) {
	parser.RequestEngine(string(parser.EngineGalop))
	t.Cleanup(func() { parser.RequestEngine("") })
	c := testcorpus.Extract(t)
	paths := c.GalaFiles()
	texts := map[string]string{}
	for _, path := range paths {
		texts[path] = testcorpus.Files()[c.Rel(path)]
	}
	sample := brokenBufferSample(t, texts, paths)
	g := digestgolden.Open(t, brokenBufferGolden, "GALA_UPDATE_BROKEN_BUFFER_GOLDEN")
	h := NewGalaHandler()
	h.SetSearchPaths([]string{c.Root})
	ctx := context.Background()
	norm := c.Normalizer()
	p := parser.NewAntlrGalaParser()
	buffers, answered := 0, 0
	start := time.Now()
	for _, path := range sample {
		rel := c.Rel(path)
		uri := lsp.DocumentURI("file:///" + filepath.ToSlash(path))
		doc := lsp.TextDocumentIdentifier{URI: uri}
		for _, b := range breakings {
			// LF line ends, so that the cursors' byte offsets do not depend
			// on the checkout.
			text, ok := b.apply(digestgolden.NormalizeNewlines(texts[path]))
			if !ok || p.ParseFile(text).Err() == nil {
				continue // still parses: not a broken buffer
			}
			buffers++
			var answers strings.Builder
			record := func(what string, off int, v any) {
				js := jsonOf(t, v)
				if js != "null" && js != "[]" {
					answered++
				}
				fmt.Fprintf(&answers, "%s@%d %s\n", what, off, norm.Replace(js))
			}
			require.NoError(t, h.DidOpen(ctx, &lsp.DidOpenTextDocumentParams{TextDocument: lsp.TextDocumentItem{URI: uri, LanguageID: "gala", Text: text}}))
			// The diagnostics DidOpen published, as analyzeFile gives them.
			record("diagnostics", 0, h.analyzeFile(string(uri), uriToPath(string(uri)), text))
			for _, off := range spread(identifierStarts(text), 20) {
				at := wirePosition(text, off)
				res, err := h.Hover(ctx, &lsp.HoverParams{TextDocumentPositionParams: lsp.TextDocumentPositionParams{TextDocument: doc, Position: at}})
				record("hover", off, []any{res, err})
			}
			for _, off := range spread(callCursors(text), 15) {
				at := wirePosition(text, off)
				res, err := h.SignatureHelp(ctx, &lsp.SignatureHelpParams{TextDocumentPositionParams: lsp.TextDocumentPositionParams{TextDocument: doc, Position: at}})
				record("signature help", off, []any{res, err})
			}
			for _, off := range spread(completionCursors(text), 6) {
				at := wirePosition(text, off)
				res, err := h.Completion(ctx, &lsp.CompletionParams{TextDocumentPositionParams: lsp.TextDocumentPositionParams{TextDocument: doc, Position: at}})
				if res != nil {
					// The server gathers some items from maps, so their order
					// is not part of the answer: an editor sorts them itself.
					slices.SortStableFunc(res.Items, func(a, b lsp.CompletionItem) int {
						return strings.Compare(jsonOf(t, a), jsonOf(t, b))
					})
				}
				record("completion", off, []any{res, err})
			}
			require.NoError(t, h.DidClose(ctx, &lsp.DidCloseTextDocumentParams{TextDocument: doc}))
			t.Logf("%s: %s after %s", rel, b.name, time.Since(start).Round(time.Millisecond))

			key := rel + ": " + b.name
			g.Check(key, digestgolden.Digest(text), digestgolden.Digest(answers.String()), answers.String)
		}
	}
	g.Finish("The language server's answers on broken buffers; see brokenBufferGolden in broken_buffer_golden_test.go.")
	if g.Updating() {
		return
	}
	t.Logf("%d broken buffers, %d of them held to the golden, %d answers", buffers, g.Compared, answered)
	require.Greater(t, buffers, 20)
	require.Greater(t, answered, 200)
}

// completionCursors is the byte offsets in text completion is asked at:
// after every '.', and every 97th byte.
func completionCursors(text string) []int {
	var out []int
	for i := 0; i < len(text); i++ {
		if text[i] == '.' || i%97 == 0 {
			out = append(out, i+1)
		}
	}
	return out
}

// brokenBufferSample is the files TestBrokenBufferGolden opens: the largest
// clean files, and the files with a dot import or non-ASCII text, a few of each.
func brokenBufferSample(t *testing.T, texts map[string]string, paths []string) []string {
	t.Helper()
	p := parser.NewAntlrGalaParser()
	var clean, dotting, wide []string
	for _, path := range paths {
		nf := p.ParseFile(texts[path]).CleanNative()
		if nf == nil || !strings.Contains(texts[path], "(") {
			continue
		}
		clean = append(clean, path)
		tree, err := nf.Tree()
		require.NoError(t, err, path)
		if len(analyzer.DotImportPathsNative(tree.Root())) > 0 {
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
