package transformer

import (
	"fmt"
	"go/ast"
	"go/token"
	"martianoff/gala/galaerr"
	"martianoff/gala/internal/parser/grammar"
	"martianoff/gala/internal/transpiler"

	"github.com/antlr4-go/antlr/v4"
)

type scope struct {
	vals     map[string]bool
	valTypes map[string]string
	parent   *scope
}

type galaASTTransformer struct {
	currentScope      *scope
	immutFields       map[string]bool
	structImmutFields map[string][]bool
	needsStdImport    bool
	activeTypeParams  map[string]bool
	structFields      map[string][]string
}

func (t *galaASTTransformer) pushScope() {
	t.currentScope = &scope{
		vals:     make(map[string]bool),
		valTypes: make(map[string]string),
		parent:   t.currentScope,
	}
}

func (t *galaASTTransformer) popScope() {
	if t.currentScope != nil {
		t.currentScope = t.currentScope.parent
	}
}

func (t *galaASTTransformer) addVal(name string, typeName string) {
	if t.currentScope != nil {
		t.currentScope.vals[name] = true
		t.currentScope.valTypes[name] = typeName
	}
}

func (t *galaASTTransformer) addVar(name string, typeName string) {
	if t.currentScope != nil {
		t.currentScope.vals[name] = false
		t.currentScope.valTypes[name] = typeName
	}
}

func (t *galaASTTransformer) getType(name string) string {
	s := t.currentScope
	for s != nil {
		if typeName, ok := s.valTypes[name]; ok {
			return typeName
		}
		s = s.parent
	}
	return ""
}

func (t *galaASTTransformer) transformCopyCall(receiver ast.Expr, argListCtx *grammar.ArgumentListContext) (ast.Expr, error) {
	// 1. Identify receiver type
	var typeName string
	if id, ok := receiver.(*ast.Ident); ok {
		typeName = t.getType(id.Name)
	} else if call, ok := receiver.(*ast.CallExpr); ok {
		// Handle p.Get().Copy() case where p is std.Immutable[T]
		if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "Get" {
			if id, ok := sel.X.(*ast.Ident); ok {
				typeName = t.getType(id.Name)
			}
		}
	}

	if typeName == "" {
		// If we can't find the type, we might still be able to proceed if it's a direct struct literal copy,
		// but GALA seems to prefer explicit types for Copy overrides.
		// For now, let's assume it's required to know the type for overrides.
		// If no overrides, we just call the regular Copy() method.
		if argListCtx == nil || len(argListCtx.AllArgument()) == 0 {
			return &ast.CallExpr{
				Fun: &ast.SelectorExpr{
					X:   receiver,
					Sel: ast.NewIdent("Copy"),
				},
			}, nil
		}
		// If there are overrides, we need the type.
		return nil, galaerr.NewSemanticError("cannot use Copy overrides: type of receiver unknown")
	}

	fields, ok := t.structFields[typeName]
	if !ok {
		// If it's not a struct type but we have overrides, compilation error
		if len(argListCtx.AllArgument()) > 0 {
			for _, argCtx := range argListCtx.AllArgument() {
				arg := argCtx.(*grammar.ArgumentContext)
				if arg.Identifier() != nil {
					return nil, galaerr.NewSemanticError("Copy overrides only supported for struct types")
				}
			}
		}
		// Fallback to regular Copy() call if no named overrides (though the grammar allows them now)
		return &ast.CallExpr{
			Fun: &ast.SelectorExpr{
				X:   receiver,
				Sel: ast.NewIdent("Copy"),
			},
		}, nil
	}

	// 2. Parse overrides
	overrides := make(map[string]ast.Expr)
	for _, argCtx := range argListCtx.AllArgument() {
		arg := argCtx.(*grammar.ArgumentContext)
		if arg.Identifier() == nil {
			return nil, galaerr.NewSemanticError("Copy overrides must be named: Copy(field = value)")
		}
		fieldName := arg.Identifier().GetText()
		found := false
		for _, f := range fields {
			if f == fieldName {
				found = true
				break
			}
		}
		if !found {
			return nil, galaerr.NewSemanticError(fmt.Sprintf("struct %s has no field %s", typeName, fieldName))
		}
		val, err := t.transformExpression(arg.Expression())
		if err != nil {
			return nil, err
		}
		overrides[fieldName] = val
	}

	// 3. Construct new struct instance
	var elts []ast.Expr
	immutFlags := t.structImmutFields[typeName]
	for i, fn := range fields {
		if val, ok := overrides[fn]; ok {
			finalVal := val
			if i < len(immutFlags) && immutFlags[i] {
				t.needsStdImport = true
				finalVal = &ast.CallExpr{
					Fun: &ast.SelectorExpr{
						X:   ast.NewIdent("std"),
						Sel: ast.NewIdent("NewImmutable"),
					},
					Args: []ast.Expr{val},
				}
			}
			elts = append(elts, &ast.KeyValueExpr{
				Key:   ast.NewIdent(fn),
				Value: finalVal,
			})
		} else {
			elts = append(elts, &ast.KeyValueExpr{
				Key: ast.NewIdent(fn),
				Value: &ast.CallExpr{
					Fun: &ast.SelectorExpr{
						X:   ast.NewIdent("std"),
						Sel: ast.NewIdent("Copy"),
					},
					Args: []ast.Expr{
						&ast.SelectorExpr{
							X:   receiver,
							Sel: ast.NewIdent(fn),
						},
					},
				},
			})
		}
	}

	return &ast.CompositeLit{
		Type: ast.NewIdent(typeName),
		Elts: elts,
	}, nil
}

func (t *galaASTTransformer) isVal(name string) bool {
	s := t.currentScope
	for s != nil {
		if isImmutable, ok := s.vals[name]; ok {
			return isImmutable
		}
		s = s.parent
	}
	return false
}

// NewGalaASTTransformer creates a new instance of ASTTransformer for GALA.
func NewGalaASTTransformer() transpiler.ASTTransformer {
	return &galaASTTransformer{
		immutFields:       make(map[string]bool),
		structImmutFields: make(map[string][]bool),
		activeTypeParams:  make(map[string]bool),
		structFields:      make(map[string][]string),
	}
}

func (t *galaASTTransformer) Transform(tree antlr.Tree) (*token.FileSet, *ast.File, error) {
	t.currentScope = nil
	t.needsStdImport = false
	t.immutFields = make(map[string]bool)
	t.structImmutFields = make(map[string][]bool)
	t.activeTypeParams = make(map[string]bool)
	t.structFields = make(map[string][]string)
	t.pushScope() // Global scope
	defer t.popScope()

	fset := token.NewFileSet()
	sourceFile, ok := tree.(*grammar.SourceFileContext)
	if !ok {
		return nil, nil, galaerr.NewSemanticError(fmt.Sprintf("expected *grammar.SourceFileContext, got %T", tree))
	}

	pkgName := sourceFile.PackageClause().(*grammar.PackageClauseContext).Identifier().GetText()
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

	if t.needsStdImport {
		// Add import at the beginning
		importDecl := &ast.GenDecl{
			Tok: token.IMPORT,
			Specs: []ast.Spec{
				&ast.ImportSpec{
					Path: &ast.BasicLit{
						Kind:  token.STRING,
						Value: "\"martianoff/gala/std\"",
					},
				},
			},
		}
		file.Decls = append([]ast.Decl{importDecl}, file.Decls...)
	}

	return fset, file, nil
}

