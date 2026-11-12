package parser

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"

	"martianoff/gala/galaerr"
	"martianoff/gala/internal/parser/galop"
	"martianoff/gala/internal/parser/grammar"
	"martianoff/gala/internal/parser/native"

	"github.com/antlr4-go/antlr/v4"
)

type AntlrGalaParser struct {
	// galop is galop's native parsers and converter the parser calls, nil
	// for galopParsers. Only tests set it, on a parser of their own.
	galop *nativeParsers
}

func NewAntlrGalaParser() *AntlrGalaParser {
	return &AntlrGalaParser{}
}

// nativeParsers is galop's native parsers of a source file and of an
// expression, and galop's converter from ANTLR's trees to the native tree,
// as a parse calls them. Every parse calls galopParsers' but a test's, which
// gives a parser of its own faulty ones (see AntlrGalaParser.galop).
type nativeParsers struct {
	sourceFile func(*native.GalaTokens) (native.SourceFileNode, []native.GalaParseError)
	expression func(*native.GalaTokens) (native.ExpressionNode, []native.GalaParseError)
	toNative   func(antlr.ParserRuleContext, *antlr.CommonTokenStream, string) *native.GalaTree
}

// galopParsers is galop's generated parsers and converter. It is never
// changed.
var galopParsers = &nativeParsers{
	sourceFile: native.ParseSourceFileWithRecovery,
	expression: native.ParseExpressionWithRecovery,
	toNative:   galop.ToNative,
}

// parsers is the native parsers and converter p calls.
func (p *AntlrGalaParser) parsers() *nativeParsers {
	if p == nil || p.galop == nil {
		return galopParsers
	}
	return p.galop
}

type sourceFileParseResult struct {
	input  *antlr.InputStream
	stream *antlr.CommonTokenStream
	tree   antlr.Tree
	errors []error
}

// parseSourceFileAttempt parses input once under the given ANTLR prediction
// mode and returns the tree, any syntax errors, and the token stream the doc
// comments can be harvested from.
//
// Concurrency: the ANTLR-generated NewgalaLexer/NewgalaParser constructors
// hand every instance the same package-global ATN simulator state — the
// decisionToDFA slice and, critically, a shared *PredictionContextCache.
// The DFA slice is safe to share because every mutation of it goes through the
// ATN's own stateMu/edgeMu mutexes. The prediction-context cache is NOT: its
// add() ends in an unsynchronised map write + slice append
// (JMap.Put: `store[h] = append(store[h], …)`), so two parses running on
// different goroutines corrupt it — which surfaces as a hard fault inside
// runtime.growslice/memmove or a "fatal error: concurrent map writes".
// isolateLexerCaches/isolateParserCaches below rebind each parse's ATN
// simulator to a per-parse PredictionContextCache while reusing a shared,
// mutex-protected DFA set (built once) so DFA state is still reused across
// files. The deserialized ATN stays shared too (it is read-mostly and guards
// its own lazily-cached token sets with a mutex).
func parseSourceFileAttempt(input string, mode int) sourceFileParseResult {
	errorListener := &GalaErrorListener{}
	is := antlr.NewInputStream(input)
	lexer := grammar.NewgalaLexer(is)
	isolateLexerCaches(lexer.BaseLexer)
	lexer.RemoveErrorListeners()
	lexer.AddErrorListener(errorListener)
	stream := newTokenStream(lexer)
	parser := grammar.NewgalaParser(stream)
	isolateParserCaches(parser.BaseParser)
	parser.GetInterpreter().SetPredictionMode(mode)

	parser.RemoveErrorListeners()
	parser.AddErrorListener(errorListener)

	tree := parser.SourceFile()

	// Buffer the whole stream so the doc comments are reachable. The parse
	// has already materialized these tokens, so this is a linear walk with no
	// extra lexing; Fill is a no-op once a complete parse has consumed it.
	stream.Fill()

	return sourceFileParseResult{
		input:  is,
		stream: stream,
		tree:   tree,
		errors: errorListener.Errors,
	}
}

