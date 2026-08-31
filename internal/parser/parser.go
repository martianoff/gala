package parser

import (
	"fmt"
	"regexp"
	"strings"
	"sync"

	"martianoff/gala/galaerr"
	"martianoff/gala/internal/parser/grammar"

	"github.com/antlr4-go/antlr/v4"
)

type AntlrGalaParser struct {
}

func NewAntlrGalaParser() *AntlrGalaParser {
	return &AntlrGalaParser{}
}

func (p *AntlrGalaParser) Parse(input string) (antlr.Tree, error) {
	tree, errs := p.ParseLenient(input)
	if len(errs) > 0 {
		return nil, &galaerr.MultiError{Errors: errs}
	}
	return tree, nil
}

// ParseLenient always returns ANTLR's error-recovered tree alongside any
// syntax errors. The tree contains error nodes but is structurally valid.
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
func (p *AntlrGalaParser) ParseLenient(input string) (antlr.Tree, []error) {
	// Drop a leading UTF-8 BOM once, up front: the lexer has no rule for
	// U+FEFF and would otherwise reject the file outright. Go, which GALA
	// transpiles to, ignores a leading BOM the same way.
	input = galaerr.StripBOM(input)

	is := antlr.NewInputStream(input)
	lexer := grammar.NewgalaLexer(is)
	isolateLexerCaches(lexer.BaseLexer)
	stream := antlr.NewCommonTokenStream(lexer, antlr.TokenDefaultChannel)
	parser := grammar.NewgalaParser(stream)
	isolateParserCaches(parser.BaseParser)

	errorListener := &GalaErrorListener{}

	lexer.RemoveErrorListeners()
	lexer.AddErrorListener(errorListener)

	parser.RemoveErrorListeners()
	parser.AddErrorListener(errorListener)

	tree := parser.SourceFile()

	var errs []error
	errs = append(errs, errorListener.Errors...)
	if err := p.checkEmptyLines(is, tree); err != nil {
		errs = append(errs, err)
	}

	return tree, errs
}

// ParseExpression parses a single GALA expression (not a whole source file)
// and returns its expression context alongside any syntax errors. Like
// ParseLenient it isolates the ANTLR prediction-context cache so concurrent
// parses stay race-free; the returned tree is error-recovered when errors are
// present. Used to re-parse the embedded expressions of an interpolated string.
func (p *AntlrGalaParser) ParseExpression(input string) (grammar.IExpressionContext, []error) {
	is := antlr.NewInputStream(input)
	lexer := grammar.NewgalaLexer(is)
	isolateLexerCaches(lexer.BaseLexer)
	stream := antlr.NewCommonTokenStream(lexer, antlr.TokenDefaultChannel)
	psr := grammar.NewgalaParser(stream)
	isolateParserCaches(psr.BaseParser)

	errorListener := &GalaErrorListener{}

	lexer.RemoveErrorListeners()
	lexer.AddErrorListener(errorListener)
	psr.RemoveErrorListeners()
	psr.AddErrorListener(errorListener)

	exprCtx := psr.Expression()
	return exprCtx, errorListener.Errors
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

	l.Errors = append(l.Errors, galaerr.NewSyntaxError(line, column, msg))
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
	idx := tok.GetTokenIndex()
	if idx <= 0 {
		return nil
	}
	prev := stream.Get(idx - 1)
	if prev == nil || symbolicTokenName(recognizer, prev) != "IDENTIFIER" {
		return nil
	}

	name := prev.GetText()
	err := galaerr.NewCodedSemanticError(
		galaerr.CodeBareLambdaParam,
		prev.GetLine(),
		prev.GetColumn(),
		"lambda parameters must be parenthesized",
		fmt.Sprintf("use `(%s) => ...`; GALA always parenthesizes a lambda's parameter list, "+
			"including a single parameter", name),
	)
	// Span the parameter itself, so the caret sits under what has to change
	// rather than under the arrow that happened to trip the parser.
	return err.WithSpan(prev.GetColumn() + len([]rune(name)))
}

// symbolicTokenName maps a token to its grammar symbol name (e.g. "IDENTIFIER")
// via the recognizer's vocabulary. The generated token-type constants are
// unexported, so the vocabulary is the only way to ask this from outside the
// grammar package.
func symbolicTokenName(recognizer antlr.Recognizer, tok antlr.Token) string {
	names := recognizer.GetSymbolicNames()
	tt := tok.GetTokenType()
	if tt < 0 || tt >= len(names) {
		return ""
	}
	return names[tt]
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

	start := offender.GetStart()
	stop := offender.GetStop()
	if start == nil {
		return nil
	}

	text := offender.GetText()
	kind := "slice"
	if strings.HasPrefix(text, "map[") {
		kind = "map"
	}

	err := galaerr.NewCodedSemanticError(
		galaerr.CodeGoTypeInExpression,
		start.GetLine(),
		start.GetColumn(),
		fmt.Sprintf("Go %s type %s is not allowed in an expression", kind, text),
		// The suggestion leads and is separated by "; " so the renderer's
		// terse caret annotation — which cuts the hint at its first clause —
		// shows the replacement rather than a truncated rule.
		galaTypeSuggestion(offender, text)+"; Go slice and map types are an interop surface, allowed only where a type is expected: a function signature, a struct field, a val/var annotation, or a type alias",
	)

	// Span the offending type exactly, so the caret covers `[]byte` rather
	// than the single token the renderer would otherwise scan out. Only when
	// the type sits on one line; a multi-line type leaves the span unset and
	// falls back to that scan.
	if stop != nil && stop.GetLine() == start.GetLine() {
		err = err.WithSpan(start.GetColumn() + len([]rune(text)))
	}
	return err
}

// galaTypeSuggestion spells the offending Go type the GALA way, so the message
// leaves the reader with the replacement and not just the prohibition.
func galaTypeSuggestion(offender grammar.ITypeContext, text string) string {
	args := offender.AllType_()
	switch {
	case strings.HasPrefix(text, "map[") && len(args) >= 2:
		return fmt.Sprintf("use HashMap[%s, %s] instead", args[0].GetText(), args[1].GetText())
	case strings.HasPrefix(text, "[]") && len(args) >= 1:
		// A byte slice is GALA's `string` far more often than it is a
		// collection of numbers, so name both rather than send text handling
		// through Array[byte].
		if args[0].GetText() == "byte" {
			return "use Array[byte], or string for text, instead"
		}
		return fmt.Sprintf("use Array[%s] instead", args[0].GetText())
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