func (t *galaASTTransformer) transformTopLevelDeclaration(ctx grammar.ITopLevelDeclarationContext) ([]ast.Decl, error) {
	if valCtx := ctx.ValDeclaration(); valCtx != nil {
		decl, err := t.transformValDeclaration(valCtx.(*grammar.ValDeclarationContext))
		if err != nil {
			return nil, err
		}
		return []ast.Decl{decl}, nil
	}
	if varCtx := ctx.VarDeclaration(); varCtx != nil {
		decl, err := t.transformVarDeclaration(varCtx.(*grammar.VarDeclarationContext))
		if err != nil {
			return nil, err
		}
		return []ast.Decl{decl}, nil
	}
	if funcCtx := ctx.FunctionDeclaration(); funcCtx != nil {
		decl, err := t.transformFunctionDeclaration(funcCtx.(*grammar.FunctionDeclarationContext))
		if err != nil {
			return nil, err
		}
		return []ast.Decl{decl}, nil
	}
	if typeCtx := ctx.TypeDeclaration(); typeCtx != nil {
		return t.transformTypeDeclaration(typeCtx.(*grammar.TypeDeclarationContext))
	}
	if structShorthandCtx := ctx.StructShorthandDeclaration(); structShorthandCtx != nil {
		return t.transformStructShorthandDeclaration(structShorthandCtx.(*grammar.StructShorthandDeclarationContext))
	}
	return nil, nil
}

func (t *galaASTTransformer) transformDeclaration(ctx grammar.IDeclarationContext) (ast.Decl, ast.Stmt, error) {
	if valCtx := ctx.ValDeclaration(); valCtx != nil {
		decl, err := t.transformValDeclaration(valCtx.(*grammar.ValDeclarationContext))
		return decl, nil, err
	}
	if varCtx := ctx.VarDeclaration(); varCtx != nil {
		decl, err := t.transformVarDeclaration(varCtx.(*grammar.VarDeclarationContext))
		return decl, nil, err
	}
	if funcCtx := ctx.FunctionDeclaration(); funcCtx != nil {
		decl, err := t.transformFunctionDeclaration(funcCtx.(*grammar.FunctionDeclarationContext))
		return decl, nil, err
	}
	if typeCtx := ctx.TypeDeclaration(); typeCtx != nil {
		decls, err := t.transformTypeDeclaration(typeCtx.(*grammar.TypeDeclarationContext))
		if err != nil {
			return nil, nil, err
		}
		if len(decls) > 0 {
			return decls[0], nil, nil
		}
		return nil, nil, nil
	}
	if importCtx := ctx.ImportDeclaration(); importCtx != nil {
		decl, err := t.transformImportDeclaration(importCtx.(*grammar.ImportDeclarationContext))
		return decl, nil, err
	}
	if ifCtx := ctx.IfStatement(); ifCtx != nil {
		stmt, err := t.transformIfStatement(ifCtx.(*grammar.IfStatementContext))
		return nil, stmt, err
	}
	if forCtx := ctx.ForStatement(); forCtx != nil {
		// TODO: implement
		return nil, nil, galaerr.NewSemanticError("for statement not implemented yet")
	}
	if simpleCtx := ctx.SimpleStatement(); simpleCtx != nil {
		stmt, err := t.transformSimpleStatement(simpleCtx.(*grammar.SimpleStatementContext))
		return nil, stmt, err
	}
	return nil, nil, nil
}

func (t *galaASTTransformer) transformSimpleStatement(ctx grammar.ISimpleStatementContext) (ast.Stmt, error) {
	if exprCtx := ctx.Expression(); exprCtx != nil {
		expr, err := t.transformExpression(exprCtx)
		if err != nil {
			return nil, err
		}
		return &ast.ExprStmt{X: expr}, nil
	}
	if assignCtx := ctx.Assignment(); assignCtx != nil {
		return t.transformAssignment(assignCtx.(*grammar.AssignmentContext))
	}
	if shortCtx := ctx.ShortVarDecl(); shortCtx != nil {
		return t.transformShortVarDecl(shortCtx.(*grammar.ShortVarDeclContext))
	}
	return nil, nil
}

func (t *galaASTTransformer) transformAssignment(ctx *grammar.AssignmentContext) (ast.Stmt, error) {
	lhsCtx := ctx.GetChild(0).(*grammar.ExpressionListContext)
	for _, exprCtx := range lhsCtx.AllExpression() {
		if p := exprCtx.Primary(); p != nil {
			pc := p.(*grammar.PrimaryContext)
			if pc.Identifier() != nil {
				name := pc.Identifier().GetText()
				if t.isVal(name) {
					return nil, galaerr.NewSemanticError(fmt.Sprintf("cannot assign to immutable variable %s", name))
				}
			}
		}
		if exprCtx.GetChildCount() == 3 && exprCtx.GetChild(1).(antlr.ParseTree).GetText() == "." {
			selName := exprCtx.GetChild(2).(antlr.ParseTree).GetText()
			if t.immutFields[selName] {
				return nil, galaerr.NewSemanticError(fmt.Sprintf("cannot assign to immutable field %s", selName))
			}
		}
	}

	lhsExprs, err := t.transformExpressionList(lhsCtx)
	if err != nil {
		return nil, err
	}
	rhsExprs, err := t.transformExpressionList(ctx.GetChild(2).(*grammar.ExpressionListContext))
	if err != nil {
		return nil, err
	}

	op := ctx.GetChild(1).(antlr.TerminalNode).GetText()
	var tok token.Token
	switch op {
	case "=":
		tok = token.ASSIGN
	case "+=":
		tok = token.ADD_ASSIGN
	case "-=":
		tok = token.SUB_ASSIGN
	case "*=":
		tok = token.MUL_ASSIGN
	case "/=":
		tok = token.QUO_ASSIGN
	default:
		return nil, galaerr.NewSemanticError(fmt.Sprintf("unknown assignment operator: %s", op))
	}

	return &ast.AssignStmt{
		Lhs: lhsExprs,
		Tok: tok,
		Rhs: rhsExprs,
	}, nil
}

func (t *galaASTTransformer) transformShortVarDecl(ctx *grammar.ShortVarDeclContext) (ast.Stmt, error) {
	idsCtx := ctx.IdentifierList().(*grammar.IdentifierListContext).AllIdentifier()
	rhsExprs, err := t.transformExpressionList(ctx.ExpressionList().(*grammar.ExpressionListContext))
	if err != nil {
		return nil, err
	}

	lhs := make([]ast.Expr, 0)
	wrappedRhs := make([]ast.Expr, 0)
	for i, idCtx := range idsCtx {
		name := idCtx.GetText()
		typeName := ""
		if len(idsCtx) == len(rhsExprs) {
			// This is a bit naive, but good enough for simple cases
			if lit, ok := rhsExprs[i].(*ast.CompositeLit); ok {
				if id, ok := lit.Type.(*ast.Ident); ok {
					typeName = id.Name
				}
			}
		}
		t.addVal(name, typeName)
		lhs = append(lhs, ast.NewIdent(name))

		var val ast.Expr
		if i < len(rhsExprs) {
			val = rhsExprs[i]
		} else {
			val = &ast.IndexExpr{X: rhsExprs[0], Index: &ast.BasicLit{Kind: token.INT, Value: fmt.Sprintf("%d", i)}}
		}

		t.needsStdImport = true
		wrappedRhs = append(wrappedRhs, &ast.CallExpr{
			Fun: &ast.SelectorExpr{
				X:   ast.NewIdent("std"),
				Sel: ast.NewIdent("NewImmutable"),
			},
			Args: []ast.Expr{val},
		})
	}

	return &ast.AssignStmt{
		Lhs: lhs,
		Tok: token.DEFINE,
		Rhs: wrappedRhs,
	}, nil
}

