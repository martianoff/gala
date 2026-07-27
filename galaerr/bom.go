package galaerr

import "strings"

// bom is the UTF-8 encoding of U+FEFF (ZERO WIDTH NO-BREAK SPACE), which many
// Windows editors and PowerShell's UTF-8 encoders emit as a byte order mark at
// the start of a text file.
const bom = "\xef\xbb\xbf"

// StripBOM removes exactly one leading UTF-8 byte order mark from s and returns
// the result unchanged otherwise.
//
// A BOM carries no semantic content, so GALA tolerates one at the start of a
// source file the same way Go does. Stripping it matters for two reasons
// beyond lexing: strings.TrimSpace does NOT remove U+FEFF (it is not Unicode
// whitespace), so line scanners looking for a `package ` prefix silently miss
// the first line; and ANTLR reports token positions as rune offsets, which only
// line up with Go's byte offsets into the same source while the text is ASCII —
// a multi-byte BOM desynchronises the two and produces misleading diagnostics
// pointing at correct code further down the file.
//
// Only a leading BOM is removed. A U+FEFF appearing anywhere else is left
// alone, since there it is ordinary content rather than an encoding marker.
func StripBOM(s string) string {
	return strings.TrimPrefix(s, bom)
}
