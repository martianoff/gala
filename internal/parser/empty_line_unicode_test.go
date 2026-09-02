package parser

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestEmptyLineChecksAreRuneIndexed guards the blank-line layout checks
// against a rune/byte offset mismatch.
//
// ANTLR reports token offsets as code-point indices, but checkEmptyLines used
// to slice the raw source string with them. A Go string slices by BYTES, so a
// single non-ASCII rune anywhere before the package/import boundary shifted
// the inspected window by the extra bytes of that rune — and the checker then
// examined text that was not the gap it meant to examine. The visible symptom
// was a well-formed file rejected with "importDeclaration should follow by an
// empty line" (or the packageClause variant) purely because a comment
// contained an em dash, a curly quote, or any accented letter.
//
// Each case below is correctly formatted and must parse cleanly. The ASCII
// twin of every Unicode case is included so a failure points at the encoding
// rather than at the layout.
func TestEmptyLineChecksAreRuneIndexed(t *testing.T) {
	p := NewAntlrGalaParser()

	cases := []struct {
		name  string
		input string
	}{
		{
			name: "ascii comment between package and import",
			input: `package main

// dot import - the symbols come into scope unqualified
import "os"

func main() {
    Println(os.Getpid())
}`,
		},
		{
			name: "em dash in comment between package and import",
			input: `package main

// dot import — the symbols come into scope unqualified
import "os"

func main() {
    Println(os.Getpid())
}`,
		},
		{
			name: "non-ascii in the package-level doc comment",
			input: `package main

// Café metrics — counts drinks served.
// Prices are in €.
import "os"

func main() {
    Println(os.Getpid())
}`,
		},
		{
			name: "non-ascii inside a string before the first declaration",
			input: `package main

import "os"

val greeting = "naïve café — ☕"

func main() {
    Println(greeting)
    Println(os.Getpid())
}`,
		},
		{
			name: "no imports, non-ascii comment before first declaration",
			input: `package main

// Ünïcödé comment — no imports in this file.
func main() {
    Println("ok")
}`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tree, _, err := p.Parse(tc.input)
			require.NoError(t, err, "well-formed source must parse")
			assert.NotNil(t, tree)
		})
	}
}

// TestEmptyLineChecksStillRejectMissingBlankLines is the negative half: the
// rune-indexing fix must not weaken the layout rules it was fixing. Both
// violations are still errors, with and without non-ASCII text present.
func TestEmptyLineChecksStillRejectMissingBlankLines(t *testing.T) {
	p := NewAntlrGalaParser()

	cases := []struct {
		name    string
		input   string
		wantMsg string
	}{
		{
			name: "no blank line after package clause",
			input: `package main
import "os"

func main() {
    Println(os.Getpid())
}`,
			wantMsg: "packageClause should follow by an empty line",
		},
		{
			name: "no blank line after package clause, with non-ascii",
			input: `package main
// café
import "os"

func main() {
    Println(os.Getpid())
}`,
			wantMsg: "packageClause should follow by an empty line",
		},
		{
			name: "no blank line after import declaration",
			input: `package main

import "os"
func main() {
    Println(os.Getpid())
}`,
			wantMsg: "importDeclaration should follow by an empty line",
		},
		{
			name: "no blank line after import declaration, with non-ascii",
			input: `package main

// café — metrics
import "os"
func main() {
    Println(os.Getpid())
}`,
			wantMsg: "importDeclaration should follow by an empty line",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := p.Parse(tc.input)
			require.Error(t, err, "layout violation must still be rejected")
			assert.Contains(t, err.Error(), tc.wantMsg)
		})
	}
}
