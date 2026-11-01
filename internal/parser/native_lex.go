package parser

import (
	"errors"
	"fmt"

	"martianoff/gala/galaerr"
	"martianoff/gala/internal/parser/native"
)

// The functions here run galop's native lexer and parser directly, without
// ANTLR's: the native tokens take GALA's line-break rule (RetypeNewlines),
// doc comments come from their hidden channel (nativeDocComments), and the
// blank-line check reads the native tree (checkEmptyLinesNative). Each
// matches its ANTLR counterpart, which the tests hold it to over the corpus.

// RetypeNewlines applies GALA's line-break rule (see lineBreakRule) to ts in
// place, as newlineTokenSource applies it to ANTLR's lexer: a '(' that starts
// a line after a token that can end an expression becomes NL_LPAREN, and a
// '*' or '&' written against its operand where a statement can begin becomes
// NL_STAR or NL_AMP. Run it once, before parsing ts.
func RetypeNewlines(ts *native.GalaTokens) {
	rule := lineBreakRule{kinds: kinds()}
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
// the line-break rule.
func lexNative(src string, line, col int) *native.GalaTokens {
	if line <= 0 {
		line, col = 1, 0
	}
	ts := native.GalaLexAt(src, line, col)
	RetypeNewlines(ts)
	return ts
}

// ErrNativeExpression is what ParseExpressionNativeAt returns when galop's
// native parser does not settle the input: a lexical or syntax error, a token
// left over after the expression, or a panic in galop's parser. The caller
// parses the input with ParseExpressionAt instead, which reports the error
// with ANTLR's message and hints.
var ErrNativeExpression = errors.New("parser: galop's native parser does not settle the expression")

// ParseExpressionNativeAt parses src as exactly one GALA expression whose
// first character sits at line/col of an enclosing file (see
// ParseExpressionAt), with galop's native lexer and parser. Every token of the
// tree carries its position in that file. On any error it returns an error
// wrapping ErrNativeExpression.
func ParseExpressionNativeAt(src string, line, col int) (native.ExpressionNode, error) {
	ts := lexNative(src, line, col)
	if len(ts.Errors) > 0 {
		return native.ExpressionNode{}, fmt.Errorf("%w: %v", ErrNativeExpression, &ts.Errors[0])
	}
	expr, err := parseNativeExpression(ts)
	if err != nil {
		return native.ExpressionNode{}, fmt.Errorf("%w: %v", ErrNativeExpression, err)
	}
	if next := ts.Tokens[expr.Tree().Next]; next.Type != native.GalaEOF {
		return native.ExpressionNode{}, fmt.Errorf("%w: unexpected %q at %d:%d", ErrNativeExpression,
			next.Text(ts.Src), next.Line, next.Column)
	}
	return expr, nil
}

// parseNativeExpression is native.ParseExpression with a panic in galop's
// parser recovered (see noteGalopPanic): the caller of ParseExpressionNativeAt
// then parses the input with ANTLR's parser, as for a syntax error.
func parseNativeExpression(ts *native.GalaTokens) (expr native.ExpressionNode, err error) {
	defer func() {
		if r := recover(); r != nil {
			noteGalopPanic(ts.Src, r)
			expr, err = native.ExpressionNode{}, fmt.Errorf("panic: %v", r)
		}
	}()
	return native.ParseExpression(ts)
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
