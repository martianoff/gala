// Package galop holds what internal/parser uses of the code galop generates in
// its antlr flavor next to ANTLR's parser in internal/parser/grammar: the
// converter from ANTLR's trees to galop's native tree. internal/parser parses
// with galop's native flavor (internal/parser/native) directly; the converter
// is how a tree of ANTLR's parser (ANTLR's engine, or a file with errors)
// becomes a native tree.
package galop

import (
	"github.com/antlr4-go/antlr/v4"

	"martianoff/gala/internal/parser/grammar"
	"martianoff/gala/internal/parser/native"
)

// ToNative converts root, read from stream over src, to galop's native tree
// with galop's generated converter.
func ToNative(root antlr.ParserRuleContext, stream *antlr.CommonTokenStream, src string) *native.GalaTree {
	tree, _ := grammar.GalaToNative(root, stream, src)
	return tree
}
