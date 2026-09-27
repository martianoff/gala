package lsp_test

import (
	"context"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf16"

	"github.com/bazelbuild/rules_go/go/tools/bazel"
	"github.com/owenrumney/go-lsp/lsp"
	"github.com/owenrumney/go-lsp/servertest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	lspserver "martianoff/gala/internal/lsp"
)

// Positions on the wire are counted in the negotiated encoding — UTF-16 code
// units unless client and server agree otherwise (LSP 3.17). These tests put
// non-ASCII text (a two-byte accented letter, a four-byte emoji that is a
// UTF-16 surrogate pair, three-byte CJK) ahead of identifiers on the same line,
// where byte, code-point and UTF-16 columns all disagree, and check every
// request and response in UTF-16 units.

// encodingSrc is the shared fixture. Line numbers are referenced below.
const encodingSrc = "package main\n" + // 0
	"\n" + // 1
	"func area(width int, height int) int = width * height\n" + // 2
	"\n" + // 3
	"/* é😀日 */ func perimeter(a int, b int) int = 2 * (a + b)\n" + // 4
	"\n" + // 5
	"type Box struct {\n" + // 6
	"\tval Width int\n" + // 7
	"}\n" + // 8
	"\n" + // 9
	"val greeting = \"é😀日本\"\n" + // 10
	"\n" + // 11
	"func main() {\n" + // 12
	"\tval total = area(2, 3)\n" + // 13
	"\tPrintln(\"é😀日本\", area(2, 3), total)\n" + // 14
	"\tPrintln(\"😀😀\", perimeter(1, 2))\n" + // 15
	"\tval b = Box(Width = 1)\n" + // 16
	"\tPrintln(\"é😀日本\", b.Width)\n" + // 17
	"\tPrintln(greeting)\n" + // 18
	"}\n" // 19

// u16 returns the UTF-16 column of the n-th (0-based) occurrence of needle on
// the given line of src, plus offset UTF-16 units.
func u16(t *testing.T, src string, line int, needle string, n int) int {
	t.Helper()
	l := strings.Split(src, "\n")[line]
	from := 0
	idx := -1
	for i := 0; i <= n; i++ {
		j := strings.Index(l[from:], needle)
		require.GreaterOrEqual(t, j, 0, "needle %q (occurrence %d) not on line %d: %q", needle, n, line, l)
		idx = from + j
		from = idx + len(needle)
	}
	return len(utf16.Encode([]rune(l[:idx])))
}

func openEncodingFixture(t *testing.T) (*servertest.Harness, lsp.DocumentURI) {
	t.Helper()
	h := newHarness(t)
	uri := openFileOnDisk(t, h, encodingSrc)
	return h, uri
}

func TestPositionEncoding_InitializeAdvertisesUTF16ByDefault(t *testing.T) {
	h := newHarness(t)
	enc := h.InitResult.Capabilities.PositionEncoding
	require.NotNil(t, enc, "the server must state the encoding it uses")
	assert.Equal(t, lsp.PositionEncodingUTF16, *enc)
}

func TestPositionEncoding_HoverAfterNonASCII(t *testing.T) {
	h, uri := openEncodingFixture(t)

	cases := []struct {
		name   string
		line   int
		needle string
		want   string
	}{
		{"call after emoji string", 14, "area", "func area"},
		{"local after emoji string", 14, "total", "total"},
		{"declaration after block comment", 4, "perimeter", "func perimeter"},
		{"field after emoji string", 17, "Width", "Width"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			col := u16(t, encodingSrc, tc.line, tc.needle, 0) + 1
			hover, err := h.Hover(uri, tc.line, col)
			require.NoError(t, err)
			require.NotNil(t, hover, "no hover at %d:%d", tc.line, col)
			assert.Contains(t, hover.Contents.Value(), tc.want)
		})
	}
}

