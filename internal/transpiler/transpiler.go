package transpiler

import (
	"go/ast"
	"go/token"

	"github.com/antlr4-go/antlr/v4"
)

const (
	StdPackage    = "std"
	StdImportPath = "martianoff/gala/std"

	TypeOption    = "Option"
	TypeImmutable = "Immutable"
	TypeTuple     = "Tuple"
	TypeEither    = "Either"

	FuncSome          = "Some"
	FuncNone          = "None"
	FuncLeft          = "Left"
	FuncRight         = "Right"
	FuncIsLeft        = "IsLeft"
	FuncIsRight       = "IsRight"
	FuncGetLeftValue  = "GetLeftValue"
	FuncGetRightValue = "GetRightValue"
	FuncNewImmutable  = "NewImmutable"
	FuncCopy          = "Copy"
	MethodGet         = "Get"
)

// RichAST provides metadata about a Gala source file.
type RichAST struct {
	Tree      antlr.Tree
	Types     map[string]*TypeMetadata
	Functions map[string]*FunctionMetadata
}

// Merge combines metadata from another RichAST into this one.
func (r *RichAST) Merge(other *RichAST) {
	if other == nil {
		return
	}
	if r.Types == nil {
		r.Types = make(map[string]*TypeMetadata)
	}
	if r.Functions == nil {
		r.Functions = make(map[string]*FunctionMetadata)
	}
	for k, v := range other.Types {
		r.Types[k] = v
	}
	for k, v := range other.Functions {
		r.Functions[k] = v
	}
}

type TypeMetadata struct {
	Name       string
	Methods    map[string]*MethodMetadata
	Fields     map[string]string // Name -> Type
	FieldNames []string          // To preserve order
	TypeParams []string
	ImmutFlags []bool
}

type MethodMetadata struct {
	Name       string
	ReturnType string
	TypeParams []string
	IsGeneric  bool // Force transformation to standalone function
}

type FunctionMetadata struct {
	Name       string
	ReturnType string
	TypeParams []string
}

// GalaParser defines the interface for parsing Gala source code.
type GalaParser interface {
	Parse(input string) (antlr.Tree, error)
}

// Analyzer analyzes a Gala ANTLR parse tree and produces a RichAST.
type Analyzer interface {
	Analyze(tree antlr.Tree) (*RichAST, error)
}

// ASTTransformer transforms a Gala RichAST into a Go AST file and its FileSet.
type ASTTransformer interface {
	Transform(richAST *RichAST) (*token.FileSet, *ast.File, error)
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
	analyzer    Analyzer
	transformer ASTTransformer
	generator   CodeGenerator
}

// NewGalaToGoTranspiler creates a new instance of GalaToGoTranspiler with its dependencies.
func NewGalaToGoTranspiler(
	parser GalaParser,
	analyzer Analyzer,
	transformer ASTTransformer,
	generator CodeGenerator,
) *GalaToGoTranspiler {
	return &GalaToGoTranspiler{
		parser:      parser,
		analyzer:    analyzer,
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

	richAST, err := t.analyzer.Analyze(tree)
	if err != nil {
		return "", err
	}

	fset, file, err := t.transformer.Transform(richAST)
	if err != nil {
		return "", err
	}

	return t.generator.Generate(fset, file)
}
