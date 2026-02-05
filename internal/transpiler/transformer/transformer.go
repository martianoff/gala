package transformer

import (
	"fmt"
	"go/ast"
	"go/token"
	"martianoff/gala/galaerr"
	"martianoff/gala/internal/parser/grammar"
	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/infer"
	"martianoff/gala/internal/transpiler/registry"
	"strings"
)

type galaASTTransformer struct {
	currentScope      *scope
	packageName       string
	immutFields       map[string]bool
	structImmutFields map[string][]bool
	needsStdImport    bool
	needsFmtImport    bool
	activeTypeParams  map[string]bool
	structFields      map[string][]string
	structFieldTypes  map[string]map[string]transpiler.Type // structName -> fieldName -> typeName
	genericMethods    map[string]map[string]bool            // receiverType -> methodName -> isGeneric
	functions         map[string]*transpiler.FunctionMetadata
	typeMetas         map[string]*transpiler.TypeMetadata
	companionObjects  map[string]*transpiler.CompanionObjectMetadata // companion name -> metadata
	importManager     *ImportManager                                 // unified import tracking
	tempVarCount      int
	inferer           *infer.Inferer
}

// NewGalaASTTransformer creates a new instance of ASTTransformer for GALA.
func NewGalaASTTransformer() transpiler.ASTTransformer {
	return &galaASTTransformer{
		immutFields:       make(map[string]bool),
		structImmutFields: make(map[string][]bool),
		activeTypeParams:  make(map[string]bool),
		structFields:      make(map[string][]string),
		structFieldTypes:  make(map[string]map[string]transpiler.Type),
		genericMethods:    make(map[string]map[string]bool),
		functions:         make(map[string]*transpiler.FunctionMetadata),
		typeMetas:         make(map[string]*transpiler.TypeMetadata),
		companionObjects:  make(map[string]*transpiler.CompanionObjectMetadata),
		importManager:     NewImportManager(),
		inferer:           infer.NewInferer(),
	}
}

func (t *galaASTTransformer) Transform(richAST *transpiler.RichAST) (fset *token.FileSet, file *ast.File, err error) {
	defer func() {
		if r := recover(); r != nil {
			if semErr, ok := r.(*galaerr.SemanticError); ok {
				err = semErr
			} else {
				panic(r)
			}
		}
	}()
	tree := richAST.Tree
	t.currentScope = nil
	t.needsStdImport = false
	t.needsFmtImport = false
	t.immutFields = make(map[string]bool)
	t.structImmutFields = make(map[string][]bool)
	t.activeTypeParams = make(map[string]bool)
	t.structFields = make(map[string][]string)
	t.structFieldTypes = make(map[string]map[string]transpiler.Type)
	t.genericMethods = make(map[string]map[string]bool)
	t.functions = richAST.Functions
	t.typeMetas = richAST.Types
	t.companionObjects = richAST.CompanionObjects
	if t.companionObjects == nil {
		t.companionObjects = make(map[string]*transpiler.CompanionObjectMetadata)
	}
	t.importManager = NewImportManager()
	t.tempVarCount = 0

	// Populate imports from richAST.Packages (includes implicit std import from analyzer)
	t.importManager.AddFromPackages(richAST.Packages)

	// Populate metadata from RichAST
	for typeName, meta := range richAST.Types {
		t.structFieldTypes[typeName] = meta.Fields
		t.structFields[typeName] = meta.FieldNames
		t.structImmutFields[typeName] = meta.ImmutFlags
		if _, ok := t.genericMethods[typeName]; !ok {
			t.genericMethods[typeName] = make(map[string]bool)
		}
		for methodName, methodMeta := range meta.Methods {
			if len(methodMeta.TypeParams) > 0 || methodMeta.IsGeneric {
				t.genericMethods[typeName][methodName] = true
			}
		}
	}

	t.pushScope() // Global scope
	defer t.popScope()

	fset = token.NewFileSet()
	sourceFile, ok := any(tree).(*grammar.SourceFileContext)
	if !ok {
		return nil, nil, galaerr.NewSemanticError(fmt.Sprintf("expected *grammar.SourceFileContext, got %T", tree))
	}

	pkgName := sourceFile.PackageClause().(*grammar.PackageClauseContext).Identifier().GetText()
	t.packageName = pkgName
	file = &ast.File{
		Name: ast.NewIdent(pkgName),
	}

	// Imports
	for _, importCtx := range sourceFile.AllImportDeclaration() {
		decl, err := t.transformImportDeclaration(importCtx.(*grammar.ImportDeclarationContext))
		if err != nil {
			return nil, nil, err
		}
		file.Decls = append(file.Decls, decl)
	}

	// Update actual package names from richAST.Packages for better type resolution
	for path, actualPkgName := range richAST.Packages {
		t.importManager.UpdateActualPackageName(path, actualPkgName)
	}

	for _, topDeclCtx := range sourceFile.AllTopLevelDeclaration() {
		decls, err := t.transformTopLevelDeclaration(topDeclCtx)
		if err != nil {
			return nil, nil, err
		}
		if decls != nil {
			file.Decls = append(file.Decls, decls...)
		}
	}

	if t.needsStdImport && t.packageName != registry.StdPackageName {
		// Check if std is already imported (e.g., as a dot import)
		stdAlreadyImported := t.importManager.IsDotImported(registry.StdPackageName)
		if !stdAlreadyImported {
			// Add import at the beginning
			importDecl := &ast.GenDecl{
				Tok: token.IMPORT,
				Specs: []ast.Spec{
					&ast.ImportSpec{
						Path: &ast.BasicLit{
							Kind:  token.STRING,
							Value: fmt.Sprintf("\"%s\"", registry.StdImportPath),
						},
					},
				},
			}
			file.Decls = append([]ast.Decl{importDecl}, file.Decls...)
		}
	}

	if t.needsFmtImport {
		_, hasFmt := t.importManager.GetByPath("fmt")

		if !hasFmt {
			importDecl := &ast.GenDecl{
				Tok: token.IMPORT,
				Specs: []ast.Spec{
					&ast.ImportSpec{
						Path: &ast.BasicLit{
							Kind:  token.STRING,
							Value: "\"fmt\"",
						},
					},
				},
			}
			// If std was added, it's at index 0. We want fmt to be there too.
			file.Decls = append([]ast.Decl{importDecl}, file.Decls...)
		}
	}

	return fset, file, nil
}

