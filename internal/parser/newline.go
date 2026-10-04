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
// a block's '{' — and only when written against its operand. A '{' opens a
// block when it ends the header of a `func`, `if` or `for`, or follows a token
// no type ends with (`=>`, `)`, `else`, ...); after a type name or ']' it opens
// a composite literal, after `struct` or `interface` a declaration body, and a
// '{' whose first token is `case` opens case arms (a match, a partial
// function, a sealed type). A header has no block when it is left for a new
// line or for `=>`, when a func's `=` gives it an expression body, when an
// if's `else` comes first (an if-expression), or when the token after an if's
// parenthesized condition is not '{'. Inside a '{' that is not a block,
// within a '(' or '[', and at the top level, '*' and '&' are never re-typed.
// Followed by whitespace or a comment it is a binary operator continuing the
// line before, like any other operator at line start: `* b` multiplies, `*b`
// dereferences. '*' and '&' are re-typed because their prefix forms are
// pointer operations a line can start with (`*p = 5`, a trailing `*p` or
// `&n`); '+', '-' and '^' are not, so a line starting with one always
// continues the expression. A line that starts with '.' continues a method
// chain.
type newlineTokenSource struct {
	antlr.Lexer
	kinds *tokenKinds

	// prevEndLine is the line the previous default-channel token ends on.
	prevEndLine int
	// prevEndsExpr reports whether that token can end an expression.
	prevEndsExpr bool
	// prevType is the previous default-channel token's type.
	prevType int
	// open holds the brackets open at this point, innermost last.
	open []openBracket
	// headers holds the `func`, `if` and `for` headers whose block's '{' is
	// still to come, innermost last.
	headers []header
}

// header is a `func`, `if` or `for` whose block's '{' is still to come.
type header struct {
	// depth is len(open) where the header started: its '{' opens there.
	depth int
	// fn marks a func, whose `=` gives it an expression body instead.
	fn bool
	// cond tracks an if's parenthesized condition: condOpen while it is
	// open, condClosed once it has closed.
	cond int8
}

const (
	condOpen int8 = 1 + iota
	condClosed
)

// openBracket is the kind of an open bracket.
type openBracket int8

const (
	parenOpen openBracket = iota // '(' or '['
	braceOpen                    // a '{' that is not a block: a composite literal, case arms, a declaration body
	blockOpen                    // a block's '{'
)

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
	identifier, lparen, lbrace, rbrace, assign, arrow, caseKw, elseKw, funcKw, ifKw int
	// bracket is indexed by token type: +1 for a bracket that opens ('(',
	// NL_LPAREN, '[', '{'), -1 for one that closes (')', ']', '}'), else 0.
	bracket []int8
	// opensHeader marks `func`, `if` and `for`, whose block's '{' follows
	// their header; notBlockAfter marks the tokens a '{' that is not a block
	// follows: a type name or ']' (a composite literal), `struct`, `interface`.
	opensHeader, notBlockAfter []bool
	// atLineStart is indexed by token type: how a token is re-typed when it
	// starts a line after a token that can end an expression.
	atLineStart []retype
	// endsExpr and spansLines are indexed by token type. spansLines marks the
	// literals whose text can hold a line break: a raw string, or any quoted
	// literal with an escaped newline.
	endsExpr, spansLines []bool
}

// at is table's entry for ttype, or the zero value for a type it does not
// cover; EOF has a negative type.
func at[T any](table []T, ttype int) T {
	if ttype < 0 || ttype >= len(table) {
		var zero T
		return zero
	}
	return table[ttype]
}

