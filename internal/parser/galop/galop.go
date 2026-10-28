// Package galop runs galop's parser for gala.g4, which Bazel generates next to
// ANTLR's in internal/parser/grammar (galop's antlr flavor builds ANTLR's own
// context types). Builds without galop link internal/parser/galopoff under the
// same import path instead; internal/parser chooses between the parsers and
// recovers if one panics.
package galop

import (
	"github.com/antlr4-go/antlr/v4"

	"martianoff/gala/internal/parser/grammar"
)

// BuiltIn says galop's parser is linked into this binary.
const BuiltIn = true

// NewLexer returns galop's lexer for input. Its tokens are ANTLR's lexer's.
func NewLexer(input antlr.CharStream) antlr.Lexer { return grammar.NewgalaGalopLexer(input) }

// SourceFile parses stream as a source file. It fills the stream first, so
// every token, the hidden ones included, is in it afterwards. ok is false when
// the input has a syntax error: galop does not recover from errors, so the
// caller parses the input with ANTLR instead.
func SourceFile(stream *antlr.CommonTokenStream) (grammar.ISourceFileContext, bool) {
	stream.Fill()
	tree, err := grammar.NewgalaGalopParser(stream).SourceFile()
	return tree, err == nil
}

// Expression parses stream as one expression, as SourceFile parses a file.
// The expression rule does not end with EOF, so ok is also false when tokens
// are left over: the caller's ANTLR parse then decides what a partial match
// means.
func Expression(stream *antlr.CommonTokenStream) (grammar.IExpressionContext, bool) {
	stream.Fill()
	expr, err := grammar.NewgalaGalopParser(stream).Expression()
	return expr, err == nil && stream.LA(1) == antlr.TokenEOF
}
