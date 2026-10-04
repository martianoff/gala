package parser

import (
	"testing"

	"github.com/antlr4-go/antlr/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"martianoff/gala/internal/parser/grammar"
)

// A '(', or a '*' or '&' written against its operand, that starts a line after
// a token that can end an expression begins a new statement; every other line
// break parses as it did before. Each case is a function body and the number
// of statements the body must parse into.
func TestNewlineParenStartsStatement(t *testing.T) {
	cases := []struct {
		name  string
		body  string
		stmts int
	}{
		// A '(' on a new line starts a new statement.
		{name: "tuple after call", body: "Println(\"zero\")\n(1, 2)", stmts: 2},
		{name: "tuple after typed val", body: "val a int64 = 1\n(a, 2)", stmts: 2},
		{name: "tuple after identifier", body: "val a = b\n(a, 2)", stmts: 2},
		{name: "tuple after literal", body: "val a = \"x\"\n(a, 2)", stmts: 2},
		{name: "tuple after true", body: "val a = true\n(a, 2)", stmts: 2},
		{name: "tuple after index", body: "val a = xs[0]\n(a, 2)", stmts: 2},
		{name: "tuple after if block", body: "if (a) { Println(1) }\n(a, 2)", stmts: 2},
		{name: "tuple after assignment", body: "x = f()\n(x, 1)", stmts: 2},
		{name: "indented tuple", body: "Println(\"zero\")\n        (1, 2)", stmts: 2},
		{name: "grouping after call", body: "Println(\"zero\")\n(a + b) * 2", stmts: 2},
		{name: "lambda after call", body: "Println(\"zero\")\n(x) => x", stmts: 2},
		{name: "after line comment", body: "Println(\"zero\") // note\n(1, 2)", stmts: 2},
		{name: "after comment line", body: "Println(\"zero\")\n// note\n(1, 2)", stmts: 2},
		{name: "after block comment", body: "Println(\"zero\") /* note */\n(1, 2)", stmts: 2},
		// As in Go, a block comment that spans lines is a line break.
		{name: "after multi-line block comment", body: "Println(\"zero\") /* a\nb */ (1, 2)", stmts: 2},
		{name: "after multi-line raw string", body: "val s = `a\nb`\n(s, 1)", stmts: 2},
		{name: "after string with escaped newline", body: "val s = \"a\\\nb\"\n(s, 1)", stmts: 2},
		{name: "slice literal after call", body: "Println(\"zero\")\n[]int{1, 2}", stmts: 2},

		// So does a '*' or '&' written against its operand.
		{name: "deref expression after val", body: "var n = 1\nval p = &n\n*p + 1", stmts: 3},
		{name: "deref assignment after val", body: "val p = &n\n*p = 5", stmts: 2},
		{name: "deref assignment after call", body: "f()\n*p = 5", stmts: 2},
		{name: "deref as trailing value", body: "val x = 1\n*p", stmts: 2},
		{name: "deref of grouping", body: "val a = b\n*(p)", stmts: 2},
		{name: "double deref", body: "val a = b\n**pp", stmts: 2},
		{name: "address-of as trailing value", body: "val x = 1\n&x", stmts: 2},
		{name: "indented deref", body: "Println(\"zero\")\n        *p = 1", stmts: 2},
		{name: "deref after nested block", body: "if (a) { f(x) }\n*p = 1", stmts: 2},
		{name: "deref after a composite literal", body: "val r = Rect{w: 1}\n*p = 1", stmts: 2},
		{name: "deref after a match", body: "val r = x match {\n    case _ => 0\n}\n*p = 1", stmts: 2},

		// Everything else keeps continuing across the line break.
		{name: "call on same line", body: "f(1)(2)", stmts: 1},
		{name: "call after single-line block comment", body: "f /* note */ (1)", stmts: 1},
		{name: "call after raw string on its closing line", body: "val s = f(`a\nb`)(1)", stmts: 1},
		{name: "call after string with escaped newline", body: "val s = \"a\\\nb\"(1)", stmts: 1},
		{name: "multi-line arguments", body: "Println(\n    1,\n    (2, 3),\n)", stmts: 1},
		{name: "tuple argument on its own line", body: "f(\n    (1, 2)\n)", stmts: 1},
		{name: "lambda argument on its own line", body: "xs.Map(\n    (x) => x\n)", stmts: 1},
		{name: "method chain with leading dots", body: "xs\n    .Map((x) => x)\n    .Filter((x) => true)", stmts: 1},
		{name: "binary plus at line start", body: "val a = 1\n    + 2", stmts: 1},
		{name: "binary minus at line start", body: "val a = b\n    - 2", stmts: 1},
		{name: "logical operators at line start", body: "val a = b\n    && c\n    || d", stmts: 1},
		{name: "binary star at line start", body: "val a = b\n    * c", stmts: 1},
		{name: "binary ampersand at line start", body: "val a = b\n    & c", stmts: 1},
		{name: "binary star before deref", body: "val a = b\n    * *p", stmts: 1},
		{name: "binary star at line end", body: "val a = b *\n    *p", stmts: 1},
		{name: "deref after val equals", body: "val a =\n    *p", stmts: 1},
		{name: "deref after comma", body: "f(1,\n*p)", stmts: 1},
		{name: "multiplication inside arguments", body: "f(w\n    *h)", stmts: 1},
		{name: "bitwise and inside parentheses", body: "val a = (w\n    &h)", stmts: 1},
		{name: "multiplication inside index", body: "val a = xs[i\n    *2]", stmts: 1},
		{name: "star before line comment", body: "val a = b\n    *// times\n    c", stmts: 1},
		{name: "star before block comment", body: "val a = b\n    */* times */c", stmts: 1},
		{name: "deref in block lambda argument", body: "xs.Map((x) => {\n    val p = &x\n    *p\n})", stmts: 1},
		{name: "multiplication in a match arm", body: "val r = x match {\n    case Some(v) => v\n        *factor\n    case _ => 0\n}", stmts: 1},
		{name: "multiplication in a partial function", body: "xs.Collect({\n    case v => v\n        *k\n})", stmts: 1},
		{name: "multiplication in a composite literal", body: "val r = Rect{\n    w: width\n        *scale,\n}", stmts: 1},
		{name: "multiplication in a generic composite literal", body: "val r = Array[int]{\n    w\n        *scale,\n}", stmts: 1},
		{name: "match on next line", body: "val r = x\n    match {\n    case _ => 1\n}", stmts: 1},
		{name: "index on next line", body: "val a = xs\n    [0]", stmts: 1},
		{name: "return value on next line", body: "return\n(1, 2)", stmts: 1},
		{name: "after val equals", body: "val t =\n    (1, 2)", stmts: 1},
		{name: "after lambda arrow", body: "val f = (x int) =>\n    (x, 1)", stmts: 1},
		{name: "after open paren", body: "f(\n(1, 2))", stmts: 1},
		{name: "after comma", body: "f(1,\n(1, 2))", stmts: 1},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			input := "package main\n\nfunc f() {\n" + tt.body + "\n}\n"
			tree, _, errs := NewAntlrGalaParser().ParseLenient(input)
			require.Empty(t, errs)
			block := tree.(grammar.ISourceFileContext).TopLevelDeclaration(0).FunctionDeclaration().Block()
			assert.Len(t, block.AllStatement(), tt.stmts)

			// SLL alone has to accept the input and agree with LL, or the
			// public SLL-first path pays for a second parse.
			llTree, _, llErrors := parseFingerprint(input, antlr.PredictionModeLL)
			sllTree, _, sllErrors := parseFingerprint(input, antlr.PredictionModeSLL)
			assert.Empty(t, sllErrors)
			assert.Empty(t, llErrors)
			assert.Equal(t, llTree, sllTree)
		})
	}
}

