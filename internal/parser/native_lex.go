package parser

import (
	"errors"
	"fmt"
	"unicode/utf8"

	"github.com/antlr4-go/antlr/v4"

	"martianoff/gala/galaerr"
	"martianoff/gala/internal/parser/grammar"
	"martianoff/gala/internal/parser/native"
)

// The functions here run galop's native lexer and parser directly, without
// ANTLR's: the native tokens take GALA's line-break rule (RetypeNewlines),
// doc comments come from their hidden channel (nativeDocComments), and the
// blank-line check reads the native tree (checkEmptyLinesNative). Each
// matches its ANTLR counterpart, which the tests hold it to over the corpus.
// With galop's engine they are how GALA parses: ParseFile parses a file
// with them, ParseExpressionCheckedAt an expression, and the language server
// lexes with them (VisitNativeTokens).

// RetypeNewlines applies GALA's line-break rule (see lineBreakRule) to ts in
// place, as newlineTokenSource applies it to ANTLR's lexer: a '(' that starts
// a line after a token that can end an expression becomes NL_LPAREN, and a
// '*' or '&' written against its operand where a statement can begin becomes
// NL_STAR or NL_AMP. Run it once, before parsing ts.
func RetypeNewlines(ts *native.GalaTokens) {
	rule := lineBreakRule{kinds: nativeKinds()}
	src := ts.Src
	for i := range ts.Tokens {
		tok := &ts.Tokens[i]
		if tok.Type == native.GalaEOF {
			break
		}
		start, end := int(tok.Start), int(tok.End)
		tok.Type = int32(rule.next(int(tok.Type), int(tok.Line),
			func() string { return src[start:end] },
			func() bool { return standsApartBefore(byteAt(src, end), byteAt(src, end+1)) }))
	}
}

// nativeTokenStream is ts, galop's native tokens of is's text lexed without
// errors and re-typed (RetypeNewlines), hidden tokens included, as ANTLR's
// token stream: the tokens ANTLR's lexer and newlineTokenSource give for the
// text, with ANTLR's types, channels, code-point offsets, lines, columns and
// token source (TestNativeTokenStreamMatchesANTLR). ANTLR's parser reads it
// without lexing the text again.
func nativeTokenStream(ts *native.GalaTokens, is *antlr.InputStream) *antlr.CommonTokenStream {
	lexer := grammar.NewgalaLexer(is)
	// Every token's source is the lexer's, as a token ANTLR's lexer emits:
	// the parser's error recovery makes the tokens it conjures from it.
	pair := lexer.GetTokenSourceCharStreamPair()
	factory := antlr.CommonTokenFactoryDEFAULT
	toks := make([]antlr.Token, 0, len(ts.Tokens)+len(ts.Hidden))
	// The tokens come in source order. at is the byte offset of the last
	// one's start, runes its offset in code points, which ANTLR counts.
	at, runes := 0, 0
	add := func(tok native.GalaToken, channel int) {
		runes += utf8.RuneCountInString(ts.Src[at:tok.Start])
		at = int(tok.Start)
		stop := runes + utf8.RuneCountInString(ts.Src[tok.Start:tok.End]) - 1
		if tok.Type == native.GalaEOF {
			stop = runes - 1 // as ANTLR's lexer emits EOF
		}
		toks = append(toks, factory.Create(pair, int(tok.Type), "", channel, runes, stop, int(tok.Line), int(tok.Column)))
	}
	hidden := ts.Hidden
	for i, tok := range ts.Tokens {
		for len(hidden) > 0 && int(hidden[0].Next) <= i {
			add(hidden[0].GalaToken, int(hidden[0].Channel))
			hidden = hidden[1:]
		}
		add(tok, antlr.TokenDefaultChannel)
	}
	return antlr.NewCommonTokenStream(&nativeTokenSource{Lexer: lexer, toks: toks}, antlr.TokenDefaultChannel)
}

// nativeTokenSource is ANTLR's token source of tokens lexed already: the
// lexer, which never lexes, gives what ANTLR's parser asks of a token source
// but the tokens.
type nativeTokenSource struct {
	antlr.Lexer
	toks []antlr.Token // ending with EOF
	next int
}

