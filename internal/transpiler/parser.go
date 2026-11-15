package transpiler

import (
	"martianoff/gala/internal/parser"
)

type antlrGalaParser struct {
	wrapper *parser.AntlrGalaParser
}

// NewAntlrGalaParser creates the GalaParser: galop's generated parser
// (parser.AntlrGalaParser, whose name predates galop).
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
