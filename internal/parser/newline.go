package parser

import (
	"slices"
	"strings"
	"sync"

	"github.com/antlr4-go/antlr/v4"

	"martianoff/gala/internal/parser/grammar"
)

// newlineTokenSource applies GALA's one line-break rule to the token stream:
// a '(' separated by a line break from a token that can end an expression is
// re-typed as NL_LPAREN.
//
// The lexer skips whitespace, so without this the parser cannot tell
//
//	Println("zero")
//	(1, 2)
//
// from `Println("zero")(1, 2)`, and it takes the call. The grammar's call
// suffix accepts only a plain '(', while every construct that can begin with a
// '(' right after such a token accepts NL_LPAREN as well, so the re-typed '('
// starts a new statement and a '(' anywhere else parses exactly as before.
//
// The tokens that end an expression are an identifier, a literal (including
// `true`, `false`, `nil`), ')', ']' and '}'. That is Go's semicolon-insertion
// set minus its keywords and `++`/`--`: none of those can be followed by a
// call, and `return` is left out on purpose so that `return` with its value on
// the next line still returns that value. A line break is a line break in the
// source, so, as in Go, a block comment that spans lines counts as one; a
// comment is otherwise ignored and never the previous token.
//
// Only '(' is affected. A line that starts with '.' continues a method chain,
// and a line that starts with a binary operator continues the expression.
type newlineTokenSource struct {
	antlr.Lexer
	kinds *tokenKinds

	// prevEndLine is the line the previous default-channel token ends on.
	prevEndLine int
	// prevEndsExpr reports whether that token can end an expression.
	prevEndsExpr bool
}

var _ antlr.Lexer = (*newlineTokenSource)(nil)

// newTokenStream is the token stream every parse reads: the lexer's tokens
// with the line-break rule above applied.
func newTokenStream(lexer antlr.Lexer) *antlr.CommonTokenStream {
	return antlr.NewCommonTokenStream(&newlineTokenSource{Lexer: lexer, kinds: kinds()}, antlr.TokenDefaultChannel)
}

// tokenKinds holds the token types the parser driver checks, looked up by name
// in the generated vocabulary: the generated constants are unexported.
type tokenKinds struct {
	lparen, nlLparen, identifier int
	// endsExpr and spansLines are indexed by token type. spansLines marks the
	// literals whose text can hold a line break: a raw string, or any quoted
	// literal with an escaped newline.
	endsExpr, spansLines []bool
}

// is reports whether ttype is marked in set; EOF has a negative type.
func is(set []bool, ttype int) bool { return ttype >= 0 && ttype < len(set) && set[ttype] }

// kinds is derived once from the generated vocabulary and never changes, like
// the generated static data it reads. It is built on first use, so a binary
// that never parses does not pay for it.
var kinds = sync.OnceValue(func() *tokenKinds {
	// The parser's vocabulary, not the lexer's: no lexer rule produces NL_LPAREN,
	// so only the parser knows its type.
	grammar.GalaParserInit()
	vocab := &grammar.GalaParserStaticData
	byName := map[string]int{}
	for _, names := range [][]string{vocab.SymbolicNames, vocab.LiteralNames} {
		for ttype, name := range names {
			if name != "" {
				byName[name] = ttype
			}
		}
	}
	mustType := func(name string) int {
		ttype, ok := byName[name]
		if !ok {
			panic("parser: grammar has no token " + name)
		}
		return ttype
	}
	k := &tokenKinds{
		lparen:     mustType("'('"),
		nlLparen:   mustType("NL_LPAREN"),
		identifier: mustType("IDENTIFIER"),
		endsExpr:   make([]bool, len(vocab.SymbolicNames)),
		spansLines: make([]bool, len(vocab.SymbolicNames)),
	}
	quoted := []string{"STRING", "CHAR_LIT", "RAW_STRING", "INTERPOLATED_STRING", "FORMAT_STRING"}
	for _, name := range quoted {
		k.spansLines[mustType(name)] = true
	}
	for _, name := range append(quoted,
		"IDENTIFIER", "INT_LIT", "FLOAT_LIT", "'true'", "'false'", "'nil'", "')'", "']'", "'}'") {
		k.endsExpr[mustType(name)] = true
	}
	return k
})

func (s *newlineTokenSource) NextToken() antlr.Token {
	tok := s.Lexer.NextToken()
	if tok.GetChannel() != antlr.TokenDefaultChannel {
		return tok
	}
	k := s.kinds
	ttype := tok.GetTokenType()
	if ttype == k.lparen && s.prevEndsExpr && tok.GetLine() > s.prevEndLine {
		tok = s.GetTokenFactory().Create(tok.GetSource(), k.nlLparen, tok.GetText(),
			tok.GetChannel(), tok.GetStart(), tok.GetStop(), tok.GetLine(), tok.GetColumn())
		ttype = k.nlLparen
	}
	s.prevEndsExpr = is(k.endsExpr, ttype)
	s.prevEndLine = tok.GetLine()
	if is(k.spansLines, ttype) {
		s.prevEndLine += strings.Count(tok.GetText(), "\n")
	}
	return tok
}

// hideNewlineParen keeps the internal NL_LPAREN token out of syntax errors.
// It rewrites only the token set ANTLR expected — after "expecting", or
// between "missing" and "at" — never the quoted input, which is the user's own
// text. NL_LPAREN is dropped from the set: wherever it is expected a plain '('
// is expected too.
func hideNewlineParen(msg string) string {
	if i := strings.LastIndex(msg, " expecting "); i >= 0 {
		i += len(" expecting ")
		return msg[:i] + withoutNewlineParen(msg[i:])
	}
	if rest, ok := strings.CutPrefix(msg, "missing "); ok {
		if i := strings.Index(rest, " at "); i >= 0 {
			return "missing " + withoutNewlineParen(rest[:i]) + rest[i:]
		}
	}
	return msg
}

// withoutNewlineParen removes NL_LPAREN from an ANTLR token set, printed
// either as one token name or as "{a, b, ...}", and unwraps a set left with a
// single token the way ANTLR prints one.
func withoutNewlineParen(set string) string {
	if set == "NL_LPAREN" {
		return "'('"
	}
	if !strings.HasPrefix(set, "{") || !strings.HasSuffix(set, "}") {
		return set
	}
	inner := set[1 : len(set)-1]
	items := slices.DeleteFunc(strings.Split(inner, ", "), func(s string) bool { return s == "NL_LPAREN" })
	if len(items) == 1 {
		return items[0]
	}
	return "{" + strings.Join(items, ", ") + "}"
}
