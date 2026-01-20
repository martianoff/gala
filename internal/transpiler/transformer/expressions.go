package transformer

import (
	"fmt"
	"github.com/antlr4-go/antlr/v4"
	"go/ast"
	"go/token"
	"martianoff/gala/galaerr"
	"martianoff/gala/internal/parser/grammar"
	"martianoff/gala/internal/transpiler"
	"strings"
)

func (t *galaASTTransformer) transformCallExpr(ctx *grammar.ExpressionContext) (ast.Expr, error) {
	// expression '(' argumentList? ')'
	child1 := ctx.GetChild(0)
	x, err := t.transformExpression(child1.(grammar.IExpressionContext))
	if err != nil {
		return nil, err
	}

	// Get argument list if present (for calls with arguments)
	var argListCtx *grammar.ArgumentListContext
	if ctx.GetChildCount() >= 3 {
		if alCtx, ok := ctx.GetChild(2).(*grammar.ArgumentListContext); ok {
			argListCtx = alCtx
		}
	}

	// Handle Copy method call with overrides
	if argListCtx != nil {
		if sel, ok := x.(*ast.SelectorExpr); ok && sel.Sel.Name == "Copy" {
			return t.transformCopyCall(sel.X, argListCtx)
		}
	}

	// Handle generic method calls or monadic methods: o.Map[T](f) -> Map[T](o, f)
	// This must happen for BOTH zero-argument and argument calls
	var receiver ast.Expr
	var method string
	var typeArgs []ast.Expr

	if sel, ok := x.(*ast.SelectorExpr); ok {
		if id, ok := sel.X.(*ast.Ident); ok && id.Name == transpiler.StdPackage {
			// Not a method call
		} else {
			receiver = sel.X
			method = sel.Sel.Name
		}
	} else if idx, ok := x.(*ast.IndexExpr); ok {
		if sel, ok := idx.X.(*ast.SelectorExpr); ok {
			if id, ok := sel.X.(*ast.Ident); ok && id.Name == transpiler.StdPackage {
				// Not a method call
			} else {
				receiver = sel.X
				method = sel.Sel.Name
				typeArgs = []ast.Expr{idx.Index}
			}
		}
	} else if idxList, ok := x.(*ast.IndexListExpr); ok {
		if sel, ok := idxList.X.(*ast.SelectorExpr); ok {
			if id, ok := sel.X.(*ast.Ident); ok && id.Name == transpiler.StdPackage {
				// Not a method call
			} else {
				receiver = sel.X
				method = sel.Sel.Name
				typeArgs = idxList.Indices
			}
		}
	}

	recvType := t.getExprTypeName(receiver)
	if qName := t.getType(recvType.BaseName()); !qName.IsNil() {
		recvType = qName
	}
	recvBaseName := recvType.BaseName()
	isGenericMethod := len(typeArgs) > 0 || (recvBaseName != "" && t.genericMethods[recvBaseName] != nil && t.genericMethods[recvBaseName][method])

	if receiver != nil && isGenericMethod {
		// Check if receiver is a package name
		isPkg := false
		if id, ok := receiver.(*ast.Ident); ok {
			if _, ok := t.imports[id.Name]; ok {
				isPkg = true
			}
		}

		if !isPkg {
			// Transform generic method call to standalone function call
			var mArgs []ast.Expr
			if argListCtx != nil {
				for _, argCtx := range argListCtx.AllArgument() {
					arg := argCtx.(*grammar.ArgumentContext)
					pat := arg.Pattern()
					ep, ok := pat.(*grammar.ExpressionPatternContext)
					if !ok {
						return nil, galaerr.NewSemanticError("only expressions allowed as function arguments")
					}
					expr, err := t.transformExpression(ep.Expression())
					if err != nil {
						return nil, err
					}
					mArgs = append(mArgs, expr)
				}
			}

			var fun ast.Expr
			if !recvType.IsNil() {
				recvPkg := recvType.GetPackage()
				if recvPkg == transpiler.StdPackage || strings.HasPrefix(recvBaseName, "std.") {
					// Receiver is from std package
					baseName := strings.TrimPrefix(recvBaseName, "std.")
					fun = t.stdIdent(baseName + "_" + method)
				} else {
					fun = t.ident(recvBaseName + "_" + method)
				}
			} else {
				fun = ast.NewIdent(method)
			}

			if len(typeArgs) == 1 {
				fun = &ast.IndexExpr{X: fun, Index: typeArgs[0]}
			} else if len(typeArgs) > 1 {
				fun = &ast.IndexListExpr{X: fun, Indices: typeArgs}
			}

			return &ast.CallExpr{
				Fun:  fun,
				Args: append([]ast.Expr{receiver}, mArgs...),
			}, nil
		}
	}

	// Regular call: parse arguments
	var args []ast.Expr
	var namedArgs map[string]ast.Expr
	if argListCtx != nil {
		for _, argCtx := range argListCtx.AllArgument() {
			arg := argCtx.(*grammar.ArgumentContext)
			pat := arg.Pattern()
			ep, ok := pat.(*grammar.ExpressionPatternContext)
			if !ok {
				return nil, galaerr.NewSemanticError("only expressions allowed as function arguments")
			}
			expr, err := t.transformExpression(ep.Expression())
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

	// Handle case where we have TypeName(...) which is a constructor call
	// GALA doesn't seem to have a specific rule for constructor calls,
	// but TypeName(...) should be transformed to TypeName{...} if it's a struct.
	rawTypeName := t.getBaseTypeName(x)
	var typeObj transpiler.Type = transpiler.NilType{}
	if rawTypeName != "" {
		typeObj = t.getType(rawTypeName)
		if typeObj.IsNil() {
			typeObj = transpiler.ParseType(rawTypeName)
		}
	}
	typeName := typeObj.String()
	typeExpr := x

	if typeName != "" {
		if fieldNames, ok := t.structFields[typeName]; ok {
			// Check if we should treat it as a constructor or Apply call
			isType := false
			baseExpr := x
			if idx, ok := x.(*ast.IndexExpr); ok {
				baseExpr = idx.X
			} else if idxList, ok := x.(*ast.IndexListExpr); ok {
				baseExpr = idxList.X
			}

			if id, ok := baseExpr.(*ast.Ident); ok {
				if !t.isVal(id.Name) && !t.isVar(id.Name) {
					if !t.getType(id.Name).IsNil() {
						isType = true
					}
				}
			} else if sel, ok := baseExpr.(*ast.SelectorExpr); ok {
				if id, ok := sel.X.(*ast.Ident); ok {
					if _, isPkg := t.imports[id.Name]; isPkg {
						isType = true
					}
				}
			}

			// If it's a type and has fields, it's definitely a constructor
			// If it's a type and has no fields, but has Apply, it's an Apply call on Type{}
			if isType && len(fieldNames) > 0 {
				var elts []ast.Expr
				immutFlags := t.structImmutFields[typeName]

				// Check if this is a generic type without explicit type parameters
				// If so, we need to infer the type parameters from field values
				// Try both rawTypeName and qualified typeName for lookup
				typeMeta, hasTypeMeta := t.typeMetas[typeName]
				if !hasTypeMeta {
					typeMeta, hasTypeMeta = t.typeMetas[rawTypeName]
				}
				hasExplicitTypeArgs := false
				if _, ok := x.(*ast.IndexExpr); ok {
					hasExplicitTypeArgs = true
				} else if _, ok := x.(*ast.IndexListExpr); ok {
					hasExplicitTypeArgs = true
				}

				if hasTypeMeta && len(typeMeta.TypeParams) > 0 && !hasExplicitTypeArgs {
					// Need to infer type parameters from field values
					typeParamToType := make(map[string]transpiler.Type)

					// Build mapping of field values to use for inference
					fieldValues := make(map[string]ast.Expr)
					if namedArgs != nil {
						fieldValues = namedArgs
					} else {
						for i, arg := range args {
							if i < len(fieldNames) {
								fieldValues[fieldNames[i]] = arg
							}
						}
					}

					// For each field, check if its type uses a type parameter
					// and infer the concrete type from the field value
					for fieldName, fieldType := range typeMeta.Fields {
						if val, ok := fieldValues[fieldName]; ok {
							// Check if fieldType is a type parameter
							fieldTypeStr := fieldType.String()
							for _, tp := range typeMeta.TypeParams {
								if fieldTypeStr == tp {
									// This field uses a type parameter, infer its type from the value
									valType := t.getExprTypeName(val)
									if valType != nil && !valType.IsNil() {
										typeParamToType[tp] = valType
									}
								}
							}
						}
					}

					// Build the new typeExpr with inferred type arguments
					if len(typeParamToType) > 0 {
						var inferredTypeArgs []ast.Expr
						for _, tp := range typeMeta.TypeParams {
							if inferredType, ok := typeParamToType[tp]; ok {
								inferredTypeArgs = append(inferredTypeArgs, t.typeToExpr(inferredType))
							} else {
								// Fallback to any if we can't infer
								inferredTypeArgs = append(inferredTypeArgs, ast.NewIdent("any"))
							}
						}

						if len(inferredTypeArgs) == 1 {
							typeExpr = &ast.IndexExpr{
								X:     baseExpr,
								Index: inferredTypeArgs[0],
							}
						} else if len(inferredTypeArgs) > 1 {
							typeExpr = &ast.IndexListExpr{
								X:       baseExpr,
								Indices: inferredTypeArgs,
							}
						}
					}
				}

				// Build a map of type parameter -> instantiated type from explicit type arguments
				typeParamInstantiations := make(map[string]string)
				if hasTypeMeta && hasExplicitTypeArgs {
					if idxExpr, ok := x.(*ast.IndexExpr); ok {
						// Single type argument
						if len(typeMeta.TypeParams) > 0 {
							if id, ok := idxExpr.Index.(*ast.Ident); ok {
								typeParamInstantiations[typeMeta.TypeParams[0]] = id.Name
							}
						}
					} else if idxList, ok := x.(*ast.IndexListExpr); ok {
						// Multiple type arguments
						for i, idx := range idxList.Indices {
							if i < len(typeMeta.TypeParams) {
								if id, ok := idx.(*ast.Ident); ok {
									typeParamInstantiations[typeMeta.TypeParams[i]] = id.Name
								}
							}
						}
					}
				}

				// Helper to check if field type resolves to 'any' in the current instantiation
				isFieldTypeAny := func(fieldName string) bool {
					if hasTypeMeta {
						if fType, ok := typeMeta.Fields[fieldName]; ok {
							fTypeStr := fType.String()
							// If field type is directly 'any'
							if fTypeStr == "any" {
								return true
							}
							// If field type is a type parameter, check what it was instantiated to
							if instantiatedType, ok := typeParamInstantiations[fTypeStr]; ok {
								return instantiatedType == "any"
							}
						}
					}
					return false
				}

				if namedArgs != nil {
					for i, fn := range fieldNames {
						if val, ok := namedArgs[fn]; ok {
							if i < len(immutFlags) && immutFlags[i] {
								// Only wrap if value is not already Immutable
								valType := t.getExprTypeName(val)
								if !t.isImmutableType(valType) {
									// If field type is 'any', cast value to any first
									if isFieldTypeAny(fn) {
										val = &ast.CallExpr{
											Fun: &ast.IndexExpr{
												X:     t.stdIdent(transpiler.FuncNewImmutable),
												Index: ast.NewIdent("any"),
											},
											Args: []ast.Expr{&ast.CallExpr{
												Fun:  ast.NewIdent("any"),
												Args: []ast.Expr{val},
											}},
										}
									} else {
										val = &ast.CallExpr{
											Fun:  t.stdIdent(transpiler.FuncNewImmutable),
											Args: []ast.Expr{val},
										}
									}
								}
							}
							elts = append(elts, &ast.KeyValueExpr{
								Key:   ast.NewIdent(fn),
								Value: val,
							})
						}
					}
				} else {
					for i, arg := range args {
						if i < len(fieldNames) {
							if i < len(immutFlags) && immutFlags[i] {
								// Only wrap if value is not already Immutable
								argType := t.getExprTypeName(arg)
								if !t.isImmutableType(argType) {
									// If field type is 'any', cast value to any first
									if isFieldTypeAny(fieldNames[i]) {
										arg = &ast.CallExpr{
											Fun: &ast.IndexExpr{
												X:     t.stdIdent(transpiler.FuncNewImmutable),
												Index: ast.NewIdent("any"),
											},
											Args: []ast.Expr{&ast.CallExpr{
												Fun:  ast.NewIdent("any"),
												Args: []ast.Expr{arg},
											}},
										}
									} else {
										arg = &ast.CallExpr{
											Fun:  t.stdIdent(transpiler.FuncNewImmutable),
											Args: []ast.Expr{arg},
										}
									}
								}
							}
							elts = append(elts, &ast.KeyValueExpr{
								Key:   ast.NewIdent(fieldNames[i]),
								Value: arg,
							})
						}
					}
				}
				return &ast.CompositeLit{
					Type: typeExpr,
					Elts: elts,
				}, nil
			}
		}
	}

	// Check if the expression being called has an Apply method
	exprType := t.getExprTypeName(x)
	if exprType.IsNil() {
		exprType = typeObj
	}
	exprBaseName := exprType.BaseName()
	if exprBaseName != "" {
		// Try to find type metadata - check both raw name and std-prefixed name
		typeMeta, ok := t.typeMetas[exprBaseName]
		if !ok && !strings.HasPrefix(exprBaseName, "std.") {
			typeMeta, ok = t.typeMetas["std."+exprBaseName]
			if ok {
				exprBaseName = "std." + exprBaseName
			}
		}
		if ok {
			if methodMeta, hasApply := typeMeta.Methods["Apply"]; hasApply {
				isGeneric := methodMeta.IsGeneric || len(methodMeta.TypeParams) > 0
				if isGeneric {
					fullName := exprBaseName + "_Apply"
					var fun ast.Expr
					// Check if type belongs to std package using resolution
					isStdType := strings.HasPrefix(exprBaseName, "std.")
					if !isStdType {
						resolvedType := t.getType(exprBaseName)
						isStdType = !resolvedType.IsNil() && resolvedType.GetPackage() == transpiler.StdPackage
					}
					if isStdType {
						fun = t.stdIdent(strings.TrimPrefix(fullName, "std."))
					} else {
						fun = t.ident(fullName)
					}

					// Extract type arguments if any
					var typeArgs []ast.Expr
					realX := x
					if idx, ok := x.(*ast.IndexExpr); ok {
						typeArgs = []ast.Expr{idx.Index}
						realX = idx.X
					} else if idxList, ok := x.(*ast.IndexListExpr); ok {
						typeArgs = idxList.Indices
						realX = idxList.X
					}

					if len(typeArgs) == 1 {
						fun = &ast.IndexExpr{X: fun, Index: typeArgs[0]}
					} else if len(typeArgs) > 1 {
						fun = &ast.IndexListExpr{X: fun, Indices: typeArgs}
					}

					receiver := realX
					isType := false
					baseExpr := realX

					if id, ok := baseExpr.(*ast.Ident); ok {
						if !t.isVal(id.Name) && !t.isVar(id.Name) {
							if !t.getType(id.Name).IsNil() {
								isType = true
							}
						}
					} else if sel, ok := baseExpr.(*ast.SelectorExpr); ok {
						if id, ok := sel.X.(*ast.Ident); ok {
							if _, isPkg := t.imports[id.Name]; isPkg {
								isType = true
							}
						}
					}

					if isType {
						receiver = &ast.CompositeLit{Type: realX}
					}

					return &ast.CallExpr{
						Fun:  fun,
						Args: append([]ast.Expr{receiver}, args...),
					}, nil
				}

				receiver := x
				isType := false
				baseExpr := x
				hasTypeArgs := false
				if idx, ok := x.(*ast.IndexExpr); ok {
					baseExpr = idx.X
					hasTypeArgs = true
				} else if idxList, ok := x.(*ast.IndexListExpr); ok {
					baseExpr = idxList.X
					hasTypeArgs = true
				}

				if id, ok := baseExpr.(*ast.Ident); ok {
					if !t.isVal(id.Name) && !t.isVar(id.Name) {
						if !t.getType(id.Name).IsNil() {
							isType = true
						}
					}
				} else if sel, ok := baseExpr.(*ast.SelectorExpr); ok {
					if id, ok := sel.X.(*ast.Ident); ok {
						if _, isPkg := t.imports[id.Name]; isPkg {
							isType = true
						}
					}
				}

				if isType {
					typeExpr := x
					// If the type has type parameters but no type arguments were provided,
					// infer them from the Apply method parameter types and actual arguments
					if !hasTypeArgs && len(typeMeta.TypeParams) > 0 {
						var typeArgExprs []ast.Expr
						if len(methodMeta.ParamTypes) > 0 && len(args) > 0 {
							// Try to infer type arguments from the first argument's type
							// e.g., Some(10) -> Some[int]{}.Apply(10)
							inferredTypes := t.inferTypeArgsFromApply(typeMeta, methodMeta, args)
							if len(inferredTypes) == len(typeMeta.TypeParams) {
								typeArgExprs = make([]ast.Expr, len(inferredTypes))
								for i, tp := range inferredTypes {
									typeArgExprs[i] = t.typeToExpr(tp)
								}
							}
						}
						// If inference failed (or no args), fall back to 'any' for each type param
						if len(typeArgExprs) == 0 {
							typeArgExprs = make([]ast.Expr, len(typeMeta.TypeParams))
							for i := range typeMeta.TypeParams {
								typeArgExprs[i] = ast.NewIdent("any")
							}
						}
						if len(typeArgExprs) == 1 {
							typeExpr = &ast.IndexExpr{X: baseExpr, Index: typeArgExprs[0]}
						} else {
							typeExpr = &ast.IndexListExpr{X: baseExpr, Indices: typeArgExprs}
						}
					}
					receiver = &ast.CompositeLit{Type: typeExpr}
				}

				return &ast.CallExpr{
					Fun: &ast.SelectorExpr{
						X:   receiver,
						Sel: ast.NewIdent("Apply"),
					},
					Args: args,
				}, nil
			}
		}
	}

	if namedArgs != nil {
		return nil, galaerr.NewSemanticError(fmt.Sprintf("named arguments only supported for Copy method or struct construction (type: %s)", typeName))
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
			if opText == "!" {
				expr = t.wrapWithAssertion(expr, ast.NewIdent("bool"))
			}
			// Automatic unwrapping for unary operands
			if opText != "&" {
				expr = t.unwrapImmutable(expr)
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
			// Don't unwrap if we're accessing Immutable's own fields/methods
			xType := t.getExprTypeName(x)
			isImmutable := t.isImmutableType(xType)

			if !isImmutable || (selName != "Get" && selName != "value") {
				x = t.unwrapImmutable(x)
			}

			selExpr := &ast.SelectorExpr{
				X:   x,
				Sel: ast.NewIdent(selName),
			}

			// Re-evaluate type after potential unwrap
			xType = t.getExprTypeName(x)
			xTypeName := xType.String()
			baseTypeName := xTypeName
			if idx := strings.Index(xTypeName, "["); idx != -1 {
				baseTypeName = xTypeName[:idx]
			}
			// Strip pointer prefix for struct field lookup
			baseTypeName = strings.TrimPrefix(baseTypeName, "*")

			// Try to find struct fields - check multiple name variants
			// Maps are keyed by fully qualified name (e.g., "collection_immutable.List")
			// but baseTypeName might just be "List"
			resolvedTypeName := t.resolveStructTypeName(baseTypeName)
			if fields, ok := t.structFields[resolvedTypeName]; ok {
				for i, f := range fields {
					if f == selName {
						if t.structImmutFields[resolvedTypeName][i] {
							return &ast.CallExpr{
								Fun: &ast.SelectorExpr{
									X:   selExpr,
									Sel: ast.NewIdent("Get"),
								},
							}, nil
						}
						break
					}
				}
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
			// Automatic unwrapping for binary operands
			left = t.unwrapImmutable(left)
			right = t.unwrapImmutable(right)
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
			x = t.unwrapImmutable(x)
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

		// First check if it's a local variable - if so, don't try to resolve as std type
		if t.isVal(name) || t.isVar(name) {
			if t.isVal(name) {
				return &ast.CallExpr{
					Fun: &ast.SelectorExpr{
						X:   ident,
						Sel: ast.NewIdent(transpiler.MethodGet),
					},
				}, nil
			}
			return ident, nil
		}

		// Check if this identifier is a std package type (not a variable with std type)
		// Only check typeMetas directly to see if std.name exists as a type definition
		if _, isStdType := t.typeMetas["std."+name]; isStdType {
			return t.stdIdent(name), nil
		}
		// Check if it's a std function (from metadata)
		resolvedFunc := t.getFunction(name)
		if resolvedFunc != nil && resolvedFunc.Package == transpiler.StdPackage {
			return t.stdIdent(name), nil
		}
		// Check if it's a known std exported function (defined in Go, not GALA)
		for _, stdFunc := range transpiler.StdExportedFunctions {
			if name == stdFunc {
				return t.stdIdent(name), nil
			}
		}
		return ident, nil
	}
	if ctx.Literal() != nil {
		return t.transformLiteral(ctx.Literal().(*grammar.LiteralContext))
	}
	// Handle composite literal (e.g., map[K]V{}, []int{1, 2, 3})
	if ctx.CompositeLiteral() != nil {
		return t.transformCompositeLiteral(ctx.CompositeLiteral().(*grammar.CompositeLiteralContext))
	}
	for i := 0; i < ctx.GetChildCount(); i++ {
		if exprListCtx, ok := ctx.GetChild(i).(grammar.IExpressionListContext); ok {
			exprs, err := t.transformExpressionList(exprListCtx.(*grammar.ExpressionListContext))
			if err != nil {
				return nil, err
			}
			if len(exprs) == 1 {
				return &ast.ParenExpr{X: exprs[0]}, nil
			}
			// Multiple expressions in parentheses -> tuple literal syntax
			return t.transformTupleLiteral(exprs)
		}
	}
	return nil, nil
}

func (t *galaASTTransformer) transformCompositeLiteral(ctx *grammar.CompositeLiteralContext) (ast.Expr, error) {
	// Transform the type
	typeExpr, err := t.transformType(ctx.Type_())
	if err != nil {
		return nil, err
	}

	// Transform the elements
	var elts []ast.Expr
	if ctx.ElementList() != nil {
		elemList := ctx.ElementList().(*grammar.ElementListContext)
		for _, keyedElem := range elemList.AllKeyedElement() {
			kv := keyedElem.(*grammar.KeyedElementContext)
			exprs := kv.AllExpression()
			if len(exprs) == 2 {
				// Key-value pair
				key, err := t.transformExpression(exprs[0].(*grammar.ExpressionContext))
				if err != nil {
					return nil, err
				}
				value, err := t.transformExpression(exprs[1].(*grammar.ExpressionContext))
				if err != nil {
					return nil, err
				}
				elts = append(elts, &ast.KeyValueExpr{Key: key, Value: value})
			} else if len(exprs) == 1 {
				// Value only
				value, err := t.transformExpression(exprs[0].(*grammar.ExpressionContext))
				if err != nil {
					return nil, err
				}
				elts = append(elts, value)
			}
		}
	}

	return &ast.CompositeLit{
		Type: typeExpr,
		Elts: elts,
	}, nil
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
	var retType ast.Expr = ast.NewIdent("any")

	if ctx.Block() != nil {
		b, err := t.transformBlock(ctx.Block().(*grammar.BlockContext))
		if err != nil {
			return nil, err
		}
		// Add return nil to ensure Go compiler is happy with 'any' return type
		b.List = append(b.List, &ast.ReturnStmt{Results: []ast.Expr{ast.NewIdent("nil")}})
		body = b
	} else if ctx.Expression() != nil {
		expr, err := t.transformExpression(ctx.Expression())
		if err != nil {
			return nil, err
		}
		retType = t.getExprType(expr)
		body = &ast.BlockStmt{
			List: []ast.Stmt{
				&ast.ReturnStmt{Results: []ast.Expr{expr}},
			},
		}
	}

	return &ast.FuncLit{
		Type: &ast.FuncType{
			Params: fieldList,
			Results: &ast.FieldList{
				List: []*ast.Field{
					{Type: retType},
				},
			},
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

	retType := transpiler.Type(transpiler.NilType{})
	if inferred, err := t.inferIfType(cond, thenExpr, elseExpr); err == nil && !inferred.IsNil() {
		retType = inferred
	}

	retTypeExpr := t.typeToExpr(retType)

	// Transpile to IIFE: func() T { if cond { return thenExpr }; return elseExpr }()
	return &ast.CallExpr{
		Fun: &ast.FuncLit{
			Type: &ast.FuncType{
				Params: &ast.FieldList{},
				Results: &ast.FieldList{
					List: []*ast.Field{{Type: retTypeExpr}},
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

func (t *galaASTTransformer) unwrapImmutable(expr ast.Expr) ast.Expr {
	if expr == nil {
		return nil
	}
	if paren, ok := expr.(*ast.ParenExpr); ok {
		return &ast.ParenExpr{
			X: t.unwrapImmutable(paren.X),
		}
	}

	// Don't unwrap if it's a type name (identifier or selector)
	if ident, ok := expr.(*ast.Ident); ok {
		if !t.isVal(ident.Name) && !t.isVar(ident.Name) {
			if !t.getType(ident.Name).IsNil() {
				return expr
			}
		}
	}
	if sel, ok := expr.(*ast.SelectorExpr); ok {
		if xIdent, ok := sel.X.(*ast.Ident); ok {
			fullPath := xIdent.Name + "." + sel.Sel.Name
			if !t.isVal(fullPath) && !t.isVar(fullPath) {
				if !t.getType(fullPath).IsNil() {
					return expr
				}
			}
		}
	}

	typeObj := t.getExprTypeName(expr)
	if t.isImmutableType(typeObj) {
		return &ast.CallExpr{
			Fun: &ast.SelectorExpr{
				X:   expr,
				Sel: ast.NewIdent(transpiler.MethodGet),
			},
		}
	}
	return expr
}

// transformTupleLiteral transforms (a, b) to std.Tuple{V1: NewImmutable(a), V2: NewImmutable(b)},
// (a, b, c) to std.Tuple3{V1: NewImmutable(a), V2: NewImmutable(b), V3: NewImmutable(c)}, etc.
func (t *galaASTTransformer) transformTupleLiteral(exprs []ast.Expr) (ast.Expr, error) {
	n := len(exprs)
	if n < 2 || n > 10 {
		return nil, galaerr.NewSemanticError(fmt.Sprintf("tuple literals must have 2-10 elements, got %d", n))
	}

	// Determine tuple type name based on arity
	var typeName string
	switch n {
	case 2:
		typeName = transpiler.TypeTuple
	case 3:
		typeName = transpiler.TypeTuple3
	case 4:
		typeName = transpiler.TypeTuple4
	case 5:
		typeName = transpiler.TypeTuple5
	case 6:
		typeName = transpiler.TypeTuple6
	case 7:
		typeName = transpiler.TypeTuple7
	case 8:
		typeName = transpiler.TypeTuple8
	case 9:
		typeName = transpiler.TypeTuple9
	case 10:
		typeName = transpiler.TypeTuple10
	}

	// Infer type parameters from expression types
	var typeParams []ast.Expr
	for _, expr := range exprs {
		exprType := t.getExprTypeName(expr)
		if exprType.IsNil() || exprType.String() == "any" {
			typeParams = append(typeParams, ast.NewIdent("any"))
		} else {
			typeParams = append(typeParams, t.typeToExpr(exprType))
		}
	}

	// Build the type expression: std.TupleN[T1, T2, ...]
	var typeExpr ast.Expr = t.stdIdent(typeName)
	if len(typeParams) == 1 {
		typeExpr = &ast.IndexExpr{X: typeExpr, Index: typeParams[0]}
	} else if len(typeParams) > 1 {
		typeExpr = &ast.IndexListExpr{X: typeExpr, Indices: typeParams}
	}

	// Build the composite literal: std.TupleN[...]{V1: NewImmutable(a), V2: NewImmutable(b), ...}
	// Tuple fields are Immutable, so we need to wrap each value
	var elts []ast.Expr
	for i, expr := range exprs {
		fieldName := fmt.Sprintf("V%d", i+1)
		// Wrap value in NewImmutable unless it's already immutable
		wrappedExpr := expr
		exprType := t.getExprTypeName(expr)
		if !t.isImmutableType(exprType) {
			wrappedExpr = &ast.CallExpr{
				Fun:  t.stdIdent(transpiler.FuncNewImmutable),
				Args: []ast.Expr{expr},
			}
		}
		elts = append(elts, &ast.KeyValueExpr{
			Key:   ast.NewIdent(fieldName),
			Value: wrappedExpr,
		})
	}

	t.needsStdImport = true
	return &ast.CompositeLit{
		Type: typeExpr,
		Elts: elts,
	}, nil
}

// inferTypeArgsFromApply infers type arguments for a generic type from its Apply method arguments.
// For example, when calling Some(10), this infers T=int from the argument type.
// It matches the type's type parameters with the Apply method's parameter types to determine
// which argument positions correspond to which type parameters.
func (t *galaASTTransformer) inferTypeArgsFromApply(
	typeMeta *transpiler.TypeMetadata,
	methodMeta *transpiler.MethodMetadata,
	args []ast.Expr,
) []transpiler.Type {
	if len(typeMeta.TypeParams) == 0 || len(methodMeta.ParamTypes) == 0 || len(args) == 0 {
		return nil
	}

	result := make([]transpiler.Type, len(typeMeta.TypeParams))

	// Build a map from type parameter name to its index
	typeParamIndex := make(map[string]int)
	for i, tp := range typeMeta.TypeParams {
		typeParamIndex[tp] = i
	}

	// For each Apply method parameter, check if it corresponds to a type parameter
	for i, paramType := range methodMeta.ParamTypes {
		if i >= len(args) {
			break
		}

		// Check if this parameter type is one of the type parameters
		// ParamTypes may be package-qualified (e.g., "std.T") so we need to check both
		paramBaseName := paramType.BaseName()
		// Strip package prefix if present (e.g., "std.T" -> "T")
		if idx := strings.LastIndex(paramBaseName, "."); idx != -1 {
			paramBaseName = paramBaseName[idx+1:]
		}
		if idx, ok := typeParamIndex[paramBaseName]; ok {
			// Get the argument's actual type
			argType := t.getExprTypeName(args[i])
			if !argType.IsNil() {
				result[idx] = argType
			}
		}
	}

	// Check if all type parameters were inferred with concrete types
	for _, tp := range result {
		if tp == nil || tp.IsNil() {
			return nil // Could not infer all type parameters
		}
		// Make sure we didn't infer a type parameter (like T) instead of a concrete type
		if t.hasTypeParams(tp) {
			return nil // Inferred type still contains type parameters
		}
	}

	return result
}
