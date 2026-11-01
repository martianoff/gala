package parser

import (
	"errors"
	"fmt"
	"unicode/utf8"

	"github.com/antlr4-go/antlr/v4"

	"martianoff/gala/galaerr"
	"martianoff/gala/internal/parser/galop"
	"martianoff/gala/internal/parser/native"
)

// The functions here run galop's native lexer and parser directly, without
// ANTLR's: the native tokens take GALA's line-break rule (RetypeNewlines),
// doc comments come from their hidden channel (nativeDocComments), and the
// blank-line check reads the native tree (checkEmptyLinesNative). Each
// matches its ANTLR counterpart, which the tests hold it to over the corpus.
// Nothing on the build path calls them yet: they are the groundwork for
// parsing with galop's native flavor directly.

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

// ErrNativeExpression is what ParseExpressionNativeAt returns when galop's
// native lexer and parser do not settle the input: a lexical or syntax error,
// a token left over after the expression, or a panic. Reporting the error is
// the caller's job: it parses the input with ParseExpressionAt, whose
// messages and hints are ANTLR's.
var ErrNativeExpression = errors.New("parser: galop's native parser does not settle the expression")

// ParseExpressionNativeAt parses src as exactly one GALA expression whose
// first character sits at line/col of an enclosing file (see
// ParseExpressionAt), with galop's native lexer and parser. Every token of the
// tree carries its position in that file. Any failure, a panic included
// (recovered and reported through noteGalopPanic), is an error wrapping
// ErrNativeExpression.
func ParseExpressionNativeAt(src string, line, col int) (expr native.ExpressionNode, err error) {
	defer func() {
		if r := recover(); r != nil {
			noteGalopPanic(src, r)
			expr, err = native.ExpressionNode{}, fmt.Errorf("%w: panic: %v", ErrNativeExpression, r)
		}
	}()
	ts := lexNative(src, line, col)
	if len(ts.Errors) > 0 {
		return native.ExpressionNode{}, fmt.Errorf("%w: %v", ErrNativeExpression, &ts.Errors[0])
	}
	expr, err = native.ParseExpression(ts)
	if err != nil {
		return native.ExpressionNode{}, fmt.Errorf("%w: %v", ErrNativeExpression, err)
	}
	if next := ts.Tokens[expr.Tree().Next]; next.Type != native.GalaEOF {
		return native.ExpressionNode{}, fmt.Errorf("%w: unexpected %q at %d:%d", ErrNativeExpression,
			next.Text(ts.Src), next.Line, next.Column)
	}
	return expr, nil
}

// ParseExpressionTreeAt is src's native tree as exactly one GALA expression
// whose first character sits at line/col of an enclosing file (see
// ParseExpressionAt), made by the engine's parser, as a file's native tree
// is: galop's native parser with galop's engine (ParseExpressionNativeAt),
// ANTLR's parser with ANTLR's, its tree converted by galop's converter
// (Parsed.Native). An unusable engine parses as galop's, as ParseFile fails
// on it anyway. Any failure is an error wrapping ErrNativeExpression.
func ParseExpressionTreeAt(src string, line, col int) (native.ExpressionNode, error) {
	if engine, err := currentEngine(); err != nil || engine != EngineANTLR {
		return ParseExpressionNativeAt(src, line, col)
	}
	return convertedExpressionAt(src, line, col)
}

// convertedExpressionAt parses src with ANTLR's parser alone (see
// ParseExpressionAt) and converts the expression to galop's native tree. A
// panic in the converter is recovered and reported through noteGalopPanic.
func convertedExpressionAt(src string, line, col int) (expr native.ExpressionNode, err error) {
	ctx, stream, errs := antlrExpressionAt(src, line, col, nil)
	if len(errs) > 0 {
		return native.ExpressionNode{}, fmt.Errorf("%w: %v", ErrNativeExpression, errs[0])
	}
	if tok := stream.LT(1); tok != nil && tok.GetTokenType() != antlr.TokenEOF {
		return native.ExpressionNode{}, fmt.Errorf("%w: unexpected %q at %d:%d", ErrNativeExpression,
			tok.GetText(), tok.GetLine(), tok.GetColumn())
	}
	defer func() {
		if r := recover(); r != nil {
			noteGalopPanic(src, r)
			expr, err = native.ExpressionNode{}, fmt.Errorf("%w: converter panic: %v", ErrNativeExpression, r)
		}
	}()
	tree, _ := galop.ToNative(ctx, stream, src)
	if tree == nil {
		return native.ExpressionNode{}, fmt.Errorf("%w: the converter gave no tree", ErrNativeExpression)
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