// NextToken is the next token, and EOF again once there is none. Once EOF is
// handed out, the token stream holds every token, so only EOF is kept.
func (s *nativeTokenSource) NextToken() antlr.Token {
	tok := s.toks[s.next]
	if s.next < len(s.toks)-1 {
		s.next++
	} else {
		s.toks, s.next = s.toks[s.next:], 0
	}
	return tok
}

// byteAt is src[i], or -1 past the end of src. Only ASCII bytes are compared
// with it, so reading bytes rather than code points is enough.
func byteAt(src string, i int) int {
	if i >= len(src) {
		return -1
	}
	return int(src[i])
}

// lexNative lexes src with galop's native lexer from line and column col (as
// ANTLR counts them; line <= 0 starts at 1:0, as seedLexer does) and applies
// the line-break rule. A leading UTF-8 BOM is dropped first, as ParseFile and
// VisitTokens drop it, so the tokens index the source without it (ts.Src).
// Invalid UTF-8 is read as ANTLR's input stream reads it: each byte that is
// not part of a valid encoding is U+FFFD, so token text, doc comments and
// columns are ANTLR's. ts.Src is then that decoded text.
func lexNative(src string, line, col int) *native.GalaTokens {
	if line <= 0 {
		line, col = 1, 0
	}
	src = galaerr.StripBOM(src)
	if !utf8.ValidString(src) {
		// A Go string-to-rune conversion turns each invalid byte into
		// U+FFFD, as ANTLR's NewInputStream does. galop's native lexer
		// keeps the raw byte in token text (martianoff/galop#45).
		src = string([]rune(src))
	}
	ts := native.GalaLexAt(src, line, col)
	RetypeNewlines(ts)
	return ts
}

// VisitNativeTokens lexes input with galop's native lexer and calls visit
// with the text of every default-channel token, in order, stopping at EOF:
// VisitTokens on galop's lexer, whose tokens match ANTLR's lexer's
// (TestNativeLexMatchesANTLR). Lexical errors are not reported. A panic in
// galop's lexer is recovered and reported through noteGalopPanic, and input
// is then lexed with ANTLR's lexer instead, before any token is visited.
//
// Like ParseFile it is safe to call from several goroutines at once.
func VisitNativeTokens(input string, visit func(text string)) {
	ts := lexNativeRecovered(input)
	if ts == nil {
		VisitTokens(input, func(tok antlr.Token) {
			if tok.GetChannel() == antlr.TokenDefaultChannel {
				visit(tok.GetText())
			}
		})
		return
	}
	for _, tok := range ts.Tokens {
		if tok.Type == native.GalaEOF {
			return
		}
		visit(tok.Text(ts.Src))
	}
}

// lexNativeRecovered is lexNative of input at its start, or nil when galop's
// lexer panics on it (reported through noteGalopPanic).
func lexNativeRecovered(input string) (ts *native.GalaTokens) {
	defer func() {
		if r := recover(); r != nil {
			noteGalopPanic("lexer", input, r)
			ts = nil
		}
	}()
	return lexNative(input, 0, 0)
}

// ErrNativeExpression is what ParseExpressionNativeAt returns when galop's
// native lexer and parser do not settle the input: a lexical or syntax error,
// a token left over after the expression, or a panic. Reporting the error is
// the caller's job: it parses the input with ParseExpressionConvertedAt,
// whose messages and hints are ANTLR's.
var ErrNativeExpression = errors.New("parser: galop's native parser does not settle the expression")

// ErrConvertExpression is the error of a failed conversion of an expression
// ANTLR's parser accepted to galop's native tree: a bug in galop's converter.
var ErrConvertExpression = errors.New("parser: galop's converter failed on the expression")