// Parsed is one parse of a source file: its doc comments, its syntax errors,
// and its tree, native (Native), ANTLR's (Tree) or both.
//
// With galop's engine (the default, see Engine) every parse is galop's: its
// native lexer and recovering parser made the native tree, and Tree is nil. A
// parse with errors has the error-recovered native tree, in RecoveredNative.
// With ANTLR's engine every parse is ANTLR's, with Tree set, and its native
// tree, clean or error-recovered, is that tree's conversion.
type Parsed struct {
	// Tree is ANTLR's tree of the parse, nil when galop's parser parsed the
	// file (every parse with galop's engine but one after a galop panic) or
	// no engine could be chosen.
	Tree   antlr.Tree
	Docs   Docs
	Errors []error
	// Native is the parse's native tree (see Native), nil when there is none:
	// the parse gave no tree, or it reported an error (Errors is not empty:
	// a syntax error, or a source-text or blank-line check), whose tree
	// RecoveredNative holds. Every parse without errors has one. One to be
	// converted from ANTLR's tree holds the token stream and the source text
	// until it converts, so a holder that keeps a Parsed long but never reads
	// the native tree drops it.
	Native *Native
	// recovered is, for a parse that reported errors, the native tree of its
	// error-recovered tree (see RecoveredNative).
	recovered *Native
}

// RecoveredNative is the native-tree handle of the parse whether or not it
// reported errors: Native for a parse without them, and for one with them the
// handle of its error-recovered tree, which marks the tokens the recovery
// skipped or made up as error children (native.GalaChild.IsError). nil when
// the parse gave no tree. An editor reads a file mid-edit through it;
// everything that needs a well-formed file takes CleanNative.
func (p *Parsed) RecoveredNative() *Native {
	if p == nil {
		return nil
	}
	if len(p.Errors) > 0 {
		return p.recovered
	}
	return p.Native
}

// CleanNative is the native-tree handle of a parse without syntax errors,
// nil for one with them (RecoveredNative has an error-recovered tree), for no
// parse, or when the holder dropped the handle. The transpiler's readers (the
// analyzer, the scope walk, the transformer) read the native tree only: a
// clean parse without one, whose conversion failed, is an internal error to
// them.
func (p *Parsed) CleanNative() *Native {
	if p == nil || len(p.Errors) > 0 {
		return nil
	}
	return p.Native
}

// Err is the parse's syntax errors as one error, or nil when there are none.
func (p *Parsed) Err() error {
	if len(p.Errors) == 0 {
		return nil
	}
	return &galaerr.MultiError{Errors: p.Errors}
}

// ParseFile parses input as a source file. When no engine can be chosen,
// there is no tree, only the error.
//
// With galop's engine (the default, see Engine), galop's native lexer and
// recovering parser parse input once (parseNativeFile), errors or not: the
// errors, their hints and the error-recovered tree all come from that parse,
// and are the ones ANTLR's engine gives. Only a panic in galop's lexer or
// parser (a galop bug, reported through noteGalopPanic) has ANTLR's parser
// parse input instead, as below.
//
// ANTLR's parser parses input twice when it does not parse cleanly: once with
// SLL prediction, and, if that attempt reported any syntax error, again from
// the start with full LL prediction. SLL is the cheaper prediction mode and
// the grammar accepts nearly everything under it, so the common case pays for
// one attempt; input that is mid-edit and syntactically invalid pays for two.
//
// The two attempts are independent: each builds its own parser and
// prediction-context cache, so a failed attempt leaves no state behind for the
// retry (see parseSourceFileAttempt). Diagnostics are reported from the
// retained attempt only, so the errors a caller sees are the LL ones and are
// unchanged from a single full-prediction parse. On a syntax error the tree is
// ANTLR's, error-recovered: it contains error nodes but is structurally valid,
// and the errors come with it.
func (p *AntlrGalaParser) ParseFile(input string) *Parsed {
	engine, err := currentEngine()
	if err != nil {
		return &Parsed{Errors: []error{err}}
	}
	// Drop a leading UTF-8 BOM once, up front: the lexer has no rule for
	// U+FEFF and would otherwise reject the file outright. Go, which GALA
	// transpiles to, ignores a leading BOM the same way.
	input = galaerr.StripBOM(input)

	// The source-text check goes first: an illegal character is the root
	// cause of any lexer error it provokes, so it should lead the report.
	charErr := checkSourceChars(input)
	if engine == EngineGalop {
		if parsed := p.parseNativeFile(input, charErr); parsed != nil {
			return parsed
		}
	}
	return p.parseFileANTLR(input, charErr)
}

// parseNativeFile parses input, a source without a leading BOM whose
// source-text error (checkSourceChars) is charErr, if any, with galop's
// native lexer and recovering parser. The parse's errors are charErr, then
// the lexical and syntax errors (nativeSyntaxErrors), then a missing blank
// line (checkEmptyLinesNative), as parseFileANTLR reports ANTLR's. It returns
// nil when galop's lexer or parser panics (see galopParse).
func (p *AntlrGalaParser) parseNativeFile(input string, charErr *galaerr.SemanticError) *Parsed {
	ts, root, parseErrs, ok := galopParse(input, 0, 0, p.parsers().sourceFile)
	if !ok {
		return nil
	}
	errs := withCharErr(charErr, nativeSyntaxErrors(root.Tree(), ts, parseErrs))
	var handle *Native
	if root.Ok() {
		handle = nativeHandle(root.Tree())
		if err := checkEmptyLinesNative(root); err != nil {
			errs = append(errs, err)
		}
	}
	return newParsed(nil, nativeDocComments(ts), errs, handle)
}