var _ transpiler.ASTTransformer = (*galaASTTransformer)(nil)

// resolveTypeName is a unified type resolution function that searches for a type name
// using a consistent resolution order. It takes a check function to determine if a
// candidate name exists in the target data structure.
//
// Resolution Order (documented and consistent):
//  1. Exact match
//  2. If name has package prefix: try replacing prefix with std/current/imported packages
//  3. Try current package prefix
//  4. Try std package prefix
//  5. Try all explicitly imported packages (non-dot)
//  6. Try dot-imported packages
//
// Returns the resolved name and whether resolution succeeded.
func (t *galaASTTransformer) resolveTypeName(typeName string, exists func(string) bool) (string, bool) {
	// 1. Try exact match first
	if exists(typeName) {
		return typeName, true
	}

	// 2. If typeName has a package prefix, extract the simple name and try other packages
	if idx := strings.LastIndex(typeName, "."); idx != -1 {
		simpleName := typeName[idx+1:]
		if resolved, found := t.tryResolveSimpleName(simpleName, exists); found {
			return resolved, true
		}
	}

	// 3. Try resolving the original typeName through all package prefixes
	if resolved, found := t.tryResolveSimpleName(typeName, exists); found {
		return resolved, true
	}

	return "", false
}

// tryResolveSimpleName attempts to resolve a simple (unqualified) type name
// by trying various package prefixes in order of precedence.
func (t *galaASTTransformer) tryResolveSimpleName(name string, exists func(string) bool) (string, bool) {
	// Try simple name without any package prefix first
	if exists(name) {
		return name, true
	}

	// Try std package for standard library types like Tuple, Option, etc.
	if stdName := registry.StdPackageName + "." + name; exists(stdName) {
		return stdName, true
	}

	// Try current package
	if t.packageName != "" {
		if fullName := t.packageName + "." + name; exists(fullName) {
			return fullName, true
		}
	}

	// Try imported packages (non-dot first, then dot imports)
	for _, entry := range t.importManager.All() {
		if entry.IsDot {
			continue
		}
		if fullName := entry.PkgName + "." + name; exists(fullName) {
			return fullName, true
		}
	}

	for _, entry := range t.importManager.All() {
		if !entry.IsDot {
			continue
		}
		if fullName := entry.PkgName + "." + name; exists(fullName) {
			return fullName, true
		}
	}

	return "", false
}

// resolveStructTypeName resolves a type name to the key used in structFields/structImmutFields maps.
// Returns the original typeName if not found (for backward compatibility).
func (t *galaASTTransformer) resolveStructTypeName(typeName string) string {
	resolved, found := t.resolveTypeName(typeName, func(name string) bool {
		_, ok := t.structFields[name]
		return ok
	})
	if found {
		return resolved
	}
	return typeName
}

// resolveTypeMetaName resolves a type name to the key used in typeMetas map.
// Returns empty string if not found.
func (t *galaASTTransformer) resolveTypeMetaName(typeName string) string {
	resolved, _ := t.resolveTypeName(typeName, func(name string) bool {
		_, ok := t.typeMetas[name]
		return ok
	})
	return resolved
}

// getTypeMeta resolves a type name and returns the corresponding TypeMetadata.
// This is the preferred method for accessing type metadata - it handles all
// resolution scenarios including package prefixes, std library fallback, and imports.
//
// Resolution precedence:
//  1. Exact match
//  2. std package prefix (for standard library types)
//  3. Current package prefix
//  4. Explicitly imported packages
//  5. Dot-imported packages
//
// Returns nil if the type is not found.
func (t *galaASTTransformer) getTypeMeta(typeName string) *transpiler.TypeMetadata {
	resolved := t.resolveTypeMetaName(typeName)
	if resolved == "" {
		return nil
	}
	return t.typeMetas[resolved]
}
