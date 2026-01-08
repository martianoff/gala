package transformer

import (
	"fmt"
	"go/ast"
	"go/token"
	"martianoff/gala/galaerr"
	"martianoff/gala/internal/parser/grammar"
	"martianoff/gala/internal/transpiler"
	"strings"
)

type galaASTTransformer struct {
	currentScope         *scope
	packageName          string
	immutFields          map[string]bool
	structImmutFields    map[string][]bool
	needsStdImport       bool
	needsFmtImport       bool
	activeTypeParams     map[string]bool
	structFields         map[string][]string
	structFieldTypes     map[string]map[string]string // structName -> fieldName -> typeName
	genericMethods       map[string]map[string]bool   // receiverType -> methodName -> isGeneric
	functions            map[string]*transpiler.FunctionMetadata
	typeMetas            map[string]*transpiler.TypeMetadata
	imports              map[string]string // alias or pkgName -> package path
	importAliases        map[string]string // alias -> actual pkgName
	reverseImportAliases map[string]string // actual pkgName -> alias
	dotImports           []string          // package names
	tempVarCount         int
}

// NewGalaASTTransformer creates a new instance of ASTTransformer for GALA.
func NewGalaASTTransformer() transpiler.ASTTransformer {
	return &galaASTTransformer{
		immutFields:          make(map[string]bool),
		structImmutFields:    make(map[string][]bool),
		activeTypeParams:     make(map[string]bool),
		structFields:         make(map[string][]string),
		structFieldTypes:     make(map[string]map[string]string),
		genericMethods:       make(map[string]map[string]bool),
		functions:            make(map[string]*transpiler.FunctionMetadata),
		typeMetas:            make(map[string]*transpiler.TypeMetadata),
		imports:              make(map[string]string),
		importAliases:        make(map[string]string),
		reverseImportAliases: make(map[string]string),
		dotImports:           make([]string, 0),
	}
}

func (t *galaASTTransformer) Transform(richAST *transpiler.RichAST) (*token.FileSet, *ast.File, error) {
	tree := richAST.Tree
	t.currentScope = nil
	t.needsStdImport = false
	t.needsFmtImport = false
	t.immutFields = make(map[string]bool)
	t.structImmutFields = make(map[string][]bool)
	t.activeTypeParams = make(map[string]bool)
	t.structFields = make(map[string][]string)
	t.structFieldTypes = make(map[string]map[string]string)
	t.genericMethods = make(map[string]map[string]bool)
	t.functions = richAST.Functions
	t.typeMetas = richAST.Types
	t.imports = make(map[string]string)
	t.importAliases = make(map[string]string)
	t.reverseImportAliases = make(map[string]string)
	t.dotImports = make([]string, 0)
	t.tempVarCount = 0

	t.imports[transpiler.StdPackage] = transpiler.StdImportPath
	t.importAliases[transpiler.StdPackage] = transpiler.StdPackage
	t.reverseImportAliases[transpiler.StdPackage] = transpiler.StdPackage

	// Populate importAliases from richAST.Packages
	// We'll do this in Transform after we have the imports from the current file.

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

	fset := token.NewFileSet()
	sourceFile, ok := any(tree).(*grammar.SourceFileContext)
	if !ok {
		return nil, nil, galaerr.NewSemanticError(fmt.Sprintf("expected *grammar.SourceFileContext, got %T", tree))
	}

	pkgName := sourceFile.PackageClause().(*grammar.PackageClauseContext).Identifier().GetText()
	t.packageName = pkgName
	file := &ast.File{
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

	for alias, path := range t.imports {
		if actualPkgName, ok := richAST.Packages[path]; ok {
			t.importAliases[alias] = actualPkgName
			t.reverseImportAliases[actualPkgName] = alias
		} else {
			parts := strings.Split(path, "/")
			pkg := parts[len(parts)-1]
			t.importAliases[alias] = pkg
			t.reverseImportAliases[pkg] = alias
		}
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

	if t.needsStdImport && t.packageName != transpiler.StdPackage {
		// Add import at the beginning
		importDecl := &ast.GenDecl{
			Tok: token.IMPORT,
			Specs: []ast.Spec{
				&ast.ImportSpec{
					Path: &ast.BasicLit{
						Kind:  token.STRING,
						Value: fmt.Sprintf("\"%s\"", transpiler.StdImportPath),
					},
				},
			},
		}
		file.Decls = append([]ast.Decl{importDecl}, file.Decls...)
	}

	if t.needsFmtImport {
		hasFmt := false
		for _, path := range t.imports {
			if path == "fmt" {
				hasFmt = true
				break
			}
		}

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