// galopParse lexes input with galop's native lexer at line/col (lexNative)
// and parses the tokens with parse, one of galop's recovering parsers. ok is
// false when either panicked, recovered and reported through noteGalopPanic,
// for the caller to have ANTLR's parser parse input instead. Only galop's
// lexer and parser run under the recover: a panic in what GALA does with
// their result is GALA's bug, and is not hidden behind ANTLR's parser.
func galopParse[N any](input string, line, col int, parse func(*native.GalaTokens) (N, []native.GalaParseError)) (ts *native.GalaTokens, root N, errs []native.GalaParseError, ok bool) {
	defer func() {
		if r := recover(); r != nil {
			noteGalopPanic("parser", input, r)
			ok = false
		}
	}()
	ts = lexNative(input, line, col)
	root, errs = parse(ts)
	return ts, root, errs, true
}

// newParsed is a parse with ANTLR's tree, if any, doc comments, errors and
// native-tree handle: the clean native tree when there are no errors, else
// the error-recovered one.
func newParsed(tree antlr.Tree, docs Docs, errs []error, handle *Native) *Parsed {
	parsed := &Parsed{Tree: tree, Docs: docs, Errors: errs}
	if len(errs) == 0 {
		parsed.Native = handle
	} else {
		parsed.recovered = handle
	}
	return parsed
}

// withCharErr is the errors of a parse whose source-text error is charErr:
// charErr first, then errs. Outside a literal the lexer has no rule for an
// illegal character either and reports a token recognition error at the same
// spot; that one says less, so it is dropped rather than shown twice.
func withCharErr(charErr *galaerr.SemanticError, errs []error) []error {
	if charErr == nil {
		return errs
	}
	out := []error{charErr}
	for _, err := range errs {
		var se *galaerr.SyntaxError
		if errors.As(err, &se) && se.Line == charErr.Line && se.Column == charErr.Column {
			continue
		}
		out = append(out, err)
	}
	return out
}

// parseFileANTLR is ParseFile with ANTLR's parser, given input's source-text
// error (checkSourceChars), if any.
func (p *AntlrGalaParser) parseFileANTLR(input string, charErr *galaerr.SemanticError) *Parsed {
	result := parseSourceFile(input)
	docs := extractDocComments(result.stream.GetAllTokens())
	errs := withCharErr(charErr, result.errors)
	if err := p.checkEmptyLines(result.input, result.tree); err != nil {
		errs = append(errs, err)
	}
	return newParsed(result.tree, docs, errs, newNative(result.tree, result.stream, input, p.parsers().toNative))
}

// seedLexer makes lexer start at line and column col, as ANTLR counts them,
// so that every token carries its position in an enclosing file. line <= 0
// leaves it at 1:0. It reports false when the lexer cannot be seeded.
func seedLexer(lexer *antlr.BaseLexer, line, col int) bool {
	if line <= 0 {
		return true
	}
	sim, ok := lexer.Interpreter.(*antlr.LexerATNSimulator)
	if !ok {
		return false
	}
	sim.Line = line
	sim.CharPositionInLine = col
	return true
}

// parseSourceFile parses input with ANTLR's parser, SLL first and then LL
// when SLL reports an error (see ParseFile). A speculative attempt has no
// reason to walk its tokens for doc comments: if it is retained the caller
// harvests them, and if it is not the LL attempt is harvested instead.
func parseSourceFile(input string) sourceFileParseResult {
	result := parseSourceFileAttempt(input, antlr.PredictionModeSLL)
	if len(result.errors) != 0 || result.tree == nil {
		result = parseSourceFileAttempt(input, antlr.PredictionModeLL)
	}
	return result
}

