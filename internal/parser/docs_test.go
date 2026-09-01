package parser

import (
	"strings"
	"testing"
)

// docFor parses src and returns the doc comment attached to the declaration
// whose first token starts at the given substring's offset.
func docFor(t *testing.T, src, declPrefix string) string {
	t.Helper()
	_, docs, errs := NewAntlrGalaParser().ParseLenient(src)
	if len(errs) > 0 {
		t.Fatalf("parse errors: %v", errs)
	}
	off := strings.Index(src, declPrefix)
	if off < 0 {
		t.Fatalf("declaration %q not found in source", declPrefix)
	}
	return docs[off]
}

func TestDocComments(t *testing.T) {
	tests := []struct {
		name    string
		src     string
		decl    string
		want    string
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