func (t *galaASTTransformer) transformValDeclaration(ctx *grammar.ValDeclarationContext) (ast.Decl, error) {
	namesCtx := ctx.IdentifierList().(*grammar.IdentifierListContext).AllIdentifier()
	rhsExprs, err := t.transformExpressionList(ctx.ExpressionList().(*grammar.ExpressionListContext))
	if err != nil {
		return nil, err
	}

	if len(rhsExprs) != len(namesCtx) {
		if len(rhsExprs) == 1 && len(namesCtx) > 1 {
			// multi-value from a single expression (e.g. function call)
		} else {
			return nil, galaerr.NewSemanticError("assignment mismatch")
		}
	}

	t.needsStdImport = true
	var idents []*ast.Ident
	var wrappedValues []ast.Expr
	for i, idCtx := range namesCtx {
		name := idCtx.GetText()
		typeName := ""
		if ctx.Type_() != nil {
			typeExpr, _ := t.transformType(ctx.Type_())
			if id, ok := typeExpr.(*ast.Ident); ok {
				typeName = id.Name
			}
		} else if len(rhsExprs) == len(namesCtx) {
			if lit, ok := rhsExprs[i].(*ast.CompositeLit); ok {
				if id, ok := lit.Type.(*ast.Ident); ok {
					typeName = id.Name
				}
			}
		}

		t.addVal(name, typeName)
		idents = append(idents, ast.NewIdent(name))

		var val ast.Expr
		if i < len(rhsExprs) {
			val = rhsExprs[i]
		} else {
			val = &ast.IndexExpr{X: rhsExprs[0], Index: &ast.BasicLit{Kind: token.INT, Value: fmt.Sprintf("%d", i)}}
		}

		wrappedValues = append(wrappedValues, &ast.CallExpr{
			Fun: &ast.SelectorExpr{
				X:   ast.NewIdent("std"),
				Sel: ast.NewIdent("NewImmutable"),
			},
			Args: []ast.Expr{val},
		})
	}

	spec := &ast.ValueSpec{
		Names:  idents,
		Values: wrappedValues,
	}

	if ctx.Type_() != nil {
		typeExpr, err := t.transformType(ctx.Type_())
		if err != nil {
			return nil, err
		}
		spec.Type = &ast.IndexExpr{
			X: &ast.SelectorExpr{
				X:   ast.NewIdent("std"),
				Sel: ast.NewIdent("Immutable"),
			},
			Index: typeExpr,
		}
	}

	return &ast.GenDecl{
		Tok:   token.VAR,
		Specs: []ast.Spec{spec},
	}, nil
}

func (t *galaASTTransformer) transformVarDeclaration(ctx *grammar.VarDeclarationContext) (ast.Decl, error) {
	namesCtx := ctx.IdentifierList().(*grammar.IdentifierListContext).AllIdentifier()
	var idents []*ast.Ident
	for _, idCtx := range namesCtx {
		name := idCtx.GetText()
		typeName := ""
		if ctx.Type_() != nil {
			// Try to get type name from transformed type
			typeExpr, _ := t.transformType(ctx.Type_())
			if id, ok := typeExpr.(*ast.Ident); ok {
				typeName = id.Name
			}
		}
		t.addVar(name, typeName)
		idents = append(idents, ast.NewIdent(name))
	}

	spec := &ast.ValueSpec{
		Names: idents,
	}

	if ctx.ExpressionList() != nil {
		rhs, err := t.transformExpressionList(ctx.ExpressionList().(*grammar.ExpressionListContext))
		if err != nil {
			return nil, err
		}
		spec.Values = rhs
	}

	if ctx.Type_() != nil {
		typeExpr, err := t.transformType(ctx.Type_())
		if err != nil {
			return nil, err
		}
		spec.Type = typeExpr
	}

	return &ast.GenDecl{
		Tok:   token.VAR,
		Specs: []ast.Spec{spec},
	}, nil
}

func (t *galaASTTransformer) transformFunctionDeclaration(ctx *grammar.FunctionDeclarationContext) (ast.Decl, error) {
	t.pushScope()
	defer t.popScope()
	name := ctx.Identifier().GetText()

	// Signature
	sigCtx := ctx.Signature().(*grammar.SignatureContext)
	paramsCtx := sigCtx.Parameters().(*grammar.ParametersContext)

	fieldList := &ast.FieldList{}
	if paramsCtx.ParameterList() != nil {
		for _, pCtx := range paramsCtx.ParameterList().(*grammar.ParameterListContext).AllParameter() {
			field, err := t.transformParameter(pCtx.(*grammar.ParameterContext))
			if err != nil {
				return nil, err
			}
			fieldList.List = append(fieldList.List, field)
		}
	}

	var results *ast.FieldList
	if sigCtx.Type_() != nil {
		retType, err := t.transformType(sigCtx.Type_())
		if err != nil {
			return nil, err
		}
		results = &ast.FieldList{
			List: []*ast.Field{{Type: retType}},
		}
	}

	funcType := &ast.FuncType{
		Params:  fieldList,
		Results: results,
	}

	// Generics
	if ctx.TypeParameters() != nil {
		// Go AST for generics uses TypeParams field in FuncDecl
		tParams, err := t.transformTypeParameters(ctx.TypeParameters().(*grammar.TypeParametersContext))
		if err != nil {
			return nil, err
		}
		funcType.TypeParams = tParams
	}

	var body *ast.BlockStmt
	if ctx.Block() != nil {
		b, err := t.transformBlock(ctx.Block().(*grammar.BlockContext))
		if err != nil {
			return nil, err
		}
		body = b
	} else if ctx.Expression() != nil {
		// func f(x int) int = x * x  =>  { return x * x }
		expr, err := t.transformExpression(ctx.Expression())
		if err != nil {
			return nil, err
		}
		body = &ast.BlockStmt{
			List: []ast.Stmt{
				&ast.ReturnStmt{Results: []ast.Expr{expr}},
			},
		}
	}

	return &ast.FuncDecl{
		Name: ast.NewIdent(name),
		Type: funcType,
		Body: body,
	}, nil
}

