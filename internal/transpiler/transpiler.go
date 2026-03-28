package transpiler

import (
	"go/ast"
	"go/token"
	"strings"

	"github.com/antlr4-go/antlr/v4"

	"martianoff/gala/internal/transpiler/profiler"
)

// Type and function name constants for the std library.
// These provide semantic names for commonly used std types and functions.
// For std package metadata (exports, conflict detection), use the registry package.
const (
	TypeOption      = "Option"
	TypeImmutable   = "Immutable"
	TypeTuple       = "Tuple"
	TypeTuple3      = "Tuple3"
	TypeTuple4      = "Tuple4"
	TypeTuple5      = "Tuple5"
	TypeTuple6      = "Tuple6"
	TypeTuple7      = "Tuple7"
	TypeTuple8      = "Tuple8"
	TypeTuple9      = "Tuple9"
	TypeTuple10     = "Tuple10"
	TypeEither      = "Either"
	TypeTry         = "Try"
	TypeTraversable = "Traversable"
	TypeIterable    = "Iterable"
	TypeEmbeddedFS  = "EmbeddedFS"

	FuncSome         = "Some"
	FuncNone         = "None"
	FuncLeft         = "Left"
	FuncRight        = "Right"
	FuncSuccess      = "Success"
	FuncFailure      = "Failure"
	FuncNewImmutable  = "NewImmutable"
	FuncNewEmbeddedFS = "NewEmbeddedFS"
	FuncCopy          = "Copy"
	MethodGet        = "Get"
	MethodPtr        = "Ptr"

	// ConstPtr - read-only pointer wrapper for pointers to immutable values
	TypeConstPtr    = "ConstPtr"
	FuncNewConstPtr = "NewConstPtr"
	MethodDeref     = "Deref"
)

// EmbedDirective represents a single `embed val` declaration parsed from GALA source.
type EmbedDirective struct {
	VarName  string   // GALA variable name (e.g., "static")
	GoVar    string   // Go variable name for //go:embed target (e.g., "_embed_static" for EmbeddedFS, or same as VarName for string)
	Patterns []string // Embed patterns (e.g., ["static/*", "templates/*.html"])
	TypeName string   // Declared type: "string", "EmbeddedFS", or "" (infer)
}

// RichAST provides metadata about a Gala source file.
type RichAST struct {
	Tree             antlr.Tree
	PackageName      string
	Types            map[string]*TypeMetadata
	Functions        map[string]*FunctionMetadata
	Packages         map[string]string                   // path -> pkgName
	CompanionObjects map[string]*CompanionObjectMetadata // companion name -> metadata
	GoExports        map[string][]string                 // pkgName -> exported symbol names (from Go-only packages)
	GoTypeInfo       *GoTypeInfo                         // type info extracted from Go source files and packages
	TypeAliases      map[string]Type                     // type alias name -> underlying type (e.g., "Handler" -> func(Request) Future[Response])
	EmbedDirectives  []EmbedDirective                    // embed val declarations
	ImportPathMap    map[string]string                   // GALA import path -> actual Go module path (when they differ due to VCS host prefix)
	FilePath         string                              // source file path (for error reporting)
	SourceContent    string                              // raw source text (for error snippets)
	AnalysisWarnings []string                            // warnings from package analysis (e.g., unresolved GALA imports)
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
		if existing, ok := r.Types[k]; ok {
			// Merge methods so methods defined across multiple files
			// in the same package are all preserved.
			if existing.Methods == nil {
				existing.Methods = make(map[string]*MethodMetadata)
			}
			for methodName, methodMeta := range v.Methods {
				existing.Methods[methodName] = methodMeta
			}
			// Update fields if incoming has them and existing doesn't
			if len(v.FieldNames) > 0 && len(existing.FieldNames) == 0 {
				existing.Fields = v.Fields
				existing.FieldNames = v.FieldNames
				existing.ImmutFlags = v.ImmutFlags
			}
			if len(v.TypeParams) > 0 && len(existing.TypeParams) == 0 {
				existing.TypeParams = v.TypeParams
				existing.TypeParamConstraints = v.TypeParamConstraints
			}
			if v.IsSealed {
				existing.IsSealed = v.IsSealed
				existing.SealedVariants = v.SealedVariants
			}
		} else {
			r.Types[k] = v
		}
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
	if len(other.GoExports) > 0 {
		if r.GoExports == nil {
			r.GoExports = make(map[string][]string)
		}
		for pkg, symbols := range other.GoExports {
			r.GoExports[pkg] = append(r.GoExports[pkg], symbols...)
		}
	}
	if other.GoTypeInfo != nil {
		if r.GoTypeInfo == nil {
			r.GoTypeInfo = NewGoTypeInfo()
		}
		r.GoTypeInfo.Merge(other.GoTypeInfo)
	}
	if len(other.TypeAliases) > 0 {
		if r.TypeAliases == nil {
			r.TypeAliases = make(map[string]Type)
		}
		for k, v := range other.TypeAliases {
			r.TypeAliases[k] = v
		}
	}
	if len(other.ImportPathMap) > 0 {
		if r.ImportPathMap == nil {
			r.ImportPathMap = make(map[string]string)
		}
		for k, v := range other.ImportPathMap {
			r.ImportPathMap[k] = v
		}
	}
}

