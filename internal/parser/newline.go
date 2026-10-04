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
// A '*' or '&' is re-typed only where a statement can begin — directly inside
// a block's '{', not within a '(' or '[', a composite literal's '{' (written
// against its type: `Rect{`), a '{' that opens case arms (a match, a partial
// function, a sealed type) or at the top level — and only when written
// against its operand. Followed by whitespace or a comment it is a binary operator
// continuing the line before, like any other operator at line start: `* b`
// multiplies, `*b` dereferences. '*' and '&' are re-typed because their prefix
// forms are pointer operations a line can start with (`*p = 5`, a trailing
// `*p` or `&n`); '+', '-' and '^' are not, so a line starting with one always
// continues the expression. A line that starts with '.' continues a method
// chain.
type newlineTokenSource struct {
	antlr.Lexer
	kinds *tokenKinds

	// prevEndLine is the line the previous default-channel token ends on.
	prevEndLine int
	// prevEndsExpr reports whether that token can end an expression.
	prevEndsExpr bool
	// prevType and prevStop are the previous default-channel token's type and
	// the index of its last character.
	prevType, prevStop int
	// open holds the brackets open at this point, innermost last: true for a
	// block's '{', false for any other bracket.
	open []bool
}

var _ antlr.Lexer = (*newlineTokenSource)(nil)

// newTokenStream is the token stream every parse reads: the lexer's tokens
// with the line-break rule above applied.
func newTokenStream(lexer antlr.Lexer) *antlr.CommonTokenStream {
	return antlr.NewCommonTokenStream(&newlineTokenSource{Lexer: lexer, kinds: kinds()}, antlr.TokenDefaultChannel)
}

// lineStartToken describes one re-typed token: the source token it is
// re-typed from, and whether it is re-typed only where a statement can begin
// and only when written directly against its operand.
type lineStartToken struct {
	from         string
	needsOperand bool
}

// lineStartTokens is keyed by the name of each re-typed token. The re-typed
// tokens are internal: syntax errors name the original instead (see
// hideNewlineTokens), which is why the table is keyed this way round.
var lineStartTokens = map[string]lineStartToken{
	"NL_LPAREN": {from: "'('"},
	"NL_STAR":   {from: "'*'", needsOperand: true},
	"NL_AMP":    {from: "'&'", needsOperand: true},
}

// retype is lineStartTokens resolved to token types, for one source token
// type: the type it becomes (0 when it is never re-typed) and needsOperand.
type retype struct {
	to           int
	needsOperand bool
}

// tokenKinds holds the token types the parser driver checks, looked up by name
// in the generated vocabulary: the generated constants are unexported.
type tokenKinds struct {
	identifier, lbrace, rbrack, caseKw int
	// bracket is indexed by token type: +1 for a bracket that opens ('(',
	// NL_LPAREN, '[', '{'), -1 for one that closes (')', ']', '}'), else 0.
	bracket []int8
	// atLineStart is indexed by token type: how a token is re-typed when it
	// starts a line after a token that can end an expression.
	atLineStart []retype
	// endsExpr and spansLines are indexed by token type. spansLines marks the
	// literals whose text can hold a line break: a raw string, or any quoted
	// literal with an escaped newline.
	endsExpr, spansLines []bool
}

// is reports whether ttype is marked in set; EOF has a negative type.
func is(set []bool, ttype int) bool { return ttype >= 0 && ttype < len(set) && set[ttype] }