func (t *galaASTTransformer) transformStructShorthandDeclaration(ctx *grammar.StructShorthandDeclarationContext) ([]ast.Decl, error) {
	name := ctx.Identifier().GetText()
	paramsCtx := ctx.Parameters().(*grammar.ParametersContext)
	t.pushScope()
	defer t.popScope()

	fields := &ast.FieldList{}
	var fieldNames []string
	var immutFlags []bool
	if paramsCtx.ParameterList() != nil {
		for _, pCtx := range paramsCtx.ParameterList().(*grammar.ParameterListContext).AllParameter() {
			param := pCtx.(*grammar.ParameterContext)
			isVal := param.VAR() == nil // Default to immutable if VAR is not present

			// For shorthand structs, we want the fields in the struct to be std.Immutable if isVal
			// but transformParameter handles function parameters.
			// We'll transform it as a parameter first, then adjust for the struct field.
			field, err := t.transformParameter(param)
			if err != nil {
				return nil, err
			}

			if isVal {
				t.needsStdImport = true
				if typ, ok := field.Type.(*ast.Ident); ok {
					// Add to immutFields only if it's a field name we are processing
					// But we also need to wrap it in std.Immutable in the struct type
					field.Type = &ast.IndexExpr{
						X: &ast.SelectorExpr{
							X:   ast.NewIdent("std"),
							Sel: ast.NewIdent("Immutable"),
						},
						Index: typ,
					}
				}
			}

			fields.List = append(fields.List, field)
			for _, n := range field.Names {
				fieldNames = append(fieldNames, n.Name)
				immutFlags = append(immutFlags, isVal)
				if isVal {
					t.immutFields[n.Name] = true
				}
			}
		}
	}

	t.structFields[name] = fieldNames
	t.structImmutFields[name] = immutFlags
	typeSpec := &ast.TypeSpec{
		Name: ast.NewIdent(name),
		Type: &ast.StructType{Fields: fields},
	}

	decls := []ast.Decl{
		&ast.GenDecl{
			Tok:   token.TYPE,
			Specs: []ast.Spec{typeSpec},
		},
	}

	// Copy and Equal methods
	copyMethod, err := t.generateCopyMethod(name, fields, nil)
	if err != nil {
		return nil, err
	}
	decls = append(decls, copyMethod)

	equalMethod, err := t.generateEqualMethod(name, fields, nil)
	if err != nil {
		return nil, err
	}
	decls = append(decls, equalMethod)

	return decls, nil
}

func (t *galaASTTransformer) transformTypeDeclaration(ctx *grammar.TypeDeclarationContext) ([]ast.Decl, error) {
	name := ctx.Identifier().GetText()
	var decls []ast.Decl

	// Process Type Parameters first
	var tParams *ast.FieldList
	if ctx.TypeParameters() != nil {
		var err error
		tParams, err = t.transformTypeParameters(ctx.TypeParameters().(*grammar.TypeParametersContext))
		if err != nil {
			return nil, err
		}
		// Populate activeTypeParams
		for _, field := range tParams.List {
			for _, id := range field.Names {
				t.activeTypeParams[id.Name] = true
			}
		}
	}
	defer func() {
		// Clear activeTypeParams after processing
		if tParams != nil {
			for _, field := range tParams.List {
				for _, id := range field.Names {
					delete(t.activeTypeParams, id.Name)
				}
			}
		}
	}()

	if ctx.StructType() != nil {
		structCtx := ctx.StructType().(*grammar.StructTypeContext)
		fields := &ast.FieldList{}
		var fieldNames []string
		var immutFlags []bool
		for _, fCtx := range structCtx.AllStructField() {
			fieldCtx := fCtx.(*grammar.StructFieldContext)
			isVal := fieldCtx.VAR() == nil

			field, err := t.transformStructField(fieldCtx)
			if err != nil {
				return nil, err
			}
			fields.List = append(fields.List, field)
			for _, n := range field.Names {
				fieldNames = append(fieldNames, n.Name)
				immutFlags = append(immutFlags, isVal)
			}
		}
		t.structFields[name] = fieldNames
		t.structImmutFields[name] = immutFlags
		typeSpec := &ast.TypeSpec{
			Name:       ast.NewIdent(name),
			Type:       &ast.StructType{Fields: fields},
			TypeParams: tParams,
		}
		decls = append(decls, &ast.GenDecl{
			Tok:   token.TYPE,
			Specs: []ast.Spec{typeSpec},
		})

		t.needsStdImport = true

		receiverType := ast.Expr(ast.NewIdent(name))
		if tParams != nil {
			var indices []ast.Expr
			for _, p := range tParams.List {
				for _, n := range p.Names {
					indices = append(indices, ast.NewIdent(n.Name))
				}
			}
			if len(indices) == 1 {
				receiverType = &ast.IndexExpr{
					X:     receiverType,
					Index: indices[0],
				}
			} else if len(indices) > 1 {
				receiverType = &ast.IndexListExpr{
					X:       receiverType,
					Indices: indices,
				}
			}
		}

		// Generate Copy method
		var copyElts []ast.Expr
		for _, fn := range fieldNames {
			copyElts = append(copyElts, &ast.KeyValueExpr{
				Key: ast.NewIdent(fn),
				Value: &ast.CallExpr{
					Fun: &ast.SelectorExpr{
						X:   ast.NewIdent("std"),
						Sel: ast.NewIdent("Copy"),
					},
					Args: []ast.Expr{
						&ast.SelectorExpr{
							X:   ast.NewIdent("s"),
							Sel: ast.NewIdent(fn),
						},
					},
				},
			})
		}

		copyDecl := &ast.FuncDecl{
			Recv: &ast.FieldList{
				List: []*ast.Field{
					{
						Names: []*ast.Ident{ast.NewIdent("s")},
						Type:  receiverType,
					},
				},
			},
			Name: ast.NewIdent("Copy"),
			Type: &ast.FuncType{
				Results: &ast.FieldList{
					List: []*ast.Field{
						{Type: receiverType},
					},
				},
			},
			Body: &ast.BlockStmt{
				List: []ast.Stmt{
					&ast.ReturnStmt{
						Results: []ast.Expr{
							&ast.CompositeLit{
								Type: receiverType,
								Elts: copyElts,
							},
						},
					},
				},
			},
		}
		decls = append(decls, copyDecl)

		// Generate Equal method
		var equalExpr ast.Expr
		if len(fieldNames) == 0 {
			equalExpr = ast.NewIdent("true")
		} else {
			for _, fn := range fieldNames {
				cond := &ast.CallExpr{
					Fun: &ast.SelectorExpr{
						X:   ast.NewIdent("std"),
						Sel: ast.NewIdent("Equal"),
					},
					Args: []ast.Expr{
						&ast.SelectorExpr{
							X:   ast.NewIdent("s"),
							Sel: ast.NewIdent(fn),
						},
						&ast.SelectorExpr{
							X:   ast.NewIdent("other"),
							Sel: ast.NewIdent(fn),
						},
					},
				}
				if equalExpr == nil {
					equalExpr = cond
				} else {
					equalExpr = &ast.BinaryExpr{
						X:  equalExpr,
						Op: token.LAND,
						Y:  cond,
					}
				}
			}
		}

		equalDecl := &ast.FuncDecl{
			Recv: &ast.FieldList{
				List: []*ast.Field{
					{
						Names: []*ast.Ident{ast.NewIdent("s")},
						Type:  receiverType,
					},
				},
			},
			Name: ast.NewIdent("Equal"),
			Type: &ast.FuncType{
				Params: &ast.FieldList{
					List: []*ast.Field{
						{
							Names: []*ast.Ident{ast.NewIdent("other")},
							Type:  receiverType,
						},
					},
				},
				Results: &ast.FieldList{
					List: []*ast.Field{
						{Type: ast.NewIdent("bool")},
					},
				},
			},
			Body: &ast.BlockStmt{
				List: []ast.Stmt{
					&ast.ReturnStmt{
						Results: []ast.Expr{equalExpr},
					},
				},
			},
		}
		decls = append(decls, equalDecl)

	} else if ctx.InterfaceType() != nil {
		// TODO: implement
		return nil, galaerr.NewSemanticError("interface type not implemented yet")
	} else if ctx.TypeAlias() != nil {
		// TODO: implement
		return nil, galaerr.NewSemanticError("type alias not implemented yet")
	}

	return decls, nil
}

