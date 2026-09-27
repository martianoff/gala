package lsp

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/owenrumney/go-lsp/lsp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"martianoff/gala/galaerr"
	"martianoff/gala/internal/transpiler"
)

// line has a 2-byte é, a 4-byte emoji (a UTF-16 surrogate pair) and a 3-byte
// CJK character before `x`:
//
//	unit     a  é  😀    日  x
//	byte     0  1  3     7   10
//	utf-16   0  1  2     4   5
//	rune     0  1  2     3   4
const mixedLine = "aé😀日x"

func TestWireToByte(t *testing.T) {
	cases := []struct {
		name string
		col  int
		enc  lsp.PositionEncodingKind
		want int
	}{
		{"utf16 start", 0, lsp.PositionEncodingUTF16, 0},
		{"utf16 after é", 2, lsp.PositionEncodingUTF16, 3},
		{"utf16 inside surrogate pair snaps back", 3, lsp.PositionEncodingUTF16, 3},
		{"utf16 after emoji", 4, lsp.PositionEncodingUTF16, 7},
		{"utf16 at x", 5, lsp.PositionEncodingUTF16, 10},
		{"utf16 end", 6, lsp.PositionEncodingUTF16, 11},
		{"utf16 past end clamps", 99, lsp.PositionEncodingUTF16, 11},
		{"negative clamps", -3, lsp.PositionEncodingUTF16, 0},
		{"utf8 identity", 7, lsp.PositionEncodingUTF8, 7},
		{"utf8 inside sequence snaps back", 5, lsp.PositionEncodingUTF8, 3},
		{"utf8 past end clamps", 99, lsp.PositionEncodingUTF8, 11},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, wireToByte(mixedLine, tc.col, tc.enc))
		})
	}
}

func TestByteToWireRoundTrips(t *testing.T) {
	for _, b := range []int{0, 1, 3, 7, 10, 11} {
		for _, enc := range []lsp.PositionEncodingKind{lsp.PositionEncodingUTF16, lsp.PositionEncodingUTF8} {
			assert.Equal(t, b, wireToByte(mixedLine, byteToWire(mixedLine, b, enc), enc), "byte %d, %s", b, enc)
		}
	}
	assert.Equal(t, 5, byteToWire(mixedLine, 10, lsp.PositionEncodingUTF16))
	assert.Equal(t, 10, byteToWire(mixedLine, 10, lsp.PositionEncodingUTF8))
}

func TestRuneColumns(t *testing.T) {
	assert.Equal(t, 10, runeToByte(mixedLine, 4))
	assert.Equal(t, 11, runeToByte(mixedLine, 99))
	assert.Equal(t, 4, byteToRune(mixedLine, 10))
}

func TestTokenOffsets(t *testing.T) {
	text := "// é😀\nfoo(x)"
	o := newTokenOffsets(text)
	// Code points: '/','/',' ','é','😀','\n','f' -> 'f' is index 6.
	assert.Equal(t, 10, o.byteOf(6))
	assert.Equal(t, len(text), o.byteOf(99))

	ascii := newTokenOffsets("foo(x)")
	assert.Equal(t, 3, ascii.byteOf(3))
	assert.Equal(t, 6, ascii.byteOf(99))
}

func TestNthLine(t *testing.T) {
	text := "a\nbé\n\nd"
	assert.Equal(t, "a", nthLine(text, 0))
	assert.Equal(t, "bé", nthLine(text, 1))
	assert.Equal(t, "", nthLine(text, 2))
	assert.Equal(t, "d", nthLine(text, 3))
	assert.Equal(t, "", nthLine(text, 4))
	assert.Equal(t, "", nthLine(text, -1))
}

// A file read from disk is BOM-free, like an open document and like the text
// the parser positions its tokens in; otherwise a location on its first line
// is three bytes out.
func TestReadSourceStripsBOM(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bom.gala")
	require.NoError(t, os.WriteFile(path, []byte("\xef\xbb\xbfpackage p\n"), 0o644))
	got, ok := readSource(path)
	require.True(t, ok)
	assert.Equal(t, "package p\n", got)
}

// Locations from the analyzer land on the identifier in a BOM'd sibling file,
// with the column converted from code points.
func TestLocationAtConvertsAnalyzerColumns(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sib.gala")
	require.NoError(t, os.WriteFile(path, []byte("\xef\xbb\xbf/* é😀 */ val answer = 42\n"), 0o644))
	h := NewGalaHandler()

	// ANTLR column of `answer`: 13 code points precede it.
	loc := h.locationAt(path, transpiler.SourcePos{Line: 1, Column: 13}, "answer")
	require.NotNil(t, loc)
	wire := h.locationsToWire("file:///other.gala", h.index(""), []lsp.Location{*loc})[0]
	assert.Equal(t, 0, wire.Range.Start.Line)
	assert.Equal(t, 14, wire.Range.Start.Character, "UTF-16: the emoji is two units")
	assert.Equal(t, 20, wire.Range.End.Character)
}

// Locations in the request's own document convert against the text the
// handler worked on, not a newer copy; a file that cannot be read keeps its
// byte columns instead of collapsing to column 0.
func TestLocationsToWireUsesSnapshotAndKeepsUnreadable(t *testing.T) {
	h := NewGalaHandler()
	const uri = "file:///doc.gala"
	h.documents[uri] = "éééé x\n" // a newer copy than the handler's snapshot
	snapshot := newLineIndex("é x\n", lsp.PositionEncodingUTF16)
	missing := lsp.DocumentURI(pathToURI(filepath.Join(t.TempDir(), "gone.gala")))
	rng := lsp.Range{Start: lsp.Position{Line: 0, Character: 3}, End: lsp.Position{Line: 0, Character: 4}}

	got := h.locationsToWire(uri, snapshot, []lsp.Location{{URI: uri, Range: rng}, {URI: missing, Range: rng}})
	assert.Equal(t, 2, got[0].Range.Start.Character, "converted on the snapshot: é is 2 bytes, 1 unit")
	assert.Equal(t, 3, got[1].Range.Start.Character, "unreadable file keeps its byte column")
}

// An error found in another file keeps its column: converting it against this
// document's unrelated line would move it, or clamp it to that line's length.
func TestErrorToDiagnosticOtherFileKeepsColumn(t *testing.T) {
	x := newLineIndex("package p\n}\n", lsp.PositionEncodingUTF16)
	here := filepath.Join("pkg", "a.gala")

	other := galaerr.NewSemanticErrorInFile(filepath.Join("pkg", "b.gala"), 2, 30, "boom")
	d := errorToDiagnostic(other, x, here)
	assert.Equal(t, 30, d.Range.Start.Character)
	assert.Equal(t, 31, d.Range.End.Character)

	own := galaerr.NewSemanticErrorInFile(here, 2, 30, "boom")
	d = errorToDiagnostic(own, x, here)
	assert.Equal(t, 1, d.Range.Start.Character, "this file's error clamps to its line")
}

// A CRLF line's "\r" is not part of the line: a past-end column clamps to the
// visible end, and a byte column there reports the visible length.
func TestLineIndexDropsCarriageReturn(t *testing.T) {
	x := newLineIndex("aé\r\nb\r\n", lsp.PositionEncodingUTF16)
	line, col := x.toByte(lsp.Position{Line: 0, Character: 99})
	assert.Equal(t, 0, line)
	assert.Equal(t, 3, col, "clamps before the \\r")
	assert.Equal(t, 2, x.toWire(lsp.Position{Line: 0, Character: 4}).Character)
	assert.Equal(t, "b", nthLine("aé\r\nb\r\n", 1))
}
