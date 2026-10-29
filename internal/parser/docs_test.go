package parser

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/antlr4-go/antlr/v4"
)

// TestDocKeyOfCountsCodePoints pins DocKey's column unit: code points, as
// ANTLR counts them, not bytes. "/* é */ " before func is 8 code points and 9
// bytes. (A documented declaration starts its line, so only whitespace comes
// before it; this pins the unit for any key built from a token.)
func TestDocKeyOfCountsCodePoints(t *testing.T) {
	var got []DocKey
	VisitTokens("/* é */ func F() int = 1\n", func(tok antlr.Token) {
		if tok.GetText() == "func" {
			got = append(got, DocKeyOf(tok))
		}
	})
	if want := (DocKey{Line: 1, Column: 8}); len(got) != 1 || got[0] != want {
		t.Errorf("DocKeyOf(func) = %v, want [%v]", got, want)
	}
}

// docFor parses src and returns the doc comment attached to the declaration
// whose first token starts where the given substring does.
func docFor(t *testing.T, src, declPrefix string) string {
	t.Helper()
	_, docs, errs := NewAntlrGalaParser().ParseFile(src).Lenient()
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
		_, _, errs := NewAntlrGalaParser().ParseFile(src).Lenient()
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
