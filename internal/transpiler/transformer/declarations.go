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

	var idents []*ast.Ident
	var wrappedValues []ast.Expr
	for i, idCtx := range namesCtx {
		name := idCtx.GetText()
		typeName := ""
		if ctx.Type_() != nil {
			typeExpr, _ := t.transformType(ctx.Type_())
			typeName = t.getBaseTypeName(typeExpr)
		} else if len(rhsExprs) == len(namesCtx) {
			typeName = t.getExprTypeName(rhsExprs[i])
		}

		t.addVal(name, typeName)
		idents = append(idents, ast.NewIdent(name))

		var val ast.Expr
		if i < len(rhsExprs) {
			val = rhsExprs[i]
		} else {
			val = &ast.IndexExpr{X: rhsExprs[0], Index: &ast.BasicLit{Kind: token.INT, Value: fmt.Sprintf("%d", i)}}
		}

		if t.isNoneCall(val) && ctx.Type_() == nil {
			return nil, galaerr.NewSemanticError("variable assigned to None() must have an explicit type")
		}

		var fun ast.Expr = t.stdIdent("NewImmutable")
		if ctx.Type_() != nil {
			typeExpr, err := t.transformType(ctx.Type_())
			if err != nil {
				return nil, err
			}
			fun = &ast.IndexExpr{
				X:     fun,
				Index: typeExpr,
			}
		}

		wrappedValues = append(wrappedValues, &ast.CallExpr{
			Fun:  fun,
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
			X:     t.stdIdent("Immutable"),
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
			typeName = t.getBaseTypeName(typeExpr)
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

		if ctx.Type_() == nil {
			for _, r := range rhs {
				if t.isNoneCall(r) {
					return nil, galaerr.NewSemanticError("variable assigned to None() must have an explicit type")
				}
			}
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

	// Receiver
	var receiver *ast.FieldList
	var receiverTypeName string
	if ctx.Receiver() != nil {
		recvCtx := ctx.Receiver().(*grammar.ReceiverContext)
		recvName := recvCtx.Identifier().GetText()
		recvTypeExpr, err := t.transformType(recvCtx.Type_())
		if err != nil {
			return nil, err
		}

		receiverTypeName = t.getBaseTypeName(recvTypeExpr)

		isVal := recvCtx.VAL() != nil
		if isVal {
			t.addVal(recvName, "")
			recvTypeExpr = &ast.IndexExpr{
				X:     t.stdIdent(transpiler.TypeImmutable),
				Index: recvTypeExpr,
			}
		} else {
			t.addVar(recvName, "")
		}

		receiver = &ast.FieldList{
			List: []*ast.Field{
				{
					Names: []*ast.Ident{ast.NewIdent(recvName)},
					Type:  recvTypeExpr,
				},
			},
		}

		if t.genericMethods[receiverTypeName] == nil {
			t.genericMethods[receiverTypeName] = make(map[string]bool)
		}
		if ctx.TypeParameters() != nil {
			t.genericMethods[receiverTypeName][name] = true
		}
	}

	// Type Parameters
	var typeParams *ast.FieldList
	if ctx.TypeParameters() != nil {
		tp, err := t.transformTypeParameters(ctx.TypeParameters().(*grammar.TypeParametersContext))
		if err != nil {
			return nil, err
		}
		typeParams = tp
	}

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

	if receiver != nil && (typeParams != nil || (t.genericMethods[receiverTypeName] != nil && t.genericMethods[receiverTypeName][name])) {
		// Generic method: transform to standalone function
		name = receiverTypeName + "_" + name
		// 1. Add receiver as first parameter
		fieldList.List = append([]*ast.Field{receiver.List[0]}, fieldList.List...)

		// 2. Extract type parameters from receiver type and add to typeParams
		recvTypeParams := t.extractTypeParams(receiver.List[0].Type)
		if len(recvTypeParams) > 0 {
			if typeParams == nil {
				typeParams = &ast.FieldList{}
			}
			// Check for duplicates
			for _, rtp := range recvTypeParams {
				exists := false
				for _, tp := range typeParams.List {
					if tp.Names[0].Name == rtp.Names[0].Name {
						exists = true
						break
					}
				}
				if !exists {
					typeParams.List = append(typeParams.List, rtp)
				}
			}
		}
		receiver = nil
	}

	var results *ast.FieldList
	if sigCtx.Type_() != nil {
		retType, err := t.transformType(sigCtx.Type_())
		if err != nil {
			return nil, err
		}
		results = &ast.FieldList{
			List: []*ast.Field{
				{Type: retType},
			},
		}
	}

	funcType := &ast.FuncType{
		TypeParams: typeParams,
		Params:     fieldList,
		Results:    results,
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
		if results != nil && len(results.List) > 0 {
			expr = t.wrapWithAssertion(expr, results.List[0].Type)
		}
		body = &ast.BlockStmt{
			List: []ast.Stmt{
				&ast.ReturnStmt{Results: []ast.Expr{expr}},
			},
		}
	}

	return &ast.FuncDecl{
		Recv: receiver,
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
				if typ, ok := field.Type.(*ast.Ident); ok {
					// Add to immutFields only if it's a field name we are processing
					// But we also need to wrap it in std.Immutable in the struct type
					field.Type = &ast.IndexExpr{
						X:     t.stdIdent("Immutable"),
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
	if t.structFieldTypes[name] == nil {
		t.structFieldTypes[name] = make(map[string]string)
	}
	if paramsCtx.ParameterList() != nil {
		for _, pCtx := range paramsCtx.ParameterList().(*grammar.ParameterListContext).AllParameter() {
			param := pCtx.(*grammar.ParameterContext)
			pName := param.Identifier().GetText()
			pType := ""
			if param.Type_() != nil {
				typeExpr, _ := t.transformType(param.Type_())
				pType = t.getBaseTypeName(typeExpr)
			}
			t.structFieldTypes[name][pName] = pType
		}
	}
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

	unapplyMethod, err := t.generateUnapplyMethod(name, fields, nil)
	if err != nil {
		return nil, err
	}
	if unapplyMethod != nil {
		decls = append(decls, unapplyMethod)
	}

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
			for _, n := range field.Names {
				t.activeTypeParams[n.Name] = true
			}
		}
		defer func() {
			for _, field := range tParams.List {
				for _, n := range field.Names {
					delete(t.activeTypeParams, n.Name)
				}
			}
		}()
	}

	if ctx.StructType() != nil {
		structCtx := ctx.StructType().(*grammar.StructTypeContext)
		fields := &ast.FieldList{}
		var fieldNames []string
		var immutFlags []bool

		if t.structFieldTypes[name] == nil {
			t.structFieldTypes[name] = make(map[string]string)
		}

		for _, fCtx := range structCtx.AllStructField() {
			f, err := t.transformStructField(fCtx.(*grammar.StructFieldContext))
			if err != nil {
				return nil, err
			}
			fields.List = append(fields.List, f)
			for _, n := range f.Names {
				fieldNames = append(fieldNames, n.Name)
				immutFlags = append(immutFlags, fCtx.(*grammar.StructFieldContext).VAR() == nil)

				fType := ""
				if fCtx.(*grammar.StructFieldContext).Type_() != nil {
					typeExpr, _ := t.transformType(fCtx.(*grammar.StructFieldContext).Type_())
					fType = t.getBaseTypeName(typeExpr)
				}
				t.structFieldTypes[name][n.Name] = fType
			}
		}
		t.structFields[name] = fieldNames
		t.structImmutFields[name] = immutFlags

		typeSpec := &ast.TypeSpec{
			Name:       ast.NewIdent(name),
			TypeParams: tParams,
			Type:       &ast.StructType{Fields: fields},
		}

		decls = append(decls, &ast.GenDecl{
			Tok:   token.TYPE,
			Specs: []ast.Spec{typeSpec},
		})

		// Methods
		copyMethod, err := t.generateCopyMethod(name, fields, tParams)
		if err != nil {
			return nil, err
		}
		decls = append(decls, copyMethod)

		equalMethod, err := t.generateEqualMethod(name, fields, tParams)
		if err != nil {
			return nil, err
		}
		decls = append(decls, equalMethod)

		unapplyMethod, err := t.generateUnapplyMethod(name, fields, tParams)
		if err != nil {
			return nil, err
		}
		if unapplyMethod != nil {
			decls = append(decls, unapplyMethod)
		}

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
		typeName = t.getBaseTypeName(typeExpr)
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
			field.Type = &ast.IndexExpr{
				X:     t.stdIdent("Immutable"),
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
		t.immutFields[name] = true
		field.Type = &ast.IndexExpr{
			X:     t.stdIdent("Immutable"),
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
