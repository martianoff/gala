package parser

import (
	"testing"

	"martianoff/gala/galaerr"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGoDirectiveIsCoded pins GALA-E0119: a Go compiler directive in a GALA
// file is one diagnostic at its `//`, whose hint names the GALA form. GALA
// does not carry the line into the generated Go, so it used to be dropped
// silently: a `//go:embed` variable stayed empty, and a `//go:build linux`
// file built and ran on every platform.
func TestGoDirectiveIsCoded(t *testing.T) {
	cases := []struct {
		name  string
		input string
		line  int
		col   int // 0-based, as the parser reports it
		hint  string
	}{
		{
			name:  "embed above a var",
			input: "package main\n\n//go:embed hello.txt\nvar content string\n",
			line:  3,
			hint:  "`embed val content = \"hello.txt\"`",
		},
		{
			name:  "embed of several patterns",
			input: "package main\n\n//go:embed static/* \"a b.txt\"\nvar files string\n",
			line:  3,
			hint:  "`embed val files EmbeddedFS = \"static/*\", \"a b.txt\"`",
		},
		{
			name:  "build constraint above the package clause",
			input: "//go:build linux\n\npackage main\n\nfunc main() {\n    Println(\"x\")\n}\n",
			line:  1,
			hint:  "a GALA file has no build constraint",
		},
		{
			name:  "legacy +build constraint",
			input: "// +build linux\n\npackage main\n",
			line:  1,
			hint:  "a GALA file has no build constraint",
		},
		{
			name:  "a directive in a doc comment",
			input: "package main\n\n// Pragma is documented.\n//go:noinline\nfunc Pragma() int = 1\n",
			line:  4,
			hint:  "GALA has no form of `//go:noinline`",
		},
		{
			name:  "a directive inside a function body",
			input: "package main\n\nfunc main() {\n    x := 1 //go:noinline\n    Println(x)\n}\n",
			line:  4,
			col:   11,
			hint:  "GALA has no form of `//go:noinline`",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			errs := NewAntlrGalaParser().ParseFile(tc.input).Errors
			require.Len(t, errs, 1, "errors: %v", errs)
			se, ok := errs[0].(*galaerr.SemanticError)
			require.True(t, ok, "expected GALA-E0119, got: %v", errs[0])
			require.Equal(t, galaerr.CodeGoDirective, se.Code)
			assert.Equal(t, tc.line, se.Line)
			assert.Equal(t, tc.col, se.Column)
			assert.Contains(t, se.Hint, tc.hint)
		})
	}
}

// TestGoDirectiveLookalikesAreComments pins what is not a directive: prose
// that mentions one, `// go:` with a space (Go reads only `//go:` as a
// directive), a block comment, and a `//go:` inside a string.
func TestGoDirectiveLookalikesAreComments(t *testing.T) {
	for _, input := range []string{
		"package main\n\n// Use embed val, not //go:embed.\nfunc A() int = 1\n",
		"package main\n\n// go:embed is Go's directive.\nfunc A() int = 1\n",
		"package main\n\n/*\n//go:embed hello.txt\n+build linux\n*/\nfunc A() int = 1\n",
		"package main\n\nfunc A() string = \"//go:embed x\"\n",
		"package main\n\n//go: no name follows\nfunc A() int = 1\n",
	} {
		require.Empty(t, NewAntlrGalaParser().ParseFile(input).Errors, "input:\n%s", input)
	}
}