func (t *galaASTTransformer) transformImportDeclaration(ctx *grammar.ImportDeclarationContext) (ast.Decl, error) {
	// import "pkg"  or import ( "pkg1" "pkg2" )
	var specs []ast.Spec
	for _, specCtx := range ctx.AllImportSpec() {
		s := specCtx.(*grammar.ImportSpecContext)
		importSpec := &ast.ImportSpec{
			Path: &ast.BasicLit{Kind: token.STRING, Value: s.STRING().GetText()},
		}
		if s.Identifier() != nil {
			importSpec.Name = ast.NewIdent(s.Identifier().GetText())
		} else if s.GetChildCount() > 1 {
			// Check for '.'
			if dot := s.GetChild(0); dot != nil {
				if terminal, ok := dot.(antlr.TerminalNode); ok && terminal.GetText() == "." {
					importSpec.Name = ast.NewIdent(".")
				}
			}
		}
		specs = append(specs, importSpec)
	}
	return &ast.GenDecl{
		Tok:   token.IMPORT,
		Specs: specs,
	}, nil
}

func (t *galaASTTransformer) transformParameter(ctx *grammar.ParameterContext) (*ast.Field, error) {
	name := ctx.Identifier().GetText()
	field := &ast.Field{
		Names: []*ast.Ident{ast.NewIdent(name)},
	}

	typeName := ""
	if ctx.Type_() != nil {
		typeExpr, _ := t.transformType(ctx.Type_())
		if id, ok := typeExpr.(*ast.Ident); ok {
			typeName = id.Name
		}
	}
	isVal := ctx.VAL() != nil
	if isVal {
		t.addVal(name, typeName)
	} else {
		t.addVar(name, typeName)
	}

	if ctx.Type_() != nil {
		typ, err := t.transformType(ctx.Type_())
		if err != nil {
			return nil, err
		}
		if isVal {
			t.needsStdImport = true
			field.Type = &ast.IndexExpr{
				X: &ast.SelectorExpr{
					X:   ast.NewIdent("std"),
					Sel: ast.NewIdent("Immutable"),
				},
				Index: typ,
			}
		} else {
			field.Type = typ
		}
	}
	return field, nil
}

func (t *galaASTTransformer) transformStructField(ctx *grammar.StructFieldContext) (*ast.Field, error) {
	name := ctx.Identifier().GetText()
	typ, err := t.transformType(ctx.Type_())
	if err != nil {
		return nil, err
	}

	isVal := ctx.VAR() == nil

	field := &ast.Field{
		Names: []*ast.Ident{ast.NewIdent(name)},
	}

	if isVal {
		t.needsStdImport = true
		t.immutFields[name] = true
		field.Type = &ast.IndexExpr{
			X: &ast.SelectorExpr{
				X:   ast.NewIdent("std"),
				Sel: ast.NewIdent("Immutable"),
			},
			Index: typ,
		}
	} else {
		field.Type = typ
	}

	if ctx.STRING() != nil {
		field.Tag = &ast.BasicLit{Kind: token.STRING, Value: ctx.STRING().GetText()}
	}
	return field, nil
}

func (t *galaASTTransformer) transformTypeParameters(ctx *grammar.TypeParametersContext) (*ast.FieldList, error) {
	list := &ast.FieldList{}
	for _, tpCtx := range ctx.TypeParameterList().(*grammar.TypeParameterListContext).AllTypeParameter() {
		tp := tpCtx.(*grammar.TypeParameterContext)
		list.List = append(list.List, &ast.Field{
			Names: []*ast.Ident{ast.NewIdent(tp.Identifier(0).GetText())},
			Type:  ast.NewIdent(tp.Identifier(1).GetText()),
		})
	}
	return list, nil
}

func (t *galaASTTransformer) transformBlock(ctx *grammar.BlockContext) (*ast.BlockStmt, error) {
	t.pushScope()
	defer t.popScope()
	block := &ast.BlockStmt{}
	for _, stmtCtx := range ctx.AllStatement() {
		stmt, err := t.transformStatement(stmtCtx.(*grammar.StatementContext))
		if err != nil {
			return nil, err
		}
		block.List = append(block.List, stmt)
	}
	return block, nil
}

func (t *galaASTTransformer) transformStatement(ctx *grammar.StatementContext) (ast.Stmt, error) {
	if declCtx := ctx.Declaration(); declCtx != nil {
		decl, stmt, err := t.transformDeclaration(declCtx)
		if err != nil {
			return nil, err
		}
		if stmt != nil {
			return stmt, nil
		}
		if decl != nil {
			return &ast.DeclStmt{Decl: decl}, nil
		}
		return nil, nil
	}
	if retCtx := ctx.ReturnStatement(); retCtx != nil {
		var results []ast.Expr
		if retCtx.Expression() != nil {
			expr, err := t.transformExpression(retCtx.Expression())
			if err != nil {
				return nil, err
			}
			results = append(results, expr)
		}
		return &ast.ReturnStmt{Results: results}, nil
	}
	return nil, nil
}