func TestPositionEncoding_DefinitionAfterNonASCII(t *testing.T) {
	h, uri := openEncodingFixture(t)

	cases := []struct {
		name     string
		line     int
		needle   string
		wantLine int
		wantCol  int
	}{
		// The request position is in UTF-16 after non-ASCII text.
		{"request after emoji", 14, "area", 2, u16(t, encodingSrc, 2, "area", 0)},
		// The response position is in UTF-16 after non-ASCII text: the
		// analyzer records code-point columns, which differ from UTF-16 by one
		// for every astral-plane character (the emoji).
		{"response after emoji", 15, "perimeter", 4, u16(t, encodingSrc, 4, "perimeter", 0)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			col := u16(t, encodingSrc, tc.line, tc.needle, 0) + 1
			locs, err := h.Definition(uri, tc.line, col)
			require.NoError(t, err)
			require.NotEmpty(t, locs)
			assert.Equal(t, tc.wantLine, locs[0].Range.Start.Line)
			assert.Equal(t, tc.wantCol, locs[0].Range.Start.Character)
			assert.Equal(t, tc.wantCol+len(tc.needle), locs[0].Range.End.Character)
		})
	}
}

func TestPositionEncoding_ReferencesAfterNonASCII(t *testing.T) {
	h, uri := openEncodingFixture(t)

	locs, err := h.References(uri, 13, u16(t, encodingSrc, 13, "total", 0)+1, true)
	require.NoError(t, err)
	var onLine14 []lsp.Range
	for _, l := range locs {
		if l.URI == uri && l.Range.Start.Line == 14 {
			onLine14 = append(onLine14, l.Range)
		}
	}
	require.Len(t, onLine14, 1)
	want := u16(t, encodingSrc, 14, "total", 0)
	assert.Equal(t, want, onLine14[0].Start.Character)
	assert.Equal(t, want+len("total"), onLine14[0].End.Character)
}

func TestPositionEncoding_CompletionAfterNonASCII(t *testing.T) {
	h := newHarness(t)
	src := strings.Replace(encodingSrc, "b.Width)", "b.)", 1)
	uri := openFileOnDisk(t, h, src)

	col := u16(t, src, 17, "b.", 0) + 2 // right after the dot
	list, err := h.Completion(uri, 17, col)
	require.NoError(t, err)
	require.NotNil(t, list)
	assert.True(t, collectLabels(list)["Width"], "member completion after non-ASCII text, got %v", labelSlice(list))
}

func TestPositionEncoding_SignatureHelpAfterNonASCII(t *testing.T) {
	h, uri := openEncodingFixture(t)
	time.Sleep(200 * time.Millisecond)

	// Inside the second argument of area(...) on a line that starts with
	// non-ASCII text.
	col := u16(t, encodingSrc, 14, "3)", 0)
	sh := requestSignatureHelp(t, h, uri, 14, col)
	require.NotNil(t, sh)
	require.Len(t, sh.Signatures, 1)
	assert.Contains(t, sh.Signatures[0].Label, "area")
	assert.Equal(t, 1, activeParam(sh))
}

// Signature help walks the parse tree, whose token offsets count code points
// from the start of the file — so non-ASCII text on an EARLIER line moved the
// call it found even when the caret's own line was plain ASCII.
func TestPositionEncoding_SignatureHelpAfterNonASCIIEarlierInFile(t *testing.T) {
	h := newHarness(t)
	src := "package main\n" +
		"\n" +
		"// café 😀 日本語 — an ordinary comment with non-ASCII text\n" +
		"func greet(name string, times int) string = name\n" +
		"\n" +
		"func main() {\n" +
		"    greet(\"world\", 2)\n" +
		"}\n"
	uri := openFileOnDisk(t, h, src)
	time.Sleep(200 * time.Millisecond)

	sh := requestSignatureHelp(t, h, uri, 6, u16(t, src, 6, "2)", 0))
	require.NotNil(t, sh)
	require.Len(t, sh.Signatures, 1)
	assert.Contains(t, sh.Signatures[0].Label, "greet")
	assert.Equal(t, 1, activeParam(sh))
}

