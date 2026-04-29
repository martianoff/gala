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
	ImportAliases    map[string]string                   // alias -> pkgName (e.g., "im" -> "collection_immutable")
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
//
// Pure read on `other`. When a key collides we copy `existing` before
// adding fields/methods, so neither `r` nor `other` ever observes a
// `TypeMetadata` mutation made on behalf of a third RichAST. Without
// the copy-on-write, repeated cross-package merges mutate cached
// `TypeMetadata.Methods` maps in place — which (a) races across
// concurrent analyses and (b) inflates each on-disk cache entry with
// methods accumulated from every package that ever transited the
// shared pointer (gala_team's app/cli cache reached 4.3 GB before the
// fix landed).
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
		existing, ok := r.Types[k]
		if !ok {
			r.Types[k] = v
			continue
		}
		// Decide whether `existing` needs any change. If `v` brings no
		// new methods, fields, type-params, or sealed-variant info, the
		// existing pointer can stand as-is.
		methodsToAdd := 0
		for mn := range v.Methods {
			if _, present := existing.Methods[mn]; !present {
				methodsToAdd++
			}
		}
		fieldsNeeded := len(v.FieldNames) > 0 && len(existing.FieldNames) == 0
		typeParamsNeeded := len(v.TypeParams) > 0 && len(existing.TypeParams) == 0
		sealedNeeded := v.IsSealed && !existing.IsSealed
		if methodsToAdd == 0 && !fieldsNeeded && !typeParamsNeeded && !sealedNeeded {
			continue
		}
		// Copy-on-write: clone `existing` so we never mutate a pointer
		// that another RichAST (e.g. the analyzer's std cache) shares.
		copied := *existing
		if methodsToAdd > 0 {
			merged := make(map[string]*MethodMetadata, len(existing.Methods)+methodsToAdd)
			for mn, mm := range existing.Methods {
				merged[mn] = mm
			}
			for mn, mm := range v.Methods {
				if _, present := merged[mn]; !present {
					merged[mn] = mm
				}
			}
			copied.Methods = merged
		}
		if fieldsNeeded {
			copied.Fields = v.Fields
			copied.FieldNames = v.FieldNames
			copied.ImmutFlags = v.ImmutFlags
		}
		if typeParamsNeeded {
			copied.TypeParams = v.TypeParams
			copied.TypeParamConstraints = v.TypeParamConstraints
		}
		if sealedNeeded {
			copied.IsSealed = v.IsSealed
			copied.SealedVariants = v.SealedVariants
		}
		r.Types[k] = &copied
	}
	for k, v := range other.Functions {
		r.Functions[k] = v
	}
	for k, v := range other.Packages {
		r.Packages[k] = v
	}
	if len(other.ImportAliases) > 0 {
		if r.ImportAliases == nil {
			r.ImportAliases = make(map[string]string)
		}
		for k, v := range other.ImportAliases {
			r.ImportAliases[k] = v
		}
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

// OwnView returns a copy of `r` containing only entries that belong to
// `r.PackageName` — its own types, functions, companions, aliases, and
// the slice of GoTypeInfo entries keyed under this package's prefix.
// `Packages` and `ImportPathMap` are kept intact so consumers can
// recursively chase transitive imports.
//
// Use this at the analyzer cache boundary: the on-disk cache file then
// carries only the package's own metadata (KB-MB instead of tens of MB
// for a deeply-imported package), and consumers reconstruct the merged
// view at scan-time by walking `Packages`. The analyzer's working set
// scales O(N) in the number of packages instead of the previous O(N^2).
func (r *RichAST) OwnView() *RichAST {
	if r == nil {
		return nil
	}
	belongs := func(pkg string) bool {
		// An entry is "own" when its Package field matches the
		// RichAST's PackageName. The analyzer leaves Package empty
		// for a few primitives — keep those too so OwnView is a
		// no-op for entries the analyzer never tagged.
		return pkg == r.PackageName || pkg == ""
	}
	out := &RichAST{
		Tree:             r.Tree,
		PackageName:      r.PackageName,
		ImportAliases:    r.ImportAliases,
		EmbedDirectives:  r.EmbedDirectives,
		ImportPathMap:    r.ImportPathMap,
		FilePath:         r.FilePath,
		SourceContent:    r.SourceContent,
		AnalysisWarnings: r.AnalysisWarnings,
	}
	// GoExports is keyed by package name. Keep only this package's entries
	// (and any unkeyed/empty entries) so cached files don't duplicate
	// every transitive package's exports.
	if len(r.GoExports) > 0 {
		out.GoExports = make(map[string][]string)
		for pkg, syms := range r.GoExports {
			if pkg == r.PackageName || pkg == "" {
				out.GoExports[pkg] = syms
			}
		}
	}
	// GoTypeInfo entries are keyed "<pkg>.<Name>". Filter to this
	// package's prefix; consumers recover other packages' Go type info
	// by recursively merging cached entries from those packages.
	if r.GoTypeInfo != nil {
		out.GoTypeInfo = filterGoTypeInfoToPackage(r.GoTypeInfo, r.PackageName)
	}
	if len(r.Packages) > 0 {
		out.Packages = make(map[string]string, len(r.Packages))
		for k, v := range r.Packages {
			out.Packages[k] = v
		}
	}
	if len(r.Types) > 0 {
		out.Types = make(map[string]*TypeMetadata)
		for k, v := range r.Types {
			if v != nil && belongs(v.Package) {
				out.Types[k] = v
			}
		}
	}
	if len(r.Functions) > 0 {
		out.Functions = make(map[string]*FunctionMetadata)
		for k, v := range r.Functions {
			if v != nil && belongs(v.Package) {
				out.Functions[k] = v
			}
		}
	}
	if len(r.CompanionObjects) > 0 {
		out.CompanionObjects = make(map[string]*CompanionObjectMetadata)
		for k, v := range r.CompanionObjects {
			if v != nil && belongs(v.Package) {
				out.CompanionObjects[k] = v
			}
		}
	}
	if len(r.TypeAliases) > 0 {
		// TypeAliases values are Type, not metadata with a Package
		// field. They are package-local by construction (a sibling
		// `type X = ...` declaration) and only land in `r` when the
		// analyzer added them for `r.PackageName`, so keep them all.
		out.TypeAliases = make(map[string]Type, len(r.TypeAliases))
		for k, v := range r.TypeAliases {
			out.TypeAliases[k] = v
		}
	}
	return out
}

// filterGoTypeInfoToPackage returns a copy of `g` containing only the
// Functions/Types/Variables/Constants/TypeAliases keyed by entries
// whose qualified name starts with `pkgName.`. Used by RichAST.OwnView
// at the analyzer cache boundary; consumers reassemble the merged view
// by chasing transitive imports.
func filterGoTypeInfoToPackage(g *GoTypeInfo, pkgName string) *GoTypeInfo {
	if g == nil {
		return nil
	}
	prefix := pkgName + "."
	keep := func(k string) bool {
		// pkgName == "" or "main"/"test" packages may have empty prefix;
		// fall back to "no prefix filter" so the analyzer's invariants
		// don't change for those edge cases.
		if pkgName == "" || pkgName == "main" || pkgName == "test" {
			return true
		}
		return strings.HasPrefix(k, prefix)
	}
	out := NewGoTypeInfo()
	for k, v := range g.Functions {
		if keep(k) {
			out.Functions[k] = v
		}
	}
	for k, v := range g.Types {
		if keep(k) {
			out.Types[k] = v
		}
	}
	for k, v := range g.Variables {
		if keep(k) {
			out.Variables[k] = v
		}
	}
	for k, v := range g.Constants {
		if keep(k) {
			out.Constants[k] = v
		}
	}
	for k, v := range g.TypeAliases {
		if keep(k) {
			out.TypeAliases[k] = v
		}
	}
	return out
}

type TypeMetadata struct {
	Name                 string
	Package              string
	Pos                  SourcePos // Position of the type name identifier in DefinedIn
	Methods              map[string]*MethodMetadata
	Fields               map[string]Type // Name -> Type
	FieldNames           []string        // To preserve order
	FieldPositions       map[string]SourcePos // Name -> (line, column) of the field declaration identifier
	TypeParams           []string
	TypeParamConstraints map[string]string // TypeParam name -> constraint (e.g., "T" -> "comparable")
	ImmutFlags           []bool
	IsSealed             bool            // True if this type was generated from a sealed type declaration
	SealedVariants       []SealedVariant // Variant info for sealed types (empty for non-sealed)
	DefinedIn            string          // Source file where the type definition (fields/variants) was first seen
}

// SourcePos is a 1-based line, 0-based column (ANTLR convention) position in a source file.
type SourcePos struct {
	Line   int
	Column int
}

// antlrToken is the minimal subset of antlr.Token we need for position extraction.
// Declared locally so the transpiler package doesn't pull in the antlr dependency.
type antlrToken interface {
	GetLine() int
	GetColumn() int
}

// PosFromToken builds a SourcePos from any ANTLR token (or anything that
// exposes GetLine/GetColumn). Centralizes the Line/Column mapping so callers
// can't accidentally swap them or forget the 1-based/0-based convention.
func PosFromToken(tok antlrToken) SourcePos {
	if tok == nil {
		return SourcePos{}
	}
	return SourcePos{Line: tok.GetLine(), Column: tok.GetColumn()}
}

// SealedVariant holds metadata about a single case in a sealed type declaration.
type SealedVariant struct {
	Name       string
	Pos        SourcePos // Position of the variant identifier in the enclosing sealed type's DefinedIn
	FieldNames []string
	FieldTypes []Type
}

type MethodMetadata struct {
	Name         string
	Package      string
	Pos          SourcePos // Position of the method name identifier in DefinedIn
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
	Pos           SourcePos // Position of the function name identifier in DefinedIn
	ParamTypes    []Type
	ParamNames    []string         // Parameter names (for named argument matching and default injection)
	ReturnType    Type
	TypeParams    []string
	DefaultExprs  map[int]string   // Param index -> default expression source text (nil = required)
	DefinedIn     string           // Source file where this function was defined
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
	// ParseLenient always returns ANTLR's error-recovered tree alongside any
	// syntax errors. The tree may contain error nodes but is structurally
	// valid enough for the analyzer to extract types, methods, and functions.
	// Used by the LSP so completion/hover/definition keep working while the
	// user is mid-edit.
	ParseLenient(input string) (tree antlr.Tree, errors []error)
}

// Analyzer analyzes a Gala ANTLR parse tree and produces a RichAST.
type Analyzer interface {
	Analyze(tree antlr.Tree, filePath string) (*RichAST, error)
}

// TransformResult holds the output of a GALA-to-Go AST transformation.
type TransformResult struct {
	Fset *token.FileSet
	File *ast.File
	// VarTypes maps variable names to their resolved GALA types.
	// Populated from the transformer's scope after transformation.
	// Used by the LSP server for inlay hints and type-aware completion.
	VarTypes map[string]Type
	// LambdaParamHints lists lambda parameter positions whose types were
	// inferred from the expected call-site type. Used by the LSP for
	// reliable inlay hint placement without text-based parsing.
	LambdaParamHints []LambdaParamHint
}

// LambdaParamHint records a single lambda parameter whose type was inferred
// by the transformer. Line is 1-based, Column is 0-based (ANTLR convention),
// pointing at the start of the parameter identifier.
type LambdaParamHint struct {
	Line   int
	Column int
	Name   string
	Type   Type
}

// ASTTransformer transforms a Gala RichAST into a Go AST file and its FileSet.
type ASTTransformer interface {
	Transform(richAST *RichAST) (*token.FileSet, *ast.File, error)
	// TransformForLSP is like Transform but also returns resolved variable types.
	TransformForLSP(richAST *RichAST) (*TransformResult, error)
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
