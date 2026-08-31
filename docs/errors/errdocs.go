// Package errdocs serves the error-code reference pages that sit beside it.
//
// The Go file lives in docs/errors/ rather than under internal/ on purpose:
// //go:embed can only reach files in its own package directory, and the
// alternative — copying the pages somewhere a Go package could see them — would
// give the project two copies of every page and a way for them to disagree.
// Keeping the code next to the content means `gala explain` serves exactly the
// file a reader would open on disk or find published on the website.
package errdocs

import (
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"strings"
)

//go:embed GALA-E*.md
var pages embed.FS

// Normalize turns user input into a canonical code. It accepts the code in the
// spellings someone actually types or pastes: `GALA-E0044`, `gala-e0044`,
// `E0044`, or the bare number `44`. Returns "" when the input cannot be read
// as a code at all.
func Normalize(input string) string {
	s := strings.TrimSpace(input)
	if s == "" {
		return ""
	}
	s = strings.ToUpper(s)
	s = strings.TrimPrefix(s, "GALA-")

	// A bare number, or E-prefixed: pad to the four digits the codes use.
	digits := strings.TrimPrefix(s, "E")
	if digits == "" || strings.IndexFunc(digits, func(r rune) bool { return r < '0' || r > '9' }) >= 0 {
		return ""
	}
	if len(digits) > 4 {
		return ""
	}
	return fmt.Sprintf("GALA-E%04s", digits)
}

// Page returns the markdown reference page for a code. The code is normalized
// first, so any spelling Normalize accepts works here too.
func Page(code string) (string, error) {
	norm := Normalize(code)
	if norm == "" {
		return "", fmt.Errorf("%q is not a GALA error code (expected a form like GALA-E0044, E0044 or 44)", code)
	}
	data, err := pages.ReadFile(norm + ".md")
	if err != nil {
		return "", fmt.Errorf("no reference page for %s; run `gala explain --list` to see the codes that exist", norm)
	}
	return string(data), nil
}

// Codes lists every documented code, ascending. Used by `gala explain --list`
// and by the guard that asserts the embedded set matches what the compiler can
// actually emit.
func Codes() []string {
	entries, err := fs.Glob(pages, "GALA-E*.md")
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, strings.TrimSuffix(e, ".md"))
	}
	sort.Strings(out)
	return out
}

// Title returns the one-line summary of what a code means: the page's H1 with
// the leading "# " and the code itself removed, since every page's heading
// opens with the code and a listing that repeats it in both columns is noise.
// Falls back to the code when there is no heading to read.
func Title(code string) string {
	norm := Normalize(code)
	page, err := Page(code)
	if err != nil {
		return code
	}
	for _, line := range strings.Split(page, "\n") {
		if !strings.HasPrefix(line, "# ") {
			continue
		}
		heading := strings.TrimSpace(strings.TrimPrefix(line, "# "))
		// Headings read "GALA-E0044 — type has no such method"; keep the half
		// after the dash. An em dash is what the pages use; a plain hyphen is
		// tolerated so a hand-written page still lists cleanly.
		trimmed := strings.TrimPrefix(heading, norm)
		trimmed = strings.TrimLeft(trimmed, " —-")
		if trimmed != "" {
			return trimmed
		}
		return heading
	}
	return code
}
