package transformer

import (
	"fmt"
	"go/ast"
	"go/token"
	"martianoff/gala/galaerr"
	"martianoff/gala/internal/parser/grammar"
	"martianoff/gala/internal/transpiler"
)

type galaASTTransformer struct {
	currentScope      *scope
	packageName       string
	immutFields       map[string]bool
	structImmutFields map[string][]bool
	needsStdImport    bool
	activeTypeParams  map[string]bool
	structFields      map[string][]string
	structFieldTypes  map[string]map[string]string // structName -> fieldName -> typeName
	genericMethods    map[string]map[string]bool   // receiverType -> methodName -> isGeneric
	functions         map[string]*transpiler.FunctionMetadata
	typeMetas         map[string]*transpiler.TypeMetadata
	tempVarCount      int
}

// NewGalaASTTransformer creates a new instance of ASTTransformer for GALA.
func NewGalaASTTransformer() transpiler.ASTTransformer {
	return &galaASTTransformer{
		immutFields:       make(map[string]bool),
		structImmutFields: make(map[string][]bool),
		activeTypeParams:  make(map[string]bool),
		structFields:      make(map[string][]string),
		structFieldTypes:  make(map[string]map[string]string),
		genericMethods:    make(map[string]map[string]bool),
		functions:         make(map[string]*transpiler.FunctionMetadata),
		typeMetas:         make(map[string]*transpiler.TypeMetadata),
	}
}

func (t *galaASTTransformer) Transform(richAST *transpiler.RichAST) (*token.FileSet, *ast.File, error) {
	tree := richAST.Tree
	t.currentScope = nil
	t.needsStdImport = false
	t.immutFields = make(map[string]bool)
	t.structImmutFields = make(map[string][]bool)
	t.activeTypeParams = make(map[string]bool)
	t.structFields = make(map[string][]string)
	t.structFieldTypes = make(map[string]map[string]string)
	t.genericMethods = make(map[string]map[string]bool)
	t.functions = richAST.Functions
	t.typeMetas = richAST.Types
	t.tempVarCount = 0

	// Populate metadata from RichAST
	for typeName, meta := range richAST.Types {
		t.structFieldTypes[typeName] = meta.Fields
		t.structFields[typeName] = meta.FieldNames
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
	sourceFile, ok := tree.(*grammar.SourceFileContext)
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

	return fset, file, nil
}

var _ transpiler.ASTTransformer = (*galaASTTransformer)(nil)
