package transformer

import (
	"fmt"
	"go/ast"
	"go/token"
	"martianoff/gala/galaerr"
	"martianoff/gala/internal/parser/grammar"
	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/registry"
	"strings"

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
		stmt, err := t.transformForStatement(forCtx.(*grammar.ForStatementContext))
		return nil, stmt, err
	}
	if simpleCtx := ctx.SimpleStatement(); simpleCtx != nil {
		stmt, err := t.transformSimpleStatement(simpleCtx.(*grammar.SimpleStatementContext))
		return nil, stmt, err
	}
	return nil, nil, nil
}

func (t *galaASTTransformer) transformValDeclaration(ctx *grammar.ValDeclarationContext) (ast.Decl, error) {
	// Handle tuple pattern: val (a, b) = tuple
	if ctx.TuplePattern() != nil {
		return t.transformValTuplePattern(ctx)
	}

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
		var typeName transpiler.Type = transpiler.NilType{}
		if ctx.Type_() != nil {
			typeExpr, _ := t.transformType(ctx.Type_())
			typeName = t.exprToType(typeExpr)
			if t.isImmutableType(typeName) {
				panic(galaerr.NewSemanticError("recursive Immutable wrapping is not allowed"))
			}
		} else if len(rhsExprs) == len(namesCtx) {
			typeName = t.getExprTypeName(rhsExprs[i])
			if t.isImmutableType(typeName) {
				if gen, ok := typeName.(transpiler.GenericType); ok && len(gen.Params) > 0 {
					typeName = gen.Params[0]
				}
			}
		}

		if qName := t.getType(typeName.String()); !qName.IsNil() {
			typeName = qName
		}

		t.addVal(name, typeName)
		idents = append(idents, ast.NewIdent(name))

		var val ast.Expr
		if i < len(rhsExprs) {
			val = t.unwrapImmutable(rhsExprs[i])
		} else {
			val = &ast.IndexExpr{X: t.unwrapImmutable(rhsExprs[0]), Index: &ast.BasicLit{Kind: token.INT, Value: fmt.Sprintf("%d", i)}}
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

// transformValTuplePattern handles val declarations with tuple destructuring: val (a, b) = tuple
// It generates:
//
//	var (
//	    __tuple_N = tuple
//	    a = NewImmutable(__tuple_N.V1)
//	    b = NewImmutable(__tuple_N.V2)
//	)
func (t *galaASTTransformer) transformValTuplePattern(ctx *grammar.ValDeclarationContext) (ast.Decl, error) {
	tupleCtx := ctx.TuplePattern().(*grammar.TuplePatternContext)
	namesCtx := tupleCtx.IdentifierList().(*grammar.IdentifierListContext).AllIdentifier()

	rhsExprs, err := t.transformExpressionList(ctx.ExpressionList().(*grammar.ExpressionListContext))
	if err != nil {
		return nil, err
	}

	if len(rhsExprs) != 1 {
		return nil, galaerr.NewSemanticError("tuple destructuring requires exactly one expression on the right side")
	}

	// Get the type of the tuple for type inference
	tupleType := t.getExprTypeName(rhsExprs[0])
	tupleGenericType, isGeneric := tupleType.(transpiler.GenericType)

	// Generate unique temp variable name
	tempName := fmt.Sprintf("__tuple_%d", t.nextTupleID())

	// First spec: temp variable holding the tuple
	tempSpec := &ast.ValueSpec{
		Names:  []*ast.Ident{ast.NewIdent(tempName)},
		Values: []ast.Expr{t.unwrapImmutable(rhsExprs[0])},
	}

	specs := []ast.Spec{tempSpec}

	// Create specs for each destructured variable
	for i, idCtx := range namesCtx {
		name := idCtx.GetText()

		// Determine the type of this component
		var componentType transpiler.Type = transpiler.NilType{}
		if isGeneric && i < len(tupleGenericType.Params) {
			componentType = tupleGenericType.Params[i]
			if t.isImmutableType(componentType) {
				if gen, ok := componentType.(transpiler.GenericType); ok && len(gen.Params) > 0 {
					componentType = gen.Params[0]
				}
			}
		}

		t.addVal(name, componentType)

		// Access tuple field: __tuple_N.V1, __tuple_N.V2, etc.
		fieldAccess := &ast.SelectorExpr{
			X:   ast.NewIdent(tempName),
			Sel: ast.NewIdent(fmt.Sprintf("V%d", i+1)),
		}

		// Wrap with NewImmutable
		wrappedValue := &ast.CallExpr{
			Fun:  t.stdIdent("NewImmutable"),
			Args: []ast.Expr{fieldAccess},
		}

		spec := &ast.ValueSpec{
			Names:  []*ast.Ident{ast.NewIdent(name)},
			Values: []ast.Expr{wrappedValue},
		}

		specs = append(specs, spec)
	}

	return &ast.GenDecl{
		Tok:   token.VAR,
		Specs: specs,
	}, nil
}

func (t *galaASTTransformer) transformVarDeclaration(ctx *grammar.VarDeclarationContext) (ast.Decl, error) {
	namesCtx := ctx.IdentifierList().(*grammar.IdentifierListContext).AllIdentifier()
	rhsExprs := make([]ast.Expr, 0)
	if ctx.ExpressionList() != nil {
		var err error
		rhsExprs, err = t.transformExpressionList(ctx.ExpressionList().(*grammar.ExpressionListContext))
		if err != nil {
			return nil, err
		}
	}

	var idents []*ast.Ident
	for i, idCtx := range namesCtx {
		name := idCtx.GetText()
		var typeName transpiler.Type = transpiler.NilType{}
		if ctx.Type_() != nil {
			// Try to get type name from transformed type
			typeExpr, _ := t.transformType(ctx.Type_())
			typeName = t.exprToType(typeExpr)
			t.isImmutableType(typeName) // This will panic if recursive
		} else if len(rhsExprs) == len(namesCtx) {
			typeName = t.getExprTypeName(rhsExprs[i])
		}

		if t.isImmutableType(typeName) {
			if gen, ok := typeName.(transpiler.GenericType); ok && len(gen.Params) > 0 {
				typeName = gen.Params[0]
			}
		}

		if qName := t.getType(typeName.String()); !qName.IsNil() {
			typeName = qName
		}

		t.addVar(name, typeName)
		idents = append(idents, ast.NewIdent(name))
	}

	spec := &ast.ValueSpec{
		Names: idents,
	}

	if len(rhsExprs) > 0 {
		if ctx.Type_() == nil {
			for _, r := range rhsExprs {
				if t.isNoneCall(r) {
					return nil, galaerr.NewSemanticError("variable assigned to None() must have an explicit type")
				}
			}
		}

		unwrappedRhs := make([]ast.Expr, len(rhsExprs))
		for i, r := range rhsExprs {
			unwrappedRhs[i] = t.unwrapImmutable(r)
		}
		spec.Values = unwrappedRhs
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
	var originalRecvTypeExpr ast.Expr // Keep original for cycle detection
	if ctx.Receiver() != nil {
		recvCtx := ctx.Receiver().(*grammar.ReceiverContext)
		recvName := recvCtx.Identifier().GetText()
		recvTypeExpr, err := t.transformType(recvCtx.Type_())
		if err != nil {
			return nil, err
		}
		originalRecvTypeExpr = recvTypeExpr // Store before potential Immutable wrapping

		receiverType := t.resolveType(t.getBaseTypeName(recvTypeExpr))
		receiverBaseName := receiverType.BaseName()

		// For non-pointer receivers, try to preserve type parameters for lambda type inference
		// Pointer receivers keep using the simple type to avoid field lookup issues
		typeForScope := receiverType
		if _, isPointer := recvTypeExpr.(*ast.StarExpr); !isPointer {
			if fullType := t.exprToType(recvTypeExpr); !fullType.IsNil() {
				typeForScope = fullType
			}
		}

		isVal := recvCtx.VAL() != nil
		if isVal {
			t.addVal(recvName, typeForScope)
			recvTypeExpr = &ast.IndexExpr{
				X:     t.stdIdent(transpiler.TypeImmutable),
				Index: recvTypeExpr,
			}
		} else {
			t.addVar(recvName, typeForScope)
		}

		receiver = &ast.FieldList{
			List: []*ast.Field{
				{
					Names: []*ast.Ident{ast.NewIdent(recvName)},
					Type:  recvTypeExpr,
				},
			},
		}

		if t.genericMethods[receiverBaseName] == nil {
			t.genericMethods[receiverBaseName] = make(map[string]bool)
		}
		if ctx.TypeParameters() != nil {
			t.genericMethods[receiverBaseName][name] = true
		}

		// Keep track of the base name for standalone function transformation
		receiverTypeName = receiverBaseName
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
	funcType, err := t.transformSignature(ctx.Signature().(*grammar.SignatureContext), typeParams)
	if err != nil {
		return nil, err
	}

	// Register function parameters in scope for type inference
	// This is necessary so that type inference works correctly when using parameters.
	sigCtx := ctx.Signature().(*grammar.SignatureContext)
	paramsCtx := sigCtx.Parameters().(*grammar.ParametersContext)
	if paramsCtx.ParameterList() != nil {
		for _, pCtx := range paramsCtx.ParameterList().(*grammar.ParameterListContext).AllParameter() {
			param := pCtx.(*grammar.ParameterContext)
			paramName := param.Identifier().GetText()
			var paramType transpiler.Type = transpiler.NilType{}
			if param.Type_() != nil {
				typeExpr, _ := t.transformType(param.Type_())
				paramType = t.exprToType(typeExpr)
			}
			// Check if parameter has 'val' modifier - if so, it needs .Get() unwrapping
			// Otherwise, treat as var (no .Get() unwrapping needed)
			if param.VAL() != nil {
				t.addVal(paramName, paramType)
			} else {
				t.addVar(paramName, paramType)
			}
		}
	}

	// Check if method return type would cause Go instantiation cycle
	// This happens when a method of Container[T] returns Container[SomeType[T, ...]]
	wouldCauseCycle := false
	if receiver != nil && funcType.Results != nil && len(funcType.Results.List) > 0 {
		returnType := funcType.Results.List[0].Type
		wouldCauseCycle = t.causesInstantiationCycle(originalRecvTypeExpr, returnType)
	}

	// Register method as generic if it would cause instantiation cycle
	// This ensures call sites are also transformed to function calls
	if wouldCauseCycle && receiverTypeName != "" {
		if t.genericMethods[receiverTypeName] == nil {
			t.genericMethods[receiverTypeName] = make(map[string]bool)
		}
		t.genericMethods[receiverTypeName][name] = true
	}

	if receiver != nil && (funcType.TypeParams != nil || wouldCauseCycle || (t.genericMethods[receiverTypeName] != nil && t.genericMethods[receiverTypeName][name])) {
		// Generic method or method with instantiation cycle: transform to standalone function
		identName := receiverTypeName
		if strings.HasPrefix(identName, t.packageName+".") {
			identName = strings.TrimPrefix(identName, t.packageName+".")
		}
		identName = strings.ReplaceAll(identName, ".", "_")
		if identName == "" {
			identName = "Unknown"
		}

		name = identName + "_" + name
		// 1. Add receiver as first parameter
		funcType.Params.List = append([]*ast.Field{receiver.List[0]}, funcType.Params.List...)

		// 2. Extract type parameters from receiver type and add to typeParams
		// Use originalRecvTypeExpr to avoid issues with Immutable-wrapped types
		recvTypeParams := t.extractTypeParams(originalRecvTypeExpr)
		if len(recvTypeParams) > 0 {
			if funcType.TypeParams == nil {
				funcType.TypeParams = &ast.FieldList{}
			}
			// Check for duplicates
			for _, rtp := range recvTypeParams {
				exists := false
				for _, tp := range funcType.TypeParams.List {
					if tp.Names[0].Name == rtp.Names[0].Name {
						exists = true
						break
					}
				}
				if !exists {
					funcType.TypeParams.List = append(funcType.TypeParams.List, rtp)
				}
			}
		}
		receiver = nil
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
		if funcType.Results != nil && len(funcType.Results.List) > 0 {
			expr = t.wrapWithAssertion(expr, funcType.Results.List[0].Type)
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
				// Only wrap if it's not already wrapped by transformParameter (e.g. if 'val' was explicit)
				alreadyWrapped := false
				if idxExpr, ok := field.Type.(*ast.IndexExpr); ok {
					if sel, ok := idxExpr.X.(*ast.SelectorExpr); ok {
						// Check if it's std.Immutable or any package's Immutable
						if sel.Sel.Name == transpiler.TypeImmutable {
							alreadyWrapped = true
						}
					}
				}

				if !alreadyWrapped {
					field.Type = &ast.IndexExpr{
						X:     t.stdIdent("Immutable"),
						Index: field.Type,
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
		t.structFieldTypes[name] = make(map[string]transpiler.Type)
	}
	if paramsCtx.ParameterList() != nil {
		for _, pCtx := range paramsCtx.ParameterList().(*grammar.ParameterListContext).AllParameter() {
			param := pCtx.(*grammar.ParameterContext)
			pName := param.Identifier().GetText()
			var pType transpiler.Type = transpiler.NilType{}
			if param.Type_() != nil {
				typeExpr, _ := t.transformType(param.Type_())
				pType = t.resolveType(t.getBaseTypeName(typeExpr))
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

	// Check if Unapply already exists
	hasUnapply := false
	if meta := t.getTypeMeta(name); meta != nil {
		if _, ok := meta.Methods["Unapply"]; ok {
			hasUnapply = true
		}
	}

	if !hasUnapply {
		unapplyMethod, err := t.generateUnapplyMethod(name, fields, nil)
		if err != nil {
			return nil, err
		}
		if unapplyMethod != nil {
			decls = append(decls, unapplyMethod)
		}
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
			t.structFieldTypes[name] = make(map[string]transpiler.Type)
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

				var fType transpiler.Type = transpiler.NilType{}
				if fCtx.(*grammar.StructFieldContext).Type_() != nil {
					typeExpr, _ := t.transformType(fCtx.(*grammar.StructFieldContext).Type_())
					fType = t.exprToType(typeExpr)
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

		// For generic structs, generate marker interface for wildcard pattern matching
		if tParams != nil {
			interfaceDecl, markerMethod := t.generateInstanceMarker(name, tParams)
			decls = append(decls, interfaceDecl, markerMethod)
		}

		// Check if Unapply already exists
		hasUnapply := false
		if meta := t.getTypeMeta(name); meta != nil {
			if _, ok := meta.Methods["Unapply"]; ok {
				hasUnapply = true
			}
		}

		if !hasUnapply {
			unapplyMethod, err := t.generateUnapplyMethod(name, fields, tParams)
			if err != nil {
				return nil, err
			}
			if unapplyMethod != nil {
				decls = append(decls, unapplyMethod)
			}
		}

	} else if ctx.InterfaceType() != nil {
		interfaceType, err := t.transformInterfaceType(ctx.InterfaceType().(*grammar.InterfaceTypeContext))
		if err != nil {
			return nil, err
		}

		typeSpec := &ast.TypeSpec{
			Name:       ast.NewIdent(name),
			TypeParams: tParams,
			Type:       interfaceType,
		}

		decls = append(decls, &ast.GenDecl{
			Tok:   token.TYPE,
			Specs: []ast.Spec{typeSpec},
		})
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
		path := strings.Trim(s.STRING().GetText(), "\"")
		importSpec := &ast.ImportSpec{
			Path: &ast.BasicLit{Kind: token.STRING, Value: s.STRING().GetText()},
		}
		if s.Identifier() != nil {
			alias := s.Identifier().GetText()
			importSpec.Name = ast.NewIdent(alias)
			t.importManager.Add(path, alias, false, "")
		} else if s.GetChildCount() > 1 {
			// Check for '.'
			if dot := s.GetChild(0); dot != nil {
				if terminal, ok := dot.(antlr.TerminalNode); ok && terminal.GetText() == "." {
					importSpec.Name = ast.NewIdent(".")
					t.importManager.Add(path, "", true, "")
				}
			}
		} else {
			// No alias, use the last part of path as package name
			t.importManager.Add(path, "", false, "")
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

	var typeName transpiler.Type = transpiler.NilType{}
	if ctx.Type_() != nil {
		typeExpr, _ := t.transformType(ctx.Type_())
		typeName = t.exprToType(typeExpr)
	}
	isVal := ctx.VAL() != nil
	isVariadic := ctx.ELLIPSIS() != nil
	if qName := t.getType(typeName.String()); !qName.IsNil() {
		typeName = qName
	}
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
		typeName = t.exprToType(typ)
		t.isImmutableType(typeName)
		if isVariadic {
			// Variadic parameter: ...T becomes ...T in Go
			field.Type = &ast.Ellipsis{Elt: typ}
		} else if isVal {
			field.Type = &ast.IndexExpr{
				X:     t.stdIdent("Immutable"),
				Index: typ,
			}
		} else {
			field.Type = typ
		}
	} else {
		// Default to any if type is not specified
		if isVariadic {
			field.Type = &ast.Ellipsis{Elt: ast.NewIdent("any")}
		} else {
			field.Type = ast.NewIdent("any")
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
	t.isImmutableType(t.exprToType(typ))

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

func (t *galaASTTransformer) transformSignature(ctx *grammar.SignatureContext, typeParams *ast.FieldList) (*ast.FuncType, error) {
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

	var results *ast.FieldList
	if ctx.Type_() != nil {
		retType, err := t.transformType(ctx.Type_())
		if err != nil {
			return nil, err
		}
		t.isImmutableType(t.exprToType(retType))
		results = &ast.FieldList{
			List: []*ast.Field{
				{Type: retType},
			},
		}
	}

	return &ast.FuncType{
		TypeParams: typeParams,
		Params:     fieldList,
		Results:    results,
	}, nil
}

// transformFuncTypeSignature transforms a function type's signature (used in type positions like func(T) bool).
// In function types, parameters without explicit types should be treated as anonymous params with the identifier as the type.
func (t *galaASTTransformer) transformFuncTypeSignature(ctx *grammar.SignatureContext) (*ast.FuncType, error) {
	paramsCtx := ctx.Parameters().(*grammar.ParametersContext)

	fieldList := &ast.FieldList{}
	if paramsCtx.ParameterList() != nil {
		for _, pCtx := range paramsCtx.ParameterList().(*grammar.ParameterListContext).AllParameter() {
			paramCtx := pCtx.(*grammar.ParameterContext)
			field := &ast.Field{}

			if paramCtx.Type_() != nil {
				// Explicit type provided: `name type` -> use type for the field
				typ, err := t.transformType(paramCtx.Type_())
				if err != nil {
					return nil, err
				}
				field.Type = typ
			} else {
				// No explicit type: treat identifier as the type (for function types like func(T) bool)
				typeName := paramCtx.Identifier().GetText()
				// Check if this identifier resolves to a known type
				resolvedType := t.getType(typeName)
				if !resolvedType.IsNil() {
					if pkg := resolvedType.GetPackage(); pkg != "" && pkg != t.packageName {
						if pkg == registry.StdPackageName {
							field.Type = t.stdIdent(typeName)
						} else if alias, ok := t.importManager.GetAlias(pkg); ok {
							field.Type = &ast.SelectorExpr{X: ast.NewIdent(alias), Sel: ast.NewIdent(typeName)}
						} else {
							field.Type = ast.NewIdent(typeName)
						}
					} else {
						field.Type = ast.NewIdent(typeName)
					}
				} else {
					field.Type = ast.NewIdent(typeName)
				}
			}

			if paramCtx.ELLIPSIS() != nil {
				field.Type = &ast.Ellipsis{Elt: field.Type}
			}

			fieldList.List = append(fieldList.List, field)
		}
	}

	var results *ast.FieldList
	if ctx.Type_() != nil {
		retType, err := t.transformType(ctx.Type_())
		if err != nil {
			return nil, err
		}
		results = &ast.FieldList{
			List: []*ast.Field{
				{Type: retType},
			},
		}
	}

	return &ast.FuncType{
		Params:  fieldList,
		Results: results,
	}, nil
}

func (t *galaASTTransformer) transformInterfaceType(ctx *grammar.InterfaceTypeContext) (*ast.InterfaceType, error) {
	methods := &ast.FieldList{}
	for _, mCtx := range ctx.AllMethodSpec() {
		spec := mCtx.(*grammar.MethodSpecContext)
		name := spec.Identifier().GetText()
		sig := spec.Signature().(*grammar.SignatureContext)

		// Check for method-level type parameters
		var typeParams *ast.FieldList
		if spec.TypeParameters() != nil {
			var err error
			typeParams, err = t.transformTypeParameters(spec.TypeParameters().(*grammar.TypeParametersContext))
			if err != nil {
				return nil, err
			}
		}

		funcType, err := t.transformSignature(sig, typeParams)
		if err != nil {
			return nil, err
		}

		methods.List = append(methods.List, &ast.Field{
			Names: []*ast.Ident{ast.NewIdent(name)},
			Type:  funcType,
		})
	}

	return &ast.InterfaceType{
		Methods: methods,
	}, nil
}
