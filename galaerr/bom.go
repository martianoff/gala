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
// source file the same way Go does. Two things make it worth removing rather
// than ignoring: the lexer has no rule for U+FEFF and rejects the file outright,
// and strings.TrimSpace does NOT remove it either (it is not Unicode
// whitespace), so the line scanners that look for a `package ` prefix silently
// miss the first line.
//
// Only a leading BOM is removed. A U+FEFF anywhere else is left in place for
// the parser to report: as in Go, it is an illegal character there
// (GALA-E0051).
func StripBOM(s string) string {
	return strings.TrimPrefix(s, bom)
}
