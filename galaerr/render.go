package galaerr

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// This file holds the Rust/Elm-style compile-error renderer used by the CLI.
// It is intentionally CLI-only: SemanticError.Error() / SyntaxError.Error()
// still produce the terse single-line form the LSP and tests depend on, and
// RenderRich here layers a framed source snippet on top for humans reading a
// terminal. It adds no third-party dependency — color is hand-written ANSI.
//
// Approved layout (color off):
//
//	error[GALA-E0035]: bare `len` is forbidden
//	  --> foo.gala:12:13
//	   |
//	12 |     val n = len(s)
//	   |             ^^^ use .Size() / .ByteSize() instead
//	   |
//	   = hint: GALA strings & collections expose .Size()

// ANSI escapes, hand-written so the renderer stays dependency-free. Red+bold
// for the error header and the caret, dim for the frame gutter, cyan for the
// hint label. When color is off none of these are emitted and the output is
// byte-identical plain text.
const (
	ansiReset   = "\x1b[0m"
	ansiRedBold = "\x1b[1;31m"
	ansiDim     = "\x1b[2m"
	ansiCyan    = "\x1b[36m"
)

// Options controls how RenderRich resolves source and colorizes output.
type Options struct {
	// FallbackPath is the path shown in the locus (and used to match
	// FallbackSource) when a diagnostic carries no FilePath of its own.
	FallbackPath string
	// FallbackSource is the source text used for the snippet when the
	// diagnostic has no readable FilePath. The CLI supplies the file it is
	// currently compiling so single-file transpiles still frame the snippet.
	FallbackSource string
	// Color enables ANSI colorization. Callers typically pass ColorEnabled().
	Color bool
}

// ColorEnabled reports whether colorized diagnostics should be emitted: only
// when stderr is a character device (a TTY) and NO_COLOR is unset. It adds no
// dependency — the TTY probe is a plain os.Stderr.Stat() mode check.
func ColorEnabled() bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	stat, err := os.Stderr.Stat()
	if err != nil {
		return false
	}
	return stat.Mode()&os.ModeCharDevice != 0
}

// diagnostic is the renderer's normalized view of a single typed error.
type diagnostic struct {
	code      ErrorCode
	msg       string
	hint      string
	filePath  string
	line      int // 1-based; 0 means "no position"
	column    int // 0-based rune index of the span start
	endColumn int // 0-based exclusive rune index; <=column means "unset"
}

// RenderRich renders err as one or more framed diagnostics. A *MultiError is
// unwrapped and each contained error rendered in turn (blank line between).
// Errors that are not GALA typed errors — notably `go build` failures — are
// passed through verbatim (err.Error()), never framed.
func RenderRich(err error, opts Options) string {
	if err == nil {
		return ""
	}

	var multi *MultiError
	if errors.As(err, &multi) && multi != nil {
		parts := make([]string, 0, len(multi.Errors))
		for _, sub := range multi.Errors {
			parts = append(parts, RenderRich(sub, opts))
		}
		return strings.Join(parts, "\n\n")
	}

	d, ok := toDiagnostic(err)
	if !ok {
		// Not a GALA diagnostic (e.g. a wrapped `go build` error). Pass the
		// underlying text through unchanged so generated-file references and
		// the Go compiler's own framing survive intact.
		return err.Error()
	}
	return renderDiagnostic(d, opts)
}

// toDiagnostic extracts a normalized diagnostic from the first GALA typed error
// reachable through the unwrap chain. Returns ok=false for anything else.
func toDiagnostic(err error) (diagnostic, bool) {
	var se *SemanticError
	if errors.As(err, &se) {
		return diagnostic{
			code:      se.Code,
			msg:       se.Msg,
			hint:      se.Hint,
			filePath:  se.FilePath,
			line:      se.Line,
			column:    se.Column,
			endColumn: se.EndColumn,
		}, true
	}
	var sy *SyntaxError
	if errors.As(err, &sy) {
		// SyntaxError has no FilePath/Code/Hint; the CLI supplies the source
		// via the fallback path so its snippet can still be framed.
		return diagnostic{
			msg:    sy.Msg,
			line:   sy.Line,
			column: sy.Column,
		}, true
	}
	return diagnostic{}, false
}

