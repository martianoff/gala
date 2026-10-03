package parser

import (
	"strings"

	"github.com/antlr4-go/antlr/v4"

	"martianoff/gala/internal/parser/grammar"
)

// newlineTokenSource applies GALA's one line-break rule to the token stream:
// a '(' that starts a new line, right after a token that can end an
// expression, is re-typed as NL_LPAREN.
//
// The lexer skips whitespace, so without this the parser cannot tell
//
//	Println("zero")
//	(1, 2)
//
// from `Println("zero")(1, 2)`, and it takes the call. The grammar's call
// suffix accepts only a plain '(', while every construct that can begin with a
// '(' accepts NL_LPAREN as well, so the re-typed '(' starts a new statement and
// a '(' anywhere else parses exactly as before. The tokens that end an
// expression are the ones Go inserts a semicolon after: an identifier, a
// literal, ')', ']' and '}'. Hidden-channel tokens (comments) are passed
// through and do not count as the previous token.
//
// Only '(' is affected. A line that starts with '.' continues a method chain,
// a line that starts with a binary operator continues the expression, and
// `return` followed by a value on the next line still returns that value.
type newlineTokenSource struct {
	antlr.Lexer

	// prevEndLine is the line the previous default-channel token ends on.
	prevEndLine int
	// prevEndsExpr reports whether that token can end an expression.
	prevEndsExpr bool
}

// newTokenStream is the token stream every parse reads: the lexer's tokens
// with the line-break rule above applied.
func newTokenStream(lexer antlr.Lexer) *antlr.CommonTokenStream {
	return antlr.NewCommonTokenStream(&newlineTokenSource{Lexer: lexer}, antlr.TokenDefaultChannel)
}

// tokenKinds holds the token types the parser driver checks, looked up by name
// in the generated vocabulary: the generated constants are unexported.
type tokenKinds struct {
	lparen, nlLparen, rawString, identifier int
	// endsExpr is indexed by token type.
	endsExpr []bool
}

var kinds = func() tokenKinds {
	// The parser's vocabulary, not the lexer's: no lexer rule produces NL_LPAREN,
	// so only the parser knows its type.
	vocab := grammar.NewgalaParser(nil)
	byName := map[string]int{}
	for _, names := range [][]string{vocab.GetSymbolicNames(), vocab.GetLiteralNames()} {
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
	k := tokenKinds{
		lparen:     mustType("'('"),
		nlLparen:   mustType("NL_LPAREN"),
		rawString:  mustType("RAW_STRING"),
		identifier: mustType("IDENTIFIER"),
		endsExpr:   make([]bool, len(vocab.GetSymbolicNames())),
	}
	for _, name := range []string{
		"IDENTIFIER", "INT_LIT", "FLOAT_LIT", "STRING", "CHAR_LIT", "RAW_STRING",
		"INTERPOLATED_STRING", "FORMAT_STRING", "'true'", "'false'", "'nil'",
		"')'", "']'", "'}'",
	} {
		k.endsExpr[mustType(name)] = true
	}
	return k
}()

func (s *newlineTokenSource) NextToken() antlr.Token {
	tok := s.Lexer.NextToken()
	if tok.GetChannel() != antlr.TokenDefaultChannel {
		return tok
	}
	ttype := tok.GetTokenType()
	if ttype == kinds.lparen && s.prevEndsExpr && tok.GetLine() > s.prevEndLine {
		tok = s.GetTokenFactory().Create(tok.GetSource(), kinds.nlLparen, tok.GetText(),
			tok.GetChannel(), tok.GetStart(), tok.GetStop(), tok.GetLine(), tok.GetColumn())
		ttype = kinds.nlLparen
	}
	// EOF has a negative type and ends nothing.
	s.prevEndsExpr = ttype >= 0 && ttype < len(kinds.endsExpr) && kinds.endsExpr[ttype]
	s.prevEndLine = tok.GetLine()
	// A raw string is the only default-channel token that can span lines.
	if ttype == kinds.rawString {
		s.prevEndLine += strings.Count(tok.GetText(), "\n")
	}
	return tok
}

// hideNewlineParen keeps the internal NL_LPAREN token out of syntax errors.
// Wherever NL_LPAREN is expected a plain '(' is expected too, so ANTLR's
// "expecting {..., '(', ..., NL_LPAREN}" sets lose the duplicate; a set that
// names it alone shows it as the '(' the user writes.
var newlineParenNames = strings.NewReplacer(", NL_LPAREN", "", "NL_LPAREN, ", "", "NL_LPAREN", "'('")

func hideNewlineParen(msg string) string { return newlineParenNames.Replace(msg) }
