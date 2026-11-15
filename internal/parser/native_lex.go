package parser

import (
	"fmt"
	"unicode/utf8"

	"martianoff/gala/galaerr"
	"martianoff/gala/internal/parser/native"
)

// The functions here run galop's native lexer and parser: the native tokens
// take GALA's line-break rule (retypeNewlines), doc comments come from their
// hidden channel (nativeDocComments), and the blank-line check reads the
// native tree (checkEmptyLinesNative). ParseFile parses a file with them,
// ParseExpressionCheckedAt and ParseExpressionTree an expression, and the
// language server lexes with them (VisitNativeTokens).

// retypeNewlines applies GALA's line-break rule (see lineBreakRule) to ts in
// place: a '(' that starts a line after a token that can end an expression
// becomes NL_LPAREN, and a '*' or '&' written against its operand where a
// statement can begin becomes NL_STAR or NL_AMP. Run it once, before parsing
// ts.
func retypeNewlines(ts *native.GalaTokens) {
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

// lexNative lexes src with galop's native lexer from line and column col
// (line 1-based, col 0-based, counted in code points; line <= 0 starts at
// 1:0) and applies the line-break rule. A leading UTF-8 BOM is the caller's
// to drop, as ParseFile and VisitNativeTokens drop it; an expression's text
// keeps it. Each byte that is not valid UTF-8 reads as U+FFFD
// (antlrText), one character of token text, doc comments and columns. ts.Src
// is then that decoded text.
func lexNative(src string, line, col int) *native.GalaTokens {
	ts := lexGalop(src, line, col)
	retypeNewlines(ts)
	return ts
}

// lexGalop is lexNative without the line-break rule: galop's lexer alone.
func lexGalop(src string, line, col int) *native.GalaTokens {
	if line <= 0 {
		line, col = 1, 0
	}
	return native.GalaLexAt(antlrText(src), line, col)
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
// with the text of every default-channel token, in order, stopping at EOF. A
// leading BOM is dropped. Lexical errors are not reported, and a panic in
// galop's lexer is recovered: input is then visited as no tokens, and the
// panic is reported as ParseFile reports it, on standard error under
// GALA_PANIC_STACK=1 (the parse of the same text reports it as an error).
//
// Like ParseFile it is safe to call from several goroutines at once.
func VisitNativeTokens(input string, visit func(text string)) {
	ts := lexNativeRecovered(galaerr.StripBOM(input))
	if ts == nil {
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
// lexer panics on it (reported on standard error under GALA_PANIC_STACK=1;
// a parse of the same text reports it as a diagnostic, see galopPanicError).
func lexNativeRecovered(input string) *native.GalaTokens {
	var ts *native.GalaTokens
	if underGalop("lexer", input, 0, 0, func() { ts = lexGalop(input, 0, 0) }) != nil {
		return nil
	}
	retypeNewlines(ts)
	return ts
}

// parseExpressionAt parses input as an expression whose first character sits
// at line/col of an enclosing file (line 1-based, col 0-based; line <= 0
// parses at 1:0), with galop's native lexer and recovering parser, not
// anchored at end of input: its tree, error-recovered when there are errors,
// and the errors (nativeSyntaxErrors). Every token of the tree carries its
// position in that file. A panic in galop's lexer or parser gives no tree
// and the internal compiler error alone (galopPanicError).
func parseExpressionAt(input string, line, col int) (native.ExpressionNode, []error) {
	ts, expr, parseErrs, panicErr := galopParse(input, line, col, parseExpression)
	if panicErr != nil {
		return native.ExpressionNode{}, []error{panicErr}
	}
	return expr, nativeSyntaxErrors(expr.Tree(), ts, parseErrs)
}

// ParseExpressionCheckedAt is input's native tree as exactly one GALA
// expression whose first character sits at line/col of an enclosing file
// (see parseExpressionAt). It is how the transpiler parses expression text it
// holds apart from its file: the embedded expressions of an interpolated
// string, and declared default values. A syntax error is the first error of
// the parse; a token left over after the expression is an error naming what
// input is parsed as (`x +` is not `x` with the operator dropped). A panic in
// galop's lexer or parser is an internal compiler error at line/col.
func ParseExpressionCheckedAt(input string, line, col int, what string) (native.ExpressionNode, error) {
	expr, errs := parseExpressionAt(input, line, col)
	if len(errs) > 0 {
		return native.ExpressionNode{}, errs[0]
	}
	tree := expr.Tree()
	if next := tree.Tokens[tree.Next]; next.Type != native.GalaEOF {
		return native.ExpressionNode{}, galaerr.NewSyntaxError(int(next.Line), int(next.Column),
			fmt.Sprintf("unexpected %q in %s", next.Text(tree.Src), what))
	}
	return expr, nil
}

// ParseExpressionTree is src's native tree as an expression, and the syntax
// errors: src parsed by galop's native lexer and recovering parser as an
// expression (parseExpressionAt), not anchored at end of input, so `x +`
// gives the tree of `x`, with no error. The tree is error-recovered when
// there are errors. A panic in galop's lexer or parser gives the zero node
// and the internal compiler error alone (see IsParserPanic).
func ParseExpressionTree(src string) (native.ExpressionNode, []error) {
	return parseExpressionAt(src, 0, 0)
}

// checkEmptyLinesNative enforces the blank line required after the package
// clause and after the import block. Native tokens span bytes of the source,
// so the gaps are read straight from it.
func checkEmptyLinesNative(root native.SourceFileNode) error {
	pkg := root.PackageClause()
	if !pkg.Ok() || pkg.StopIndex() < pkg.StartIndex() {
		return nil
	}
	src := root.Tree().Src
	// gapLacksBlankLine reports whether the source between the end of token
	// last and the start of token next has no blank line. An empty gap is
	// never reported: there is no gap to inspect (GALA's grammar always
	// separates the two tokens).
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
