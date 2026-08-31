package galaerr

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
)

// Machine-readable diagnostics
//
// RenderRich is for a person reading a terminal: a framed snippet with a caret
// under the offending token. A program consuming that has to parse ASCII art to
// recover the code and position, which is exactly the wrong thing to ask of a
// tool — or of a coding agent, which otherwise regexes `error[GALA-E….*]` out
// of the frame and hopes the layout never changes.
//
// This file renders the same diagnostics as JSON, from the same typed errors,
// so the two can never disagree about what was reported.

// Diagnostic is one machine-readable compiler diagnostic.
//
// Positions are 1-BASED for both line and column, matching the `-->` locus the
// human renderer prints, so a value read here points at the same character the
// terminal underlined. (Internally columns are 0-based; the conversion happens
// once, here.)
type Diagnostic struct {
	// Severity is always "error" today. It is emitted anyway so a consumer can
	// switch on it without a schema change if warnings are ever added.
	Severity string `json:"severity"`
	// Code is the stable GALA-Exxxx identifier, or "" for an uncoded
	// diagnostic (a plain syntax error).
	Code string `json:"code,omitempty"`
	// Message is the one-line description, without the code prefix.
	Message string `json:"message"`
	// Hint is the remediation advice, when the diagnostic carries one.
	Hint string `json:"hint,omitempty"`
	// File is the source file, when known.
	File string `json:"file,omitempty"`
	// Line is 1-based; 0 means the diagnostic carries no position.
	Line int `json:"line,omitempty"`
	// Column is 1-based; 0 means no position.
	Column int `json:"column,omitempty"`
	// EndColumn is 1-based and EXCLUSIVE, set only when the diagnostic
	// recorded an exact span.
	EndColumn int `json:"endColumn,omitempty"`
	// DocsURL is the published page for Code, when there is one.
	DocsURL string `json:"docsUrl,omitempty"`
}

// diagnosticsEnvelope is the top-level shape. Diagnostics are wrapped in an
// object rather than emitted as a bare array so the schema has somewhere to
// grow (a summary, a timing block) without breaking consumers.
type diagnosticsEnvelope struct {
	Diagnostics []Diagnostic `json:"diagnostics"`
}

// Diagnostics flattens err into machine-readable records. A MultiError yields
// one entry per contained error; a non-GALA error (notably a wrapped `go build`
// failure) yields a single uncoded, position-less entry carrying its text, so a
// consumer always gets valid output rather than an empty list for a real
// failure.
func Diagnostics(err error) []Diagnostic {
	if err == nil {
		return nil
	}

	var multi *MultiError
	if errors.As(err, &multi) && multi != nil {
		out := make([]Diagnostic, 0, len(multi.Errors))
		for _, sub := range multi.Errors {
			out = append(out, Diagnostics(sub)...)
		}
		return out
	}

	d, ok := toDiagnostic(err)
	if !ok {
		return []Diagnostic{{Severity: "error", Message: err.Error()}}
	}
	return []Diagnostic{toJSONDiagnostic(d)}
}

// toJSONDiagnostic converts the renderer's internal view, rebasing columns from
// the 0-based form used inside the compiler to the 1-based form both the
// terminal locus and this output present.
func toJSONDiagnostic(d diagnostic) Diagnostic {
	out := Diagnostic{
		Severity: "error",
		Code:     string(d.code),
		Message:  d.msg,
		Hint:     d.hint,
		File:     d.filePath,
		Line:     d.line,
		DocsURL:  DocsURL(d.code),
	}
	if d.line > 0 {
		out.Column = d.column + 1
		if d.endColumn > d.column {
			out.EndColumn = d.endColumn + 1
		}
	}
	return out
}

// RenderJSON renders err as the JSON envelope, indented for readability. It
// always returns valid JSON: a nil error yields an empty diagnostics list.
//
// HTML escaping is turned off. Diagnostics quote GALA source, and Go's default
// encoder escapes `<`, `>` and `&` — so the arrow in a lambda hint comes out as
// a > escape. That is still valid JSON and decodes correctly, but it makes
// the output unreadable to anyone eyeballing it, and the escaping buys nothing
// here since compiler diagnostics are never embedded in HTML.
func RenderJSON(err error) (string, error) {
	return RenderJSONWithHint(err, "")
}

// RenderJSONWithHint is RenderJSON plus a caller-supplied hint applied to the
// first diagnostic that has none. Some remediation is known only to the CLI —
// the ambiguous-main-package failure names a candidate to build — and would
// otherwise be lost to a machine-readable consumer while the text output
// prints it.
func RenderJSONWithHint(err error, hint string) (string, error) {
	env := diagnosticsEnvelope{Diagnostics: Diagnostics(err)}
	if env.Diagnostics == nil {
		env.Diagnostics = []Diagnostic{}
	}
	if hint != "" {
		for i := range env.Diagnostics {
			if env.Diagnostics[i].Hint == "" {
				env.Diagnostics[i].Hint = hint
				break
			}
		}
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if marshalErr := enc.Encode(env); marshalErr != nil {
		return "", marshalErr
	}
	// Encode appends a newline; the caller adds its own.
	return strings.TrimRight(buf.String(), "\n"), nil
}
