package transformer

import (
	"fmt"
	"go/ast"
	"go/token"
	"martianoff/gala/galaerr"
	"martianoff/gala/internal/parser/grammar"
	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/infer"
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
	structFieldTypes     map[string]map[string]transpiler.Type // structName -> fieldName -> typeName
	genericMethods       map[string]map[string]bool            // receiverType -> methodName -> isGeneric
	functions            map[string]*transpiler.FunctionMetadata
	typeMetas            map[string]*transpiler.TypeMetadata
	companionObjects     map[string]*transpiler.CompanionObjectMetadata // companion name -> metadata
	imports              map[string]string                              // alias or pkgName -> package path
	importAliases        map[string]string                              // alias -> actual pkgName
	reverseImportAliases map[string]string                              // actual pkgName -> alias
	dotImports           []string                                       // package names
	tempVarCount         int
	inferer              *infer.Inferer
}

// NewGalaASTTransformer creates a new instance of ASTTransformer for GALA.
func NewGalaASTTransformer() transpiler.ASTTransformer {
	return &galaASTTransformer{
		immutFields:          make(map[string]bool),
		structImmutFields:    make(map[string][]bool),
		activeTypeParams:     make(map[string]bool),
		structFields:         make(map[string][]string),
		structFieldTypes:     make(map[string]map[string]transpiler.Type),
		genericMethods:       make(map[string]map[string]bool),
		functions:            make(map[string]*transpiler.FunctionMetadata),
		typeMetas:            make(map[string]*transpiler.TypeMetadata),
		companionObjects:     make(map[string]*transpiler.CompanionObjectMetadata),
		imports:              make(map[string]string),
		importAliases:        make(map[string]string),
		reverseImportAliases: make(map[string]string),
		dotImports:           make([]string, 0),
		inferer:              infer.NewInferer(),
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
	t.imports = make(map[string]string)
	t.importAliases = make(map[string]string)
	t.reverseImportAliases = make(map[string]string)
	t.dotImports = make([]string, 0)
	t.tempVarCount = 0

	// Populate imports from richAST.Packages (includes implicit std import from analyzer)
	for path, pkgName := range richAST.Packages {
		t.imports[pkgName] = path
		t.importAliases[pkgName] = pkgName
		t.reverseImportAliases[pkgName] = pkgName
	}

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

	for alias, path := range t.imports {
		if actualPkgName, ok := richAST.Packages[path]; ok {
			t.importAliases[alias] = actualPkgName
			// Only update reverseImportAliases if alias is different from actualPkgName
			// This ensures explicit import aliases (like libalias "pkg/lib") take precedence
			// over implicit aliases set from richAST.Packages
			if alias != actualPkgName {
				t.reverseImportAliases[actualPkgName] = alias
			} else if _, exists := t.reverseImportAliases[actualPkgName]; !exists {
				// Only set if no explicit alias exists
				t.reverseImportAliases[actualPkgName] = alias
			}
		} else {
			parts := strings.Split(path, "/")
			pkg := parts[len(parts)-1]
			t.importAliases[alias] = pkg
			if alias != pkg {
				t.reverseImportAliases[pkg] = alias
			} else if _, exists := t.reverseImportAliases[pkg]; !exists {
				t.reverseImportAliases[pkg] = alias
			}
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
		// Check if std is already imported (e.g., as a dot import)
		stdAlreadyImported := false
		for _, dotPkg := range t.dotImports {
			if dotPkg == transpiler.StdPackage {
				stdAlreadyImported = true
				break
			}
		}
		if !stdAlreadyImported {
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

// resolveStructTypeName tries to resolve a type name to the key used in structFields/structImmutFields maps.
// The maps are keyed by fully qualified names (e.g., "collection_immutable.List", "std.Tuple"),
// but we may receive unqualified names (e.g., "List", "Tuple").
func (t *galaASTTransformer) resolveStructTypeName(typeName string) string {
	// 1. Try exact match first
	if _, ok := t.structFields[typeName]; ok {
		return typeName
	}

	// 2. If typeName has a package prefix, extract the simple name and try other packages
	if idx := strings.LastIndex(typeName, "."); idx != -1 {
		simpleName := typeName[idx+1:]
		// Try std package for standard library types like Tuple, Option, etc.
		stdName := transpiler.StdPackage + "." + simpleName
		if _, ok := t.structFields[stdName]; ok {
			return stdName
		}
		// Try current package
		if t.packageName != "" {
			fullName := t.packageName + "." + simpleName
			if _, ok := t.structFields[fullName]; ok {
				return fullName
			}
		}
		// Try imported packages
		for alias := range t.imports {
			actualPkg := alias
			if actual, ok := t.importAliases[alias]; ok {
				actualPkg = actual
			}
			fullName := actualPkg + "." + simpleName
			if _, ok := t.structFields[fullName]; ok {
				return fullName
			}
		}
	}

	// 3. Try current package prefix (including "main")
	if t.packageName != "" {
		fullName := t.packageName + "." + typeName
		if _, ok := t.structFields[fullName]; ok {
			return fullName
		}
	}

	// 4. Try std package prefix
	stdName := transpiler.StdPackage + "." + typeName
	if _, ok := t.structFields[stdName]; ok {
		return stdName
	}

	// 5. Try all imported packages
	for alias := range t.imports {
		actualPkg := alias
		if actual, ok := t.importAliases[alias]; ok {
			actualPkg = actual
		}
		fullName := actualPkg + "." + typeName
		if _, ok := t.structFields[fullName]; ok {
			return fullName
		}
	}

	// Return original if not found
	return typeName
}

// resolveTypeMetaName tries to resolve a type name to the key used in typeMetas map.
// Similar to resolveStructTypeName but for typeMetas.
func (t *galaASTTransformer) resolveTypeMetaName(typeName string) string {
	// 1. Try exact match first
	if _, ok := t.typeMetas[typeName]; ok {
		return typeName
	}

	// 2. If typeName has a package prefix, extract the simple name and try other packages
	if idx := strings.LastIndex(typeName, "."); idx != -1 {
		simpleName := typeName[idx+1:]
		// Try std package for standard library types like Tuple, Option, etc.
		stdName := transpiler.StdPackage + "." + simpleName
		if _, ok := t.typeMetas[stdName]; ok {
			return stdName
		}
		// Try current package
		if t.packageName != "" {
			fullName := t.packageName + "." + simpleName
			if _, ok := t.typeMetas[fullName]; ok {
				return fullName
			}
		}
		// Try imported packages
		for alias := range t.imports {
			actualPkg := alias
			if actual, ok := t.importAliases[alias]; ok {
				actualPkg = actual
			}
			fullName := actualPkg + "." + simpleName
			if _, ok := t.typeMetas[fullName]; ok {
				return fullName
			}
		}
	}

	// 3. Try current package prefix (including "main")
	if t.packageName != "" {
		fullName := t.packageName + "." + typeName
		if _, ok := t.typeMetas[fullName]; ok {
			return fullName
		}
	}

	// 4. Try std package prefix
	stdName := transpiler.StdPackage + "." + typeName
	if _, ok := t.typeMetas[stdName]; ok {
		return stdName
	}

	// 5. Try all imported packages
	for alias := range t.imports {
		actualPkg := alias
		if actual, ok := t.importAliases[alias]; ok {
			actualPkg = actual
		}
		fullName := actualPkg + "." + typeName
		if _, ok := t.typeMetas[fullName]; ok {
			return fullName
		}
	}

	// 6. Try dot imports
	for _, dotPkg := range t.dotImports {
		fullName := dotPkg + "." + typeName
		if _, ok := t.typeMetas[fullName]; ok {
			return fullName
		}
	}

	// Return empty string if not found
	return ""
}
