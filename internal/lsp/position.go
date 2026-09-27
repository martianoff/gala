package lsp

import (
	"strings"
	"unicode/utf8"

	"github.com/owenrumney/go-lsp/lsp"
)

// Position columns come in three units, and this file is the only place that
// converts between them.
//
//   - Wire columns: lsp.Position.Character as the client sends and receives it,
//     counted in the encoding negotiated at initialize — UTF-16 code units
//     unless the client offered UTF-8 (LSP 3.17 `general.positionEncodings`).
//   - Byte columns: offsets into a Go string. Every handler works in these
//     internally, because every handler indexes the document text.
//   - Code-point columns: what ANTLR reports (token columns, SourcePos, the
//     line:col of syntax and semantic errors) — its input stream is a []rune.
//
// On plain ASCII the three agree, which is how treating a wire column as a byte
// offset survived: one é, emoji or CJK character earlier on the line and hover,
// go-to-definition, completion, signature help and every returned range point
// at the wrong column.
//
// The rule: a handler converts its request position with lineIndex.toByte on
// entry, works in bytes, and converts every position it returns with
// lineIndex.toWire (or GalaHandler.locationsToWire) on exit. A column taken
// from the analyzer or the parser is converted to bytes with runeToByte before
// it meets anything else. TestNoHandlerReadsWireColumns keeps
// Position.Character reads confined to this file.

// negotiatePositionEncoding picks the encoding for this session: the client's
// most preferred of the ones the server implements, and UTF-16 — the one every
// client must support — when it offers none of them.
func negotiatePositionEncoding(caps lsp.ClientCapabilities) lsp.PositionEncodingKind {
	if caps.General != nil {
		for _, enc := range caps.General.PositionEncodings {
			switch enc {
			case lsp.PositionEncodingUTF8, lsp.PositionEncodingUTF16:
				return enc
			}
		}
	}
	return lsp.PositionEncodingUTF16
}

// positionEncoding is the encoding negotiated at initialize.
func (h *GalaHandler) positionEncoding() lsp.PositionEncodingKind {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.posEncoding == "" {
		return lsp.PositionEncodingUTF16
	}
	return h.posEncoding
}

// lineIndex converts positions within one document's text.
type lineIndex struct {
	lines []string
	enc   lsp.PositionEncodingKind
}

// index returns the converter for text under the session's encoding. text
// must be what the handler works on — the BOM-stripped document.
func (h *GalaHandler) index(text string) lineIndex {
	return newLineIndex(text, h.positionEncoding())
}

func newLineIndex(text string, enc lsp.PositionEncodingKind) lineIndex {
	return lineIndex{lines: strings.Split(text, "\n"), enc: enc}
}

// line returns the text of line n, without its "\n"; "" when out of range.
func (x lineIndex) line(n int) string {
	if n < 0 || n >= len(x.lines) {
		return ""
	}
	return x.lines[n]
}

// toByte converts a request position into a line and a byte column on it.
func (x lineIndex) toByte(p lsp.Position) (line, col int) {
	return p.Line, wireToByte(x.line(p.Line), p.Character, x.enc)
}

// toWire converts a position whose column is a byte offset into a wire
// position.
func (x lineIndex) toWire(p lsp.Position) lsp.Position {
	return lsp.Position{Line: p.Line, Character: byteToWire(x.line(p.Line), p.Character, x.enc)}
}

// rangeToWire converts both ends of a byte-column range.
func (x lineIndex) rangeToWire(r lsp.Range) lsp.Range {
	return lsp.Range{Start: x.toWire(r.Start), End: x.toWire(r.End)}
}

// runeToByte converts a code-point column on line n — an ANTLR column — into a
// byte column.
func (x lineIndex) runeToByte(n, runeCol int) int {
	return runeToByte(x.line(n), runeCol)
}