func TestPositionEncoding_DiagnosticAfterNonASCII(t *testing.T) {
	h := newHarness(t)
	src := "package main\n" +
		"\n" +
		"func main() {\n" +
		"\tPrintln(\"é😀日\", 1 2)\n" +
		"}\n"
	uri := openFileOnDisk(t, h, src)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	diags, err := h.WaitForDiagnostics(ctx, uri)
	require.NoError(t, err)
	require.NotEmpty(t, diags)
	d := diags[0]
	require.Equal(t, 3, d.Range.Start.Line, "diagnostic: %+v", d)
	assert.Equal(t, u16(t, src, 3, "2)", 0), d.Range.Start.Character, "diagnostic: %s", d.Message)
}

func TestPositionEncoding_DocumentSymbolRangeAfterNonASCII(t *testing.T) {
	h, uri := openEncodingFixture(t)

	syms, err := h.DocumentSymbol(uri)
	require.NoError(t, err)
	var greeting *lsp.DocumentSymbol
	for i := range syms {
		if syms[i].Name == "greeting" {
			greeting = &syms[i]
		}
	}
	require.NotNil(t, greeting)
	line := strings.Split(encodingSrc, "\n")[10]
	assert.Equal(t, len(utf16.Encode([]rune(line))), greeting.Range.End.Character,
		"a declaration's range ends at the end of its line, in UTF-16 units")
}

func TestPositionEncoding_InlayHintAfterNonASCII(t *testing.T) {
	h := newHarness(t)
	src := "package main\n" +
		"\n" +
		"func main() {\n" +
		"\tval opt = Some(42)\n" +
		"\tPrintln(\"é😀日\", opt.Map((x) => x * 2))\n" +
		"}\n"
	uri := openFileOnDisk(t, h, src)

	raw, err := h.Call("textDocument/inlayHint", map[string]interface{}{
		"textDocument": map[string]string{"uri": string(uri)},
		"range": map[string]interface{}{
			"start": map[string]int{"line": 0, "character": 0},
			"end":   map[string]int{"line": 10, "character": 0},
		},
	})
	require.NoError(t, err)
	var hints []lsp.InlayHint
	require.NoError(t, json.Unmarshal(raw, &hints))
	var onLine4 []lsp.InlayHint
	for _, hint := range hints {
		if hint.Position.Line == 4 {
			onLine4 = append(onLine4, hint)
		}
	}
	require.Len(t, onLine4, 1, "hints: %s", string(raw))
	assert.Equal(t, u16(t, src, 4, "(x)", 0)+2, onLine4[0].Position.Character, "the hint sits right after the lambda parameter")
}

// A client that offers utf-8 gets it, and then byte columns are the right
// answer on both sides of the wire.
func TestPositionEncoding_NegotiatesUTF8WhenOffered(t *testing.T) {
	handler := lspserver.NewGalaHandler()
	if root := findProjectRoot(); root != "" {
		handler.SetSearchPaths([]string{root})
	}
	h := servertest.New(t, handler, servertest.WithInitializeParams(&lsp.InitializeParams{
		Capabilities: lsp.ClientCapabilities{
			General: &lsp.GeneralClientCapabilities{
				PositionEncodings: []lsp.PositionEncodingKind{lsp.PositionEncodingUTF8, lsp.PositionEncodingUTF16},
			},
		},
	}))
	enc := h.InitResult.Capabilities.PositionEncoding
	require.NotNil(t, enc)
	require.Equal(t, lsp.PositionEncodingUTF8, *enc)

	uri := openFileOnDisk(t, h, encodingSrc)
	line15 := strings.Split(encodingSrc, "\n")[15]
	locs, err := h.Definition(uri, 15, strings.Index(line15, "perimeter")+1)
	require.NoError(t, err)
	require.NotEmpty(t, locs)
	line4 := strings.Split(encodingSrc, "\n")[4]
	assert.Equal(t, 4, locs[0].Range.Start.Line)
	assert.Equal(t, strings.Index(line4, "perimeter"), locs[0].Range.Start.Character)
}

