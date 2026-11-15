package parser

import (
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

	"martianoff/gala/galaerr"
	"martianoff/gala/internal/parser/native"
)

// TestDocKeyOfCountsCodePoints pins DocKey's column unit: code points, as
// diagnostics count them, not bytes. "/* é */ " before func is 8 code points
// and 9 bytes. (A documented declaration starts its line, so only whitespace
// comes before it; this pins the unit for any key built from a token.)
func TestDocKeyOfCountsCodePoints(t *testing.T) {
	root, err := native.ParseSourceFile(lexNative("package main\n\n/* é */ func F() int = 1\n", 0, 0))
	if err != nil {
		t.Fatal(err)
	}
	var got []DocKey
	root.Walk(func(n native.GalaNodeRef) bool {
		if n.Kind() == native.GalaKIND_functionDeclaration {
			got = append(got, NativeDocKeyOf(n))
		}
		return true
	})
	if want := (DocKey{Line: 3, Column: 8}); len(got) != 1 || got[0] != want {
		t.Errorf("NativeDocKeyOf(func) = %v, want [%v]", got, want)
	}
}

// docFor parses src and returns the doc comment attached to the declaration
// whose first token starts where the given substring does.
func docFor(t *testing.T, src, declPrefix string) string {
	t.Helper()
	parsed := NewAntlrGalaParser().ParseFile(src)
	docs, errs := parsed.Docs, parsed.Errors
	if len(errs) > 0 {
		t.Fatalf("parse errors: %v", errs)
	}
	off := strings.Index(src, declPrefix)
	if off < 0 {
		t.Fatalf("declaration %q not found in source", declPrefix)
	}
	lineStart := strings.LastIndex(src[:off], "\n") + 1
	return docs[DocKey{Line: 1 + strings.Count(src[:off], "\n"), Column: utf8.RuneCountInString(src[lineStart:off])}]
}

func TestDocComments(t *testing.T) {
	tests := []struct {
		name string
		src  string
		decl string
		want string
	}{
		{
			name: "function",
			src:  "package main\n\n// Greet builds a greeting.\nfunc Greet(name string) string = name\n",
			decl: "func Greet",
			want: "Greet builds a greeting.",
		},
		{
			name: "multi-line with parameter convention",
			src:  "package main\n\n// Map applies f to each element.\n// f: the function to apply.\nfunc Map(f func(int) int) int = f(1)\n",
			decl: "func Map",
			want: "Map applies f to each element.\nf: the function to apply.",
		},
		{
			name: "method attaches from func keyword, not identifier",
			src:  "package main\n\ntype Box struct {\n    val v int\n}\n\n// Get returns the boxed value.\nfunc (b Box) Get() int = b.v\n",
			decl: "func (b Box) Get",
			want: "Get returns the boxed value.",
		},
		{
			name: "blank line severs the run",
			src:  "package main\n\n// Not documentation.\n\nfunc Orphan() int = 1\n",
			decl: "func Orphan",
			want: "",
		},
		{
			name: "trailing comment is not documentation",
			src:  "package main\n\nfunc First() int = 1 // trailing\nfunc Second() int = 2\n",
			decl: "func Second",
			want: "",
		},
		{
			name: "paragraph break preserved",
			src:  "package main\n\n// Summary line.\n//\n// Detail paragraph.\nfunc Doc() int = 1\n",
			decl: "func Doc",
			want: "Summary line.\n\nDetail paragraph.",
		},
		{
			name: "pragma contributes no prose but does not sever",
			src:  "package main\n\n// Real documentation.\n//go:noinline\nfunc Pragma() int = 1\n",
			decl: "func Pragma",
			want: "Real documentation.",
		},
		{
			name: "+build constraint contributes no prose",
			src:  "package main\n\n// Real documentation.\n// +build ignore\nfunc Constrained() int = 1\n",
			decl: "func Constrained",
			want: "Real documentation.",
		},
		{
			name: "+build line inside a block comment contributes no prose",
			src:  "package main\n\n/* Real documentation.\n+build ignore */\nfunc BlockConstrained() int = 1\n",
			decl: "func BlockConstrained",
			want: "Real documentation.",
		},
		{
			name: "comment syntax inside a string is not a comment",
			src:  "package main\n\nfunc Sneaky() string = \"// not a comment\"\nfunc After() int = 1\n",
			decl: "func After",
			want: "",
		},
		{
			name: "block comment",
			src:  "package main\n\n/* Block documentation. */\nfunc Blocky() int = 1\n",
			decl: "func Blocky",
			want: "Block documentation.",
		},
		{
			name: "type declaration",
			src:  "package main\n\n// Box holds a value.\ntype Box struct {\n    val v int\n}\n",
			decl: "type Box",
			want: "Box holds a value.",
		},
		{
			name: "sealed type and case",
			src:  "package main\n\n// Shape is a shape.\nsealed type Shape {\n    // Circle is round.\n    case Circle(r float64)\n}\n",
			decl: "case Circle",
			want: "Circle is round.",
		},
		{
			name: "package clause",
			src:  "// Package main is the entry point.\npackage main\n\nfunc main() {}\n",
			decl: "package main",
			want: "Package main is the entry point.",
		},
		{
			name: "adjacent block and line comment on one line join",
			src:  "package main\n\n/* Deprecated. */ // Use Bar instead.\nfunc Foo() int = 1\n",
			decl: "func Foo",
			want: "Deprecated.\nUse Bar instead.",
		},
		{
			name: "block comment preserves indented code sample",
			src:  "package main\n\n/*\n * Example:\n *     val x = 1\n */\nfunc Sample() int = 1\n",
			decl: "func Sample",
			want: "Example:\n    val x = 1",
		},
		{
			name: "multi-line boxed block comment",
			src:  "package main\n\n/*\n * Boxed documentation.\n *\n * Second paragraph.\n */\nfunc Boxed() int = 1\n",
			decl: "func Boxed",
			want: "Boxed documentation.\n\nSecond paragraph.",
		},
		{
			name: "trailing comment after a multi-line raw string",
			src:  "package main\n\nval s = `line one\nline two` // trailing\nfunc AfterRaw() int = 1\n",
			decl: "func AfterRaw",
			want: "",
		},
		{
			name: "undocumented declaration",
			src:  "package main\n\nfunc Bare() int = 1\n",
			decl: "func Bare",
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := docFor(t, tt.src, tt.decl)
			if got != tt.want {
				t.Errorf("doc mismatch\n got: %q\nwant: %q", got, tt.want)
			}
		})
	}
}

