// Package galop runs galop's parser for gala.g4, which Bazel generates next to
// ANTLR's in internal/parser/grammar (galop's antlr flavor builds ANTLR's own
// context types). internal/parser chooses between the parsers and recovers if
// one panics.
package galop

import (
	"github.com/antlr4-go/antlr/v4"

	"martianoff/gala/internal/parser/grammar"
	"martianoff/gala/internal/parser/native"
)

// NewLexer returns galop's lexer for input, and the ANTLR base lexer in it
// (whose simulator position seeds where the input starts). Its tokens are
// ANTLR's lexer's.
func NewLexer(input antlr.CharStream) (antlr.Lexer, *antlr.BaseLexer) {
	lexer := grammar.NewgalaGalopLexer(input)
	return lexer, lexer.BaseLexer
}

// SourceFile parses stream as a source file. Building the parser fills the
// stream, so every token, the hidden ones included, is in it afterwards. ok is
// false when the input has a syntax error: galop does not recover from errors,
// so the caller parses the input with ANTLR instead.
func SourceFile(stream *antlr.CommonTokenStream) (grammar.ISourceFileContext, bool) {
	tree, err := grammar.NewgalaGalopParser(stream).SourceFile()
	return tree, err == nil
}

// Expression parses stream as one expression, as SourceFile parses a file.
// The expression rule does not end with EOF, so ok is also false when tokens
// are left over: the caller's ANTLR parse then decides what a partial match
// means.
func Expression(stream *antlr.CommonTokenStream) (grammar.IExpressionContext, bool) {
	expr, err := grammar.NewgalaGalopParser(stream).Expression()
	return expr, err == nil && stream.LA(1) == antlr.TokenEOF
}

// ToNative converts root, read from stream over src, to galop's native tree
// with galop's generated converter, and returns the ANTLR context of each
// native node, by node index (the root's last).
func ToNative(root antlr.ParserRuleContext, stream *antlr.CommonTokenStream, src string) (*native.GalaTree, []antlr.ParserRuleContext) {
	tree, _, ctxs := grammar.GalaToNativeContexts(root, stream, src)
	return tree, ctxs
}