type TypeMetadata struct {
	Name                 string
	Package              string
	Methods              map[string]*MethodMetadata
	Fields               map[string]Type // Name -> Type
	FieldNames           []string        // To preserve order
	TypeParams           []string
	TypeParamConstraints map[string]string // TypeParam name -> constraint (e.g., "T" -> "comparable")
	ImmutFlags           []bool
	IsSealed             bool            // True if this type was generated from a sealed type declaration
	SealedVariants       []SealedVariant // Variant info for sealed types (empty for non-sealed)
	DefinedIn            string          // Source file where the type definition (fields/variants) was first seen
}

// SealedVariant holds metadata about a single case in a sealed type declaration.
type SealedVariant struct {
	Name       string
	FieldNames []string
	FieldTypes []Type
}

type MethodMetadata struct {
	Name         string
	Package      string
	ParamTypes   []Type
	ParamNames   []string         // Parameter names (for named argument matching)
	ReturnType   Type
	TypeParams   []string
	DefaultExprs map[int]string   // Param index -> default expression source text (nil = required)
	ReceiverName string           // Receiver parameter name (e.g., "s" in "func (s Server)") for default expr substitution
	IsGeneric    bool             // Force transformation to standalone function
	DefinedIn    string           // Source file where this method was defined (for redefinition detection)
}

type FunctionMetadata struct {
	Name          string
	Package       string
	ParamTypes    []Type
	ParamNames    []string         // Parameter names (for named argument matching and default injection)
	ReturnType    Type
	TypeParams    []string
	DefaultExprs  map[int]string   // Param index -> default expression source text (nil = required)
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
	prof := profiler.New(filePath)
	defer prof.Report()

	done := prof.Phase("parse")
	tree, err := t.parser.Parse(input)
	done()
	if err != nil {
		return "", err
	}

	done = prof.Phase("analyze")
	richAST, err := t.analyzer.Analyze(tree, filePath)
	done()
	if err != nil {
		return "", err
	}
	richAST.FilePath = filePath
	richAST.SourceContent = input

	done = prof.Phase("transform")
	fset, file, err := t.transformer.Transform(richAST)
	done()
	if err != nil {
		return "", err
	}

	done = prof.Phase("generate")
	code, err := t.generator.Generate(fset, file)
	done()
	if err != nil {
		return "", err
	}

	// Post-process: insert //go:embed directives before the matching var declarations.
	// The Go AST doesn't support attaching pragmas to synthetic nodes (position 0),
	// so we insert them as a string transformation on the formatted output.
	if len(richAST.EmbedDirectives) > 0 {
		code = insertEmbedDirectives(code, richAST.EmbedDirectives)
	}

	return code, nil
}

// insertEmbedDirectives inserts //go:embed comments before matching var declarations
// in the generated Go source code.
func insertEmbedDirectives(code string, directives []EmbedDirective) string {
	lines := strings.Split(code, "\n")
	var result []string

	// Build lookup: Go var name → embed directive
	lookup := make(map[string]*EmbedDirective)
	for i := range directives {
		lookup[directives[i].GoVar] = &directives[i]
	}

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		// Match "var <name> " or "var <name>\t" patterns
		for goVar, ed := range lookup {
			// Check for: "var _embed_x embed.FS" or "var readme string"
			prefix := "var " + goVar + " "
			prefixTab := "var " + goVar + "\t"
			if strings.HasPrefix(trimmed, prefix) || strings.HasPrefix(trimmed, prefixTab) {
				// Insert //go:embed directives (one per pattern)
				for _, pattern := range ed.Patterns {
					result = append(result, "//go:embed "+pattern)
				}
				delete(lookup, goVar)
				break
			}
		}

		result = append(result, line)
	}

	return strings.Join(result, "\n")
}
