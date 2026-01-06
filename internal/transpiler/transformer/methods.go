package transformer

import (
	"fmt"
	"go/ast"
	"go/token"
	"martianoff/gala/galaerr"
	"martianoff/gala/internal/parser/grammar"
	"martianoff/gala/internal/transpiler"
)

func (t *galaASTTransformer) transformCopyCall(receiver ast.Expr, argListCtx *grammar.ArgumentListContext) (ast.Expr, error) {
	// 1. Identify receiver type
	var typeName string
	if id, ok := receiver.(*ast.Ident); ok {
		typeName = t.getType(id.Name)
	} else if call, ok := receiver.(*ast.CallExpr); ok {
		// Handle p.Get().Copy() case where p is std.Immutable[T]
		if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == transpiler.MethodGet {
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
		pat := arg.Pattern()
		ep, ok := pat.(*grammar.ExpressionPatternContext)
		if !ok {
			return nil, galaerr.NewSemanticError("Copy overrides must be expressions")
		}
		val, err := t.transformExpression(ep.Expression())
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
				finalVal = &ast.CallExpr{
					Fun:  t.stdIdent(transpiler.FuncNewImmutable),
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
					Fun: t.stdIdent(transpiler.FuncCopy),
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

func (t *galaASTTransformer) initGenericMethods() {
	t.genericMethods = make(map[string]map[string]bool)
	t.structFieldTypes = make(map[string]map[string]string)
	t.functions = make(map[string]*transpiler.FunctionMetadata)
	t.typeMetas = make(map[string]*transpiler.TypeMetadata)
}

func (t *galaASTTransformer) generateCopyMethod(name string, fields *ast.FieldList, tParams *ast.FieldList) (*ast.FuncDecl, error) {
	var elts []ast.Expr
	for _, field := range fields.List {
		for _, fieldName := range field.Names {
			elts = append(elts, &ast.KeyValueExpr{
				Key: ast.NewIdent(fieldName.Name),
				Value: &ast.CallExpr{
					Fun: t.stdIdent("Copy"),
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
				Fun: t.stdIdent("Equal"),
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

func (t *galaASTTransformer) generateUnapplyMethod(name string, fields *ast.FieldList, tParams *ast.FieldList) (*ast.FuncDecl, error) {
	if meta, ok := t.typeMetas[name]; ok {
		if _, ok := meta.Methods["Unapply"]; ok {
			return nil, nil
		}
	}

	// Only generate Unapply if all fields are Public (exported)
	for _, field := range fields.List {
		for _, fieldName := range field.Names {
			if !ast.IsExported(fieldName.Name) {
				return nil, nil
			}
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
		if len(indices) == 1 {
			retType = &ast.IndexExpr{
				X:     ast.NewIdent(name),
				Index: indices[0],
			}
		} else if len(indices) > 1 {
			retType = &ast.IndexListExpr{
				X:       ast.NewIdent(name),
				Indices: indices,
			}
		}
	}

	var results []*ast.Field
	var retVals []ast.Expr
	var zeroVals []ast.Expr

	hasFields := false
	for _, field := range fields.List {
		for _, fieldName := range field.Names {
			hasFields = true
			results = append(results, &ast.Field{Type: field.Type})
			retVals = append(retVals, &ast.SelectorExpr{
				X:   ast.NewIdent("p"),
				Sel: ast.NewIdent(fieldName.Name),
			})
			// Zero value trick: *new(Type)
			zeroVals = append(zeroVals, &ast.StarExpr{
				X: &ast.CallExpr{
					Fun:  ast.NewIdent("new"),
					Args: []ast.Expr{field.Type},
				},
			})
		}
	}
	results = append(results, &ast.Field{Type: ast.NewIdent("bool")})
	retVals = append(retVals, ast.NewIdent("true"))
	zeroVals = append(zeroVals, ast.NewIdent("false"))

	pName := "p"
	if !hasFields {
		pName = "_"
	}

	body := []ast.Stmt{
		&ast.IfStmt{
			Init: &ast.AssignStmt{
				Lhs: []ast.Expr{ast.NewIdent(pName), ast.NewIdent("ok")},
				Tok: token.DEFINE,
				Rhs: []ast.Expr{
					&ast.TypeAssertExpr{
						X:    ast.NewIdent("v"),
						Type: retType,
					},
				},
			},
			Cond: ast.NewIdent("ok"),
			Body: &ast.BlockStmt{
				List: []ast.Stmt{
					&ast.ReturnStmt{Results: retVals},
				},
			},
		},
		&ast.IfStmt{
			Init: &ast.AssignStmt{
				Lhs: []ast.Expr{ast.NewIdent(pName), ast.NewIdent("ok")},
				Tok: token.DEFINE,
				Rhs: []ast.Expr{
					&ast.TypeAssertExpr{
						X:    ast.NewIdent("v"),
						Type: &ast.StarExpr{X: retType},
					},
				},
			},
			Cond: &ast.BinaryExpr{
				X:  ast.NewIdent("ok"),
				Op: token.LAND,
				Y: &ast.BinaryExpr{
					X:  ast.NewIdent("p"), // still use p here for nil check if it exists
					Op: token.NEQ,
					Y:  ast.NewIdent("nil"),
				},
			},
			Body: &ast.BlockStmt{
				List: []ast.Stmt{
					&ast.ReturnStmt{Results: retVals},
				},
			},
		},
		&ast.ReturnStmt{Results: zeroVals},
	}

	// Update the nil check if p is _
	if !hasFields {
		ifStmt := body[1].(*ast.IfStmt)
		ifStmt.Cond = ast.NewIdent("ok") // No nil check needed if we don't use p
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
		Name: ast.NewIdent("Unapply"),
		Type: &ast.FuncType{
			Params: &ast.FieldList{
				List: []*ast.Field{
					{
						Names: []*ast.Ident{ast.NewIdent("v")},
						Type:  ast.NewIdent("any"),
					},
				},
			},
			Results: &ast.FieldList{List: results},
		},
		Body: &ast.BlockStmt{List: body},
	}, nil
}

func (t *galaASTTransformer) generateGenericInterface(name string, fields *ast.FieldList, tParams *ast.FieldList) ([]ast.Decl, error) {
	if tParams == nil || len(tParams.List) == 0 {
		return nil, nil
	}

	interfaceName := name + "Interface"
	var methods []*ast.Field

	for _, field := range fields.List {
		for _, fieldName := range field.Names {
			// Getter method: Get_<FieldName>() any
			methods = append(methods, &ast.Field{
				Names: []*ast.Ident{ast.NewIdent("Get_" + fieldName.Name)},
				Type: &ast.FuncType{
					Params: &ast.FieldList{},
					Results: &ast.FieldList{
						List: []*ast.Field{
							{Type: ast.NewIdent("any")},
						},
					},
				},
			})
		}
	}

	interfaceDecl := &ast.GenDecl{
		Tok: token.TYPE,
		Specs: []ast.Spec{
			&ast.TypeSpec{
				Name: ast.NewIdent(interfaceName),
				Type: &ast.InterfaceType{
					Methods: &ast.FieldList{List: methods},
				},
			},
		},
	}

	var decls []ast.Decl
	decls = append(decls, interfaceDecl)

	// Receiver type (Name[T])
	var indices []ast.Expr
	for _, p := range tParams.List {
		for _, n := range p.Names {
			indices = append(indices, ast.NewIdent(n.Name))
		}
	}
	var recvType ast.Expr = ast.NewIdent(name)
	if len(indices) == 1 {
		recvType = &ast.IndexExpr{X: ast.NewIdent(name), Index: indices[0]}
	} else if len(indices) > 1 {
		recvType = &ast.IndexListExpr{X: ast.NewIdent(name), Indices: indices}
	}

	// Implement methods for the generic struct
	for _, field := range fields.List {
		for _, fieldName := range field.Names {
			var bodyExpr ast.Expr = &ast.SelectorExpr{
				X:   ast.NewIdent("r"),
				Sel: ast.NewIdent(fieldName.Name),
			}

			// Check if it's an immutable field (wrapped in std.Immutable)
			isImmut := false
			if idx, ok := field.Type.(*ast.IndexExpr); ok {
				if sel, ok := idx.X.(*ast.SelectorExpr); ok {
					if id, ok := sel.X.(*ast.Ident); ok && id.Name == transpiler.StdPackage && sel.Sel.Name == "Immutable" {
						isImmut = true
					}
				} else if id, ok := idx.X.(*ast.Ident); ok {
					if id.Name == "Immutable" {
						isImmut = true
					}
				}
			}

			if isImmut {
				bodyExpr = &ast.CallExpr{
					Fun: &ast.SelectorExpr{
						X:   bodyExpr,
						Sel: ast.NewIdent("Get"),
					},
				}
			}

			method := &ast.FuncDecl{
				Recv: &ast.FieldList{
					List: []*ast.Field{
						{
							Names: []*ast.Ident{ast.NewIdent("r")},
							Type:  recvType,
						},
					},
				},
				Name: ast.NewIdent("Get_" + fieldName.Name),
				Type: &ast.FuncType{
					Params: &ast.FieldList{},
					Results: &ast.FieldList{
						List: []*ast.Field{
							{Type: ast.NewIdent("any")},
						},
					},
				},
				Body: &ast.BlockStmt{
					List: []ast.Stmt{
						&ast.ReturnStmt{
							Results: []ast.Expr{bodyExpr},
						},
					},
				},
			}
			decls = append(decls, method)
		}
	}

	return decls, nil
}