// ParseExpressionNativeAt parses src as exactly one GALA expression whose
// first character sits at line/col of an enclosing file (line 1-based, col
// 0-based, as ANTLR reports them; line <= 0 parses at 1:0), with galop's
// native lexer and parser. Every token of the tree carries its position in
// that file. Any failure, a panic included (recovered and reported through
// noteGalopPanic), is an error wrapping ErrNativeExpression.
func ParseExpressionNativeAt(src string, line, col int) (native.ExpressionNode, error) {
	return galopParsers.expressionNativeAt(src, line, col)
}

// expressionNativeAt is ParseExpressionNativeAt with g's parser.
func (g *nativeParsers) expressionNativeAt(src string, line, col int) (expr native.ExpressionNode, err error) {
	defer func() {
		if r := recover(); r != nil {
			noteGalopPanic("parser", src, r)
			expr, err = native.ExpressionNode{}, fmt.Errorf("%w: panic: %v", ErrNativeExpression, r)
		}
	}()
	ts := lexNative(src, line, col)
	if len(ts.Errors) > 0 {
		return native.ExpressionNode{}, fmt.Errorf("%w: %v", ErrNativeExpression, &ts.Errors[0])
	}
	expr, err = g.expression(ts)
	if err != nil {
		return native.ExpressionNode{}, fmt.Errorf("%w: %v", ErrNativeExpression, err)
	}
	if next := ts.Tokens[expr.Tree().Next]; next.Type != native.GalaEOF {
		return native.ExpressionNode{}, fmt.Errorf("%w: unexpected %q at %d:%d", ErrNativeExpression,
			next.Text(ts.Src), next.Line, next.Column)
	}
	return expr, nil
}

// ParseExpressionCheckedAt is input's native tree as exactly one GALA
// expression whose first character sits at line/col of an enclosing file (see
// ParseExpressionNativeAt), made by the engine's parser as a file's native
// tree is. With galop's engine, galop's native parser parses input, and when
// it does not settle it ANTLR's parser does (ParseExpressionConvertedAt);
// with ANTLR's engine, ANTLR's parser parses it once and its tree is
// converted. A syntax error is ANTLR's, as ParseExpressionConvertedAt
// reports it, naming what input is parsed as. engineParsed is whether the
// tree is the engine's own parse rather than the fallback's: always so with
// ANTLR's engine.
func ParseExpressionCheckedAt(input string, line, col int, what string) (expr native.ExpressionNode, engineParsed bool, err error) {
	return galopParsers.expressionCheckedAt(input, line, col, what)
}

// expressionCheckedAt is ParseExpressionCheckedAt with g's parser and
// converter.
func (g *nativeParsers) expressionCheckedAt(input string, line, col int, what string) (expr native.ExpressionNode, engineParsed bool, err error) {
	engine, engineErr := currentEngine()
	antlrEngine := engineErr == nil && engine == EngineANTLR
	if !antlrEngine {
		if expr, err := g.expressionNativeAt(input, line, col); err == nil {
			return expr, true, nil
		}
	}
	expr, err = g.expressionConvertedAt(input, line, col, what)
	return expr, antlrEngine && err == nil, err
}

// ParseExpressionConvertedAt parses input with ANTLR's parser alone, whatever
// the engine, as exactly one expression at line/col (see
// antlrExpressionAtChecked), and converts it to galop's native tree: the
// fallback of a caller whose engine's parse of input did not settle it. A
// syntax error, or a token left over after the expression, is ANTLR's first
// error, naming what input is parsed as. When ANTLR's parser accepts input
// (galop's parser panicked on it, or disagreed with ANTLR's), the converted
// tree is the expression; a failed conversion is an error wrapping
// ErrConvertExpression. It lexes input again with ANTLR's lexer: the fallback
// runs only on a syntax error or a galop panic, on text no longer than an
// expression.
func ParseExpressionConvertedAt(input string, line, col int, what string) (native.ExpressionNode, error) {
	return galopParsers.expressionConvertedAt(input, line, col, what)
}

// expressionConvertedAt is ParseExpressionConvertedAt with g's converter.
func (g *nativeParsers) expressionConvertedAt(input string, line, col int, what string) (native.ExpressionNode, error) {
	ctx, stream, err := antlrExpressionAtChecked(input, line, col, what)
	if err != nil {
		return native.ExpressionNode{}, err
	}
	return g.convertExpression(ctx, stream, input)
}