// A comment between a lambda parameter and its arrow must not mask the
// bare-lambda-parameter diagnostic. Comments now occupy real token indices
// (hidden channel), so any token-index arithmetic has to skip them.
func TestBareLambdaDiagnosticSurvivesComment(t *testing.T) {
	for _, src := range []string{
		"package main\n\nfunc main() {\n    val f = x => x + 1\n}\n",
		"package main\n\nfunc main() {\n    val f = x /* note */ => x + 1\n}\n",
	} {
		errs := NewAntlrGalaParser().ParseFile(src).Errors
		if len(errs) == 0 {
			t.Fatalf("expected a diagnostic for %q", src)
		}
		var found bool
		for _, e := range errs {
			if strings.Contains(e.Error(), "parenthesized") {
				found = true
			}
		}
		if !found {
			t.Errorf("bare-lambda diagnostic lost for %q; got %v", src, errs)
		}
	}
}

// TestInvalidUTF8DocCommentAndString: in a file that is not valid UTF-8, a doc
// comment and a token's text read each invalid byte as U+FFFD, as ANTLR reads
// it, while the token spans and Src stay on the file's own bytes; diagnostics
// count each invalid byte as one column. The doc comment holds a lone
// continuation byte and a cut three-byte sequence (two invalid bytes); the
// string holds an invalid byte after a two-byte é.
func TestInvalidUTF8DocCommentAndString(t *testing.T) {
	const src = "package main\n\n// Sum of a\x94b, é\xe2\x80.\nval s = \"é\x94\" )\n"
	p := parseGalop(t, src)

	if got, want := p.Docs[DocKey{Line: 4, Column: 0}], "Sum of a�b, é��."; got != want {
		t.Errorf("doc of val s = %q, want %q", got, want)
	}

	// The source-text error names the first invalid byte, in the doc comment
	// at 3:11 (`// Sum of a` is 11 code points) and spans it alone. The syntax
	// error is the ')' at 4:13: `val s = "é` is 10 code points, then the
	// invalid byte, the closing quote and a space.
	if len(p.Errors) != 2 {
		t.Fatalf("errors = %v, want 2", p.Errors)
	}
	charErr, ok := p.Errors[0].(*galaerr.SemanticError)
	if !ok || charErr.Line != 3 || charErr.Column != 11 || charErr.EndColumn != 12 ||
		charErr.Msg != "invalid UTF-8 encoding (byte 0x94)" {
		t.Errorf("first error = %#v, want the invalid byte 0x94 at 3:11, ending at 12", p.Errors[0])
	}
	syntaxErr, ok := p.Errors[1].(*galaerr.SyntaxError)
	if !ok || syntaxErr.Line != 4 || syntaxErr.Column != 13 || !strings.Contains(syntaxErr.Msg, "')'") {
		t.Errorf("second error = %#v, want the ')' at 4:13", p.Errors[1])
	}

	ts := lexNative(src, 0, 0)
	if ts.Src != src {
		t.Fatalf("Src = %q, want the source as given", ts.Src)
	}
	var strToks []int
	for i, tok := range ts.Tokens {
		if tok.Type == native.GalaSTRING {
			strToks = append(strToks, i)
		}
	}
	if len(strToks) != 1 {
		t.Fatalf("string tokens at %v, want one", strToks)
	}
	i := strToks[0]
	tok := ts.Tokens[i]
	if got, want := ts.Text(i), "\"é�\""; got != want {
		t.Errorf("string token text = %q, want %q", got, want)
	}
	if got, want := src[tok.Start:tok.End], "\"é\x94\""; got != want {
		t.Errorf("string token span = %q, want %q", got, want)
	}
	if tok.Line != 4 || tok.Column != 8 {
		t.Errorf("string token at %d:%d, want 4:8", tok.Line, tok.Column)
	}
	var visited []string
	VisitNativeTokens(src, func(text string) { visited = append(visited, text) })
	if !slices.Contains(visited, "\"é�\"") {
		t.Errorf("VisitNativeTokens visited %q, want the string read with U+FFFD", visited)
	}
}