// antlrExpressionAt parses input with ANTLR's parser as an expression
// starting at line:col and returns the expression, the token stream and the
// lexical and syntax errors.
func antlrExpressionAt(input string, line, col int) (grammar.IExpressionContext, *antlr.CommonTokenStream, []error) {
	errorListener := &GalaErrorListener{}
	lexer := grammar.NewgalaLexer(antlr.NewInputStream(input))
	isolateLexerCaches(lexer.BaseLexer)
	if !seedLexer(lexer.BaseLexer, line, col) {
		return nil, nil, []error{fmt.Errorf("cannot start ANTLR's lexer at %d:%d", line, col)}
	}
	lexer.RemoveErrorListeners()
	lexer.AddErrorListener(errorListener)
	stream := newTokenStream(lexer)
	psr := grammar.NewgalaParser(stream)
	isolateParserCaches(psr.BaseParser)
	psr.RemoveErrorListeners()
	psr.AddErrorListener(errorListener)
	return psr.Expression(), stream, errorListener.Errors
}

// antlrExpressionAtChecked parses input with ANTLR's parser as exactly one
// GALA expression whose first character sits at line/col of some enclosing
// source file (line 1-based, col 0-based, as ANTLR reports them; line <= 0
// parses at 1:0), and returns the expression and the token stream it was read
// from, or the first syntax error. It is how ParseExpressionConvertedAt
// parses expression text the transpiler holds apart from its file (the
// embedded expressions of an interpolated string, and declared default
// values) with ANTLR's engine, or after galop's parser panicked on it, before
// converting the tree.
//
// Three things make that safe:
//
//   - Positions are absolute. The lexer is SEEDED at line/col before it reads
//     anything, so every token — and every diagnostic, line marker or hint
//     derived from one — carries its real position. Seeding is O(1); padding
//     the text with the equivalent newlines would re-lex a prefix as long as
//     the line number on every call.
//   - The ANTLR prediction-context caches are isolated, as for every other
//     parse, so concurrent transpiles cannot corrupt a shared cache.
//   - The whole input must be consumed. The `expression` rule is not anchored
//     to EOF and happily matches a prefix: `x +` parses as `x` with the
//     operator silently dropped. A leftover token is reported as a syntax
//     error naming what, which the caller describes ("interpolated
//     expression", "default value").
func antlrExpressionAtChecked(input string, line, col int, what string) (grammar.IExpressionContext, *antlr.CommonTokenStream, error) {
	exprCtx, stream, errs := antlrExpressionAt(input, line, col)
	if len(errs) > 0 {
		return nil, nil, errs[0]
	}
	if tok := stream.LT(1); tok != nil && tok.GetTokenType() != antlr.TokenEOF {
		return nil, nil, galaerr.NewSyntaxError(tok.GetLine(), tok.GetColumn(),
			fmt.Sprintf("unexpected %q in %s", tok.GetText(), what))
	}
	return exprCtx, stream, nil
}

// sharedDFA lazily builds one DFA slice per ATN and reuses it forever. The
// generated static decisionToDFA is unexported, so we build our own from the
// same (shared) ATN. Concurrent parses may mutate these DFA states, but every
// such mutation is serialised by the ATN's stateMu/edgeMu, so a single shared
// slice is safe — and reusing it preserves ANTLR's cross-parse DFA caching.
type sharedDFA struct {
	once sync.Once
	dfa  []*antlr.DFA
}

func (s *sharedDFA) get(atn *antlr.ATN) []*antlr.DFA {
	s.once.Do(func() {
		s.dfa = make([]*antlr.DFA, len(atn.DecisionToState))
		for i, ds := range atn.DecisionToState {
			s.dfa[i] = antlr.NewDFA(ds, i)
		}
	})
	return s.dfa
}

var (
	lexerSharedDFA  sharedDFA
	parserSharedDFA sharedDFA
)

// VisitTokens lexes input and calls visit with every token the lexer emits, in
// order, stopping at EOF. Lexical errors are not reported.
//
// Like ParseFile it is safe to call from several goroutines at once.
func VisitTokens(input string, visit func(antlr.Token)) {
	lexer := grammar.NewgalaLexer(antlr.NewInputStream(galaerr.StripBOM(input)))
	isolateLexerCaches(lexer.BaseLexer)
	lexer.RemoveErrorListeners()
	for tok := lexer.NextToken(); tok.GetTokenType() != antlr.TokenEOF; tok = lexer.NextToken() {
		visit(tok)
	}
}

// isolateLexerCaches rebinds a lexer's ATN simulator to a per-parse
// PredictionContextCache (the one piece the ANTLR runtime does not guard),
// while keeping the shared, mutex-protected DFA set so concurrent lexing is
// both race-free and DFA-cache-warm.
func isolateLexerCaches(l *antlr.BaseLexer) {
	atn := l.GetATN()
	l.Interpreter = antlr.NewLexerATNSimulator(l, atn, lexerSharedDFA.get(atn), antlr.NewPredictionContextCache())
}

