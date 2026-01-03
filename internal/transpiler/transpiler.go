package transpiler

import (
	"go/ast"
	"go/token"

	"github.com/antlr4-go/antlr/v4"
)

// GalaParser defines the interface for parsing Gala source code.
type GalaParser interface {
	Parse(input string) (antlr.Tree, error)
}

// ASTTransformer transforms a Gala ANTLR parse tree into a Go AST file and its FileSet.
type ASTTransformer interface {
	Transform(tree antlr.Tree) (*token.FileSet, *ast.File, error)
}

// CodeGenerator generates Go source code from a Go AST file and its FileSet.
type CodeGenerator interface {
	Generate(fset *token.FileSet, file *ast.File) (string, error)
}

// Transpiler defines the high-level interface for the Gala to Go conversion.
type Transpiler interface {
	Transpile(input string) (string, error)
}

// GalaToGoTranspiler orchestrates the transpilation process.
type GalaToGoTranspiler struct {
	parser      GalaParser
	transformer ASTTransformer
	generator   CodeGenerator
}

// NewGalaToGoTranspiler creates a new instance of GalaToGoTranspiler with its dependencies.
func NewGalaToGoTranspiler(
	parser GalaParser,
	transformer ASTTransformer,
	generator CodeGenerator,
) *GalaToGoTranspiler {
	return &GalaToGoTranspiler{
		parser:      parser,
		transformer: transformer,
		generator:   generator,
	}
}

// Transpile executes the full transpilation pipeline.
func (t *GalaToGoTranspiler) Transpile(input string) (string, error) {
	tree, err := t.parser.Parse(input)
	if err != nil {
		return "", err
	}

	fset, file, err := t.transformer.Transform(tree)
	if err != nil {
		return "", err
	}

	return t.generator.Generate(fset, file)
}
