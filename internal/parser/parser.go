package parser

import (
	"regexp"

	"martianoff/gala/galaerr"
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
		return nil, &galaerr.MultiError{Errors: errorListener.Errors}
	}

	if err := p.checkEmptyLines(input, tree); err != nil {
		return nil, &galaerr.MultiError{Errors: []error{err}}
	}

	return tree, nil
}

var emptyLineRegex = regexp.MustCompile(`\r?\n\s*\r?\n`)

func (p *AntlrGalaParser) checkEmptyLines(input string, tree antlr.Tree) error {
	sourceFile, ok := tree.(grammar.ISourceFileContext)
	if !ok {
		return nil
	}

	pkg := sourceFile.PackageClause()
	if pkg == nil || pkg.GetStop() == nil {
		return nil
	}

	pkgEnd := pkg.GetStop().GetStop()

	imports := sourceFile.AllImportDeclaration()
	tops := sourceFile.AllTopLevelDeclaration()

	var nextToken antlr.Token
	if len(imports) > 0 {
		nextToken = imports[0].GetStart()
	} else if len(tops) > 0 {
		nextToken = tops[0].GetStart()
	}

	if nextToken != nil {
		if pkgEnd+1 < len(input) && nextToken.GetStart() <= len(input) {
			between := input[pkgEnd+1 : nextToken.GetStart()]
			if !emptyLineRegex.MatchString(between) {
				return galaerr.NewSyntaxError(nextToken.GetLine(), 0, "packageClause should follow by an empty line")
			}
		}
	}

	if len(imports) > 0 && len(tops) > 0 {
		lastImport := imports[len(imports)-1]
		if lastImport.GetStop() != nil {
			importEnd := lastImport.GetStop().GetStop()
			nextTop := tops[0].GetStart()

			if importEnd+1 < len(input) && nextTop.GetStart() <= len(input) {
				between := input[importEnd+1 : nextTop.GetStart()]
				if !emptyLineRegex.MatchString(between) {
					return galaerr.NewSyntaxError(nextTop.GetLine(), 0, "importDeclaration should follow by an empty line")
				}
			}
		}
	}

	return nil
}

type GalaErrorListener struct {
	*antlr.DefaultErrorListener
	Errors []error
}

func (l *GalaErrorListener) SyntaxError(recognizer antlr.Recognizer, offendingSymbol interface{}, line, column int, msg string, e antlr.RecognitionException) {
	l.Errors = append(l.Errors, galaerr.NewSyntaxError(line, column, msg))
}