func renderDiagnostic(d diagnostic, opts Options) string {
	var b strings.Builder
	b.WriteString(renderHeader(d, opts))

	src := resolveSource(d.filePath, opts)
	lines := strings.Split(src, "\n")

	// No usable position or no source: header (+ hint) only, never a frame.
	if d.line <= 0 || src == "" || d.line-1 >= len(lines) {
		if d.hint != "" {
			b.WriteString("\n")
			b.WriteString(renderFooter(d.hint, opts.Color))
		}
		return b.String()
	}

	displayPath := d.filePath
	if displayPath == "" {
		if opts.FallbackPath != "" {
			displayPath = opts.FallbackPath
		} else {
			displayPath = "<input>"
		}
	}

	srcLine := lines[d.line-1]
	gutter := strconv.Itoa(d.line)
	gw := len(gutter)
	pad := strings.Repeat(" ", gw)
	pipe := colorize("|", ansiDim, opts.Color)

	// Locus: 1-based column shown to the user (stored 0-based).
	arrow := colorize("-->", ansiDim, opts.Color)
	b.WriteString("\n")
	b.WriteString(fmt.Sprintf("  %s %s:%d:%d", arrow, displayPath, d.line, d.column+1))

	// Frame: blank line, source row, caret row, blank line.
	b.WriteString("\n")
	b.WriteString(pad + " " + pipe)

	b.WriteString("\n")
	b.WriteString(gutter + " " + pipe + " " + srcLine)

	width := spanWidth(srcLine, d.column, d.endColumn)
	carets := colorize(strings.Repeat("^", width), ansiRedBold, opts.Color)
	caretRow := pad + " " + pipe + " " + caretIndent(srcLine, d.column) + carets
	if ann := terseHint(d.hint); ann != "" {
		caretRow += " " + ann
	}
	b.WriteString("\n")
	b.WriteString(caretRow)

	b.WriteString("\n")
	b.WriteString(pad + " " + pipe)

	if d.hint != "" {
		b.WriteString("\n")
		b.WriteString(pad + " " + renderFooter(d.hint, opts.Color))
	}
	return b.String()
}

// renderHeader builds the `error[CODE]: message` (or `error: message`) line.
func renderHeader(d diagnostic, opts Options) string {
	label := "error"
	if d.code != "" {
		label = "error[" + string(d.code) + "]"
	}
	return colorize(label, ansiRedBold, opts.Color) + ": " + d.msg
}

// renderFooter builds the `= hint: <hint>` footer (without the leading pad,
// which the caller aligns to the gutter width).
func renderFooter(hint string, color bool) string {
	return "= " + colorize("hint:", ansiCyan, color) + " " + hint
}

// resolveSource returns the source text for the snippet: the diagnostic's own
// file if readable, else the caller-provided fallback when it applies.
func resolveSource(filePath string, opts Options) string {
	if filePath != "" {
		if data, err := os.ReadFile(filePath); err == nil {
			return string(data)
		}
	}
	if opts.FallbackSource != "" && (filePath == "" || filePath == opts.FallbackPath) {
		return opts.FallbackSource
	}
	return ""
}

// caretIndent produces the whitespace that positions the caret under column
// (0-based rune index) of srcLine. Non-tab characters become spaces while tabs
// are preserved, so the caret lines up with tab-expanded source in the terminal.
func caretIndent(srcLine string, column int) string {
	runes := []rune(srcLine)
	var b strings.Builder
	for i := 0; i < column; i++ {
		if i < len(runes) && runes[i] == '\t' {
			b.WriteByte('\t')
		} else {
			b.WriteByte(' ')
		}
	}
	return b.String()
}

// spanWidth computes the caret width. When an exact end is set (endColumn >
// column) it is used (clamped to the line); otherwise the width is derived by
// scanning the token at column: an identifier run, a quoted string/backtick to
// its closing delimiter, or a single character.
func spanWidth(srcLine string, column, endColumn int) int {
	runes := []rune(srcLine)
	if endColumn > column {
		end := endColumn
		if end > len(runes) {
			end = len(runes)
		}
		if end > column {
			return end - column
		}
		return 1
	}
	if column < 0 || column >= len(runes) {
		return 1
	}
	ch := runes[column]
	if ch == '"' || ch == '`' {
		for j := column + 1; j < len(runes); j++ {
			if runes[j] == ch {
				return j - column + 1
			}
		}
		return len(runes) - column
	}
	if isIdentRune(ch) {
		j := column
		for j < len(runes) && isIdentRune(runes[j]) {
			j++
		}
		return j - column
	}
	return 1
}

func isIdentRune(r rune) bool {
	return r == '_' ||
		(r >= 'a' && r <= 'z') ||
		(r >= 'A' && r <= 'Z') ||
		(r >= '0' && r <= '9')
}

// terseHint reduces a full hint to a short inline annotation for the caret row:
// the first clause (before " (", "; " or ". "), trimmed and capped in length.
// The full hint is still shown in the footer.
func terseHint(hint string) string {
	if hint == "" {
		return ""
	}
	cut := len(hint)
	for _, sep := range []string{" (", "; ", ". "} {
		if i := strings.Index(hint, sep); i >= 0 && i < cut {
			cut = i
		}
	}
	s := strings.TrimSpace(hint[:cut])
	const maxLen = 60
	if r := []rune(s); len(r) > maxLen {
		s = strings.TrimSpace(string(r[:maxLen])) + "…"
	}
	if s == "" {
		return strings.TrimSpace(hint)
	}
	return s
}

func colorize(s, code string, color bool) string {
	if !color {
		return s
	}
	return code + s + ansiReset
}