// isolateParserCaches is the parser-side counterpart of isolateLexerCaches.
func isolateParserCaches(p *antlr.BaseParser) {
	atn := p.GetATN()
	p.Interpreter = antlr.NewParserATNSimulator(p, atn, parserSharedDFA.get(atn), antlr.NewPredictionContextCache())
}

var emptyLineRegex = regexp.MustCompile(`\r?\n\s*\r?\n`)

// checkEmptyLines enforces the blank line required after the package clause and
// after the import block. It reads the gaps out of `is` rather than out of the
// raw source string: ANTLR token offsets index the input as a stream of CODE
// POINTS, and a Go string slices by BYTES, so slicing the raw string with them
// silently reads the wrong window as soon as the file contains any non-ASCII
// rune (an em dash in a comment is enough) — which rejected perfectly
// well-formed files with a bogus "should follow by an empty line" syntax error.
// The InputStream owns the rune buffer those offsets index into and exposes it
// through GetText, so asking it keeps the offsets and the text in the same
// coordinate space by construction, with no second conversion of the source.
func (p *AntlrGalaParser) checkEmptyLines(is *antlr.InputStream, tree antlr.Tree) error {
	sourceFile, ok := tree.(grammar.ISourceFileContext)
	if !ok {
		return nil
	}

	pkg := sourceFile.PackageClause()
	if pkg == nil || pkg.GetStop() == nil {
		return nil
	}

	pkgEnd := pkg.GetStop().GetStop()

	imports := sourceFile.AllImportDeclaration()
	tops := sourceFile.AllTopLevelDeclaration()

	var nextToken antlr.Token
	if len(imports) > 0 {
		nextToken = imports[0].GetStart()
	} else if len(tops) > 0 {
		nextToken = tops[0].GetStart()
	}

	if nextToken != nil {
		if between, ok := runeSpan(is, pkgEnd+1, nextToken.GetStart()); ok {
			if !emptyLineRegex.MatchString(between) {
				return galaerr.NewSyntaxError(nextToken.GetLine(), 0, "packageClause should follow by an empty line")
			}
		}
	}

	if len(imports) > 0 && len(tops) > 0 {
		lastImport := imports[len(imports)-1]
		if lastImport.GetStop() != nil {
			importEnd := lastImport.GetStop().GetStop()
			nextTop := tops[0].GetStart()

			if between, ok := runeSpan(is, importEnd+1, nextTop.GetStart()); ok {
				if !emptyLineRegex.MatchString(between) {
					return galaerr.NewSyntaxError(nextTop.GetLine(), 0, "importDeclaration should follow by an empty line")
				}
			}
		}
	}

	return nil
}

// runeSpan returns the source text between the code-point offsets [start, end)
// as a string, reporting ok=false when the span is not a usable window into the
// source. It keeps the bounds checks in one place so both call sites above
// agree, and converts the caller's exclusive end to the inclusive stop
// InputStream.GetText expects.
//
// A degenerate span (start >= end) reports ok=false, so the caller skips the
// check rather than testing an empty string — which the blank-line regex can
// never match and which would therefore report a missing blank line at a
// position where there is no gap to inspect at all. GALA's grammar has no way
// to produce a zero-width gap here (the package name and the next keyword are
// always separated by at least one space), so this only pins down the
// degenerate case rather than changing any reachable behaviour.
func runeSpan(is *antlr.InputStream, start, end int) (string, bool) {
	if start < 0 || start >= end || end > is.Size() {
		return "", false
	}
	return is.GetText(start, end-1), true
}

type GalaErrorListener struct {
	*antlr.DefaultErrorListener
	Errors []error

	// reportedGoType is the composite-literal context a GALA-E0040 has already
	// been reported for, if any. See SyntaxError for why it is remembered.
	reportedGoType *grammar.CompositeLiteralContext

	// bareLambdaLine is the line a GALA-E0042 has already been reported on, or
	// 0. An unparenthesized lambda parameter derails the parser for the rest of
	// the expression, so ANTLR re-reports it against each enclosing rule it
	// unwinds through — four errors for one missing pair of parentheses, all on
	// the same line.
	//
	// Keyed on the LINE, unlike reportedGoType above which keys on context
	// identity. Context identity does not work for this cascade: the follow-on
	// errors are reported from the different rules the parser unwinds through,
	// so no single context is common to them. The line is what they share.
	//
	// The tradeoff is the usual one for cascade suppression: a genuinely
	// unrelated later error on the same line is swallowed too, and a lambda
	// whose body wraps onto the next line can still leak one follow-on. The
	// first error on any line is always reported, so nothing is hidden that
	// would not resurface on the next compile.
	bareLambdaLine int
}

