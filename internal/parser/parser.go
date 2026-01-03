package parser

import (
	"fmt"
	"martianoff/gala/internal/parser/grammar"

	"github.com/antlr4-go/antlr/v4"
)

type AntlrGalaParser struct {
}

func NewAntlrGalaParser() *AntlrGalaParser {
	return &AntlrGalaParser{}
}

func (p *AntlrGalaParser) Parse(input string) (antlr.Tree, error) {
	is := antlr.NewInputStream(input)
	lexer := grammar.NewgalaLexer(is)
	stream := antlr.NewCommonTokenStream(lexer, antlr.TokenDefaultChannel)
	parser := grammar.NewgalaParser(stream)

	parser.RemoveErrorListeners()
	errorListener := &GalaErrorListener{}
	parser.AddErrorListener(errorListener)

	tree := parser.SourceFile()

	if len(errorListener.Errors) > 0 {
		return nil, fmt.Errorf("parsing errors: %v", errorListener.Errors)
	}

	return tree, nil
}

type GalaErrorListener struct {
	*antlr.DefaultErrorListener
	Errors []string
}

func (l *GalaErrorListener) SyntaxError(recognizer antlr.Recognizer, offendingSymbol interface{}, line, column int, msg string, e antlr.RecognitionException) {
	l.Errors = append(l.Errors, fmt.Sprintf("line %d:%d %s", line, column, msg))
}
