package transpiler

import (
	"martianoff/gala/internal/parser"

	"github.com/antlr4-go/antlr/v4"
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

// Parse implements the GalaParser interface.
func (p *antlrGalaParser) Parse(input string) (antlr.Tree, map[int]string, error) {
	return p.wrapper.Parse(input)
}

// ParseLenient implements the GalaParser interface.
func (p *antlrGalaParser) ParseLenient(input string) (antlr.Tree, map[int]string, []error) {
	return p.wrapper.ParseLenient(input)
}

// Ensure antlrGalaParser implements GalaParser interface.
var _ GalaParser = (*antlrGalaParser)(nil)