func (l *GalaErrorListener) SyntaxError(recognizer antlr.Recognizer, offendingSymbol interface{}, line, column int, msg string, e antlr.RecognitionException) {
	lit := currentCompositeLiteral(recognizer)

	// Cascade suppression. Once E0040 is reported, ANTLR recovers by INSERTING
	// the '{' it wanted and carries on parsing the rest of the file as that
	// literal's element list, so every following token is re-reported against
	// the same context ("mismatched input 'Println' expecting {',', '}'}").
	// Those follow-ups describe a brace the user never wrote. Keyed on context
	// identity, so an unrelated error elsewhere in the file is untouched.
	if lit != nil && lit == l.reportedGoType {
		return
	}

	if err := goTypeInExpressionError(lit); err != nil {
		l.reportedGoType = lit
		l.Errors = append(l.Errors, err)
		return
	}

	// Cascade suppression for the unparenthesized lambda parameter, for the
	// same reason as above: one missing `(` yields a pile of follow-on errors
	// on that line, all describing the wreckage rather than the cause.
	if l.bareLambdaLine != 0 && line == l.bareLambdaLine {
		return
	}
	if err := bareLambdaParamError(recognizer, offendingSymbol); err != nil {
		l.bareLambdaLine = line
		l.Errors = append(l.Errors, err)
		return
	}

	if err := structTagError(recognizer, offendingSymbol); err != nil {
		l.Errors = append(l.Errors, err)
		return
	}

	l.Errors = append(l.Errors, galaerr.NewSyntaxError(line, column, hideNewlineTokens(msg)))
}

// structTagError recognizes a Go struct tag written after a struct field's
// type, in either string form (`Name string "json:\"name\""`), and
// returns GALA-E0085 for it, or nil when the error is something else.
//
// GALA has no struct tags, so the grammar has no slot for one and ANTLR
// reports the string as extraneous input. The shape is a string literal that
// the parser hit in the list of a struct's fields, directly after a field and
// on its line: the field list of a block-form struct, the parameter list of a
// shorthand struct, or the field list of a sealed case. A string anywhere deeper, such
// as inside a field's default value, fails in a nested rule and is left to the
// ordinary syntax error, and so is one in a function's parameter list.
func structTagError(recognizer antlr.Recognizer, offendingSymbol interface{}) *galaerr.SemanticError {
	tok, ok := offendingSymbol.(antlr.Token)
	if !ok || tok == nil {
		return nil
	}
	if tt := tok.GetTokenType(); tt != kinds().str && tt != kinds().rawStr {
		return nil
	}
	psr, ok := recognizer.(antlr.Parser)
	if !ok {
		return nil
	}
	field := lastStructField(psr.GetParserRuleContext())
	if field == nil || field.Identifier() == nil {
		return nil
	}
	// A tag sits right after the field, on its line. A string after a ',' or
	// on a line of its own is not one: it is left to the ordinary error.
	end := field.GetStop()
	prev := prevCodeToken(psr.GetTokenStream(), tok.GetTokenIndex())
	if end == nil || prev == nil || prev.GetTokenIndex() != end.GetTokenIndex() || end.GetLine() != tok.GetLine() {
		return nil
	}

	return structTagDiagnostic(tok.GetLine(), tok.GetColumn(), field.Identifier().GetText(), tok.GetText())
}

// structTagDiagnostic is GALA-E0085 for the tag text at line:col after the
// field named name.
func structTagDiagnostic(line, col int, name, text string) *galaerr.SemanticError {
	err := galaerr.NewCodedSemanticError(
		galaerr.CodeStructTag,
		line,
		col,
		fmt.Sprintf("struct field %s has a tag: GALA has no struct tags", name),
		"remove the tag; set JSON and YAML keys on Codec[T] with .Rename(field, key), .Omit and .OmitEmpty, "+
			"and write a type that a Go library reads through its tags in a .go file of the package",
	)
	// Span the tag, so the caret covers what has to go. A raw-string tag can
	// span lines; it is left to the renderer's single-token scan.
	if !strings.ContainsAny(text, "\r\n") {
		err = err.WithSpan(col + len([]rune(text)))
	}
	return err
}

// structField is what a block-form field, a shorthand struct's parameter and a
// sealed case's field have in common: a name and an end.
type structField interface {
	Identifier() grammar.IIdentifierContext
	GetStop() antlr.Token
}

