// Package galop, in this directory, stands in for internal/parser/galop in
// builds without galop's parser: on platforms galop publishes no binary for,
// and in builds with --//internal/parser:antlr. Bazel links it under the same
// import path; GALA then parses with ANTLR's parser alone.
package galop

import (
	"github.com/antlr4-go/antlr/v4"

	"martianoff/gala/internal/parser/grammar"
	"martianoff/gala/internal/parser/native"
)

// BuiltIn says galop's parser is linked into this binary: here it is not.
const BuiltIn = false

// NewLexer is not called when BuiltIn is false.
func NewLexer(antlr.CharStream) (antlr.Lexer, *antlr.BaseLexer) { return nil, nil }

// SourceFile never parses: ok is false.
func SourceFile(*antlr.CommonTokenStream) (grammar.ISourceFileContext, bool) { return nil, false }

// Expression never parses: ok is false.
func Expression(*antlr.CommonTokenStream) (grammar.IExpressionContext, bool) { return nil, false }

// ToNative has no converter to call, the native tree being galop's: no tree.
// (A build without galop makes no native-tree handle to call it.)
func ToNative(antlr.ParserRuleContext, *antlr.CommonTokenStream, string) (*native.GalaTree, []antlr.ParserRuleContext) {
	return nil, nil
}