// retypeOf is how the line-break rule re-types ttype; EOF is never re-typed.
func (k *tokenKinds) retypeOf(ttype int) retype {
	if ttype < 0 || ttype >= len(k.atLineStart) {
		return retype{}
	}
	return k.atLineStart[ttype]
}

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
		identifier:  mustType("IDENTIFIER"),
		lbrace:      mustType("'{'"),
		rbrack:      mustType("']'"),
		caseKw:      mustType("'case'"),
		bracket:     make([]int8, len(vocab.SymbolicNames)),
		atLineStart: make([]retype, len(vocab.SymbolicNames)),
		endsExpr:    make([]bool, len(vocab.SymbolicNames)),
		spansLines:  make([]bool, len(vocab.SymbolicNames)),
	}
	for _, name := range []string{"'('", "NL_LPAREN", "'['", "'{'"} {
		k.bracket[mustType(name)] = 1
	}
	for _, name := range []string{"')'", "']'", "'}'"} {
		k.bracket[mustType(name)] = -1
	}
	for to, tok := range lineStartTokens {
		k.atLineStart[mustType(tok.from)] = retype{to: mustType(to), needsOperand: tok.needsOperand}
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
	line := tok.GetLine()
	if r := k.retypeOf(ttype); r.to != 0 && s.prevEndsExpr && line > s.prevEndLine &&
		(!r.needsOperand || s.atStatementLevel() && !s.operatorStandsApart()) {
		tok = s.GetTokenFactory().Create(tok.GetSource(), r.to, tok.GetText(),
			tok.GetChannel(), tok.GetStart(), tok.GetStop(), line, tok.GetColumn())
		ttype = r.to
	}
	s.trackBrackets(tok, ttype)
	s.prevType, s.prevStop = ttype, tok.GetStop()
	s.prevEndsExpr = is(k.endsExpr, ttype)
	s.prevEndLine = line
	if is(k.spansLines, ttype) {
		s.prevEndLine += strings.Count(tok.GetText(), "\n")
	}
	return tok
}

// trackBrackets keeps open up to date with tok, of type ttype. A '{' opens a
// block unless it is written against a type name or ']' (a composite literal,
// `Rect{` or `Array[int]{`); a 'case' right after a '{' shows that '{' opens
// case arms instead.
func (s *newlineTokenSource) trackBrackets(tok antlr.Token, ttype int) {
	k := s.kinds
	switch {
	case ttype >= 0 && ttype < len(k.bracket) && k.bracket[ttype] > 0:
		block := ttype == k.lbrace &&
			!((s.prevType == k.identifier || s.prevType == k.rbrack) && tok.GetStart() == s.prevStop+1)
		s.open = append(s.open, block)
	case ttype >= 0 && ttype < len(k.bracket) && k.bracket[ttype] < 0:
		if len(s.open) > 0 {
			s.open = s.open[:len(s.open)-1]
		}
	case ttype == k.caseKw && s.prevType == k.lbrace && len(s.open) > 0:
		s.open[len(s.open)-1] = false
	}
}

// atStatementLevel reports whether a statement can begin here: directly inside
// a block's '{'. At the top level only declarations begin, and those start
// with a keyword.
func (s *newlineTokenSource) atStatementLevel() bool {
	return len(s.open) > 0 && s.open[len(s.open)-1]
}

// operatorStandsApart reports whether the token the lexer has just consumed is
// followed by whitespace or a comment rather than by its operand. The lexer
// has just consumed it, so LA(1) is the character after it.
func (s *newlineTokenSource) operatorStandsApart() bool {
	in := s.GetInputStream()
	switch in.LA(1) {
	case ' ', '\t', '\r', '\n':
		// The characters the grammar's WS rule skips.
		return true
	case '/':
		next := in.LA(2)
		return next == '/' || next == '*'
	}
	return false
}

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
	if tok, ok := lineStartTokens[set]; ok {
		return tok.from
	}
	if !strings.HasPrefix(set, "{") || !strings.HasSuffix(set, "}") {
		return set
	}
	inner := set[1 : len(set)-1]
	items := slices.DeleteFunc(strings.Split(inner, ", "), func(s string) bool {
		_, retyped := lineStartTokens[s]
		return retyped
	})
	if len(items) == 1 {
		return items[0]
	}
	return "{" + strings.Join(items, ", ") + "}"
}
