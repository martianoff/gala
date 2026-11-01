package lsp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
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
	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/analyzer"
)

// antlrOnlyParser parses as its GalaParser does but drops each parse's
// native-tree handle, so that a handler parsing with it analyzes,
// transforms and answers on ANTLR's tree alone, its documents and the files
// their analysis reads alike.
type antlrOnlyParser struct{ transpiler.GalaParser }

func (p antlrOnlyParser) ParseFile(input string) *parser.Parsed {
	parsed := p.GalaParser.ParseFile(input)
	parsed.Native = nil
	return parsed
}

// nativeCorpus is the GALA sources staged as the test's data, sorted, with
// their text as the handler holds it (BOM-stripped) and with LF line endings,
// so that a checkout with CRLF endings asks the same requests at the same
// places, and the search path that resolves their imports.
func nativeCorpus(t *testing.T) (map[string]string, []string, string) {
	t.Helper()
	// Parse with galop's engine, the CLI's default, whatever the build's or
	// environment's default engine.
	parser.RequestEngine(string(parser.EngineGalop))
	t.Cleanup(func() { parser.RequestEngine("") })
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
		texts[f.Path] = strings.ReplaceAll(galaerr.StripBOM(string(data)), "\r\n", "\n")
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

// updateHandlerGolden rewrites handlerGolden instead of checking it. Run the
// test with --test_arg=-update and
// --test_env=BUILD_WORKSPACE_DIRECTORY=<the workspace>.
var updateHandlerGolden = flag.Bool("update", false, "rewrite "+handlerGolden)

// handlerGolden holds TestHandlerAnswers' answers: a line per request, its
// file relative to the corpus root, what it asked and where, and a digest of
// the answer's JSON. It was written from the code on which the native handler
// still answered every one of these requests as a handler on ANTLR's tree alone.
const handlerGolden = "testdata/handler_answers.golden"

// TestHandlerAnswers serves signature help at the cursors of callCursors and
// hover at the start of every identifier, on a sample of the corpus, and
// holds each answer, positions in UTF-16 columns included, to its digest in
// handlerGolden. Every read of the handler must take the native tree. Hover
// reads the native tree only once something has converted it
// (dotImportPathsOf), so each file's hovers come after its signature-help
// requests, which convert the document's cached parse.
func TestHandlerAnswers(t *testing.T) {
	texts, paths, root := nativeCorpus(t)
	sample := handlerSample(t, texts, paths, root)
	var reads treeReads
	h := NewGalaHandler()
	h.SetSearchPaths([]string{root})
	h.onTreeRead = reads.note
	want, wantLines := map[string]string{}, 0
	if !*updateHandlerGolden {
		data, err := os.ReadFile(handlerGolden)
		require.NoError(t, err, "no golden: run the test with --test_arg=-update")
		for _, l := range strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n") {
			if where, digest, ok := strings.Cut(l, "\t=> "); ok {
				want[where] = digest
				wantLines++
			}
		}
	}
	var golden strings.Builder
	ctx := context.Background()
	sigCases, hoverCases, answered := 0, 0, 0
	for _, path := range sample {
		text := texts[path]
		uri := lsp.DocumentURI("file:///" + filepath.ToSlash(path))
		doc := lsp.TextDocumentIdentifier{URI: uri}
		require.NoError(t, h.DidOpen(ctx, &lsp.DidOpenTextDocumentParams{TextDocument: lsp.TextDocumentItem{URI: uri, LanguageID: "gala", Text: text}}))
		rel, err := filepath.Rel(root, path)
		require.NoError(t, err)
		// ask serves one request and holds its answer to its golden.
		ask := func(what string, off int, serve func(h *GalaHandler, at lsp.Position) (any, error)) {
			at := wirePosition(text, off)
			r0 := reads
			got, gotErr := serve(h, at)
			where := fmt.Sprintf("%s\t%s@%d:%d", what, filepath.ToSlash(rel), at.Line, at.Character)
			answer := jsonOf(t, got)
			if gotErr != nil {
				answer = "error: " + gotErr.Error()
			}
			sum := sha256.Sum256([]byte(answer))
			digest := hex.EncodeToString(sum[:8])
			fmt.Fprintf(&golden, "%s\t=> %s\n", where, digest)
			if !*updateHandlerGolden {
				assert.Equalf(t, want[where], digest, "%s: the answer differs from its golden: %s", where, answer)
			}
			require.Zero(t, reads.antlr-r0.antlr, "%s: the handler read ANTLR's tree", where)
			if answer != "null" {
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
	if *updateHandlerGolden {
		ws := os.Getenv("BUILD_WORKSPACE_DIRECTORY")
		require.NotEmpty(t, ws, "-update writes into the workspace: pass --test_env=BUILD_WORKSPACE_DIRECTORY")
		dest := filepath.Join(ws, "internal", "lsp", filepath.FromSlash(handlerGolden))
		require.NoError(t, os.MkdirAll(filepath.Dir(dest), 0o755))
		require.NoError(t, os.WriteFile(dest, []byte(golden.String()), 0o644))
	} else {
		assert.Equal(t, wantLines, sigCases+hoverCases, "the golden holds requests the test no longer asks")
	}
	t.Logf("%d files, %d signature-help and %d hover requests, %d answered; reads: %+v", len(sample), sigCases, hoverCases, answered, reads)
	assert.Greater(t, answered, 200)
	assert.Greater(t, reads.native, 300)
}

// handlerSample is the files TestHandlerAnswers opens (handlerFiles): every
// one staged and parsing cleanly, with a dot import and non-ASCII text among them.
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
		require.Truef(t, ok, "%s is not staged", rel)
		parsed := p.ParseFile(texts[path])
		require.NotNilf(t, parsed.CleanNative(), "%s does not parse cleanly", rel)
		if len(analyzer.DotImportPaths(parsed.Tree)) > 0 {
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
// large library files, which every platform stages, with dot imports and
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
// transformed on its native tree, a buffer with a syntax error is analyzed
// on ANTLR's error-recovered tree and not transformed, and a handler on
// ANTLR's tree alone analyzes and transforms the clean buffer on ANTLR's
// tree. A collection_immutable file is opened, so its siblings are read
// too.
func TestBufferAnalysisTrees(t *testing.T) {
	_, _, root := nativeCorpus(t)
	path := filepath.Join(root, "collection_immutable", "array.gala")
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	clean := galaerr.StripBOM(string(data))
	broken := strings.Replace(clean, "func ", "func (", 1)
	uri := lsp.DocumentURI("file:///" + filepath.ToSlash(path))
	for _, c := range []struct {
		name      string
		antlrOnly bool
		text      string
		want      map[string]bool
	}{
		{"clean", false, clean, map[string]bool{treeReadAnalysis: true, treeReadTransform: true}},
		{"syntax error", false, broken, map[string]bool{treeReadAnalysis: false}},
		{"clean, ANTLR's tree alone", true, clean, map[string]bool{treeReadAnalysis: false, treeReadTransform: false}},
	} {
		h := NewGalaHandler()
		h.SetSearchPaths([]string{root})
		if c.antlrOnly {
			h.parser = antlrOnlyParser{h.parser}
		}
		got := map[string]bool{}
		h.onTreeRead = func(what string, onNative bool) {
			if prev, seen := got[what]; seen && prev != onNative {
				t.Errorf("%s: the reads of %s took different trees", c.name, what)
			}
			got[what] = onNative
		}
		require.NoError(t, h.DidOpen(context.Background(), &lsp.DidOpenTextDocumentParams{TextDocument: lsp.TextDocumentItem{URI: uri, LanguageID: "gala", Text: c.text}}))
		assert.Equal(t, c.want, got, "%s: the trees the analysis and the transform read", c.name)
	}
}