func (t *galaASTTransformer) transformCallExpr(ctx *grammar.ExpressionContext) (ast.Expr, error) {
	// expression '(' argumentList? ')'
	child1 := ctx.GetChild(0)
	x, err := t.transformExpression(child1.(grammar.IExpressionContext))
	if err != nil {
		return nil, err
	}

	var args []ast.Expr
	var namedArgs map[string]ast.Expr
	if ctx.GetChildCount() >= 3 {
		if argListCtx, ok := ctx.GetChild(2).(*grammar.ArgumentListContext); ok {
			// Handle Copy method call with overrides
			if sel, ok := x.(*ast.SelectorExpr); ok && sel.Sel.Name == "Copy" {
				return t.transformCopyCall(sel.X, argListCtx)
			}

			for _, argCtx := range argListCtx.AllArgument() {
				arg := argCtx.(*grammar.ArgumentContext)
				expr, err := t.transformExpression(arg.Expression())
				if err != nil {
					return nil, err
				}

				if arg.Identifier() != nil {
					if namedArgs == nil {
						namedArgs = make(map[string]ast.Expr)
					}
					namedArgs[arg.Identifier().GetText()] = expr
				} else {
					args = append(args, expr)
				}
			}
		}
	}

	// Handle case where we have TypeName(...) which is a constructor call
	// GALA doesn't seem to have a specific rule for constructor calls,
	// but TypeName(...) should be transformed to TypeName{...} if it's a struct.
	if id, ok := x.(*ast.Ident); ok {
		if fields, ok := t.structFields[id.Name]; ok {
			immutFlags := t.structImmutFields[id.Name]
			var elts []ast.Expr
			if namedArgs != nil {
				if len(args) > 0 {
					return nil, galaerr.NewSemanticError("cannot mix positional and named arguments in struct construction")
				}
				// Use fields order for stable output
				for i, name := range fields {
					if val, ok := namedArgs[name]; ok {
						finalVal := val
						if i < len(immutFlags) && immutFlags[i] {
							t.needsStdImport = true
							finalVal = &ast.CallExpr{
								Fun: &ast.SelectorExpr{
									X:   ast.NewIdent("std"),
									Sel: ast.NewIdent("NewImmutable"),
								},
								Args: []ast.Expr{val},
							}
						}

						elts = append(elts, &ast.KeyValueExpr{
							Key:   ast.NewIdent(name),
							Value: finalVal,
						})
					}
				}
				// Validation for unknown fields
				for name := range namedArgs {
					found := false
					for _, f := range fields {
						if f == name {
							found = true
							break
						}
					}
					if !found {
						return nil, galaerr.NewSemanticError(fmt.Sprintf("struct %s has no field %s", id.Name, name))
					}
				}
			} else {
				for i, arg := range args {
					if i < len(fields) {
						val := arg
						if i < len(immutFlags) && immutFlags[i] {
							t.needsStdImport = true
							val = &ast.CallExpr{
								Fun: &ast.SelectorExpr{
									X:   ast.NewIdent("std"),
									Sel: ast.NewIdent("NewImmutable"),
								},
								Args: []ast.Expr{arg},
							}
						}

						elts = append(elts, &ast.KeyValueExpr{
							Key:   ast.NewIdent(fields[i]),
							Value: val,
						})
					}
				}
			}
			return &ast.CompositeLit{
				Type: id,
				Elts: elts,
			}, nil
		}
	}

	if namedArgs != nil {
		return nil, galaerr.NewSemanticError("named arguments only supported for Copy method or struct construction")
	}

	return &ast.CallExpr{Fun: x, Args: args}, nil
}
func (t *galaASTTransformer) transformExpression(ctx grammar.IExpressionContext) (ast.Expr, error) {
	if ctx == nil {
		return nil, nil
	}

	// expression: primary
	if p := ctx.Primary(); p != nil {
		return t.transformPrimary(p.(*grammar.PrimaryContext))
	}

	// expression: lambdaExpression
	if l := ctx.LambdaExpression(); l != nil {
		return t.transformLambda(l.(*grammar.LambdaExpressionContext))
	}

	// expression: ifExpression
	if i := ctx.IfExpression(); i != nil {
		return t.transformIfExpression(i.(*grammar.IfExpressionContext))
	}

	// expression: expression 'match' '{' caseClause+ '}'
	// We check if it's a match by checking the number of children and existence of MATCH token
	if ctx.GetChildCount() >= 4 {
		for i := 0; i < ctx.GetChildCount(); i++ {
			if ctx.GetChild(i).(antlr.ParseTree).GetText() == "match" {
				return t.transformMatchExpression(ctx)
			}
		}
	}

	// Handle recursive expression patterns
	// Since there are no labels, we check the number of children and the tokens
	childCount := ctx.GetChildCount()
	if childCount == 2 {
		child1 := ctx.GetChild(0)
		child2 := ctx.GetChild(1)

		if _, ok := child1.(*grammar.UnaryOpContext); ok {
			expr, err := t.transformExpression(child2.(grammar.IExpressionContext))
			if err != nil {
				return nil, err
			}
			opText := child1.(antlr.ParseTree).GetText()
			if opText == "*" {
				return &ast.StarExpr{X: expr}, nil
			}
			return &ast.UnaryExpr{
				Op: t.getUnaryToken(opText),
				X:  expr,
			}, nil
		}
	}

	if childCount == 3 {
		child1 := ctx.GetChild(0)
		child2 := ctx.GetChild(1)
		child3 := ctx.GetChild(2)

		c2Text := child2.(antlr.ParseTree).GetText()

		if c2Text == "." {
			// expression '.' identifier
			x, err := t.transformExpression(child1.(grammar.IExpressionContext))
			if err != nil {
				return nil, err
			}
			selName := child3.(antlr.ParseTree).GetText()
			selExpr := &ast.SelectorExpr{
				X:   x,
				Sel: ast.NewIdent(selName),
			}
			if t.immutFields[selName] {
				return &ast.CallExpr{
					Fun: &ast.SelectorExpr{
						X:   selExpr,
						Sel: ast.NewIdent("Get"),
					},
				}, nil
			}
			return selExpr, nil
		}

		if c2Text == "(" && child3.(antlr.ParseTree).GetText() == ")" {
			// expression '(' ')'
			return t.transformCallExpr(ctx.(*grammar.ExpressionContext))
		}

		// expression binaryOp expression
		// Note: child2 might be the binaryOp rule or a terminal.
		// In our grammar, binaryOp is a rule.
		if _, ok := child2.(*grammar.BinaryOpContext); ok {
			left, err := t.transformExpression(child1.(grammar.IExpressionContext))
			if err != nil {
				return nil, err
			}
			right, err := t.transformExpression(child3.(grammar.IExpressionContext))
			if err != nil {
				return nil, err
			}
			return &ast.BinaryExpr{
				X:  left,
				Op: t.getBinaryToken(c2Text),
				Y:  right,
			}, nil
		}
	}

	if childCount == 4 {
		child2 := ctx.GetChild(1)
		child4 := ctx.GetChild(3)

		c2Text := child2.(antlr.ParseTree).GetText()
		c4Text := child4.(antlr.ParseTree).GetText()

		if c2Text == "(" && c4Text == ")" {
			// expression '(' argumentList? ')'
			return t.transformCallExpr(ctx.(*grammar.ExpressionContext))
		}

		if c2Text == "[" && c4Text == "]" {
			// expression '[' expressionList ']'
			child1 := ctx.GetChild(0)
			child3 := ctx.GetChild(2)
			x, err := t.transformExpression(child1.(grammar.IExpressionContext))
			if err != nil {
				return nil, err
			}
			indices, err := t.transformExpressionList(child3.(*grammar.ExpressionListContext))
			if err != nil {
				return nil, err
			}
			if len(indices) == 1 {
				return &ast.IndexExpr{X: x, Index: indices[0]}, nil
			} else {
				return &ast.IndexListExpr{X: x, Indices: indices}, nil
			}
		}
	}

	return nil, galaerr.NewSemanticError(fmt.Sprintf("expression transformation not fully implemented for %T: %s", ctx, ctx.GetText()))
}

func (t *galaASTTransformer) transformExpressionList(ctx *grammar.ExpressionListContext) ([]ast.Expr, error) {
	var exprs []ast.Expr
	for _, eCtx := range ctx.AllExpression() {
		e, err := t.transformExpression(eCtx)
		if err != nil {
			return nil, err
		}
		exprs = append(exprs, e)
	}
	return exprs, nil
}

func (t *galaASTTransformer) getBinaryToken(op string) token.Token {
	switch op {
	case "||":
		return token.LOR
	case "&&":
		return token.LAND
	case "==":
		return token.EQL
	case "!=":
		return token.NEQ
	case "<":
		return token.LSS
	case "<=":
		return token.LEQ
	case ">":
		return token.GTR
	case ">=":
		return token.GEQ
	case "+":
		return token.ADD
	case "-":
		return token.SUB
	case "|":
		return token.OR
	case "^":
		return token.XOR
	case "*":
		return token.MUL
	case "/":
		return token.QUO
	case "%":
		return token.REM
	case "<<":
		return token.SHL
	case ">>":
		return token.SHR
	case "&":
		return token.AND
	case "&^":
		return token.AND_NOT
	default:
		return token.ILLEGAL
	}
}

func (t *galaASTTransformer) getUnaryToken(op string) token.Token {
	switch op {
	case "+":
		return token.ADD
	case "-":
		return token.SUB
	case "!":
		return token.NOT
	case "^":
		return token.XOR
	case "&":
		return token.AND
	default:
		return token.ILLEGAL
	}
}

func (t *galaASTTransformer) transformPrimary(ctx *grammar.PrimaryContext) (ast.Expr, error) {
	if ctx.Identifier() != nil {
		name := ctx.Identifier().GetText()
		ident := ast.NewIdent(name)
		if t.isVal(name) {
			return &ast.CallExpr{
				Fun: &ast.SelectorExpr{
					X:   ident,
					Sel: ast.NewIdent("Get"),
				},
			}, nil
		}
		return ident, nil
	}
	if ctx.Literal() != nil {
		return t.transformLiteral(ctx.Literal().(*grammar.LiteralContext))
	}
	if ctx.Expression() != nil {
		return t.transformExpression(ctx.Expression())
	}
	return nil, nil
}