// Declarations that open with '(' still parse when that '(' is put on a new
// line after a name, since they accept the re-typed '(' as well.
func TestNewlineParenInDeclarations(t *testing.T) {
	cases := []struct {
		name string
		decl string
	}{
		{name: "function parameters", decl: "func g\n(x int) int = x"},
		{name: "generic function parameters", decl: "func g[T any]\n(x T) T = x"},
		{name: "struct shorthand fields", decl: "struct P\n(x int)"},
		{name: "sealed case fields", decl: "sealed type S {\n    case A\n    (x int)\n    case B\n}"},
		{name: "interface method parameters", decl: "type I interface {\n    M\n    (x int) int\n}"},
		{name: "pointer result type", decl: "func g()\n*int = nil"},
		{name: "interface method pointer result", decl: "type I interface {\n    M()\n    *int\n}"},
		{name: "top-level multiplication", decl: "val a = b\n*c"},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			_, _, errs := NewAntlrGalaParser().ParseLenient("package main\n\n" + tt.decl + "\n")
			assert.Empty(t, errs)
		})
	}
}

// ParseExpressionAt re-parses text it holds apart from its file, so it has to
// apply the same rule: a '(' on a new line does not call the line before.
func TestNewlineParenInParseExpressionAt(t *testing.T) {
	_, err := ParseExpressionAt("f(1)\n(2)", 3, 4, "default value")
	require.Error(t, err)
	assert.Contains(t, err.Error(), `unexpected "(" in default value`)

	_, err = ParseExpressionAt("f(1)(2)", 3, 4, "default value")
	assert.NoError(t, err)
}

