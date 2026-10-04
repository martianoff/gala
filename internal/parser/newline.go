package parser

import (
	"slices"
	"strings"
	"sync"

	"github.com/antlr4-go/antlr/v4"

	"martianoff/gala/internal/parser/grammar"
)

// newlineTokenSource applies GALA's line-break rule to the token stream: a
// '(' separated by a line break from a token that can end an expression is
// re-typed as NL_LPAREN, and so is a '*' or '&' written directly against its
// operand, as NL_STAR or NL_AMP.
//
// The lexer skips whitespace, so without this the parser cannot tell
//
//	Println("zero")
//	(1, 2)
//
// from `Println("zero")(1, 2)`, and it takes the call. Likewise
//
//	val p = &n
//	*p = 5
//
// reads as `&n * p = 5`, and `*p + 1` on its own line multiplies the line
// before. The grammar's call suffix accepts only a plain '(', and
// multiplication and bitwise and only a plain '*' and '&', while every
// construct that can begin with one of these tokens right after such a token
// accepts the re-typed form as well. So the re-typed token starts a new
// statement and the token anywhere else parses exactly as before.
//
// The tokens that end an expression are an identifier, a literal (including
// `true`, `false`, `nil`), ')', ']' and '}'. That is Go's semicolon-insertion
// set minus its keywords and `++`/`--`: none of those can be followed by a
// call, and `return` is left out on purpose so that `return` with its value on
// the next line still returns that value. A line break is a line break in the
// source, so, as in Go, a block comment that spans lines counts as one; a
// comment is otherwise ignored and never the previous token.
//
// A '*' or '&' followed by whitespace is a binary operator continuing the line
// before, like any other operator at line start: `* b` multiplies, `*b`
// dereferences. A line that starts with '.' continues a method chain.
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

// lineStartTokens maps each token the line-break rule re-types to the token
// it becomes. The re-typed tokens are internal: syntax errors name the
// original instead (see hideNewlineTokens).
var lineStartTokens = map[string]string{
	"'('": "NL_LPAREN",
	"'*'": "NL_STAR",
	"'&'": "NL_AMP",
}

// tokenKinds holds the token types the parser driver checks, looked up by name
// in the generated vocabulary: the generated constants are unexported.
type tokenKinds struct {
	lparen, identifier int
	// atLineStart is indexed by token type: the type a token is re-typed as
	// when it starts a line after a token that can end an expression, or 0.
	atLineStart []int
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
	// The parser's vocabulary, not the lexer's: no lexer rule produces the
	// re-typed tokens, so only the parser knows their types.
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
		lparen:      mustType("'('"),
		identifier:  mustType("IDENTIFIER"),
		atLineStart: make([]int, len(vocab.SymbolicNames)),
		endsExpr:    make([]bool, len(vocab.SymbolicNames)),
		spansLines:  make([]bool, len(vocab.SymbolicNames)),
	}
	for from, to := range lineStartTokens {
		k.atLineStart[mustType(from)] = mustType(to)
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
	if s.prevEndsExpr && tok.GetLine() > s.prevEndLine && ttype >= 0 && ttype < len(k.atLineStart) {
		// The lexer has just consumed the token, so LA(1) is the character
		// after it: a '*' or '&' followed by whitespace stays binary.
		if to := k.atLineStart[ttype]; to != 0 && (ttype == k.lparen || !isSpace(s.GetInputStream().LA(1))) {
			tok = s.GetTokenFactory().Create(tok.GetSource(), to, tok.GetText(),
				tok.GetChannel(), tok.GetStart(), tok.GetStop(), tok.GetLine(), tok.GetColumn())
			ttype = to
		}
	}
	s.prevEndsExpr = is(k.endsExpr, ttype)
	s.prevEndLine = tok.GetLine()
	if is(k.spansLines, ttype) {
		s.prevEndLine += strings.Count(tok.GetText(), "\n")
	}
	return tok
}

// isSpace reports whether the character code r, as a CharStream returns it,
// is one the grammar's WS rule skips; EOF (-1) is not.
func isSpace(r int) bool { return r == ' ' || r == '\t' || r == '\r' || r == '\n' }

// hideNewlineTokens keeps the internal re-typed tokens out of syntax errors.
// It rewrites only the token set ANTLR expected — after "expecting", or
// between "missing" and "at" — never the quoted input, which is the user's own
// text. A re-typed token is dropped from the set: wherever it is expected, the
// token it was re-typed from is expected too.
func hideNewlineTokens(msg string) string {
	if i := strings.LastIndex(msg, " expecting "); i >= 0 {
		i += len(" expecting ")
		return msg[:i] + withoutNewlineTokens(msg[i:])
	}
	if rest, ok := strings.CutPrefix(msg, "missing "); ok {
		if i := strings.Index(rest, " at "); i >= 0 {
			return "missing " + withoutNewlineTokens(rest[:i]) + rest[i:]
		}
	}
	return msg
}

// withoutNewlineTokens removes the re-typed tokens from an ANTLR token set,
// printed either as one token name or as "{a, b, ...}", and unwraps a set left
// with a single token the way ANTLR prints one.
func withoutNewlineTokens(set string) string {
	for from, to := range lineStartTokens {
		if set == to {
			return from
		}
	}
	if !strings.HasPrefix(set, "{") || !strings.HasSuffix(set, "}") {
		return set
	}
	inner := set[1 : len(set)-1]
	items := slices.DeleteFunc(strings.Split(inner, ", "), isNewlineToken)
	if len(items) == 1 {
		return items[0]
	}
	return "{" + strings.Join(items, ", ") + "}"
}

// isNewlineToken reports whether name is one of the re-typed tokens.
func isNewlineToken(name string) bool {
	for _, to := range lineStartTokens {
		if name == to {
			return true
		}
	}
	return false
}