// locationsToWire converts byte-column locations, which may name any file,
// into wire locations. Each file's text is looked up once: the editor's copy
// when it is open, otherwise the file on disk.
func (h *GalaHandler) locationsToWire(locs []lsp.Location) []lsp.Location {
	if len(locs) == 0 {
		return locs
	}
	enc := h.positionEncoding()
	indexes := make(map[lsp.DocumentURI]lineIndex)
	out := make([]lsp.Location, len(locs))
	for i, loc := range locs {
		x, ok := indexes[loc.URI]
		if !ok {
			text, _ := h.fileText(uriToPath(string(loc.URI)))
			x = newLineIndex(text, enc)
			indexes[loc.URI] = x
		}
		out[i] = lsp.Location{URI: loc.URI, Range: x.rangeToWire(loc.Range)}
	}
	return out
}

// wireToByte converts a column counted in enc units into a byte offset on
// line. Out-of-range columns clamp to the line, as the protocol asks; a column
// that falls inside a character (half a surrogate pair, the middle of a UTF-8
// sequence) snaps back to that character's start.
func wireToByte(line string, col int, enc lsp.PositionEncodingKind) int {
	if col <= 0 {
		return 0
	}
	if enc == lsp.PositionEncodingUTF8 {
		col = min(col, len(line))
		for col > 0 && col < len(line) && !utf8.RuneStart(line[col]) {
			col--
		}
		return col
	}
	units := 0
	for i, r := range line {
		units += utf16Len(r)
		if units > col {
			return i
		}
	}
	return len(line)
}

// byteToWire converts a byte offset on line into a column in enc units.
func byteToWire(line string, b int, enc lsp.PositionEncodingKind) int {
	b = min(max(b, 0), len(line))
	if enc == lsp.PositionEncodingUTF8 {
		return b
	}
	units := 0
	for _, r := range line[:b] {
		units += utf16Len(r)
	}
	return units
}

// nthLine returns line n of text without its "\n"; "" when out of range.
func nthLine(text string, n int) string {
	for ; n > 0; n-- {
		i := strings.IndexByte(text, '\n')
		if i < 0 {
			return ""
		}
		text = text[i+1:]
	}
	if n < 0 {
		return ""
	}
	if i := strings.IndexByte(text, '\n'); i >= 0 {
		return text[:i]
	}
	return text
}

// runeToByte converts a code-point column on line into a byte offset, clamped
// to the line.
func runeToByte(line string, runeCol int) int {
	n := 0
	for i := range line {
		if n >= runeCol {
			return i
		}
		n++
	}
	return len(line)
}

// byteToRune converts a byte offset on line into a code-point column.
func byteToRune(line string, b int) int {
	return utf8.RuneCountInString(line[:min(max(b, 0), len(line))])
}

func utf16Len(r rune) int {
	if r >= 0x10000 {
		return 2
	}
	return 1
}

// tokenOffsets maps ANTLR character indexes (token.GetStart/GetStop), which
// count code points from the start of the input, to byte offsets in the same
// text. Nil byteAt means the text is ASCII and the two coincide.
type tokenOffsets struct {
	byteAt []int
	size   int
}

func newTokenOffsets(text string) tokenOffsets {
	o := tokenOffsets{size: len(text)}
	for i := 0; i < len(text); i++ {
		if text[i] >= utf8.RuneSelf {
			o.byteAt = make([]int, 0, len(text)+1)
			for b := range text {
				o.byteAt = append(o.byteAt, b)
			}
			o.byteAt = append(o.byteAt, len(text))
			break
		}
	}
	return o
}

// byteOf returns the byte offset of the character at ANTLR index idx; an index
// past the end maps to the end of the text.
func (o tokenOffsets) byteOf(idx int) int {
	if idx < 0 {
		return idx
	}
	if o.byteAt == nil {
		return min(idx, o.size)
	}
	if idx >= len(o.byteAt) {
		return o.size
	}
	return o.byteAt[idx]
}
