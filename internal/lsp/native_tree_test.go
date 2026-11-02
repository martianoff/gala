package lsp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/owenrumney/go-lsp/lsp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"martianoff/gala/galaerr"
	"martianoff/gala/internal/digestgolden"
	"martianoff/gala/internal/parser"
	"martianoff/gala/internal/testcorpus"
	"martianoff/gala/internal/transpiler"
)

// nativeCorpus is the frozen corpus (internal/testcorpus), extracted: its
// .gala files in the order of their names, with their text as the handler
// holds it (BOM-stripped, LF line ends), and its root, the search path that
// resolves their imports.
func nativeCorpus(t *testing.T) (map[string]string, []string, string) {
	t.Helper()
	// Parse with galop's engine, the CLI's default, whatever the build's or
	// environment's default engine.
	parser.RequestEngine(string(parser.EngineGalop))
	t.Cleanup(func() { parser.RequestEngine("") })
	c := testcorpus.Extract(t)
	paths := c.GalaFiles()
	texts := make(map[string]string, len(paths))
	for _, path := range paths {
		texts[path] = galaerr.StripBOM(testcorpus.Files()[c.Rel(path)])
	}
	return texts, paths, c.Root
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

// handlerGolden holds TestHandlerAnswers' answers: a line per file of the
// sample, its path relative to the corpus root, a digest of its text and a
// digest of the answers to the requests on it, as JSON with the corpus root
// rewritten. It was written from the code on which the handler still read
// ANTLR's tree too, and answered on the native tree as on ANTLR's. A file the
// golden does not list, one whose text changed and a line no file has fail
// the test. To regenerate the golden, run the test with
// GALA_UPDATE_HANDLER_GOLDEN set to its path in the source tree:
//
//	bazel test //internal/lsp:lsp_native_test --test_filter=TestHandlerAnswers \
//	  --test_env=GALA_UPDATE_HANDLER_GOLDEN=$PWD/internal/lsp/testdata/handler_answers.golden
const handlerGolden = "internal/lsp/testdata/handler_answers.golden"

// TestHandlerAnswers serves signature help at the cursors of callCursors and
// hover at the start of every identifier, on a sample of the corpus opened in
// one handler, in that order, and holds each file's answers, positions in
// UTF-16 columns included, to its digest in handlerGolden.
func TestHandlerAnswers(t *testing.T) {
	texts, paths, root := nativeCorpus(t)
	c := testcorpus.Corpus{Root: root}
	norm := c.Normalizer()
	sample := handlerSample(t, texts, paths, root)
	g := digestgolden.Open(t, handlerGolden, "GALA_UPDATE_HANDLER_GOLDEN")
	h := NewGalaHandler()
	h.SetSearchPaths([]string{root})
	ctx := context.Background()
	sigCases, hoverCases, answered := 0, 0, 0
	for _, path := range sample {
		text := texts[path]
		uri := lsp.DocumentURI("file:///" + filepath.ToSlash(path))
		doc := lsp.TextDocumentIdentifier{URI: uri}
		require.NoError(t, h.DidOpen(ctx, &lsp.DidOpenTextDocumentParams{TextDocument: lsp.TextDocumentItem{URI: uri, LanguageID: "gala", Text: text}}))
		var answers strings.Builder
		// ask serves one request and records its answer.
		ask := func(what string, off int, serve func(at lsp.Position) (any, error)) {
			at := wirePosition(text, off)
			got, gotErr := serve(at)
			answer := jsonOf(t, got)
			if gotErr != nil {
				answer = "error: " + gotErr.Error()
			}
			if answer != "null" {
				answered++
			}
			fmt.Fprintf(&answers, "%s@%d:%d %s\n", what, at.Line, at.Character, norm.Replace(answer))
		}
		for _, off := range spread(callCursors(text), 40) {
			ask("signature help", off, func(at lsp.Position) (any, error) {
				return h.SignatureHelp(ctx, &lsp.SignatureHelpParams{TextDocumentPositionParams: lsp.TextDocumentPositionParams{TextDocument: doc, Position: at}})
			})
			sigCases++
		}
		for _, off := range spread(identifierStarts(text), 60) {
			ask("hover", off, func(at lsp.Position) (any, error) {
				return h.Hover(ctx, &lsp.HoverParams{TextDocumentPositionParams: lsp.TextDocumentPositionParams{TextDocument: doc, Position: at}})
			})
			hoverCases++
		}
		got := answers.String()
		g.Check(c.Rel(path), digestgolden.Digest(text), digestgolden.Digest(got), func() string { return got })
	}
	g.Finish("TestHandlerAnswers: per file of the sample, a digest of its text and of the handler's answers on it; see handlerGolden in native_tree_test.go.")
	t.Logf("%d files, %d signature-help and %d hover requests, %d answered", len(sample), sigCases, hoverCases, answered)
	assert.Greater(t, answered, 200)
}

// handlerSample is the files TestHandlerAnswers opens (handlerFiles): every
// one in the corpus and parsing cleanly, with a dot import and non-ASCII text
// among them.
func handlerSample(t *testing.T, texts map[string]string, paths []string, root string) []string {
	t.Helper()
	byRel := map[string]string{}
	for _, path := range paths {
		if rel, err := filepath.Rel(root, path); err == nil {
			byRel[filepath.ToSlash(rel)] = path
		}
	}
	p := parser.NewAntlrGalaParser()
	var out []string
	dotting, wide := 0, 0
	for _, rel := range handlerFiles {
		path, ok := byRel[rel]
		require.Truef(t, ok, "%s is not in the corpus", rel)
		handle := p.ParseFile(texts[path]).CleanNative()
		require.NotNilf(t, handle, "%s does not parse cleanly", rel)
		if len(dotImportPathsOf(handle)) > 0 {
			dotting++
		}
		if strings.ContainsFunc(texts[path], func(r rune) bool { return r > 0x7F }) {
			wide++
		}
		out = append(out, path)
	}
	require.Positive(t, dotting, "no file of the sample dot-imports")
	require.Positive(t, wide, "no file of the sample has non-ASCII text")
	return out
}

// handlerFiles is TestHandlerAnswers' sample, in the order it opens them:
// large library files of the corpus, with dot imports and
// non-ASCII text among them.
var handlerFiles = []string{
	"collection_immutable/array.gala",
	"collection_mutable/treemap.gala",
	"collection_immutable/treemap.gala",
	"concurrent/future.gala",
	"collection_mutable/treeset.gala",
	"collection_immutable/treeset.gala",
	"collection_immutable/list.gala",
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

// TestBufferAnalysisTrees: a buffer that parses cleanly is analyzed and
// transformed, and a buffer with a syntax error is analyzed on the native
// tree of its error-recovered tree and not transformed. Either way the
// document keeps, for the features that read it, the native tree of its own
// text alone (Native.TreeOnly), without the bridge to ANTLR's tree. A
// collection_immutable file is opened, so its siblings are read too.
func TestBufferAnalysisTrees(t *testing.T) {
	clean, broken, uri, root := bufferTexts(t)
	for _, c := range []struct {
		name        string
		text        string
		transformed bool
	}{
		{"clean", clean, true},
		{"syntax error", broken, false},
	} {
		h := NewGalaHandler()
		h.SetSearchPaths([]string{root})
		got := openBuffer(t, h, uri, c.text)
		require.NotNil(t, got.richAST, "%s: no analysis", c.name)
		assert.Contains(t, got.richAST.Types, "collection_immutable.Array", "%s: the analysis did not collect the file's types", c.name)
		assertTreeOf(t, c.name, got, c.text)
		assert.Equal(t, c.transformed, got.varTypes != nil, "%s: transformed", c.name)
	}
}

// TestBufferTreeFollowsEdits opens one document with a clean text, then a
// broken one, then the clean one again: after each, the document's analysis
// and native tree are the new text's, and no earlier text's tree is kept.
func TestBufferTreeFollowsEdits(t *testing.T) {
	clean, broken, uri, root := bufferTexts(t)
	h := NewGalaHandler()
	h.SetSearchPaths([]string{root})
	var prev bufferState
	for i, text := range []string{clean, broken, clean} {
		name := fmt.Sprintf("text %d", i)
		got := openBuffer(t, h, uri, text)
		require.NotNil(t, got.richAST, "%s: no analysis", name)
		assertTreeOf(t, name, got, text)
		assert.NotSame(t, prev.richAST, got.richAST, "%s: the analysis is an earlier text's", name)
		assert.NotSame(t, prev.handle, got.handle, "%s: the native tree is an earlier text's", name)
		prev = got
	}
}

// bufferTexts is collection_immutable/array.gala of the corpus, clean
// and with a syntax error, with the file's URI and the corpus root.
func bufferTexts(t *testing.T) (clean, broken string, uri lsp.DocumentURI, root string) {
	t.Helper()
	_, _, root = nativeCorpus(t)
	path := filepath.Join(root, "collection_immutable", "array.gala")
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	clean = galaerr.StripBOM(string(data))
	broken = strings.Replace(clean, "func ", "func (", 1)
	require.NotEqual(t, clean, broken)
	return clean, broken, lsp.DocumentURI("file:///" + filepath.ToSlash(path)), root
}

// bufferState is what a handler keeps of a document for its features.
type bufferState struct {
	richAST  *transpiler.RichAST
	handle   *parser.Native
	text     string // the text handle's tree was parsed from
	varTypes map[string]string
}

// openBuffer opens uri with text in h, which analyzes it before returning,
// and returns what h then keeps of it.
func openBuffer(t *testing.T, h *GalaHandler, uri lsp.DocumentURI, text string) bufferState {
	t.Helper()
	require.NoError(t, h.DidOpen(context.Background(), &lsp.DidOpenTextDocumentParams{TextDocument: lsp.TextDocumentItem{URI: uri, LanguageID: "gala", Text: text}}))
	h.mu.Lock()
	defer h.mu.Unlock()
	key := string(uri)
	return bufferState{h.richASTs[key], h.parseNatives[key], h.parseTexts[key], h.varTypes[key]}
}

// assertTreeOf holds the native tree got keeps to be text's, and its tree
// alone.
func assertTreeOf(t *testing.T, name string, got bufferState, text string) {
	t.Helper()
	require.NotNil(t, got.handle, "%s: no native tree kept", name)
	assert.False(t, got.handle.Bridged(), "%s: the bridge to ANTLR's tree is kept", name)
	assert.Equal(t, text, got.text, "%s: the kept tree's text", name)
	root, ok := got.handle.Root()
	require.True(t, ok, "%s: the kept tree has no root", name)
	// The root spans the text from its first token to its last: all of it
	// but the space and comments around them.
	src := nativeSourceText(root)
	assert.Contains(t, text, src, "%s: the kept tree is not of the text", name)
	assert.Greater(t, len(src), len(text)/2, "%s: the kept tree is not of the text", name)
}