// A client that offers only encodings the server does not implement still gets
// UTF-16, the mandatory one.
func TestPositionEncoding_FallsBackToUTF16(t *testing.T) {
	h := servertest.New(t, lspserver.NewGalaHandler(), servertest.WithInitializeParams(&lsp.InitializeParams{
		Capabilities: lsp.ClientCapabilities{
			General: &lsp.GeneralClientCapabilities{
				PositionEncodings: []lsp.PositionEncodingKind{lsp.PositionEncodingUTF32},
			},
		},
	}))
	enc := h.InitResult.Capabilities.PositionEncoding
	require.NotNil(t, enc)
	assert.Equal(t, lsp.PositionEncodingUTF16, *enc)
}

// Go-to-definition of a pattern binding must land on the binding itself, not
// on the first occurrence of its name as a substring of the case line — `a`
// sits inside `area`.
func TestDefinition_PatternBindingIsWholeWord(t *testing.T) {
	h := newHarness(t)
	src := "package main\n" +
		"\n" +
		"sealed type Shape {\n" +
		"\tcase Rect(area int, a int)\n" +
		"}\n" +
		"\n" +
		"func side(s Shape) int = s match {\n" +
		"\tcase Rect(area, a) => a\n" +
		"}\n" +
		"\n" +
		"func main() {\n" +
		"\tPrintln(side(Rect(4, 2)))\n" +
		"}\n"
	uri := openFileOnDisk(t, h, src)

	line := strings.Split(src, "\n")[7]
	locs, err := h.Definition(uri, 7, strings.LastIndex(line, "a"))
	require.NoError(t, err)
	require.NotEmpty(t, locs)
	assert.Equal(t, 7, locs[0].Range.Start.Line)
	assert.Equal(t, strings.Index(line, ", a)")+2, locs[0].Range.Start.Character)
}

// TestNoHandlerReadsWireColumns keeps the conversion layer the only reader of
// Position.Character. A wire column is in the negotiated encoding, so a handler
// that reads one directly and indexes the document with it is wrong on any line
// with non-ASCII text before the cursor — the class of bug this layer exists to
// close. Handlers convert with lineIndex.toByte instead (see position.go).
//
// Writing a Character field (`lsp.Position{Character: col}`) is fine: internal
// positions are byte columns and are converted on the way out.
func TestNoHandlerReadsWireColumns(t *testing.T) {
	dir := lspSourceDir(t)
	files, err := filepath.Glob(filepath.Join(dir, "*.go"))
	require.NoError(t, err)
	require.NotEmpty(t, files)

	fset := token.NewFileSet()
	checked := 0
	for _, path := range files {
		name := filepath.Base(path)
		if strings.HasSuffix(name, "_test.go") || name == "position.go" {
			continue
		}
		f, err := parser.ParseFile(fset, path, nil, 0)
		require.NoError(t, err)
		checked++
		ast.Inspect(f, func(n ast.Node) bool {
			if sel, ok := n.(*ast.SelectorExpr); ok && sel.Sel.Name == "Character" {
				t.Errorf("%s reads .Character directly; convert the position with lineIndex.toByte / toWire (position.go)",
					fset.Position(sel.Pos()))
			}
			return true
		})
	}
	require.Greater(t, checked, 5, "the package's sources were not found in %s", dir)
}

// lspSourceDir locates this package's sources: in runfiles under Bazel,
// otherwise the working directory `go test` runs in.
func lspSourceDir(t *testing.T) string {
	t.Helper()
	if p, err := bazel.Runfile("internal/lsp/position.go"); err == nil {
		return filepath.Dir(p)
	}
	wd, err := os.Getwd()
	require.NoError(t, err)
	return wd
}

// A local's declaration is the name after `val`, not the first matching letter
// on the line — `a` also appears in `val`.
func TestDefinition_LocalDeclarationIsWholeWord(t *testing.T) {
	h := newHarness(t)
	src := "package main\n" +
		"\n" +
		"func main() {\n" +
		"\tval a = 1\n" +
		"\tPrintln(a)\n" +
		"}\n"
	uri := openFileOnDisk(t, h, src)

	locs, err := h.Definition(uri, 4, len("\tPrintln("))
	require.NoError(t, err)
	require.NotEmpty(t, locs)
	assert.Equal(t, 3, locs[0].Range.Start.Line)
	assert.Equal(t, len("\tval "), locs[0].Range.Start.Character)
}
