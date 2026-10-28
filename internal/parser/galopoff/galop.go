// Package galop, in this directory, stands in for internal/parser/galop in
// builds without galop's parser: on platforms galop publishes no binary for,
// and in builds with --//internal/parser:antlr. Bazel links it under the same
// import path; GALA then parses with ANTLR's parser alone.
package galop

import (
	"github.com/antlr4-go/antlr/v4"

	"martianoff/gala/internal/parser/grammar"
)

// BuiltIn says galop's parser is linked into this binary: here it is not.
const BuiltIn = false

// NewLexer is not called when BuiltIn is false.
func NewLexer(antlr.CharStream, int, int) antlr.Lexer { return nil }

// SourceFile never parses: ok is false.
func SourceFile(*antlr.CommonTokenStream) (grammar.ISourceFileContext, bool) { return nil, false }

// Expression never parses: ok is false.
func Expression(*antlr.CommonTokenStream) (grammar.IExpressionContext, bool) { return nil, false }