// lastStructField returns, when ctx — the rule the parser was in when it
// rejected a string literal — is a struct's field list, the last field parsed
// in it; otherwise nil.
func lastStructField(ctx antlr.ParserRuleContext) structField {
	// ANTLR reports the string from the list rule or, at a closing ')', from
	// the rule that holds the list; both are accepted.
	switch c := ctx.(type) {
	case *grammar.StructTypeContext:
		return lastOf(c.AllStructField())
	case *grammar.ParameterListContext:
		if params, ok := c.GetParent().(*grammar.ParametersContext); ok && isShorthandStruct(params) {
			return lastOf(c.AllParameter())
		}
	case *grammar.ParametersContext:
		if list, ok := c.ParameterList().(*grammar.ParameterListContext); ok && isShorthandStruct(c) {
			return lastOf(list.AllParameter())
		}
	case *grammar.SealedCaseFieldListContext:
		return lastOf(c.AllSealedCaseField())
	case *grammar.SealedCaseContext:
		if list, ok := c.SealedCaseFieldList().(*grammar.SealedCaseFieldListContext); ok {
			return lastOf(list.AllSealedCaseField())
		}
	}
	return nil
}

// isShorthandStruct reports whether params is a shorthand struct's field list
// rather than a function's or a lambda's parameter list.
func isShorthandStruct(params *grammar.ParametersContext) bool {
	_, ok := params.GetParent().(*grammar.StructShorthandDeclarationContext)
	return ok
}

func lastOf[F structField](fields []F) structField {
	if len(fields) == 0 {
		return nil
	}
	return fields[len(fields)-1]
}

// bareLambdaParamError recognizes a lambda written without parentheses around
// its single parameter — `x => e` where GALA requires `(x) => e` — and returns
// the coded diagnostic for it, or nil when the error is something else.
//
// The shape is identified from the tokens rather than the parse tree, because
// by the time ANTLR reports this the tree is already error-recovered garbage:
// the offending token is `=>` and the token immediately before it is a plain
// IDENTIFIER. That is enough, since every legal `=>` in GALA is preceded by
// either `)` (a lambda's parameter list) or a pattern in a `case` arm — and a
// `case` arm's own `=>` is reached only after `case`, which parses fine.
func bareLambdaParamError(recognizer antlr.Recognizer, offendingSymbol interface{}) *galaerr.SemanticError {
	tok, ok := offendingSymbol.(antlr.Token)
	if !ok || tok == nil || tok.GetText() != "=>" {
		return nil
	}
	psr, ok := recognizer.(antlr.Parser)
	if !ok {
		return nil
	}
	stream := psr.GetTokenStream()
	if stream == nil {
		return nil
	}
	prev := prevCodeToken(stream, tok.GetTokenIndex())
	if prev == nil || prev.GetTokenType() != kinds().identifier {
		return nil
	}

	return bareLambdaError(prev.GetLine(), prev.GetColumn(), prev.GetText())
}

// bareLambdaError is GALA-E0042 for the parameter name at line:col.
func bareLambdaError(line, col int, name string) *galaerr.SemanticError {
	err := galaerr.NewCodedSemanticError(
		galaerr.CodeBareLambdaParam,
		line,
		col,
		"lambda parameters must be parenthesized",
		fmt.Sprintf("use `(%s) => ...`; GALA always parenthesizes a lambda's parameter list, "+
			"including a single parameter", name),
	)
	// Span the parameter itself, so the caret sits under what has to change
	// rather than under the arrow that happened to trip the parser.
	return err.WithSpan(col + len([]rune(name)))
}

// prevCodeToken returns the nearest default-channel token before idx, skipping
// comments.
//
// Comments ride the hidden channel (see docs.go), so they occupy real token
// indices: `x /* note */ => ...` puts a COMMENT between the identifier and the
// arrow. Walking back a single index would land on the comment and silently
// drop this diagnostic in favour of the generic parse error. Any other
// token-index arithmetic on this stream needs the same treatment.
func prevCodeToken(stream antlr.TokenStream, idx int) antlr.Token {
	for i := idx - 1; i >= 0; i-- {
		tok := stream.Get(i)
		if tok == nil {
			return nil
		}
		if tok.GetChannel() == antlr.TokenDefaultChannel {
			return tok
		}
	}
	return nil
}

// currentCompositeLiteral returns the rule the parser was inside when it
// reported an error, if that rule is a compositeLiteral. Any other rule — and
// any non-parser recognizer, i.e. the lexer — yields nil.
func currentCompositeLiteral(recognizer antlr.Recognizer) *grammar.CompositeLiteralContext {
	psr, ok := recognizer.(antlr.Parser)
	if !ok {
		return nil
	}
	lit, _ := psr.GetParserRuleContext().(*grammar.CompositeLiteralContext)
	return lit
}