func (t *galaASTTransformer) transformLiteral(ctx *grammar.LiteralContext) (ast.Expr, error) {
	if ctx.INT_LIT() != nil {
		return &ast.BasicLit{Kind: token.INT, Value: ctx.INT_LIT().GetText()}, nil
	}
	if ctx.FLOAT_LIT() != nil {
		return &ast.BasicLit{Kind: token.FLOAT, Value: ctx.FLOAT_LIT().GetText()}, nil
	}
	if ctx.STRING() != nil {
		return &ast.BasicLit{Kind: token.STRING, Value: ctx.STRING().GetText()}, nil
	}
	if ctx.GetText() == "true" || ctx.GetText() == "false" {
		return ast.NewIdent(ctx.GetText()), nil
	}
	if ctx.GetText() == "nil" {
		return ast.NewIdent("nil"), nil
	}
	return nil, nil
}

func (t *galaASTTransformer) transformType(ctx grammar.ITypeContext) (ast.Expr, error) {
	if ctx == nil {
		return nil, nil
	}
	// Simplified type handling
	if ctx.Identifier() != nil {
		ident := ast.NewIdent(ctx.Identifier().GetText())
		if ctx.TypeArguments() != nil {
			// Generic type: T[A, B] -> *ast.IndexExpr or *ast.IndexListExpr
			args := ctx.TypeArguments().(*grammar.TypeArgumentsContext).TypeList().(*grammar.TypeListContext).AllType_()
			var argExprs []ast.Expr
			for _, arg := range args {
				ae, err := t.transformType(arg)
				if err != nil {
					return nil, err
				}
				argExprs = append(argExprs, ae)
			}
			if len(argExprs) == 1 {
				return &ast.IndexExpr{X: ident, Index: argExprs[0]}, nil
			} else {
				return &ast.IndexListExpr{X: ident, Indices: argExprs}, nil
			}
		}
		return ident, nil
	}
	if ctx.GetChildCount() > 0 && ctx.GetChild(0).(antlr.ParseTree).GetText() == "*" {
		typ, err := t.transformType(ctx.GetChild(1).(grammar.ITypeContext))
		if err != nil {
			return nil, err
		}
		return &ast.StarExpr{X: typ}, nil
	}
	return ast.NewIdent(ctx.GetText()), nil
}

func (t *galaASTTransformer) transformLambda(ctx *grammar.LambdaExpressionContext) (ast.Expr, error) {
	t.pushScope()
	defer t.popScope()
	paramsCtx := ctx.Parameters().(*grammar.ParametersContext)
	fieldList := &ast.FieldList{}
	if paramsCtx.ParameterList() != nil {
		for _, pCtx := range paramsCtx.ParameterList().(*grammar.ParameterListContext).AllParameter() {
			field, err := t.transformParameter(pCtx.(*grammar.ParameterContext))
			if err != nil {
				return nil, err
			}
			fieldList.List = append(fieldList.List, field)
		}
	}

	var body *ast.BlockStmt
	if ctx.Block() != nil {
		b, err := t.transformBlock(ctx.Block().(*grammar.BlockContext))
		if err != nil {
			return nil, err
		}
		body = b
	} else if ctx.Expression() != nil {
		expr, err := t.transformExpression(ctx.Expression())
		if err != nil {
			return nil, err
		}
		body = &ast.BlockStmt{
			List: []ast.Stmt{
				&ast.ReturnStmt{Results: []ast.Expr{expr}},
			},
		}
	}

	return &ast.FuncLit{
		Type: &ast.FuncType{
			Params: fieldList,
		},
		Body: body,
	}, nil
}

func (t *galaASTTransformer) transformIfExpression(ctx *grammar.IfExpressionContext) (ast.Expr, error) {
	// 'if' '(' cond ')' thenExpr 'else' elseExpr
	cond, err := t.transformExpression(ctx.Expression(0))
	if err != nil {
		return nil, err
	}
	thenExpr, err := t.transformExpression(ctx.Expression(1))
	if err != nil {
		return nil, err
	}
	elseExpr, err := t.transformExpression(ctx.Expression(2))
	if err != nil {
		return nil, err
	}

	// Transpile to IIFE: func() any { if cond { return thenExpr }; return elseExpr }()
	return &ast.CallExpr{
		Fun: &ast.FuncLit{
			Type: &ast.FuncType{
				Params: &ast.FieldList{},
				Results: &ast.FieldList{
					List: []*ast.Field{{Type: ast.NewIdent("any")}},
				},
			},
			Body: &ast.BlockStmt{
				List: []ast.Stmt{
					&ast.IfStmt{
						Cond: cond,
						Body: &ast.BlockStmt{
							List: []ast.Stmt{
								&ast.ReturnStmt{Results: []ast.Expr{thenExpr}},
							},
						},
					},
					&ast.ReturnStmt{Results: []ast.Expr{elseExpr}},
				},
			},
		},
	}, nil
}

func (t *galaASTTransformer) transformIfStatement(ctx *grammar.IfStatementContext) (ast.Stmt, error) {
	cond, err := t.transformExpression(ctx.Expression())
	if err != nil {
		return nil, err
	}
	body, err := t.transformBlock(ctx.Block(0).(*grammar.BlockContext))
	if err != nil {
		return nil, err
	}
	stmt := &ast.IfStmt{
		Cond: cond,
		Body: body,
	}

	if ctx.SimpleStatement() != nil {
		init, err := t.transformSimpleStatement(ctx.SimpleStatement().(*grammar.SimpleStatementContext))
		if err != nil {
			return nil, err
		}
		stmt.Init = init
	}

	if ctx.ELSE() != nil {
		if ctx.Block(1) != nil {
			elseBody, err := t.transformBlock(ctx.Block(1).(*grammar.BlockContext))
			if err != nil {
				return nil, err
			}
			stmt.Else = elseBody
		} else if ctx.IfStatement() != nil {
			elseIf, err := t.transformIfStatement(ctx.IfStatement().(*grammar.IfStatementContext))
			if err != nil {
				return nil, err
			}
			stmt.Else = elseIf
		}
	}

	return stmt, nil
}

