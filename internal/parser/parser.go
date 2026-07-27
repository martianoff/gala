package parser

import (
	"regexp"
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
	if err := p.checkEmptyLines(input, tree); err != nil {
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

func (p *AntlrGalaParser) checkEmptyLines(input string, tree antlr.Tree) error {
	sourceFile, ok := tree.(grammar.ISourceFileContext)
	if !ok {
		return nil
	}

	pkg := sourceFile.PackageClause()
	if pkg == nil || pkg.GetStop() == nil {
		return nil
	}

	// ANTLR token offsets index the input as a stream of CODE POINTS, not
	// bytes. Slicing the raw string with them silently reads the wrong window
	// as soon as the file contains any non-ASCII rune (an em dash in a comment
	// is enough), which used to reject perfectly well-formed files with a
	// bogus "should follow by an empty line" syntax error. Work in runes so
	// the offsets and the slicing agree.
	runes := []rune(input)

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
		if between, ok := runeSpan(runes, pkgEnd+1, nextToken.GetStart()); ok {
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

			if between, ok := runeSpan(runes, importEnd+1, nextTop.GetStart()); ok {
				if !emptyLineRegex.MatchString(between) {
					return galaerr.NewSyntaxError(nextTop.GetLine(), 0, "importDeclaration should follow by an empty line")
				}
			}
		}
	}

	return nil
}

// runeSpan returns runes[start:end] as a string, reporting ok=false when the
// span is not a usable window into the source (out of range or empty). It
// keeps the bounds checks in one place so both call sites above agree.
func runeSpan(runes []rune, start, end int) (string, bool) {
	if start < 0 || end > len(runes) || start >= end {
		return "", false
	}
	return string(runes[start:end]), true
}

type GalaErrorListener struct {
	*antlr.DefaultErrorListener
	Errors []error
}

func (l *GalaErrorListener) SyntaxError(recognizer antlr.Recognizer, offendingSymbol interface{}, line, column int, msg string, e antlr.RecognitionException) {
	l.Errors = append(l.Errors, galaerr.NewSyntaxError(line, column, msg))
}