// ParseExpressionTree is src's native tree as an expression, and the syntax
// errors: src parsed as an expression, not anchored at end of input,
// so `x +` gives the tree of `x`, with no error. With galop's engine, galop's
// native parser parses src that is exactly one expression; anything else,
// and all of it with ANTLR's engine, is parsed by ANTLR's parser, its
// error-recovered tree when there are errors, and converted by galop's
// converter. No tree is the zero node, with the reason among the errors: the
// converter failed (an error wrapping ErrConvertExpression; a panic is also
// reported through noteGalopPanic), or no engine can be chosen.
func ParseExpressionTree(src string) (native.ExpressionNode, []error) {
	return galopParsers.expressionTree(src)
}

// expressionTree is ParseExpressionTree with g's parser and converter.
func (g *nativeParsers) expressionTree(src string) (native.ExpressionNode, []error) {
	engine, err := currentEngine()
	if err != nil {
		return native.ExpressionNode{}, []error{err}
	}
	if engine == EngineGalop {
		if expr, err := g.expressionNativeAt(src, 0, 0); err == nil {
			return expr, nil
		}
	}
	ctx, stream, errs := antlrExpressionAt(src, 0, 0)
	if ctx == nil {
		return native.ExpressionNode{}, errs
	}
	expr, err := g.convertExpression(ctx, stream, src)
	if err != nil {
		return native.ExpressionNode{}, append(errs, err)
	}
	return expr, errs
}

// convertExpression converts ctx, an expression ANTLR's parser read from
// stream over src, to galop's native tree with g's converter. A panic in the
// converter is recovered and reported through noteGalopPanic.
func (g *nativeParsers) convertExpression(ctx antlr.ParserRuleContext, stream *antlr.CommonTokenStream, src string) (expr native.ExpressionNode, err error) {
	defer func() {
		if r := recover(); r != nil {
			noteGalopPanic("parser", src, r)
			expr, err = native.ExpressionNode{}, fmt.Errorf("%w: converter panic: %v", ErrConvertExpression, r)
		}
	}()
	tree := g.toNative(ctx, stream, src)
	if tree == nil {
		return native.ExpressionNode{}, fmt.Errorf("%w: the converter gave no tree", ErrConvertExpression)
	}
	return native.ExpressionNode{GalaNodeRef: tree.Root()}, nil
}

// checkEmptyLinesNative is checkEmptyLines on the native tree: the blank line
// required after the package clause and after the import block. Native
// tokens span bytes of the source, so the gaps are read straight from it.
func checkEmptyLinesNative(root native.SourceFileNode) error {
	pkg := root.PackageClause()
	if !pkg.Ok() || pkg.StopIndex() < pkg.StartIndex() {
		return nil
	}
	src := root.Tree().Src
	// gapLacksBlankLine reports whether the source between the end of token
	// last and the start of token next has no blank line. An empty gap is
	// never reported (see runeSpan).
	gapLacksBlankLine := func(last, next native.GalaToken) bool {
		return last.End < next.Start && !emptyLineRegex.MatchString(src[last.End:next.Start])
	}
	imports := root.AllImportDeclaration()
	tops := root.AllTopLevelDeclaration()
	var next native.GalaNodeRef
	if len(imports) > 0 {
		next = imports[0].GalaNodeRef
	} else if len(tops) > 0 {
		next = tops[0].GalaNodeRef
	}
	if next.Ok() {
		if start := next.Start(); gapLacksBlankLine(pkg.Stop(), start) {
			return galaerr.NewSyntaxError(int(start.Line), 0, "packageClause should follow by an empty line")
		}
	}
	if len(imports) > 0 && len(tops) > 0 {
		last := imports[len(imports)-1]
		if last.StopIndex() >= last.StartIndex() {
			if start := tops[0].Start(); gapLacksBlankLine(last.Stop(), start) {
				return galaerr.NewSyntaxError(int(start.Line), 0, "importDeclaration should follow by an empty line")
			}
		}
	}
	return nil
}
