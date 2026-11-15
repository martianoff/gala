package parser

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"martianoff/gala/galaerr"
	"martianoff/gala/internal/parser/native"
)

// AntlrGalaParser parses GALA source files with galop's native lexer and
// recovering parser (internal/parser/native), which galop generates from
// internal/parser/grammar/gala.g4. Its name predates galop: GALA no longer
// parses with ANTLR.
type AntlrGalaParser struct{}

func NewAntlrGalaParser() *AntlrGalaParser {
	return &AntlrGalaParser{}
}

// parseSourceFile and parseExpression are galop's recovering parsers of a
// source file and of an expression. Only a test replaces them, with ones
// that panic as a galop bug would.
var (
	parseSourceFile = native.ParseSourceFileWithRecovery
	parseExpression = native.ParseExpressionWithRecovery
)

// Parsed is one parse of a source file: its doc comments, its errors, and its
// native tree (internal/parser/native), clean or error-recovered.
type Parsed struct {
	Docs   Docs
	Errors []error
	// Native is the parse's native tree (see Native), nil when there is none:
	// the parse gave no tree, or it reported an error (Errors is not empty:
	// a syntax error, or a source-text or blank-line check), whose tree
	// RecoveredNative holds. Every parse without errors has one.
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
// analyzer, the scope walk, the transformer) read the native tree only.
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

// ParseFile parses input as a source file with galop's native lexer and
// recovering parser, once, errors or not: the errors, their hints and the
// error-recovered tree all come from that parse. The errors are, in order,
// input's source-text error (checkSourceChars), the lexical and syntax errors
// (nativeSyntaxErrors) with the Go directives among its comments merged in by
// position (goDirectives), and a missing blank line (checkEmptyLinesNative).
//
// A panic in galop's lexer or parser (a galop bug) is recovered: the parse
// then has no tree and one error, an internal compiler error for the file
// (galopPanicError), so an editor and a build go on with the other files.
func (p *AntlrGalaParser) ParseFile(input string) *Parsed {
	// Drop a leading UTF-8 BOM once, up front: the lexer has no rule for
	// U+FEFF and would otherwise reject the file outright. Go, which GALA
	// transpiles to, ignores a leading BOM the same way.
	input = galaerr.StripBOM(input)

	// The source-text check goes first: an illegal character is the root
	// cause of any lexer error it provokes, so it should lead the report.
	charErr := checkSourceChars(input)
	ts, root, parseErrs, panicErr := galopParse(input, 0, 0, parseSourceFile)
	if panicErr != nil {
		return &Parsed{Errors: withCharErr(charErr, []error{panicErr})}
	}
	errs := withCharErr(charErr, mergeByPosition(nativeSyntaxErrors(root.Tree(), ts, parseErrs, reparseWith(parseSourceFile)), goDirectives(ts)))
	var handle *Native
	if root.Ok() {
		handle = nativeHandle(root.Tree())
		if err := checkEmptyLinesNative(root); err != nil {
			errs = append(errs, err)
		}
	}
	return newParsed(nativeDocComments(ts), errs, handle)
}

// galopParse lexes input with galop's native lexer at line/col (lexNative)
// and parses the tokens with parse, one of galop's recovering parsers. When
// either panics, the panic is recovered and returned as an internal compiler
// error at line/col (galopPanicError), with no tokens or tree. Only galop's
// lexer and parser run under the recover: a panic in GALA's own code (the
// line-break rule, what GALA does with the result) is GALA's bug, and is not
// reported as galop's.
func galopParse[N any](input string, line, col int, parse func(*native.GalaTokens) (N, []native.GalaParseError)) (*native.GalaTokens, N, []native.GalaParseError, error) {
	var zero N
	var ts *native.GalaTokens
	if err := underGalop("lexer", input, line, col, func() { ts = lexGalop(input, line, col) }); err != nil {
		return nil, zero, nil, err
	}
	retypeNewlines(ts)
	var root N
	var errs []native.GalaParseError
	if err := underGalop("parser", input, line, col, func() { root, errs = parse(ts) }); err != nil {
		return nil, zero, nil, err
	}
	return ts, root, errs, nil
}

// underGalop runs f, a call of galop's stage ("lexer" or "parser") on input
// at line/col, and returns the internal compiler error of a panic in it
// (galopPanicError), or nil.
func underGalop(stage, input string, line, col int, f func()) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = galopPanicError(stage, input, line, col, r)
		}
	}()
	f()
	return nil
}

// newParsed is a parse with doc comments, errors and native-tree handle: the
// clean native tree when there are no errors, else the error-recovered one.
func newParsed(docs Docs, errs []error, handle *Native) *Parsed {
	parsed := &Parsed{Docs: docs, Errors: errs}
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

var emptyLineRegex = regexp.MustCompile(`\r?\n\s*\r?\n`)

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
