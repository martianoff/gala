package transformer

import (
	"fmt"
	"strings"

	"martianoff/gala/galaerr"
	"martianoff/gala/internal/parser/grammar"

	"github.com/antlr4-go/antlr/v4"
)

// GALA-E0058: a name the transpiler generates for a codec.
//
// A library package emits an exported StructMeta_X for each struct X it
// declares, and the main package emits _StructMeta_X and _ValueMeta_X on
// demand. A StructMeta reads and builds the struct's unexported fields: that
// is what lets another package encode it. Codecs reach it through Codec[T] and
// StructMeta[T](), which check at build time that the struct may be decoded.
// GALA code that named one would skip that check — a struct without Validate
// would fail only at run time — and could call its EncodeFields to read
// fields the package keeps to itself. So any identifier spelled like a
// generated name is rejected — a use, and also a declaration, which could
// collide with what the transpiler emits.
//
// The analyzer checks each source file before anything else reads it; an
// interpolated expression is parsed separately, so the transformer checks it
// when it parses it.

// The prefixes of the metadata type names the codec transpilation emits: a
// library struct's exported StructMeta, a main-package struct's StructMeta,
// and a ValueMeta.
const (
	structMetaPrefix     = "StructMeta_"
	mainStructMetaPrefix = "_StructMeta_"
	valueMetaPrefix      = "_ValueMeta_"
)

var generatedCodecPrefixes = []string{structMetaPrefix, mainStructMetaPrefix, valueMetaPrefix}

// CheckGeneratedCodecNames reports the first identifier in tree spelled like a
// generated codec metadata type, or nil when there is none.
func CheckGeneratedCodecNames(tree antlr.Tree) error {
	var found *grammar.IdentifierContext
	var visit func(antlr.Tree)
	visit = func(node antlr.Tree) {
		if found != nil {
			return
		}
		if id, ok := node.(*grammar.IdentifierContext); ok {
			if isGeneratedCodecName(id.GetText()) {
				found = id
			}
			return
		}
		for _, child := range node.GetChildren() {
			visit(child)
		}
	}
	visit(tree)
	if found == nil {
		return nil
	}
	name := found.GetText()
	tok := found.GetStart()
	return galaerr.NewCodedSemanticError(
		galaerr.CodeGeneratedCodecName,
		tok.GetLine(), tok.GetColumn(),
		fmt.Sprintf("%s is codec metadata the transpiler generates and cannot be named in GALA code", name),
		"use json.Codec[T], yaml.Codec[T] or StructMeta[T]() instead; "+
			"those check that a struct with private fields may be decoded",
	).WithSpan(tok.GetColumn() + len(name))
}

// isGeneratedCodecName reports whether name is spelled like a generated codec
// metadata type: one of the prefixes followed by at least one character.
func isGeneratedCodecName(name string) bool {
	for _, prefix := range generatedCodecPrefixes {
		if len(name) > len(prefix) && strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}
