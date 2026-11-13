package parser

import (
	"errors"
	"fmt"
	"unicode/utf8"

	"github.com/antlr4-go/antlr/v4"

	"martianoff/gala/galaerr"
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
// the line-break rule. A leading UTF-8 BOM is the caller's to drop, as
// ParseFile and VisitNativeTokens drop it; an expression's text keeps it, as
// ANTLR's lexer reads it. Invalid UTF-8 is read as ANTLR's input stream reads
// it (antlrText), so token text, doc comments and columns are ANTLR's. ts.Src
// is then that decoded text.
func lexNative(src string, line, col int) *native.GalaTokens {
	if line <= 0 {
		line, col = 1, 0
	}
	ts := native.GalaLexAt(antlrText(src), line, col)
	RetypeNewlines(ts)
	return ts
}

// antlrText is src as ANTLR's input stream reads it: each byte that is not
// part of a valid UTF-8 encoding is U+FFFD. The native trees index it, so
// their byte offsets and token texts agree with ANTLR's code-point offsets
// and texts. galop's native lexer and converter keep the raw byte
// (martianoff/galop#45), so both are given this text.
func antlrText(src string) string {
	if utf8.ValidString(src) {
		return src
	}
	// A Go string-to-rune conversion turns each invalid byte into U+FFFD,
	// as ANTLR's NewInputStream does.
	return string([]rune(src))
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

// lexNativeRecovered is lexNative of input at its start, a leading BOM
// dropped as VisitTokens drops it, or nil when galop's lexer panics on it
// (reported through noteGalopPanic).
func lexNativeRecovered(input string) (ts *native.GalaTokens) {
	defer func() {
		if r := recover(); r != nil {
			noteGalopPanic("lexer", input, r)
			ts = nil
		}
	}()
	return lexNative(galaerr.StripBOM(input), 0, 0)
}

// ErrConvertExpression is the error of a failed conversion of an expression
// ANTLR's parser accepted to galop's native tree: a bug in galop's converter.
var ErrConvertExpression = errors.New("parser: galop's converter failed on the expression")

// expressionNative parses input as an expression whose first character sits
// at line/col of an enclosing file (line 1-based, col 0-based, as ANTLR
// reports them; line <= 0 parses at 1:0), with galop's native lexer and
// recovering parser, not anchored at end of input: its tree, error-recovered
// when there are errors, and the errors, as ANTLR's lexer and parser report
// them (nativeSyntaxErrors). Every token of the tree carries its position in
// that file. ok is false when galop's lexer or parser panicked (see
// galopParse).
func (g *nativeParsers) expressionNative(input string, line, col int) (native.ExpressionNode, []error, bool) {
	ts, expr, parseErrs, ok := galopParse(input, line, col, g.expression)
	if !ok {
		return native.ExpressionNode{}, nil, false
	}
	return expr, nativeSyntaxErrors(expr.Tree(), ts, parseErrs), true
}

// expressionCheckedNative is expressionNative of input as exactly one
// expression: a syntax error is the first error ANTLR's parser reports, with
// its hint, and a token left over after the expression is an error naming
// what input is parsed as, as antlrExpressionAtChecked reports them. ok is
// false when galop panicked.
func (g *nativeParsers) expressionCheckedNative(input string, line, col int, what string) (native.ExpressionNode, error, bool) {
	expr, errs, ok := g.expressionNative(input, line, col)
	switch {
	case !ok:
		return native.ExpressionNode{}, nil, false
	case len(errs) > 0:
		return native.ExpressionNode{}, errs[0], true
	}
	tree := expr.Tree()
	if next := tree.Tokens[tree.Next]; next.Type != native.GalaEOF {
		return native.ExpressionNode{}, galaerr.NewSyntaxError(int(next.Line), int(next.Column),
			fmt.Sprintf("unexpected %q in %s", next.Text(tree.Src), what)), true
	}
	return expr, nil, true
}

// ParseExpressionCheckedAt is input's native tree as exactly one GALA
// expression whose first character sits at line/col of an enclosing file (see
// expressionNative), made by the engine's parser as a file's native tree is.
// With galop's engine, galop's native lexer and recovering parser parse
// input, and report a syntax error as ANTLR's parser does
// (expressionCheckedNative); only a galop panic has ANTLR's parser parse it
// instead (ParseExpressionConvertedAt). With ANTLR's engine, ANTLR's parser
// parses it once and its tree is converted. A syntax error names what input
// is parsed as. engineParsed is whether there is a tree and it is the
// engine's own parse rather than the fallback's: always so with ANTLR's
// engine when it parses.
func ParseExpressionCheckedAt(input string, line, col int, what string) (expr native.ExpressionNode, engineParsed bool, err error) {
	return galopParsers.expressionCheckedAt(input, line, col, what)
}

// expressionCheckedAt is ParseExpressionCheckedAt with g's parser and
// converter.
func (g *nativeParsers) expressionCheckedAt(input string, line, col int, what string) (expr native.ExpressionNode, engineParsed bool, err error) {
	engine, engineErr := currentEngine()
	antlrEngine := engineErr == nil && engine == EngineANTLR
	if !antlrEngine {
		if expr, err, ok := g.expressionCheckedNative(input, line, col, what); ok {
			return expr, err == nil, err
		}
	}
	expr, err = g.expressionConvertedAt(input, line, col, what)
	return expr, antlrEngine && err == nil, err
}

// ParseExpressionConvertedAt parses input with ANTLR's parser alone, whatever
// the engine, as exactly one expression at line/col (see
// antlrExpressionAtChecked), and converts it to galop's native tree: the
// fallback of a caller whose engine's parser panicked on input. A syntax
// error, or a token left over after the expression, is ANTLR's first error,
// naming what input is parsed as. A failed conversion is an error wrapping
// ErrConvertExpression.
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
// native lexer and recovering parser parse src (expressionNative), its tree
// error-recovered when there are errors; only a galop panic has ANTLR's
// parser parse it instead. With ANTLR's engine, ANTLR's parser parses it, its
// tree error-recovered when there are errors, and galop's converter converts
// it. No tree is the zero node, with the reason among the errors: the
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
		if expr, errs, ok := g.expressionNative(src, 0, 0); ok {
			return expr, errs
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
// stream over src, to galop's native tree with g's converter, reading src as
// ANTLR does (antlrText). A panic in the
// converter is recovered and reported through noteGalopPanic.
func (g *nativeParsers) convertExpression(ctx antlr.ParserRuleContext, stream *antlr.CommonTokenStream, src string) (expr native.ExpressionNode, err error) {
	defer func() {
		if r := recover(); r != nil {
			noteGalopPanic("parser", src, r)
			expr, err = native.ExpressionNode{}, fmt.Errorf("%w: converter panic: %v", ErrConvertExpression, r)
		}
	}()
	tree := g.toNative(ctx, stream, antlrText(src))
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