// The re-typed '(' is an implementation detail and never named in a syntax
// error: ANTLR's expected-token sets list the '(' the user writes instead.
func TestHideNewlineParen(t *testing.T) {
	cases := []struct{ in, want string }{
		{in: "mismatched input 'int' expecting {'(', NL_LPAREN}", want: "mismatched input 'int' expecting '('"},
		{in: "mismatched input '}' expecting {'(', ')', NL_LPAREN}", want: "mismatched input '}' expecting {'(', ')'}"},
		{in: "missing {'(', NL_LPAREN} at 'x'", want: "missing '(' at 'x'"},
		{in: "missing NL_LPAREN at 'x'", want: "missing '(' at 'x'"},
		{in: "extraneous input '}' expecting {'*', NL_STAR, '&', NL_AMP, IDENTIFIER}", want: "extraneous input '}' expecting {'*', '&', IDENTIFIER}"},
		{in: "missing NL_STAR at 'x'", want: "missing '*' at 'x'"},
		{in: "extraneous input 'x' expecting '('", want: "extraneous input 'x' expecting '('"},
		// The quoted input is the user's text and is left alone.
		{in: "mismatched input 'NL_LPAREN' expecting {'(', NL_LPAREN}", want: "mismatched input 'NL_LPAREN' expecting '('"},
		{in: "no viable alternative at input 'NL_LPAREN'", want: "no viable alternative at input 'NL_LPAREN'"},
	}
	for _, tt := range cases {
		assert.Equal(t, tt.want, hideNewlineTokens(tt.in))
	}
}

// End to end: a parse error where only a '(' can follow names the '(' alone.
func TestNewlineParenAbsentFromSyntaxErrors(t *testing.T) {
	for _, input := range []string{
		"package main\n\nval f func int = g\n",
		"package main\n\nfunc f() {\n    val x = 1\n    (\n}\n",
		"package main\n\nsealed type S {\n    case A\n    (\n}\n",
		"package main\n\nfunc f() {\n    val x = 1\n    *}\n",
	} {
		_, _, errs := NewAntlrGalaParser().ParseLenient(input)
		require.NotEmpty(t, errs, input)
		for _, err := range errs {
			assert.NotContains(t, err.Error(), "NL_", input)
		}
	}
	_, _, errs := NewAntlrGalaParser().ParseLenient("package main\n\nval f func int = g\n")
	require.NotEmpty(t, errs)
	assert.Contains(t, errs[0].Error(), "missing '(' at 'int'")
}
