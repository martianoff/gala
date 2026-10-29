package transpiler

import (
	"martianoff/gala/internal/parser"
)

type antlrGalaParser struct {
	wrapper *parser.AntlrGalaParser
}

// NewAntlrGalaParser creates a new GalaParser implementation using ANTLR.
func NewAntlrGalaParser() GalaParser {
	return &antlrGalaParser{
		wrapper: parser.NewAntlrGalaParser(),
	}
}

// ParseFile implements the GalaParser interface.
func (p *antlrGalaParser) ParseFile(input string) *parser.Parsed {
	return p.wrapper.ParseFile(input)
}

// Ensure antlrGalaParser implements GalaParser interface.
var _ GalaParser = (*antlrGalaParser)(nil)
