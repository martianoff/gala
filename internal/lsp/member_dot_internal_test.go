package lsp

import (
	"testing"

	"martianoff/gala/internal/transpiler"
)

func TestMemberDotOffset(t *testing.T) {
	for _, tt := range []struct {
		name       string
		text       string
		line, char int
		want       int
	}{
		{"right after the dot", "a.", 0, 2, 1},
		{"partial member typed", "a.Wi", 0, 4, 1},
		{"dot trailing the previous line", "a.\n    ", 1, 4, 1},
		{"partial member on the continuation line", "a.\n    Wi", 1, 6, 1},
		{"comment after the trailing dot", "a. // configure\n    Wi", 1, 6, 1},
		{"comment line between links", "a.\n    // the name\n    Wi", 2, 6, 1},
		{"blank line ends the expression", "a.\n\n    Wi", 2, 6, -1},
		{"no dot", "a b", 0, 3, -1},
		{"call argument, not a member", "f(\n    x", 1, 5, -1},
		{"line out of range", "a.", 3, 0, -1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := memberDotOffset(tt.text, tt.line, tt.char); got != tt.want {
				t.Errorf("memberDotOffset(%q, %d, %d) = %d, want %d", tt.text, tt.line, tt.char, got, tt.want)
			}
		})
	}
}

// Signature help parses the call being typed; the `)` closing it is added only
// when the document is missing one, because editors insert it as `(` is typed.
func TestPatchTextForSignature(t *testing.T) {
	for _, tt := range []struct {
		name       string
		text       string
		line, char int
		wantText   string
		wantOffset int
	}{
		{"unclosed call is closed at the caret", "f(\n}", 0, 2, "f()\n}", 2},
		{"auto-closed call is left alone", "f()\n}", 0, 2, "f()\n}", 2},
		{"caret past the end of its line is clamped", "f(\n}", 0, 9, "f()\n}", 2},
		{"a paren inside a string does not count", "f(\")\"\n}", 0, 2, "f()\")\"\n}", 2},
		{"a paren inside a comment does not count", "f()\n// see g(\n}", 0, 2, "f()\n// see g(\n}", 2},
		{"a closing paren inside a comment does not count", "f(\n// 1) first\n}", 0, 2, "f()\n// 1) first\n}", 2},
		{"a paren in a char literal does not count", "f(')')\n}", 0, 2, "f(')')\n}", 2},
		{"line out of range", "f(", 4, 0, "", -1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			gotText, gotOffset := patchTextForSignature(tt.text, tt.line, tt.char)
			if gotText != tt.wantText || gotOffset != tt.wantOffset {
				t.Errorf("patchTextForSignature(%q, %d, %d) = (%q, %d), want (%q, %d)",
					tt.text, tt.line, tt.char, gotText, gotOffset, tt.wantText, tt.wantOffset)
			}
		})
	}
}

// callInsertText writes out required parameters as named arguments for a
// snippet client, and opens the call for any other.
func TestCallInsertText(t *testing.T) {
	for _, tt := range []struct {
		name     string
		params   []string
		defaults map[int]transpiler.DefaultExpr
		snippets bool
		want     string
		snippet  bool
	}{
		{"no parameters", nil, nil, true, "Name()", false},
		{"required parameters", []string{"a", "b"}, nil, true, "Name(a = $1, b = $2)$0", true},
		{"defaults are left out", []string{"a", "b", "c"}, map[int]transpiler.DefaultExpr{1: {Text: "1"}, 2: {Text: "2"}}, true, "Name(a = $1)$0", true},
		{"only defaults", []string{"a"}, map[int]transpiler.DefaultExpr{0: {Text: "1"}}, true, "Name($0)", true},
		{"plain-text client", []string{"a"}, nil, false, "Name(", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, format := callInsertText("Name", tt.params, tt.defaults, tt.snippets)
			if got != tt.want || (format != nil) != tt.snippet {
				t.Errorf("callInsertText = (%q, snippet=%v), want (%q, snippet=%v)", got, format != nil, tt.want, tt.snippet)
			}
		})
	}
}
