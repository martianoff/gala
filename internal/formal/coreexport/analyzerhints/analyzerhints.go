// Package analyzerhints gives the Core JSON exporter the analyzer's types for
// integer literals (coreexport.Options.LiteralType). It is a package of its
// own so that the exporter itself depends on the parser alone.
package analyzerhints

import (
	"martianoff/gala/internal/formal/coreexport"
	"martianoff/gala/internal/parser/native"
	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/analyzer"
)

var _ coreexport.LiteralTyper = LiteralType

// LiteralType is the analyzer's type for lit; false when it has none.
func LiteralType(lit native.LiteralNode) (string, bool) {
	if t, ok := analyzer.NativeLiteralType(lit).(transpiler.BasicType); ok {
		return t.Name, true
	}
	return "", false
}