// goTypeInExpressionError turns the parse failure behind ANTLR's
// "missing '{' at …" into GALA-E0040 when — and only when — its cause is a Go
// slice or map type written in expression position. It returns nil for every
// other failure, so the raw syntax error still reaches the user unchanged.
//
// The gate is structural rather than message-based, because ANTLR's recovery
// wording is not part of any contract:
//
//   - the innermost rule is `compositeLiteral` (`type ('{' … '}')`). The parser
//     only lands there for a bracketed expression when the bracket contents are
//     not an expression list, which for `f[…](…)` means a type the expression
//     grammar cannot spell;
//   - that context has exactly one child, the `type` — so the parse died at the
//     '{' slot, before consuming a brace. A malformed but genuine composite
//     literal (`[]int{1, 2,`) has consumed its brace by the time it fails and
//     is left to the ordinary syntax error;
//   - and that `type` actually contains a Go slice or map type. Without this
//     the code would fire on a func type, which is a different problem.
func goTypeInExpressionError(lit *grammar.CompositeLiteralContext) error {
	if lit == nil || lit.GetChildCount() != 1 {
		return nil
	}
	typeCtx := lit.Type_()
	if typeCtx == nil {
		return nil
	}
	offender := findGoSliceOrMapType(typeCtx)
	if offender == nil {
		return nil
	}
	start, stop := offender.GetStart(), offender.GetStop()
	if start == nil {
		return nil
	}
	var args []string
	for _, a := range offender.AllType_() {
		args = append(args, a.GetText())
	}
	return goTypeDiagnostic(start.GetLine(), start.GetColumn(), offender.GetText(), args,
		stop != nil && stop.GetLine() == start.GetLine())
}

// goTypeDiagnostic is GALA-E0040 for the Go slice or map type text at
// line:col, whose type arguments' texts are args; oneLine is whether the
// type sits on one line.
func goTypeDiagnostic(line, col int, text string, args []string, oneLine bool) error {
	kind := "slice"
	if strings.HasPrefix(text, "map[") {
		kind = "map"
	}
	err := galaerr.NewCodedSemanticError(
		galaerr.CodeGoTypeInExpression,
		line,
		col,
		fmt.Sprintf("Go %s type %s is not allowed in an expression", kind, text),
		// The suggestion leads and is separated by "; " so the renderer's
		// terse caret annotation — which cuts the hint at its first clause —
		// shows the replacement rather than a truncated rule.
		typeSuggestion(text, args)+"; Go slice and map types are an interop surface, allowed only where a type is expected: a function signature, a struct field, a val/var annotation, or a type alias",
	)
	// Span the offending type exactly, so the caret covers `[]byte` rather
	// than the single token the renderer would otherwise scan out. Only when
	// the type sits on one line; a multi-line type leaves the span unset and
	// falls back to that scan.
	if oneLine {
		err = err.WithSpan(col + len([]rune(text)))
	}
	return err
}

// typeSuggestion spells the offending Go type the GALA way, given its text
// and its type arguments' texts, so the message leaves the reader with the
// replacement and not just the prohibition.
func typeSuggestion(text string, args []string) string {
	switch {
	case strings.HasPrefix(text, "map[") && len(args) >= 2:
		return fmt.Sprintf("use HashMap[%s, %s] instead", args[0], args[1])
	case strings.HasPrefix(text, "[]") && len(args) >= 1:
		// A byte slice is GALA's `string` far more often than it is a
		// collection of numbers, so name both rather than send text handling
		// through Array[byte].
		if args[0] == "byte" {
			return "use Array[byte], or string for text, instead"
		}
		return fmt.Sprintf("use Array[%s] instead", args[0])
	}
	return "use a GALA collection type (Array/HashMap) instead"
}

// findGoSliceOrMapType returns the first Go slice or map type at or below node,
// in source order, or nil when there is none. Searching the whole subtree is
// what lets the outer type be an ordinary generic — the offender in
// `EmptyHashMap[string, []byte]` is the type argument, not the literal's type.
func findGoSliceOrMapType(node antlr.Tree) grammar.ITypeContext {
	if node == nil {
		return nil
	}
	if ty, ok := node.(*grammar.TypeContext); ok {
		if txt := ty.GetText(); strings.HasPrefix(txt, "[]") || strings.HasPrefix(txt, "map[") {
			return ty
		}
	}
	for _, child := range node.GetChildren() {
		if found := findGoSliceOrMapType(child); found != nil {
			return found
		}
	}
	return nil
}