func (t *galaASTTransformer) transformMatchExpression(ctx grammar.IExpressionContext) (ast.Expr, error) {
	// expression 'match' '{' caseClause+ '}'
	// Use children because it's not a distinct context type
	exprCtx := ctx.GetChild(0).(grammar.IExpressionContext)
	expr, err := t.transformExpression(exprCtx)
	if err != nil {
		return nil, err
	}

	paramName := "obj"
	if primary := exprCtx.Primary(); primary != nil {
		if p, ok := primary.(*grammar.PrimaryContext); ok && p.Identifier() != nil {
			paramName = p.Identifier().GetText()
		}
	}

	t.pushScope()
	defer t.popScope()
	t.addVar(paramName, "")

	var clauses []ast.Stmt
	var defaultBody []ast.Stmt
	foundDefault := false

	// case clauses start from child 3 (0: expr, 1: match, 2: {, 3: case...)
	for i := 3; i < ctx.GetChildCount()-1; i++ {
		ccCtx, ok := ctx.GetChild(i).(*grammar.CaseClauseContext)
		if !ok {
			continue
		}

		// Check if it's a default case
		patExprCtx := ccCtx.Expression(0)
		if patExprCtx.GetText() == "_" {
			if foundDefault {
				return nil, galaerr.NewSemanticError("multiple default cases in match")
			}
			foundDefault = true

			// Transform the body of default case
			if ccCtx.Block() != nil {
				b, err := t.transformBlock(ccCtx.Block().(*grammar.BlockContext))
				if err != nil {
					return nil, err
				}
				defaultBody = b.List
			} else if len(ccCtx.AllExpression()) > 1 {
				expr, err := t.transformExpression(ccCtx.Expression(1))
				if err != nil {
					return nil, err
				}
				defaultBody = []ast.Stmt{&ast.ReturnStmt{Results: []ast.Expr{expr}}}
			}
			continue
		}

		clause, err := t.transformCaseClause(ccCtx, paramName)
		if err != nil {
			return nil, err
		}
		clauses = append(clauses, clause)
	}

	if !foundDefault {
		return nil, galaerr.NewSemanticError("match expression must have a default case (case _ => ...)")
	}

	t.needsStdImport = true
	// Transpile to IIFE: func(obj any) any { ... }(expr)
	return &ast.CallExpr{
		Fun: &ast.FuncLit{
			Type: &ast.FuncType{
				Params: &ast.FieldList{
					List: []*ast.Field{
						{
							Names: []*ast.Ident{ast.NewIdent(paramName)},
							Type:  ast.NewIdent("any"),
						},
					},
				},
				Results: &ast.FieldList{
					List: []*ast.Field{{Type: ast.NewIdent("any")}},
				},
			},
			Body: &ast.BlockStmt{
				List: append([]ast.Stmt{
					&ast.SwitchStmt{
						Body: &ast.BlockStmt{
							List: clauses,
						},
					},
				}, defaultBody...),
			},
		},
		Args: []ast.Expr{expr},
	}, nil
}

func (t *galaASTTransformer) transformCaseClause(ctx *grammar.CaseClauseContext, paramName string) (ast.Stmt, error) {
	// 'case' expression '=>' (expression | block)
	patExpr, err := t.transformExpression(ctx.Expression(0))
	if err != nil {
		return nil, err
	}

	var body []ast.Stmt
	if ctx.Block() != nil {
		b, err := t.transformBlock(ctx.Block().(*grammar.BlockContext))
		if err != nil {
			return nil, err
		}
		body = b.List
	} else if len(ctx.AllExpression()) > 1 {
		expr, err := t.transformExpression(ctx.Expression(1))
		if err != nil {
			return nil, err
		}
		body = []ast.Stmt{&ast.ReturnStmt{Results: []ast.Expr{expr}}}
	}

	if ident, ok := patExpr.(*ast.Ident); ok && ident.Name == "_" {
		return &ast.CaseClause{
			List: nil,
			Body: body,
		}, nil
	}

	// Use std.UnapplyCheck(paramName, patExpr)
	check := &ast.CallExpr{
		Fun: &ast.SelectorExpr{
			X:   ast.NewIdent("std"),
			Sel: ast.NewIdent("UnapplyCheck"),
		},
		Args: []ast.Expr{
			ast.NewIdent(paramName),
			patExpr,
		},
	}

	return &ast.CaseClause{
		List: []ast.Expr{check},
		Body: body,
	}, nil
}

func (t *galaASTTransformer) generateCopyMethod(name string, fields *ast.FieldList, tParams *ast.FieldList) (*ast.FuncDecl, error) {
	var elts []ast.Expr
	for _, field := range fields.List {
		for _, fieldName := range field.Names {
			elts = append(elts, &ast.KeyValueExpr{
				Key: ast.NewIdent(fieldName.Name),
				Value: &ast.CallExpr{
					Fun: &ast.SelectorExpr{
						X:   ast.NewIdent("std"),
						Sel: ast.NewIdent("Copy"),
					},
					Args: []ast.Expr{
						&ast.SelectorExpr{
							X:   ast.NewIdent("s"),
							Sel: ast.NewIdent(fieldName.Name),
						},
					},
				},
			})
		}
	}

	retType := ast.Expr(ast.NewIdent(name))
	if tParams != nil {
		var indices []ast.Expr
		for _, p := range tParams.List {
			for _, n := range p.Names {
				indices = append(indices, ast.NewIdent(n.Name))
			}
		}
		retType = &ast.IndexListExpr{
			X:       ast.NewIdent(name),
			Indices: indices,
		}
	}

	return &ast.FuncDecl{
		Recv: &ast.FieldList{
			List: []*ast.Field{
				{
					Names: []*ast.Ident{ast.NewIdent("s")},
					Type:  retType,
				},
			},
		},
		Name: ast.NewIdent("Copy"),
		Type: &ast.FuncType{
			Results: &ast.FieldList{
				List: []*ast.Field{{Type: retType}},
			},
		},
		Body: &ast.BlockStmt{
			List: []ast.Stmt{
				&ast.ReturnStmt{
					Results: []ast.Expr{
						&ast.CompositeLit{
							Type: retType,
							Elts: elts,
						},
					},
				},
			},
		},
	}, nil
}

func (t *galaASTTransformer) generateEqualMethod(name string, fields *ast.FieldList, tParams *ast.FieldList) (*ast.FuncDecl, error) {
	var condition ast.Expr
	for _, field := range fields.List {
		for _, fieldName := range field.Names {
			expr := &ast.CallExpr{
				Fun: &ast.SelectorExpr{
					X:   ast.NewIdent("std"),
					Sel: ast.NewIdent("Equal"),
				},
				Args: []ast.Expr{
					&ast.SelectorExpr{
						X:   ast.NewIdent("s"),
						Sel: ast.NewIdent(fieldName.Name),
					},
					&ast.SelectorExpr{
						X:   ast.NewIdent("other"),
						Sel: ast.NewIdent(fieldName.Name),
					},
				},
			}

			if condition == nil {
				condition = expr
			} else {
				condition = &ast.BinaryExpr{
					X:  condition,
					Op: token.LAND,
					Y:  expr,
				}
			}
		}
	}

	if condition == nil {
		condition = ast.NewIdent("true")
	}

	retType := ast.Expr(ast.NewIdent(name))
	if tParams != nil {
		var indices []ast.Expr
		for _, p := range tParams.List {
			for _, n := range p.Names {
				indices = append(indices, ast.NewIdent(n.Name))
			}
		}
		retType = &ast.IndexListExpr{
			X:       ast.NewIdent(name),
			Indices: indices,
		}
	}

	return &ast.FuncDecl{
		Recv: &ast.FieldList{
			List: []*ast.Field{
				{
					Names: []*ast.Ident{ast.NewIdent("s")},
					Type:  retType,
				},
			},
		},
		Name: ast.NewIdent("Equal"),
		Type: &ast.FuncType{
			Params: &ast.FieldList{
				List: []*ast.Field{
					{
						Names: []*ast.Ident{ast.NewIdent("other")},
						Type:  retType,
					},
				},
			},
			Results: &ast.FieldList{
				List: []*ast.Field{{Type: ast.NewIdent("bool")}},
			},
		},
		Body: &ast.BlockStmt{
			List: []ast.Stmt{
				&ast.ReturnStmt{
					Results: []ast.Expr{condition},
				},
			},
		},
	}, nil
}

var _ transpiler.ASTTransformer = (*galaASTTransformer)(nil)
