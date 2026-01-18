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

	FuncSome         = "Some"
	FuncNone         = "None"
	FuncLeft         = "Left"
	FuncRight        = "Right"
	FuncNewImmutable = "NewImmutable"
	FuncCopy         = "Copy"
	MethodGet        = "Get"
)

// RichAST provides metadata about a Gala source file.
type RichAST struct {
	Tree             antlr.Tree
	PackageName      string
	Types            map[string]*TypeMetadata
	Functions        map[string]*FunctionMetadata
	Packages         map[string]string                   // path -> pkgName
	CompanionObjects map[string]*CompanionObjectMetadata // companion name -> metadata
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
	if r.Packages == nil {
		r.Packages = make(map[string]string)
	}
	if r.CompanionObjects == nil {
		r.CompanionObjects = make(map[string]*CompanionObjectMetadata)
	}
	for k, v := range other.Types {
		r.Types[k] = v
	}
	for k, v := range other.Functions {
		r.Functions[k] = v
	}
	for k, v := range other.Packages {
		r.Packages[k] = v
	}
	for k, v := range other.CompanionObjects {
		r.CompanionObjects[k] = v
	}
}

type TypeMetadata struct {
	Name       string
	Package    string
	Methods    map[string]*MethodMetadata
	Fields     map[string]Type // Name -> Type
	FieldNames []string        // To preserve order
	TypeParams []string
	ImmutFlags []bool
}

type MethodMetadata struct {
	Name       string
	Package    string
	ParamTypes []Type
	ReturnType Type
	TypeParams []string
	IsGeneric  bool // Force transformation to standalone function
}

type FunctionMetadata struct {
	Name       string
	Package    string
	ParamTypes []Type
	ReturnType Type
	TypeParams []string
}

// CompanionObjectMetadata stores information about companion objects that can be used
// for pattern matching (types with Unapply methods).
type CompanionObjectMetadata struct {
	Name           string // e.g., "Some", "Left", "Right"
	Package        string // e.g., "std"
	TargetType     string // The container type this extracts from, e.g., "Option", "Either"
	ExtractIndices []int  // Which type param indices to extract (e.g., [0] for Some, [1] for Right)
}

// GalaParser defines the interface for parsing Gala source code.
type GalaParser interface {
	Parse(input string) (antlr.Tree, error)
}

// Analyzer analyzes a Gala ANTLR parse tree and produces a RichAST.
type Analyzer interface {
	Analyze(tree antlr.Tree, filePath string) (*RichAST, error)
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
	Transpile(input string, filePath string) (string, error)
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
func (t *GalaToGoTranspiler) Transpile(input string, filePath string) (string, error) {
	tree, err := t.parser.Parse(input)
	if err != nil {
		return "", err
	}

	richAST, err := t.analyzer.Analyze(tree, filePath)
	if err != nil {
		return "", err
	}

	fset, file, err := t.transformer.Transform(richAST)
	if err != nil {
		return "", err
	}

	return t.generator.Generate(fset, file)
}