// is reports whether ttype is marked in set.
func is(set []bool, ttype int) bool { return at(set, ttype) }

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
	n := len(vocab.SymbolicNames)
	k := &tokenKinds{
		identifier:    mustType("IDENTIFIER"),
		lparen:        mustType("'('"),
		lbrace:        mustType("'{'"),
		rbrace:        mustType("'}'"),
		assign:        mustType("'='"),
		arrow:         mustType("'=>'"),
		caseKw:        mustType("'case'"),
		elseKw:        mustType("'else'"),
		funcKw:        mustType("'func'"),
		ifKw:          mustType("'if'"),
		bracket:       make([]int8, n),
		opensHeader:   make([]bool, n),
		notBlockAfter: make([]bool, n),
		atLineStart:   make([]retype, n),
		endsExpr:      make([]bool, n),
		spansLines:    make([]bool, n),
	}
	for _, name := range []string{"'('", "NL_LPAREN", "'['", "'{'"} {
		k.bracket[mustType(name)] = 1
	}
	for _, name := range []string{"')'", "']'", "'}'"} {
		k.bracket[mustType(name)] = -1
	}
	for _, name := range []string{"'func'", "'if'", "'for'"} {
		k.opensHeader[mustType(name)] = true
	}
	for _, name := range []string{"IDENTIFIER", "']'", "'struct'", "'interface'"} {
		k.notBlockAfter[mustType(name)] = true
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
	if r := at(k.atLineStart, ttype); r.to != 0 && s.prevEndsExpr && line > s.prevEndLine &&
		(!r.needsOperand || s.atStatementLevel() && !s.operatorStandsApart()) {
		tok = s.GetTokenFactory().Create(tok.GetSource(), r.to, tok.GetText(),
			tok.GetChannel(), tok.GetStart(), tok.GetStop(), line, tok.GetColumn())
		ttype = r.to
	}
	s.trackBrackets(ttype, s.prevEndsExpr && line > s.prevEndLine)
	s.prevType = ttype
	s.prevEndsExpr = is(k.endsExpr, ttype)
	s.prevEndLine = line
	if is(k.spansLines, ttype) {
		s.prevEndLine += strings.Count(tok.GetText(), "\n")
	}
	return tok
}

// trackBrackets keeps open and headers up to date with a token of type ttype;
// newLine reports that it starts a line after a token that can end an
// expression (see the newlineTokenSource doc for which '{' opens a block).
func (s *newlineTokenSource) trackBrackets(ttype int, newLine bool) {
	k := s.kinds
	depth := len(s.open)
	h := s.pendingHeader()
	if h != nil && (newLine || h.cond == condClosed && ttype != k.lbrace) {
		// A header left for a new line, or an if whose parenthesized
		// condition is not followed by '{', has no block.
		s.headers = s.headers[:len(s.headers)-1]
		h = nil
	}
	b := at(k.bracket, ttype)
	switch {
	case b > 0 && ttype == k.lbrace:
		kind := blockOpen
		if h != nil {
			s.headers = s.headers[:len(s.headers)-1]
		} else if is(k.notBlockAfter, s.prevType) {
			kind = braceOpen
		}
		s.open = append(s.open, kind)
	case b > 0:
		if h != nil && ttype == k.lparen && s.prevType == k.ifKw {
			h.cond = condOpen
		}
		s.open = append(s.open, parenOpen)
	case b < 0 && ttype == k.rbrace:
		// A '}' closes the innermost '{', and with it any bracket an edit
		// left open inside it.
		for len(s.open) > 0 {
			top := s.open[len(s.open)-1]
			s.open = s.open[:len(s.open)-1]
			if top != parenOpen {
				break
			}
		}
	case b < 0:
		// A stray ')' or ']' does not close a '{'.
		if depth > 0 && s.open[depth-1] == parenOpen {
			s.open = s.open[:depth-1]
		}
	case ttype == k.caseKw && s.prevType == k.lbrace && depth > 0:
		s.open[depth-1] = braceOpen
	case is(k.opensHeader, ttype):
		s.headers = append(s.headers, header{depth: depth, fn: ttype == k.funcKw})
	case h != nil && (ttype == k.assign && h.fn || ttype == k.elseKw || ttype == k.arrow):
		// An expression body (`func f() int = x`), an if-expression
		// (`if (c) a else b`) or a match guard ending (`case v if ok =>`):
		// the header has no block.
		s.headers = s.headers[:len(s.headers)-1]
	}
	// A header whose depth has been closed is gone; one whose condition has
	// just closed waits to see whether a '{' follows.
	for len(s.headers) > 0 && s.headers[len(s.headers)-1].depth > len(s.open) {
		s.headers = s.headers[:len(s.headers)-1]
	}
	if h := s.pendingHeader(); h != nil && h.cond == condOpen && b < 0 && ttype != k.rbrace {
		h.cond = condClosed
	}
}

// pendingHeader is the innermost header whose '{' would open at the current
// depth, or nil.
func (s *newlineTokenSource) pendingHeader() *header {
	if n := len(s.headers); n > 0 && s.headers[n-1].depth == len(s.open) {
		return &s.headers[n-1]
	}
	return nil
}

// atStatementLevel reports whether a statement can begin here: directly inside
// a block's '{'. At the top level only declarations begin, and those start
// with a keyword.
func (s *newlineTokenSource) atStatementLevel() bool {
	return len(s.open) > 0 && s.open[len(s.open)-1] == blockOpen
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
