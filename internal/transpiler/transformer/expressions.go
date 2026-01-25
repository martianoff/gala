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
	// NOTE: This function is DEAD CODE. Call transformation goes through transformCallWithArgsCtx.
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
	// If recvType is a generic type, preserve its type parameters when resolving the base name
	if gen, ok := recvType.(transpiler.GenericType); ok {
		if qBase := t.getType(gen.Base.String()); !qBase.IsNil() {
			// Keep the type parameters but use the resolved base type
			recvType = transpiler.GenericType{Base: qBase, Params: gen.Params}
		}
	} else if qName := t.getType(recvType.BaseName()); !qName.IsNil() {
		recvType = qName
	}
	recvBaseName := recvType.BaseName()
	// Strip pointer prefix for genericMethods lookup since methods are registered under base type name
	lookupBaseName := strings.TrimPrefix(recvBaseName, "*")

	// Check for generic method - try all possible package lookups
	isGenericMethod := len(typeArgs) > 0 || t.isGenericMethodWithImports(lookupBaseName, recvType.GetPackage(), method)

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
			// Get method metadata for parameter types
			var methodMeta *transpiler.MethodMetadata
			var typeMeta *transpiler.TypeMetadata
			if tm, ok := t.typeMetas[lookupBaseName]; ok && tm != nil {
				typeMeta = tm
				methodMeta = typeMeta.Methods[method]
			} else {
				// Try package-qualified name lookup (e.g., "collection_immutable.Array")
				pkg := recvType.GetPackage()
				if pkg != "" {
					qualifiedName := pkg + "." + lookupBaseName
					if tm, ok := t.typeMetas[qualifiedName]; ok && tm != nil {
						typeMeta = tm
						methodMeta = typeMeta.Methods[method]
						// Update lookupBaseName for later use
						lookupBaseName = qualifiedName
					}
				}
				// If still not found, search through dot-imported packages
				if typeMeta == nil {
					for _, dotPkg := range t.dotImports {
						qualifiedName := dotPkg + "." + lookupBaseName
						if tm, ok := t.typeMetas[qualifiedName]; ok && tm != nil {
							typeMeta = tm
							methodMeta = typeMeta.Methods[method]
							lookupBaseName = qualifiedName
							break
						}
					}
				}
				// If still not found, search through all imported packages
				if typeMeta == nil {
					for alias := range t.imports {
						actualPkg := alias
						if actual, ok := t.importAliases[alias]; ok {
							actualPkg = actual
						}
						qualifiedName := actualPkg + "." + lookupBaseName
						if tm, ok := t.typeMetas[qualifiedName]; ok && tm != nil {
							typeMeta = tm
							methodMeta = typeMeta.Methods[method]
							lookupBaseName = qualifiedName
							break
						}
					}
				}
			}

			// Build type argument substitution map
			// For method Map[U] on Container[T], when called as c.Map[string]((x int) => ...)
			// where c: *Container[int], we have U=string and T=int
			typeSubst := make(map[string]string)
			if methodMeta != nil && typeMeta != nil {
				// Add receiver's type args (e.g., T -> int)
				recvTypeArgs := t.getReceiverTypeArgStrings(recvType)
				for i, tp := range typeMeta.TypeParams {
					if i < len(recvTypeArgs) {
						typeSubst[tp] = recvTypeArgs[i]
					}
				}
				// Add method's explicit type args (e.g., U -> string)
				// If no explicit type args provided, default to "any"
				for i, tp := range methodMeta.TypeParams {
					if i < len(typeArgs) {
						typeSubst[tp] = t.exprToTypeString(typeArgs[i])
					} else {
						// No explicit type arg provided, default to "any"
						typeSubst[tp] = "any"
					}
				}
			}

			var mArgs []ast.Expr
			if argListCtx != nil {
				for i, argCtx := range argListCtx.AllArgument() {
					arg := argCtx.(*grammar.ArgumentContext)
					pat := arg.Pattern()
					ep, ok := pat.(*grammar.ExpressionPatternContext)
					if !ok {
						return nil, galaerr.NewSemanticError("only expressions allowed as function arguments")
					}

					// Get expected parameter type if available, with type substitution
					var expectedType transpiler.Type = transpiler.NilType{}
					if methodMeta != nil && i < len(methodMeta.ParamTypes) {
						expectedType = t.substituteTranspilerTypeParams(methodMeta.ParamTypes[i], typeSubst)
					}

					expr, err := t.transformArgumentWithExpectedType(ep.Expression(), expectedType)
					if err != nil {
						return nil, err
					}
					mArgs = append(mArgs, expr)
				}
			}

			var fun ast.Expr
			if !recvType.IsNil() {
				recvPkg := recvType.GetPackage()
				if recvPkg == transpiler.StdPackage || strings.HasPrefix(lookupBaseName, "std.") {
					// Receiver is from std package
					baseName := strings.TrimPrefix(lookupBaseName, "std.")
					fun = t.stdIdent(baseName + "_" + method)
				} else {
					fun = t.ident(lookupBaseName + "_" + method)
				}
			} else {
				fun = ast.NewIdent(method)
			}

			// Prepend receiver's type arguments to explicit type arguments
			// For example, arr.Map[int](f) where arr is *Array[int] becomes Array_Map[int, int](arr, f)
			recvTypeArgs := t.getReceiverTypeArgs(recvType)
			allTypeArgs := append(typeArgs, recvTypeArgs...)

			if len(allTypeArgs) == 1 {
				fun = &ast.IndexExpr{X: fun, Index: allTypeArgs[0]}
			} else if len(allTypeArgs) > 1 {
				fun = &ast.IndexListExpr{X: fun, Indices: allTypeArgs}
			}

			return &ast.CallExpr{
				Fun:  fun,
				Args: append([]ast.Expr{receiver}, mArgs...),
			}, nil
		}
	}

	// Handle regular method calls on generic types (methods without type params on receiver types with type params)
	// These should remain as method calls but still need expected types for lambda arguments
	if receiver != nil && !isGenericMethod && method != "" && argListCtx != nil {
		var methodMeta *transpiler.MethodMetadata
		var typeMeta *transpiler.TypeMetadata
		// Try to find type metadata - first with the full name, then without std. prefix (for dot-imported std types)
		metaLookupName := lookupBaseName
		if tm, ok := t.typeMetas[metaLookupName]; ok && tm != nil && len(tm.TypeParams) > 0 {
			typeMeta = tm
			methodMeta = typeMeta.Methods[method]
		} else if strings.HasPrefix(lookupBaseName, "std.") {
			metaLookupName = strings.TrimPrefix(lookupBaseName, "std.")
			if tm, ok := t.typeMetas[metaLookupName]; ok && tm != nil && len(tm.TypeParams) > 0 {
				typeMeta = tm
				methodMeta = typeMeta.Methods[method]
			}
		}

		if methodMeta != nil {
			// Build type substitution map from receiver's type arguments
			typeSubst := make(map[string]string)
			recvTypeArgs := t.getReceiverTypeArgStrings(recvType)
			for i, tp := range typeMeta.TypeParams {
				if i < len(recvTypeArgs) {
					typeSubst[tp] = recvTypeArgs[i]
				}
			}

			// Transform arguments with expected types
			var mArgs []ast.Expr
			for i, argCtx := range argListCtx.AllArgument() {
				arg := argCtx.(*grammar.ArgumentContext)
				pat := arg.Pattern()
				ep, ok := pat.(*grammar.ExpressionPatternContext)
				if !ok {
					return nil, galaerr.NewSemanticError("only expressions allowed as function arguments")
				}

				var expectedType transpiler.Type = transpiler.NilType{}
				if i < len(methodMeta.ParamTypes) {
					expectedType = t.substituteTranspilerTypeParams(methodMeta.ParamTypes[i], typeSubst)
				}

				expr, err := t.transformArgumentWithExpectedType(ep.Expression(), expectedType)
				if err != nil {
					return nil, err
				}
				mArgs = append(mArgs, expr)
			}

			// Keep as method call: receiver.method(args)
			return &ast.CallExpr{
				Fun:  &ast.SelectorExpr{X: receiver, Sel: ast.NewIdent(method)},
				Args: mArgs,
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

	// With the new grammar, expression simply wraps orExpr
	if orExpr := ctx.OrExpr(); orExpr != nil {
		return t.transformOrExpr(orExpr.(*grammar.OrExprContext))
	}

	return nil, galaerr.NewSemanticError("expression must contain orExpr")
}

func (t *galaASTTransformer) transformOrExpr(ctx *grammar.OrExprContext) (ast.Expr, error) {
	andExprs := ctx.AllAndExpr()
	if len(andExprs) == 0 {
		return nil, galaerr.NewSemanticError("orExpr must have at least one andExpr")
	}

	result, err := t.transformAndExpr(andExprs[0].(*grammar.AndExprContext))
	if err != nil {
		return nil, err
	}

	for i := 1; i < len(andExprs); i++ {
		right, err := t.transformAndExpr(andExprs[i].(*grammar.AndExprContext))
		if err != nil {
			return nil, err
		}
		result = t.unwrapImmutable(result)
		right = t.unwrapImmutable(right)
		result = &ast.BinaryExpr{X: result, Op: token.LOR, Y: right}
	}

	return result, nil
}

func (t *galaASTTransformer) transformAndExpr(ctx *grammar.AndExprContext) (ast.Expr, error) {
	eqExprs := ctx.AllEqualityExpr()
	if len(eqExprs) == 0 {
		return nil, galaerr.NewSemanticError("andExpr must have at least one equalityExpr")
	}

	result, err := t.transformEqualityExpr(eqExprs[0].(*grammar.EqualityExprContext))
	if err != nil {
		return nil, err
	}

	for i := 1; i < len(eqExprs); i++ {
		right, err := t.transformEqualityExpr(eqExprs[i].(*grammar.EqualityExprContext))
		if err != nil {
			return nil, err
		}
		result = t.unwrapImmutable(result)
		right = t.unwrapImmutable(right)
		result = &ast.BinaryExpr{X: result, Op: token.LAND, Y: right}
	}

	return result, nil
}

func (t *galaASTTransformer) transformEqualityExpr(ctx *grammar.EqualityExprContext) (ast.Expr, error) {
	relExprs := ctx.AllRelationalExpr()
	if len(relExprs) == 0 {
		return nil, galaerr.NewSemanticError("equalityExpr must have at least one relationalExpr")
	}

	result, err := t.transformRelationalExpr(relExprs[0].(*grammar.RelationalExprContext))
	if err != nil {
		return nil, err
	}

	// Get the operators between expressions
	for i := 1; i < len(relExprs); i++ {
		// The operator is at position (i*2 - 1) in children
		opText := ctx.GetChild(i*2 - 1).(antlr.ParseTree).GetText()
		right, err := t.transformRelationalExpr(relExprs[i].(*grammar.RelationalExprContext))
		if err != nil {
			return nil, err
		}
		result = t.unwrapImmutable(result)
		right = t.unwrapImmutable(right)
		result = &ast.BinaryExpr{X: result, Op: t.getBinaryToken(opText), Y: right}
	}

	return result, nil
}

func (t *galaASTTransformer) transformRelationalExpr(ctx *grammar.RelationalExprContext) (ast.Expr, error) {
	addExprs := ctx.AllAdditiveExpr()
	if len(addExprs) == 0 {
		return nil, galaerr.NewSemanticError("relationalExpr must have at least one additiveExpr")
	}

	result, err := t.transformAdditiveExpr(addExprs[0].(*grammar.AdditiveExprContext))
	if err != nil {
		return nil, err
	}

	for i := 1; i < len(addExprs); i++ {
		opText := ctx.GetChild(i*2 - 1).(antlr.ParseTree).GetText()
		right, err := t.transformAdditiveExpr(addExprs[i].(*grammar.AdditiveExprContext))
		if err != nil {
			return nil, err
		}
		result = t.unwrapImmutable(result)
		right = t.unwrapImmutable(right)
		result = &ast.BinaryExpr{X: result, Op: t.getBinaryToken(opText), Y: right}
	}

	return result, nil
}

func (t *galaASTTransformer) transformAdditiveExpr(ctx *grammar.AdditiveExprContext) (ast.Expr, error) {
	mulExprs := ctx.AllMultiplicativeExpr()
	if len(mulExprs) == 0 {
		return nil, galaerr.NewSemanticError("additiveExpr must have at least one multiplicativeExpr")
	}

	result, err := t.transformMultiplicativeExpr(mulExprs[0].(*grammar.MultiplicativeExprContext))
	if err != nil {
		return nil, err
	}

	for i := 1; i < len(mulExprs); i++ {
		opText := ctx.GetChild(i*2 - 1).(antlr.ParseTree).GetText()
		right, err := t.transformMultiplicativeExpr(mulExprs[i].(*grammar.MultiplicativeExprContext))
		if err != nil {
			return nil, err
		}
		result = t.unwrapImmutable(result)
		right = t.unwrapImmutable(right)
		result = &ast.BinaryExpr{X: result, Op: t.getBinaryToken(opText), Y: right}
	}

	return result, nil
}

func (t *galaASTTransformer) transformMultiplicativeExpr(ctx *grammar.MultiplicativeExprContext) (ast.Expr, error) {
	unaryExprs := ctx.AllUnaryExpr()
	if len(unaryExprs) == 0 {
		return nil, galaerr.NewSemanticError("multiplicativeExpr must have at least one unaryExpr")
	}

	result, err := t.transformUnaryExpr(unaryExprs[0].(*grammar.UnaryExprContext))
	if err != nil {
		return nil, err
	}

	for i := 1; i < len(unaryExprs); i++ {
		opText := ctx.GetChild(i*2 - 1).(antlr.ParseTree).GetText()
		right, err := t.transformUnaryExpr(unaryExprs[i].(*grammar.UnaryExprContext))
		if err != nil {
			return nil, err
		}
		result = t.unwrapImmutable(result)
		right = t.unwrapImmutable(right)
		result = &ast.BinaryExpr{X: result, Op: t.getBinaryToken(opText), Y: right}
	}

	return result, nil
}

func (t *galaASTTransformer) transformUnaryExpr(ctx *grammar.UnaryExprContext) (ast.Expr, error) {
	// Check for unary operator
	if unaryOp := ctx.UnaryOp(); unaryOp != nil {
		innerUnary := ctx.UnaryExpr()
		expr, err := t.transformUnaryExpr(innerUnary.(*grammar.UnaryExprContext))
		if err != nil {
			return nil, err
		}
		opText := unaryOp.GetText()
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
		return &ast.UnaryExpr{Op: t.getUnaryToken(opText), X: expr}, nil
	}

	// Otherwise it's a postfixExpr
	if postfix := ctx.PostfixExpr(); postfix != nil {
		return t.transformPostfixExpr(postfix.(*grammar.PostfixExprContext))
	}

	return nil, galaerr.NewSemanticError("unaryExpr must have unaryOp or postfixExpr")
}

func (t *galaASTTransformer) transformPostfixExpr(ctx *grammar.PostfixExprContext) (ast.Expr, error) {
	// Check for match expression
	if ctx.GetChildCount() > 1 {
		for i := 0; i < ctx.GetChildCount(); i++ {
			if ctx.GetChild(i).(antlr.ParseTree).GetText() == "match" {
				return t.transformPostfixMatchExpression(ctx)
			}
		}
	}

	// Get the primary expression
	primaryExpr := ctx.PrimaryExpr()
	if primaryExpr == nil {
		return nil, galaerr.NewSemanticError("postfixExpr must have primaryExpr")
	}

	result, err := t.transformPrimaryExpr(primaryExpr.(*grammar.PrimaryExprContext))
	if err != nil {
		return nil, err
	}

	// Apply postfix suffixes
	suffixes := ctx.AllPostfixSuffix()
	for _, suffix := range suffixes {
		result, err = t.applyPostfixSuffix(result, suffix.(*grammar.PostfixSuffixContext))
		if err != nil {
			return nil, err
		}
	}

	return result, nil
}

func (t *galaASTTransformer) applyPostfixSuffix(base ast.Expr, suffix *grammar.PostfixSuffixContext) (ast.Expr, error) {
	// Check what type of suffix this is
	if suffix.Identifier() != nil {
		// Member access: . identifier
		selName := suffix.Identifier().GetText()

		// Don't unwrap if we're accessing Immutable's own fields/methods
		xType := t.getExprTypeName(base)
		isImmutable := t.isImmutableType(xType)

		if !isImmutable || (selName != "Get" && selName != "value") {
			base = t.unwrapImmutable(base)
		}

		selExpr := &ast.SelectorExpr{
			X:   base,
			Sel: ast.NewIdent(selName),
		}

		// Re-evaluate type after potential unwrap
		xType = t.getExprTypeName(base)
		xTypeName := xType.String()
		baseTypeName := xTypeName
		if idx := strings.Index(xTypeName, "["); idx != -1 {
			baseTypeName = xTypeName[:idx]
		}
		baseTypeName = strings.TrimPrefix(baseTypeName, "*")

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

	// Check for function call or index
	childCount := suffix.GetChildCount()
	if childCount >= 2 {
		firstChild := suffix.GetChild(0).(antlr.ParseTree).GetText()

		if firstChild == "(" {
			// Function call
			return t.applyCallSuffix(base, suffix)
		}

		if firstChild == "[" {
			// Index expression
			exprList := suffix.ExpressionList()
			if exprList == nil {
				return nil, galaerr.NewSemanticError("index expression requires expression list")
			}
			base = t.unwrapImmutable(base)
			indices, err := t.transformExpressionList(exprList.(*grammar.ExpressionListContext))
			if err != nil {
				return nil, err
			}
			if len(indices) == 1 {
				return &ast.IndexExpr{X: base, Index: indices[0]}, nil
			}
			return &ast.IndexListExpr{X: base, Indices: indices}, nil
		}
	}

	return nil, galaerr.NewSemanticError("unknown postfix suffix type")
}

func (t *galaASTTransformer) applyCallSuffix(base ast.Expr, suffix *grammar.PostfixSuffixContext) (ast.Expr, error) {
	argList := suffix.ArgumentList()
	if argList == nil {
		// Empty argument list - check for zero-argument Apply method
		typeName := t.getBaseTypeName(base)
		if typeName != "" {
			// Try to find type metadata - check both raw name and std-prefixed name
			typeMeta, ok := t.typeMetas[typeName]
			if !ok && !strings.HasPrefix(typeName, "std.") {
				typeMeta, ok = t.typeMetas["std."+typeName]
				if ok {
					typeName = "std." + typeName
				}
			}
			if ok {
				if methodMeta, hasApply := typeMeta.Methods["Apply"]; hasApply {
					// Check if Apply takes zero arguments (zero-arg Apply method like None[T]())
					if len(methodMeta.ParamTypes) == 0 {
						// Check if the base expression is a type (not a variable)
						isType := false
						baseExpr := base
						if idx, ok := base.(*ast.IndexExpr); ok {
							baseExpr = idx.X
						} else if idxList, ok := base.(*ast.IndexListExpr); ok {
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
								// Check if it's an explicitly imported package OR the std package
								if _, isPkg := t.imports[id.Name]; isPkg || id.Name == transpiler.StdPackage {
									isType = true
								}
							}
						}

						if isType {
							// Non-generic zero-argument Apply method: TypeName[T]{}.Apply()
							receiver := &ast.CompositeLit{Type: base}
							return &ast.CallExpr{
								Fun: &ast.SelectorExpr{
									X:   receiver,
									Sel: ast.NewIdent("Apply"),
								},
								Args: nil,
							}, nil
						}
					}
				}
			}
		}

		// Check for zero-argument generic method call (e.g., p.Swap())
		if sel, ok := base.(*ast.SelectorExpr); ok {
			receiver := sel.X
			method := sel.Sel.Name

			recvType := t.getExprTypeName(receiver)
			// If recvType is a generic type, preserve its type parameters when resolving the base name
			if gen, ok := recvType.(transpiler.GenericType); ok {
				if qBase := t.getType(gen.Base.String()); !qBase.IsNil() {
					// Keep the type parameters but use the resolved base type
					recvType = transpiler.GenericType{Base: qBase, Params: gen.Params}
				}
			} else if qName := t.getType(recvType.BaseName()); !qName.IsNil() {
				recvType = qName
			}
			recvBaseName := recvType.BaseName()
			// Strip pointer prefix for genericMethods lookup since methods are registered under base type name
			lookupBaseName := strings.TrimPrefix(recvBaseName, "*")

			// Check if this is a generic method - try all possible package lookups
			isGenericMethod := t.isGenericMethodWithImports(lookupBaseName, recvType.GetPackage(), method)
			if isGenericMethod {
				// Check if receiver is a package name
				isPkg := false
				if id, ok := receiver.(*ast.Ident); ok {
					if _, ok := t.imports[id.Name]; ok {
						isPkg = true
					}
				}

				if !isPkg {
					// Transform to standalone function call: TypeName_Method[T](receiver)
					var funExpr ast.Expr
					if !recvType.IsNil() {
						recvPkg := recvType.GetPackage()
						if recvPkg == transpiler.StdPackage || strings.HasPrefix(lookupBaseName, "std.") {
							baseName := strings.TrimPrefix(lookupBaseName, "std.")
							funExpr = t.stdIdent(baseName + "_" + method)
						} else {
							funExpr = t.ident(lookupBaseName + "_" + method)
						}
					} else {
						funExpr = ast.NewIdent(method)
					}

					// Add receiver's type arguments for the extracted function
					recvTypeArgs := t.getReceiverTypeArgs(recvType)
					if len(recvTypeArgs) == 1 {
						funExpr = &ast.IndexExpr{X: funExpr, Index: recvTypeArgs[0]}
					} else if len(recvTypeArgs) > 1 {
						funExpr = &ast.IndexListExpr{X: funExpr, Indices: recvTypeArgs}
					}

					return &ast.CallExpr{
						Fun:  funExpr,
						Args: []ast.Expr{receiver},
					}, nil
				}
			}
		}

		return &ast.CallExpr{Fun: base, Args: nil}, nil
	}

	return t.transformCallWithArgsCtx(base, argList.(*grammar.ArgumentListContext))
}

func (t *galaASTTransformer) transformCallWithArgsCtx(fun ast.Expr, argListCtx *grammar.ArgumentListContext) (ast.Expr, error) {
	// Handle Copy method call with overrides
	if sel, ok := fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "Copy" {
		return t.transformCopyCall(sel.X, argListCtx)
	}

	// Handle generic method calls or monadic methods: o.Map[T](f) -> Map[T](o, f)
	var receiver ast.Expr
	var method string
	var typeArgs []ast.Expr

	if sel, ok := fun.(*ast.SelectorExpr); ok {
		if id, ok := sel.X.(*ast.Ident); ok && id.Name == transpiler.StdPackage {
			// Not a method call
		} else {
			receiver = sel.X
			method = sel.Sel.Name
		}
	} else if idx, ok := fun.(*ast.IndexExpr); ok {
		if sel, ok := idx.X.(*ast.SelectorExpr); ok {
			if id, ok := sel.X.(*ast.Ident); ok && id.Name == transpiler.StdPackage {
				// Not a method call
			} else {
				receiver = sel.X
				method = sel.Sel.Name
				typeArgs = []ast.Expr{idx.Index}
			}
		}
	} else if idxList, ok := fun.(*ast.IndexListExpr); ok {
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
	// If recvType is a generic type, preserve its type parameters when resolving the base name
	if gen, ok := recvType.(transpiler.GenericType); ok {
		if qBase := t.getType(gen.Base.String()); !qBase.IsNil() {
			// Keep the type parameters but use the resolved base type
			recvType = transpiler.GenericType{Base: qBase, Params: gen.Params}
		}
	} else if qName := t.getType(recvType.BaseName()); !qName.IsNil() {
		recvType = qName
	}
	recvBaseName := recvType.BaseName()
	// Strip pointer prefix for genericMethods lookup since methods are registered under base type name
	lookupBaseName := strings.TrimPrefix(recvBaseName, "*")

	// Check for generic method - try all possible package lookups
	isGenericMethod := len(typeArgs) > 0 || t.isGenericMethodWithImports(lookupBaseName, recvType.GetPackage(), method)

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
			// Get method metadata for parameter types
			var methodMeta *transpiler.MethodMetadata
			var typeMeta *transpiler.TypeMetadata
			if tm, ok := t.typeMetas[lookupBaseName]; ok && tm != nil {
				typeMeta = tm
				methodMeta = typeMeta.Methods[method]
			} else {
				// Try package-qualified name lookup (e.g., "collection_immutable.Array")
				pkg := recvType.GetPackage()
				if pkg != "" {
					qualifiedName := pkg + "." + lookupBaseName
					if tm, ok := t.typeMetas[qualifiedName]; ok && tm != nil {
						typeMeta = tm
						methodMeta = typeMeta.Methods[method]
						// Update lookupBaseName for later use
						lookupBaseName = qualifiedName
					}
				}
				// If still not found, search through dot-imported packages
				if typeMeta == nil {
					for _, dotPkg := range t.dotImports {
						qualifiedName := dotPkg + "." + lookupBaseName
						if tm, ok := t.typeMetas[qualifiedName]; ok && tm != nil {
							typeMeta = tm
							methodMeta = typeMeta.Methods[method]
							lookupBaseName = qualifiedName
							break
						}
					}
				}
				// If still not found, search through all imported packages
				if typeMeta == nil {
					for alias := range t.imports {
						actualPkg := alias
						if actual, ok := t.importAliases[alias]; ok {
							actualPkg = actual
						}
						qualifiedName := actualPkg + "." + lookupBaseName
						if tm, ok := t.typeMetas[qualifiedName]; ok && tm != nil {
							typeMeta = tm
							methodMeta = typeMeta.Methods[method]
							lookupBaseName = qualifiedName
							break
						}
					}
				}
			}

			// Build type argument substitution map
			typeSubst := make(map[string]string)
			if methodMeta != nil && typeMeta != nil {
				// Add receiver's type args (e.g., T -> int)
				recvTypeArgs := t.getReceiverTypeArgStrings(recvType)
				for i, tp := range typeMeta.TypeParams {
					if i < len(recvTypeArgs) {
						typeSubst[tp] = recvTypeArgs[i]
					}
				}
				// Add method's explicit type args (e.g., U -> string)
				// If no explicit type args provided, default to "any"
				for i, tp := range methodMeta.TypeParams {
					if i < len(typeArgs) {
						typeSubst[tp] = t.exprToTypeString(typeArgs[i])
					} else {
						// No explicit type arg provided, default to "any"
						typeSubst[tp] = "any"
					}
				}
			}

			var mArgs []ast.Expr
			for i, argCtx := range argListCtx.AllArgument() {
				arg := argCtx.(*grammar.ArgumentContext)
				pat := arg.Pattern()
				ep, ok := pat.(*grammar.ExpressionPatternContext)
				if !ok {
					return nil, galaerr.NewSemanticError("only expressions allowed as function arguments")
				}

				// Get expected parameter type if available, with type substitution
				var expectedType transpiler.Type = transpiler.NilType{}
				if methodMeta != nil && i < len(methodMeta.ParamTypes) {
					expectedType = t.substituteTranspilerTypeParams(methodMeta.ParamTypes[i], typeSubst)
				}

				expr, err := t.transformArgumentWithExpectedType(ep.Expression(), expectedType)
				if err != nil {
					return nil, err
				}
				mArgs = append(mArgs, expr)
			}

			var funExpr ast.Expr
			if !recvType.IsNil() {
				recvPkg := recvType.GetPackage()
				if recvPkg == transpiler.StdPackage || strings.HasPrefix(lookupBaseName, "std.") {
					baseName := strings.TrimPrefix(lookupBaseName, "std.")
					funExpr = t.stdIdent(baseName + "_" + method)
				} else {
					funExpr = t.ident(lookupBaseName + "_" + method)
				}
			} else {
				funExpr = ast.NewIdent(method)
			}

			// Only add type arguments when they are explicitly provided
			// If no explicit type args, let Go infer all type parameters
			// This is important for methods with their own type params like Map[U]
			// Get receiver type args, filtering out unresolved type params
			recvTypeArgs := t.getReceiverTypeArgs(recvType)
			var concreteRecvTypeArgs []ast.Expr
			for _, arg := range recvTypeArgs {
				// Check if this is an unresolved type param (single uppercase letter)
				if ident, ok := arg.(*ast.Ident); ok {
					if len(ident.Name) == 1 && ident.Name[0] >= 'A' && ident.Name[0] <= 'Z' {
						// Skip unresolved type params like T, U, K, V
						continue
					}
				}
				concreteRecvTypeArgs = append(concreteRecvTypeArgs, arg)
			}

			// Decide whether to add type arguments:
			// - If method has its own type params (e.g., Map[U]) and no explicit type args: let Go infer
			// - Otherwise: combine explicit type args with concrete receiver type args
			shouldAddTypeArgs := len(typeArgs) > 0 || (methodMeta == nil || len(methodMeta.TypeParams) == 0)
			if shouldAddTypeArgs {
				allTypeArgs := append(typeArgs, concreteRecvTypeArgs...)
				if len(allTypeArgs) == 1 {
					funExpr = &ast.IndexExpr{X: funExpr, Index: allTypeArgs[0]}
				} else if len(allTypeArgs) > 1 {
					funExpr = &ast.IndexListExpr{X: funExpr, Indices: allTypeArgs}
				}
			}

			return &ast.CallExpr{
				Fun:  funExpr,
				Args: append([]ast.Expr{receiver}, mArgs...),
			}, nil
		}
	}

	// Handle regular method calls on generic types (methods without type params on receiver types with type params)
	// These should remain as method calls but still need expected types for lambda arguments
	if receiver != nil && !isGenericMethod && method != "" {
		var methodMeta *transpiler.MethodMetadata
		var typeMeta *transpiler.TypeMetadata
		// Try to find type metadata - first with the full name, then without std. prefix (for dot-imported std types)
		metaLookupName := lookupBaseName
		if tm, ok := t.typeMetas[metaLookupName]; ok && tm != nil && len(tm.TypeParams) > 0 {
			typeMeta = tm
			methodMeta = typeMeta.Methods[method]
		} else if strings.HasPrefix(lookupBaseName, "std.") {
			metaLookupName = strings.TrimPrefix(lookupBaseName, "std.")
			if tm, ok := t.typeMetas[metaLookupName]; ok && tm != nil && len(tm.TypeParams) > 0 {
				typeMeta = tm
				methodMeta = typeMeta.Methods[method]
			}
		}

		if methodMeta != nil {
			// Build type substitution map from receiver's type arguments
			typeSubst := make(map[string]string)
			recvTypeArgs := t.getReceiverTypeArgStrings(recvType)
			hasUnresolvedTypeParams := false
			for i, tp := range typeMeta.TypeParams {
				if i < len(recvTypeArgs) {
					arg := recvTypeArgs[i]
					// Check if this type arg is an unresolved type param (single uppercase letter)
					if len(arg) == 1 && arg[0] >= 'A' && arg[0] <= 'Z' {
						hasUnresolvedTypeParams = true
						break
					}
					typeSubst[tp] = arg
				}
			}

			// If receiver has unresolved type params, skip expected type inference
			// and let Go infer the lambda types from the body
			if hasUnresolvedTypeParams {
				// Transform arguments without expected types
				var mArgs []ast.Expr
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
				return &ast.CallExpr{
					Fun:  &ast.SelectorExpr{X: receiver, Sel: ast.NewIdent(method)},
					Args: mArgs,
				}, nil
			}

			// Transform arguments with expected types
			var mArgs []ast.Expr
			for i, argCtx := range argListCtx.AllArgument() {
				arg := argCtx.(*grammar.ArgumentContext)
				pat := arg.Pattern()
				ep, ok := pat.(*grammar.ExpressionPatternContext)
				if !ok {
					return nil, galaerr.NewSemanticError("only expressions allowed as function arguments")
				}

				var expectedType transpiler.Type = transpiler.NilType{}
				if i < len(methodMeta.ParamTypes) {
					expectedType = t.substituteTranspilerTypeParams(methodMeta.ParamTypes[i], typeSubst)
				}

				expr, err := t.transformArgumentWithExpectedType(ep.Expression(), expectedType)
				if err != nil {
					return nil, err
				}
				mArgs = append(mArgs, expr)
			}

			// Keep as method call: receiver.method(args)
			return &ast.CallExpr{
				Fun:  &ast.SelectorExpr{X: receiver, Sel: ast.NewIdent(method)},
				Args: mArgs,
			}, nil
		}
	}

	// Regular function call - transform arguments
	var args []ast.Expr
	namedArgs := make(map[string]ast.Expr)

	for _, argCtx := range argListCtx.AllArgument() {
		arg := argCtx.(*grammar.ArgumentContext)
		pat := arg.Pattern()

		// Check for named argument
		if arg.Identifier() != nil {
			// This is a named argument
			argName := arg.Identifier().GetText()
			ep, ok := pat.(*grammar.ExpressionPatternContext)
			if !ok {
				return nil, galaerr.NewSemanticError("only expressions allowed as function arguments")
			}
			expr, err := t.transformExpression(ep.Expression())
			if err != nil {
				return nil, err
			}
			namedArgs[argName] = expr
		} else {
			// Positional argument
			ep, ok := pat.(*grammar.ExpressionPatternContext)
			if !ok {
				return nil, galaerr.NewSemanticError("only expressions allowed as function arguments")
			}
			expr, err := t.transformExpression(ep.Expression())
			if err != nil {
				return nil, err
			}
			args = append(args, expr)
		}
	}

	// If we have named args, this might be struct construction
	if len(namedArgs) > 0 {
		return t.handleNamedArgsCall(fun, args, namedArgs)
	}

	// Check if the function being called is a type with an Apply method
	// This handles companion object calls like Some[A](value) -> Some[A]{}.Apply(value)
	typeName := t.getBaseTypeName(fun)
	if typeName != "" {
		// Try to find type metadata - check both raw name and std-prefixed name
		typeMeta, ok := t.typeMetas[typeName]
		if !ok && !strings.HasPrefix(typeName, "std.") {
			typeMeta, ok = t.typeMetas["std."+typeName]
			if ok {
				typeName = "std." + typeName
			}
		}
		if ok {
			// First check if this looks like positional struct construction
			// (args match struct field count) - prefer struct construction over Apply
			resolvedTypeName := t.resolveStructTypeName(typeName)
			if fields, structOk := t.structFields[resolvedTypeName]; structOk && len(args) > 0 && len(args) == len(fields) {
				// It's struct construction with positional arguments matching field count
				var elts []ast.Expr
				immutFlags := t.structImmutFields[resolvedTypeName]
				for i, fieldName := range fields {
					var valExpr ast.Expr
					if immutFlags != nil && i < len(immutFlags) && immutFlags[i] {
						valExpr = &ast.CallExpr{
							Fun:  t.stdIdent("NewImmutable"),
							Args: []ast.Expr{args[i]},
						}
					} else {
						valExpr = args[i]
					}
					elts = append(elts, &ast.KeyValueExpr{
						Key:   ast.NewIdent(fieldName),
						Value: valExpr,
					})
				}
				return &ast.CompositeLit{Type: fun, Elts: elts}, nil
			}

			if methodMeta, hasApply := typeMeta.Methods["Apply"]; hasApply {
				// Check if the base expression is a type (not a variable)
				isType := false
				baseExpr := fun
				hasTypeArgs := false
				var typeArgs []ast.Expr

				if idx, ok := fun.(*ast.IndexExpr); ok {
					baseExpr = idx.X
					hasTypeArgs = true
					typeArgs = []ast.Expr{idx.Index}
				} else if idxList, ok := fun.(*ast.IndexListExpr); ok {
					baseExpr = idxList.X
					hasTypeArgs = true
					typeArgs = idxList.Indices
				}

				if id, ok := baseExpr.(*ast.Ident); ok {
					if !t.isVal(id.Name) && !t.isVar(id.Name) {
						if !t.getType(id.Name).IsNil() {
							isType = true
						}
					}
				} else if sel, ok := baseExpr.(*ast.SelectorExpr); ok {
					if id, ok := sel.X.(*ast.Ident); ok {
						// Check if it's an explicitly imported package OR the std package
						if _, isPkg := t.imports[id.Name]; isPkg || id.Name == transpiler.StdPackage {
							isType = true
						}
					}
				}

				if isType {
					isGeneric := methodMeta.IsGeneric || len(methodMeta.TypeParams) > 0

					// If no explicit type args but type has type parameters, infer them from argument types
					if !hasTypeArgs && len(typeMeta.TypeParams) > 0 && len(args) > 0 {
						var inferredTypeArgs []ast.Expr
						// Match Apply method parameter types with argument types
						for i, arg := range args {
							if i < len(methodMeta.ParamTypes) {
								paramTypeStr := methodMeta.ParamTypes[i].String()
								// Check if param type is one of the type parameters
								for _, tp := range typeMeta.TypeParams {
									if paramTypeStr == tp {
										// Infer type from argument expression
										argType := t.getExprTypeName(arg)
										if argType != nil && !argType.IsNil() {
											inferredTypeArgs = append(inferredTypeArgs, t.typeToExpr(argType))
										} else {
											inferredTypeArgs = append(inferredTypeArgs, ast.NewIdent("any"))
										}
										break
									}
								}
							}
						}
						// If we inferred all type args, use them
						if len(inferredTypeArgs) == len(typeMeta.TypeParams) {
							typeArgs = inferredTypeArgs
							hasTypeArgs = true
							// Update fun to include the type arguments
							if len(typeArgs) == 1 {
								fun = &ast.IndexExpr{X: baseExpr, Index: typeArgs[0]}
							} else if len(typeArgs) > 1 {
								fun = &ast.IndexListExpr{X: baseExpr, Indices: typeArgs}
							}
						}
					}

					if isGeneric {
						// Generic Apply method: use standalone function
						fullName := typeName + "_Apply"
						var funExpr ast.Expr
						isStdType := strings.HasPrefix(typeName, "std.")
						if !isStdType {
							resolvedType := t.getType(typeName)
							isStdType = !resolvedType.IsNil() && resolvedType.GetPackage() == transpiler.StdPackage
						}
						if isStdType {
							funExpr = t.stdIdent(strings.TrimPrefix(fullName, "std."))
						} else {
							funExpr = t.ident(fullName)
						}

						if len(typeArgs) == 1 {
							funExpr = &ast.IndexExpr{X: funExpr, Index: typeArgs[0]}
						} else if len(typeArgs) > 1 {
							funExpr = &ast.IndexListExpr{X: funExpr, Indices: typeArgs}
						}

						receiver := &ast.CompositeLit{Type: baseExpr}
						if hasTypeArgs {
							receiver = &ast.CompositeLit{Type: fun}
						}

						return &ast.CallExpr{
							Fun:  funExpr,
							Args: append([]ast.Expr{receiver}, args...),
						}, nil
					}

					// Non-generic Apply method: call Apply on instance
					receiver := &ast.CompositeLit{Type: fun}
					return &ast.CallExpr{
						Fun: &ast.SelectorExpr{
							X:   receiver,
							Sel: ast.NewIdent("Apply"),
						},
						Args: args,
					}, nil
				}
			} else {
				// No Apply method - check if this is struct construction with positional args
				resolvedTypeName := t.resolveStructTypeName(typeName)
				if fields, ok := t.structFields[resolvedTypeName]; ok && len(args) > 0 {
					// It's struct construction with positional arguments
					var elts []ast.Expr
					immutFlags := t.structImmutFields[resolvedTypeName]
					for i, fieldName := range fields {
						if i >= len(args) {
							break
						}
						var valExpr ast.Expr
						if immutFlags != nil && i < len(immutFlags) && immutFlags[i] {
							valExpr = &ast.CallExpr{
								Fun:  t.stdIdent("NewImmutable"),
								Args: []ast.Expr{args[i]},
							}
						} else {
							valExpr = args[i]
						}
						elts = append(elts, &ast.KeyValueExpr{
							Key:   ast.NewIdent(fieldName),
							Value: valExpr,
						})
					}
					return &ast.CompositeLit{Type: fun, Elts: elts}, nil
				}
			}
		}
	}

	// Check if fun is a CompositeLit (struct literal) whose type has an Apply method
	// This handles cases like: Append("cherry")("apple") -> Append{...}.Apply("apple")
	if compLit, ok := fun.(*ast.CompositeLit); ok {
		var litTypeName string
		switch lt := compLit.Type.(type) {
		case *ast.Ident:
			litTypeName = lt.Name
		case *ast.SelectorExpr:
			litTypeName = lt.Sel.Name
		case *ast.IndexExpr:
			if id, ok := lt.X.(*ast.Ident); ok {
				litTypeName = id.Name
			} else if sel, ok := lt.X.(*ast.SelectorExpr); ok {
				litTypeName = sel.Sel.Name
			}
		case *ast.IndexListExpr:
			if id, ok := lt.X.(*ast.Ident); ok {
				litTypeName = id.Name
			} else if sel, ok := lt.X.(*ast.SelectorExpr); ok {
				litTypeName = sel.Sel.Name
			}
		}
		if litTypeName != "" {
			resolvedTypeName := t.resolveStructTypeName(litTypeName)
			if typeMeta, ok := t.typeMetas[resolvedTypeName]; ok {
				if _, hasApply := typeMeta.Methods["Apply"]; hasApply {
					// Transform to structLit.Apply(args)
					return &ast.CallExpr{
						Fun: &ast.SelectorExpr{
							X:   compLit,
							Sel: ast.NewIdent("Apply"),
						},
						Args: args,
					}, nil
				}
			}
		}
	}

	// Check if fun is a variable whose type has an Apply method
	// This handles cases like: val add5 = Adder(5); add5(10) -> add5.Apply(10)
	// For vals, the expression is add5.Get() (a CallExpr), not just add5 (Ident)
	var valName string
	if id, ok := fun.(*ast.Ident); ok {
		if t.isVal(id.Name) || t.isVar(id.Name) {
			valName = id.Name
		}
	} else if call, ok := fun.(*ast.CallExpr); ok {
		// Check if this is valName.Get()
		if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == transpiler.MethodGet && len(call.Args) == 0 {
			if id, ok := sel.X.(*ast.Ident); ok {
				if t.isVal(id.Name) {
					valName = id.Name
				}
			}
		}
	}

	if valName != "" {
		varType := t.getType(valName)
		if !varType.IsNil() {
			varTypeName := varType.BaseName()
			// Resolve type name
			resolvedTypeName := varTypeName
			if !strings.Contains(resolvedTypeName, ".") {
				if resolved := t.resolveStructTypeName(resolvedTypeName); resolved != "" {
					resolvedTypeName = resolved
				}
			}
			if typeMeta, ok := t.typeMetas[resolvedTypeName]; ok {
				if _, hasApply := typeMeta.Methods["Apply"]; hasApply {
					// Transform to variable.Apply(args) or variable.Get().Apply(args)
					return &ast.CallExpr{
						Fun: &ast.SelectorExpr{
							X:   fun,
							Sel: ast.NewIdent("Apply"),
						},
						Args: args,
					}, nil
				}
			}
		}
	}

	return &ast.CallExpr{Fun: fun, Args: args}, nil
}

func (t *galaASTTransformer) handleNamedArgsCall(fun ast.Expr, args []ast.Expr, namedArgs map[string]ast.Expr) (ast.Expr, error) {
	// Try to get the type name to check if it's struct construction
	var typeName string
	switch f := fun.(type) {
	case *ast.Ident:
		typeName = f.Name
	case *ast.IndexExpr:
		if id, ok := f.X.(*ast.Ident); ok {
			typeName = id.Name
		} else if sel, ok := f.X.(*ast.SelectorExpr); ok {
			typeName = sel.Sel.Name
		}
	case *ast.IndexListExpr:
		if id, ok := f.X.(*ast.Ident); ok {
			typeName = id.Name
		} else if sel, ok := f.X.(*ast.SelectorExpr); ok {
			typeName = sel.Sel.Name
		}
	case *ast.SelectorExpr:
		typeName = f.Sel.Name
	}

	// Check if this is a known struct type
	resolvedTypeName := t.resolveStructTypeName(typeName)
	if fields, ok := t.structFields[resolvedTypeName]; ok {
		// It's struct construction with named arguments
		var elts []ast.Expr
		immutFlags := t.structImmutFields[resolvedTypeName]
		fieldTypes := t.structFieldTypes[resolvedTypeName]

		// Check if we need to infer type parameters
		typeExpr := fun
		// Check for expressions without explicit type args: Ident (Tuple) or SelectorExpr (std.Tuple)
		needsTypeInference := false
		if _, isIdent := fun.(*ast.Ident); isIdent {
			needsTypeInference = true
		} else if _, isSel := fun.(*ast.SelectorExpr); isSel {
			needsTypeInference = true
		}
		if needsTypeInference {
			// No explicit type args - check if the type has type parameters
			if typeMeta, ok := t.typeMetas[resolvedTypeName]; ok && len(typeMeta.TypeParams) > 0 {
				// Infer type args from field values
				inferredTypeArgs := make([]ast.Expr, len(typeMeta.TypeParams))
				typeParamIndices := make(map[string]int)
				for i, tp := range typeMeta.TypeParams {
					typeParamIndices[tp] = i
				}

				// Map each field's expected type to its inferred type from the value
				for fieldName, fieldType := range fieldTypes {
					if val, ok := namedArgs[fieldName]; ok {
						valType := t.getExprTypeName(val)
						if valType != nil && !valType.IsNil() {
							// Check if the field type is a type parameter
							fieldTypeStr := fieldType.String()
							if idx, isTypeParam := typeParamIndices[fieldTypeStr]; isTypeParam {
								if inferredTypeArgs[idx] == nil {
									inferredTypeArgs[idx] = t.typeToExpr(valType)
								}
							}
						}
					}
				}

				// Check if all type args were inferred
				allInferred := true
				for _, arg := range inferredTypeArgs {
					if arg == nil {
						allInferred = false
						break
					}
				}

				if allInferred && len(inferredTypeArgs) > 0 {
					// Create the type expression with inferred type args
					// Preserve the original expression structure (Ident or SelectorExpr)
					var baseExpr ast.Expr
					if sel, isSel := fun.(*ast.SelectorExpr); isSel {
						baseExpr = &ast.SelectorExpr{X: sel.X, Sel: ast.NewIdent(typeName)}
					} else {
						baseExpr = ast.NewIdent(typeName)
					}
					if len(inferredTypeArgs) == 1 {
						typeExpr = &ast.IndexExpr{X: baseExpr, Index: inferredTypeArgs[0]}
					} else {
						typeExpr = &ast.IndexListExpr{X: baseExpr, Indices: inferredTypeArgs}
					}
				}
			}
		}

		for i, fieldName := range fields {
			if val, ok := namedArgs[fieldName]; ok {
				var valExpr ast.Expr
				if immutFlags != nil && i < len(immutFlags) && immutFlags[i] {
					valExpr = &ast.CallExpr{
						Fun:  t.stdIdent("NewImmutable"),
						Args: []ast.Expr{val},
					}
				} else {
					valExpr = val
				}
				elts = append(elts, &ast.KeyValueExpr{
					Key:   ast.NewIdent(fieldName),
					Value: valExpr,
				})
			}
		}
		return &ast.CompositeLit{Type: typeExpr, Elts: elts}, nil
	}

	return nil, galaerr.NewSemanticError(fmt.Sprintf("named arguments only supported for Copy method or struct construction (type: %s)", typeName))
}

func (t *galaASTTransformer) transformPrimaryExpr(ctx *grammar.PrimaryExprContext) (ast.Expr, error) {
	if p := ctx.Primary(); p != nil {
		return t.transformPrimary(p.(*grammar.PrimaryContext))
	}

	if l := ctx.LambdaExpression(); l != nil {
		return t.transformLambda(l.(*grammar.LambdaExpressionContext))
	}

	if i := ctx.IfExpression(); i != nil {
		return t.transformIfExpression(i.(*grammar.IfExpressionContext))
	}

	if pf := ctx.PartialFunctionLiteral(); pf != nil {
		return t.transformPartialFunctionLiteral(pf.(*grammar.PartialFunctionLiteralContext), nil)
	}

	return nil, galaerr.NewSemanticError("primaryExpr must have primary, lambda, if expression, or partial function")
}

// transformPostfixMatchExpression handles match expressions with the new grammar
func (t *galaASTTransformer) transformPostfixMatchExpression(ctx *grammar.PostfixExprContext) (ast.Expr, error) {
	// Get the primary expression being matched
	primaryExpr := ctx.PrimaryExpr()
	if primaryExpr == nil {
		return nil, galaerr.NewSemanticError("match expression must have subject")
	}

	subject, err := t.transformPrimaryExpr(primaryExpr.(*grammar.PrimaryExprContext))
	if err != nil {
		return nil, err
	}

	// Apply any suffixes before the match
	suffixes := ctx.AllPostfixSuffix()
	for _, suffix := range suffixes {
		subject, err = t.applyPostfixSuffix(subject, suffix.(*grammar.PostfixSuffixContext))
		if err != nil {
			return nil, err
		}
	}

	// Now handle the match expression
	caseClauses := ctx.AllCaseClause()
	return t.buildMatchExpressionFromClauses(subject, "obj", caseClauses)
}

// buildMatchExpressionFromClauses builds a match expression from the subject and case clauses
func (t *galaASTTransformer) buildMatchExpressionFromClauses(subject ast.Expr, paramName string, caseClauses []grammar.ICaseClauseContext) (ast.Expr, error) {
	// Get the type of the matched expression
	matchedType := t.getExprTypeNameManual(subject)
	if matchedType == nil || matchedType.IsNil() {
		matchedType, _ = t.inferExprType(subject)
	}
	if matchedType == nil || matchedType.IsNil() {
		return nil, galaerr.NewSemanticError("cannot infer type of matched expression")
	}

	if t.typeHasUnresolvedParams(matchedType) {
		matchedType = transpiler.BasicType{Name: "any"}
	}

	t.pushScope()
	defer t.popScope()
	t.addVar(paramName, matchedType)

	var clauses []ast.Stmt
	var defaultBody []ast.Stmt
	foundDefault := false
	var resultTypes []transpiler.Type
	var casePatterns []string

	for _, cc := range caseClauses {
		ccCtx := cc.(*grammar.CaseClauseContext)

		patCtx := ccCtx.Pattern()
		patternText := patCtx.GetText()
		if patternText == "_" {
			if foundDefault {
				return nil, galaerr.NewSemanticError("multiple default cases in match expression")
			}
			foundDefault = true

			if ccCtx.GetBodyBlock() != nil {
				b, err := t.transformBlock(ccCtx.GetBodyBlock().(*grammar.BlockContext))
				if err != nil {
					return nil, err
				}
				defaultBody = b.List
				if len(b.List) > 0 {
					if ret, ok := b.List[len(b.List)-1].(*ast.ReturnStmt); ok && len(ret.Results) > 0 {
						resultTypes = append(resultTypes, t.inferResultType(ret.Results[0]))
						casePatterns = append(casePatterns, "case _")
					}
				}
			} else if ccCtx.GetBody() != nil {
				bodyExpr, err := t.transformExpression(ccCtx.GetBody())
				if err != nil {
					return nil, err
				}
				defaultBody = []ast.Stmt{&ast.ReturnStmt{Results: []ast.Expr{bodyExpr}}}
				resultTypes = append(resultTypes, t.inferResultType(bodyExpr))
				casePatterns = append(casePatterns, "case _")
			}
			continue
		}

		clause, resultType, err := t.transformCaseClauseWithType(ccCtx, paramName, matchedType)
		if err != nil {
			return nil, err
		}
		if clause != nil {
			clauses = append(clauses, clause)
		}
		if resultType != nil {
			resultTypes = append(resultTypes, resultType)
			casePatterns = append(casePatterns, fmt.Sprintf("case %s", patternText))
		}
	}

	// Infer common result type from all branches
	resultType, err := t.inferCommonResultType(resultTypes, casePatterns)
	if err != nil {
		return nil, err
	}

	if t.typeHasUnresolvedParams(resultType) {
		resultType = transpiler.BasicType{Name: "any"}
	}

	if len(clauses) == 0 && len(defaultBody) == 0 {
		return nil, galaerr.NewSemanticError("match expression must have at least one case")
	}

	if len(defaultBody) == 0 {
		return nil, galaerr.NewSemanticError("match expression must have a default case (case _ => ...)")
	}

	var stmts []ast.Stmt
	for _, c := range clauses {
		stmts = append(stmts, c)
	}
	stmts = append(stmts, defaultBody...)

	// Check if result type is void (for side-effect only match statements)
	_, isVoid := resultType.(transpiler.VoidType)
	if isVoid {
		// Strip return statements for void match - convert returns to expression statements
		stmts = t.stripReturnStatements(stmts)
	}

	// Build IIFE with or without return type depending on void
	var resultsField *ast.FieldList
	if !isVoid {
		resultsField = &ast.FieldList{List: []*ast.Field{{Type: t.typeToExpr(resultType)}}}
	}

	funcLit := &ast.FuncLit{
		Type: &ast.FuncType{
			Params:  &ast.FieldList{List: []*ast.Field{{Names: []*ast.Ident{ast.NewIdent(paramName)}, Type: t.typeToExpr(matchedType)}}},
			Results: resultsField,
		},
		Body: &ast.BlockStmt{List: stmts},
	}

	return &ast.CallExpr{Fun: funcLit, Args: []ast.Expr{subject}}, nil
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

func (t *galaASTTransformer) isBinaryOperator(op string) bool {
	switch op {
	case "||", "&&", "==", "!=", "<", "<=", ">", ">=",
		"+", "-", "|", "^", "*", "/", "%", "<<", ">>", "&", "&^":
		return true
	default:
		return false
	}
}

// getPrimaryFromExpression navigates the new grammar structure to find the primary
// This is used for backward compatibility with code that expects expr.Primary()
func (t *galaASTTransformer) getPrimaryFromExpression(ctx grammar.IExpressionContext) *grammar.PrimaryContext {
	if ctx == nil {
		return nil
	}
	// expression -> orExpr
	orExpr := ctx.OrExpr()
	if orExpr == nil {
		return nil
	}
	// orExpr -> andExpr
	andExprs := orExpr.(*grammar.OrExprContext).AllAndExpr()
	if len(andExprs) == 0 {
		return nil
	}
	// andExpr -> equalityExpr
	eqExprs := andExprs[0].(*grammar.AndExprContext).AllEqualityExpr()
	if len(eqExprs) == 0 {
		return nil
	}
	// equalityExpr -> relationalExpr
	relExprs := eqExprs[0].(*grammar.EqualityExprContext).AllRelationalExpr()
	if len(relExprs) == 0 {
		return nil
	}
	// relationalExpr -> additiveExpr
	addExprs := relExprs[0].(*grammar.RelationalExprContext).AllAdditiveExpr()
	if len(addExprs) == 0 {
		return nil
	}
	// additiveExpr -> multiplicativeExpr
	mulExprs := addExprs[0].(*grammar.AdditiveExprContext).AllMultiplicativeExpr()
	if len(mulExprs) == 0 {
		return nil
	}
	// multiplicativeExpr -> unaryExpr
	unaryExprs := mulExprs[0].(*grammar.MultiplicativeExprContext).AllUnaryExpr()
	if len(unaryExprs) == 0 {
		return nil
	}
	// unaryExpr -> postfixExpr (if no unaryOp)
	unaryCtx := unaryExprs[0].(*grammar.UnaryExprContext)
	postfixExpr := unaryCtx.PostfixExpr()
	if postfixExpr == nil {
		return nil
	}
	// postfixExpr -> primaryExpr
	primaryExpr := postfixExpr.(*grammar.PostfixExprContext).PrimaryExpr()
	if primaryExpr == nil {
		return nil
	}
	// primaryExpr -> primary
	return primaryExpr.(*grammar.PrimaryExprContext).Primary().(*grammar.PrimaryContext)
}

// getCallPatternFromExpression checks if an expression is a call pattern like Left(n)
// and returns the base expression context and argument list.
// Returns nil values if not a call pattern.
func (t *galaASTTransformer) getCallPatternFromExpression(ctx grammar.IExpressionContext) (*grammar.PrimaryExprContext, *grammar.ArgumentListContext) {
	if ctx == nil {
		return nil, nil
	}
	// Navigate through: expression -> orExpr -> andExpr -> equalityExpr -> relationalExpr -> additiveExpr -> multiplicativeExpr -> unaryExpr -> postfixExpr
	orExpr := ctx.OrExpr()
	if orExpr == nil {
		return nil, nil
	}
	andExprs := orExpr.(*grammar.OrExprContext).AllAndExpr()
	if len(andExprs) == 0 || len(andExprs) > 1 {
		return nil, nil // Not a simple expression
	}
	eqExprs := andExprs[0].(*grammar.AndExprContext).AllEqualityExpr()
	if len(eqExprs) == 0 || len(eqExprs) > 1 {
		return nil, nil
	}
	relExprs := eqExprs[0].(*grammar.EqualityExprContext).AllRelationalExpr()
	if len(relExprs) == 0 || len(relExprs) > 1 {
		return nil, nil
	}
	addExprs := relExprs[0].(*grammar.RelationalExprContext).AllAdditiveExpr()
	if len(addExprs) == 0 || len(addExprs) > 1 {
		return nil, nil
	}
	mulExprs := addExprs[0].(*grammar.AdditiveExprContext).AllMultiplicativeExpr()
	if len(mulExprs) == 0 || len(mulExprs) > 1 {
		return nil, nil
	}
	unaryExprs := mulExprs[0].(*grammar.MultiplicativeExprContext).AllUnaryExpr()
	if len(unaryExprs) == 0 || len(unaryExprs) > 1 {
		return nil, nil
	}
	unaryCtx := unaryExprs[0].(*grammar.UnaryExprContext)
	// Check if there's a unary operator (like !)
	if unaryCtx.UnaryOp() != nil {
		return nil, nil
	}
	postfixExpr := unaryCtx.PostfixExpr()
	if postfixExpr == nil {
		return nil, nil
	}
	postfixCtx := postfixExpr.(*grammar.PostfixExprContext)

	// Check if there's exactly one call suffix
	suffixes := postfixCtx.AllPostfixSuffix()
	if len(suffixes) != 1 {
		return nil, nil
	}
	suffix := suffixes[0].(*grammar.PostfixSuffixContext)

	// Check if it's a call suffix (starts with '(')
	if suffix.GetChildCount() < 2 {
		return nil, nil
	}
	firstChild := suffix.GetChild(0).(antlr.ParseTree).GetText()
	if firstChild != "(" {
		return nil, nil
	}

	// Get the primary expression
	primaryExpr := postfixCtx.PrimaryExpr()
	if primaryExpr == nil {
		return nil, nil
	}

	// Get argument list (may be nil for empty calls)
	var argList *grammar.ArgumentListContext
	if al := suffix.ArgumentList(); al != nil {
		argList = al.(*grammar.ArgumentListContext)
	}

	return primaryExpr.(*grammar.PrimaryExprContext), argList
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
	// Handle composite literal (e.g., map[K]V{}, struct{}{})
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

	// Reject slice literals - users should use std.SliceOf() instead
	if _, isArray := typeExpr.(*ast.ArrayType); isArray {
		return nil, fmt.Errorf("slice literals are not supported in GALA; use std.SliceOf() or std.SliceEmpty() instead")
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
	return t.transformLambdaWithExpectedType(ctx, nil)
}

func (t *galaASTTransformer) transformLambdaWithExpectedType(ctx *grammar.LambdaExpressionContext, expectedRetType ast.Expr) (ast.Expr, error) {
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

	// Check if expected type is a concrete type (not "any" or containing "any")
	// We only use the expected type if it's more specific than "any"
	isConcreteExpectedType := expectedRetType != nil && !containsAny(expectedRetType)

	// Use expected return type if provided and concrete
	if isConcreteExpectedType {
		retType = expectedRetType
	}

	if ctx.Block() != nil {
		b, err := t.transformBlock(ctx.Block().(*grammar.BlockContext))
		if err != nil {
			return nil, err
		}
		// Try to infer return type from the block's return statements
		if !isConcreteExpectedType {
			if inferredType := t.inferBlockReturnType(b); inferredType != nil {
				retType = inferredType
			} else {
				// Only add return nil if we couldn't infer a type
				b.List = append(b.List, &ast.ReturnStmt{Results: []ast.Expr{ast.NewIdent("nil")}})
			}
		}
		body = b
	} else if ctx.Expression() != nil {
		expr, err := t.transformExpression(ctx.Expression())
		if err != nil {
			return nil, err
		}
		// Use expected type if concrete, otherwise infer from expression
		if !isConcreteExpectedType {
			retType = t.getExprType(expr)
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
			Results: &ast.FieldList{
				List: []*ast.Field{
					{Type: retType},
				},
			},
		},
		Body: body,
	}, nil
}

// inferBlockReturnType tries to infer the return type from a block's return statements.
// Returns nil if no concrete type can be inferred.
func (t *galaASTTransformer) inferBlockReturnType(block *ast.BlockStmt) ast.Expr {
	// Build a map of val variable types from declarations in this block.
	// When we see `var result = std.NewImmutable(X)`, we record the type of X for `result`.
	valTypes := make(map[string]ast.Expr)
	for _, stmt := range block.List {
		// Handle val declarations: var result = std.NewImmutable(X)
		if declStmt, ok := stmt.(*ast.DeclStmt); ok {
			if genDecl, ok := declStmt.Decl.(*ast.GenDecl); ok && genDecl.Tok == token.VAR {
				for _, spec := range genDecl.Specs {
					if valueSpec, ok := spec.(*ast.ValueSpec); ok {
						for i, name := range valueSpec.Names {
							if i < len(valueSpec.Values) {
								val := valueSpec.Values[i]
								// Check if this is a std.NewImmutable(X) call
								if callExpr, ok := val.(*ast.CallExpr); ok {
									if t.isNewImmutableCall(callExpr) && len(callExpr.Args) > 0 {
										// Store the type of the inner expression
										innerType := t.getExprType(callExpr.Args[0])
										if innerType != nil {
											valTypes[name.Name] = innerType
										}
									}
								}
							}
						}
					}
				}
			}
		}
		// Also handle short var declarations: result := std.NewImmutable(X)
		if assignStmt, ok := stmt.(*ast.AssignStmt); ok && assignStmt.Tok == token.DEFINE {
			for i, lhs := range assignStmt.Lhs {
				if ident, ok := lhs.(*ast.Ident); ok && i < len(assignStmt.Rhs) {
					rhs := assignStmt.Rhs[i]
					// Check if this is a std.NewImmutable(X) call
					if callExpr, ok := rhs.(*ast.CallExpr); ok {
						if t.isNewImmutableCall(callExpr) && len(callExpr.Args) > 0 {
							// Store the type of the inner expression
							innerType := t.getExprType(callExpr.Args[0])
							if innerType != nil {
								valTypes[ident.Name] = innerType
							}
						}
					}
				}
			}
		}
	}

	for _, stmt := range block.List {
		if retStmt, ok := stmt.(*ast.ReturnStmt); ok {
			if len(retStmt.Results) > 0 {
				result := retStmt.Results[0]
				// Handle the case of .Get() call on an Immutable (val)
				if callExpr, ok := result.(*ast.CallExpr); ok {
					if selExpr, ok := callExpr.Fun.(*ast.SelectorExpr); ok && selExpr.Sel.Name == "Get" {
						// This is a .Get() call, check if the receiver is an identifier
						if ident, ok := selExpr.X.(*ast.Ident); ok {
							// Look up the type from declarations we found earlier
							if typ, ok := valTypes[ident.Name]; ok {
								return typ
							}
						}
					}
				}
				// Fallback to direct type inference
				inferredType := t.getExprType(result)
				if inferredType != nil {
					if ident, ok := inferredType.(*ast.Ident); ok && ident.Name != "any" {
						return inferredType
					}
					// For non-ident types (like generic types), also return them
					if _, ok := inferredType.(*ast.Ident); !ok {
						return inferredType
					}
				}
			}
		}
	}
	return nil
}

// isNewImmutableCall checks if a call expression is a call to std.NewImmutable
func (t *galaASTTransformer) isNewImmutableCall(call *ast.CallExpr) bool {
	if selExpr, ok := call.Fun.(*ast.SelectorExpr); ok {
		if selExpr.Sel.Name == "NewImmutable" {
			if ident, ok := selExpr.X.(*ast.Ident); ok {
				return ident.Name == "std"
			}
		}
	}
	return false
}

// transformArgumentWithExpectedType transforms an argument expression, using the expected
// parameter type to properly type lambda expressions and partial function literals.
func (t *galaASTTransformer) transformArgumentWithExpectedType(exprCtx grammar.IExpressionContext, expectedType transpiler.Type) (ast.Expr, error) {
	// Try to find a partial function literal in this expression
	if pfCtx := t.findPartialFunctionInExpression(exprCtx); pfCtx != nil {
		return t.transformPartialFunctionLiteral(pfCtx, expectedType)
	}

	// Try to find a lambda in this expression
	if lambdaCtx := t.findLambdaInExpression(exprCtx); lambdaCtx != nil {
		// Extract the expected return type from the function type
		var expectedRetType ast.Expr
		if funcType, ok := expectedType.(transpiler.FuncType); ok && len(funcType.Results) > 0 {
			expectedRetType = t.typeToExpr(funcType.Results[0])
		}
		return t.transformLambdaWithExpectedType(lambdaCtx, expectedRetType)
	}
	// Not a lambda or partial function, transform normally
	return t.transformExpression(exprCtx)
}

// findPartialFunctionInExpression traverses the expression tree to find a partial function literal
func (t *galaASTTransformer) findPartialFunctionInExpression(exprCtx grammar.IExpressionContext) *grammar.PartialFunctionLiteralContext {
	if exprCtx == nil {
		return nil
	}

	// Walk down the expression tree following the grammar structure
	// expression -> orExpr -> andExpr -> ... -> postfixExpr -> primaryExpr -> partialFunctionLiteral
	switch ctx := exprCtx.(type) {
	case *grammar.ExpressionContext:
		if ctx.OrExpr() != nil {
			return t.findPartialFunctionInOrExpr(ctx.OrExpr().(*grammar.OrExprContext))
		}
	}
	return nil
}

func (t *galaASTTransformer) findPartialFunctionInOrExpr(ctx *grammar.OrExprContext) *grammar.PartialFunctionLiteralContext {
	if ctx == nil {
		return nil
	}
	andExprs := ctx.AllAndExpr()
	if len(andExprs) == 1 {
		return t.findPartialFunctionInAndExpr(andExprs[0].(*grammar.AndExprContext))
	}
	return nil
}

func (t *galaASTTransformer) findPartialFunctionInAndExpr(ctx *grammar.AndExprContext) *grammar.PartialFunctionLiteralContext {
	if ctx == nil {
		return nil
	}
	eqExprs := ctx.AllEqualityExpr()
	if len(eqExprs) == 1 {
		return t.findPartialFunctionInEqualityExpr(eqExprs[0].(*grammar.EqualityExprContext))
	}
	return nil
}

func (t *galaASTTransformer) findPartialFunctionInEqualityExpr(ctx *grammar.EqualityExprContext) *grammar.PartialFunctionLiteralContext {
	if ctx == nil {
		return nil
	}
	relExprs := ctx.AllRelationalExpr()
	if len(relExprs) == 1 {
		return t.findPartialFunctionInRelationalExpr(relExprs[0].(*grammar.RelationalExprContext))
	}
	return nil
}

func (t *galaASTTransformer) findPartialFunctionInRelationalExpr(ctx *grammar.RelationalExprContext) *grammar.PartialFunctionLiteralContext {
	if ctx == nil {
		return nil
	}
	addExprs := ctx.AllAdditiveExpr()
	if len(addExprs) == 1 {
		return t.findPartialFunctionInAdditiveExpr(addExprs[0].(*grammar.AdditiveExprContext))
	}
	return nil
}

func (t *galaASTTransformer) findPartialFunctionInAdditiveExpr(ctx *grammar.AdditiveExprContext) *grammar.PartialFunctionLiteralContext {
	if ctx == nil {
		return nil
	}
	mulExprs := ctx.AllMultiplicativeExpr()
	if len(mulExprs) == 1 {
		return t.findPartialFunctionInMultiplicativeExpr(mulExprs[0].(*grammar.MultiplicativeExprContext))
	}
	return nil
}

func (t *galaASTTransformer) findPartialFunctionInMultiplicativeExpr(ctx *grammar.MultiplicativeExprContext) *grammar.PartialFunctionLiteralContext {
	if ctx == nil {
		return nil
	}
	unaryExprs := ctx.AllUnaryExpr()
	if len(unaryExprs) == 1 {
		return t.findPartialFunctionInUnaryExpr(unaryExprs[0].(*grammar.UnaryExprContext))
	}
	return nil
}

func (t *galaASTTransformer) findPartialFunctionInUnaryExpr(ctx *grammar.UnaryExprContext) *grammar.PartialFunctionLiteralContext {
	if ctx == nil {
		return nil
	}
	if ctx.PostfixExpr() != nil {
		return t.findPartialFunctionInPostfixExpr(ctx.PostfixExpr().(*grammar.PostfixExprContext))
	}
	return nil
}

func (t *galaASTTransformer) findPartialFunctionInPostfixExpr(ctx *grammar.PostfixExprContext) *grammar.PartialFunctionLiteralContext {
	if ctx == nil {
		return nil
	}
	if ctx.PrimaryExpr() != nil {
		return t.findPartialFunctionInPrimaryExpr(ctx.PrimaryExpr().(*grammar.PrimaryExprContext))
	}
	return nil
}

func (t *galaASTTransformer) findPartialFunctionInPrimaryExpr(ctx *grammar.PrimaryExprContext) *grammar.PartialFunctionLiteralContext {
	if ctx == nil {
		return nil
	}
	if pf := ctx.PartialFunctionLiteral(); pf != nil {
		return pf.(*grammar.PartialFunctionLiteralContext)
	}
	return nil
}

// containsAny checks if the given type expression contains "any" as a type or type parameter.
// This is used to determine if an expected type is concrete enough to use for lambda return type.
func containsAny(expr ast.Expr) bool {
	if expr == nil {
		return false
	}
	switch e := expr.(type) {
	case *ast.Ident:
		return e.Name == "any"
	case *ast.IndexExpr:
		// Generic type like Option[any]
		return containsAny(e.X) || containsAny(e.Index)
	case *ast.IndexListExpr:
		// Multiple type args like Map[K, V]
		if containsAny(e.X) {
			return true
		}
		for _, idx := range e.Indices {
			if containsAny(idx) {
				return true
			}
		}
		return false
	case *ast.SelectorExpr:
		// pkg.Type - check X for any
		return containsAny(e.X)
	case *ast.StarExpr:
		return containsAny(e.X)
	case *ast.ArrayType:
		return containsAny(e.Elt)
	case *ast.MapType:
		return containsAny(e.Key) || containsAny(e.Value)
	case *ast.FuncType:
		if e.Params != nil {
			for _, f := range e.Params.List {
				if containsAny(f.Type) {
					return true
				}
			}
		}
		if e.Results != nil {
			for _, f := range e.Results.List {
				if containsAny(f.Type) {
					return true
				}
			}
		}
		return false
	}
	return false
}

// findLambdaInExpression traverses the expression tree to find a lambda expression
// if the expression is simply a lambda (not part of a larger expression).
func (t *galaASTTransformer) findLambdaInExpression(exprCtx grammar.IExpressionContext) *grammar.LambdaExpressionContext {
	if exprCtx == nil {
		return nil
	}
	orExpr := exprCtx.OrExpr()
	if orExpr == nil {
		return nil
	}
	orCtx := orExpr.(*grammar.OrExprContext)
	if len(orCtx.AllAndExpr()) != 1 {
		return nil
	}
	andCtx := orCtx.AndExpr(0).(*grammar.AndExprContext)
	if len(andCtx.AllEqualityExpr()) != 1 {
		return nil
	}
	eqCtx := andCtx.EqualityExpr(0).(*grammar.EqualityExprContext)
	if len(eqCtx.AllRelationalExpr()) != 1 {
		return nil
	}
	relCtx := eqCtx.RelationalExpr(0).(*grammar.RelationalExprContext)
	if len(relCtx.AllAdditiveExpr()) != 1 {
		return nil
	}
	addCtx := relCtx.AdditiveExpr(0).(*grammar.AdditiveExprContext)
	if len(addCtx.AllMultiplicativeExpr()) != 1 {
		return nil
	}
	mulCtx := addCtx.MultiplicativeExpr(0).(*grammar.MultiplicativeExprContext)
	if len(mulCtx.AllUnaryExpr()) != 1 {
		return nil
	}
	unaryCtx := mulCtx.UnaryExpr(0).(*grammar.UnaryExprContext)
	postfixExpr := unaryCtx.PostfixExpr()
	if postfixExpr == nil {
		return nil
	}
	postfixCtx := postfixExpr.(*grammar.PostfixExprContext)
	// Check that there are no postfix suffixes (no method calls, indexing, etc.)
	if len(postfixCtx.AllPostfixSuffix()) > 0 {
		return nil
	}
	primExpr := postfixCtx.PrimaryExpr()
	if primExpr == nil {
		return nil
	}
	primCtx := primExpr.(*grammar.PrimaryExprContext)
	lambdaExpr := primCtx.LambdaExpression()
	if lambdaExpr == nil {
		return nil
	}
	return lambdaExpr.(*grammar.LambdaExpressionContext)
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

// transformPartialFunctionLiteral transforms a partial function literal { case ... => ... }
// into a function that returns Option[T], where matched cases return Some(result)
// and unmatched cases return None[T]()
func (t *galaASTTransformer) transformPartialFunctionLiteral(ctx *grammar.PartialFunctionLiteralContext, expectedType transpiler.Type) (ast.Expr, error) {
	caseClauses := ctx.AllCaseClause()
	if len(caseClauses) == 0 {
		return nil, galaerr.NewSemanticError("partial function must have at least one case")
	}

	// Try to infer parameter type from expected function type or from patterns
	var paramType transpiler.Type
	if expectedType != nil {
		if funcType, ok := expectedType.(transpiler.FuncType); ok && len(funcType.Params) > 0 {
			paramType = funcType.Params[0]
		}
	}

	// If we couldn't infer from context, try to infer from the patterns themselves
	if paramType == nil || paramType.IsNil() {
		paramType = t.inferPartialFunctionParamType(caseClauses)
	}

	// Fall back to 'any' if we still can't infer
	if paramType == nil || paramType.IsNil() {
		paramType = transpiler.BasicType{Name: "any"}
	}

	paramName := "_pf_arg"
	t.pushScope()
	defer t.popScope()
	t.addVar(paramName, paramType)

	var clauses []ast.Stmt
	var resultTypes []transpiler.Type
	var casePatterns []string

	// Transform each case clause, wrapping results in Some(...)
	for _, cc := range caseClauses {
		ccCtx := cc.(*grammar.CaseClauseContext)
		patCtx := ccCtx.Pattern()
		patternText := patCtx.GetText()

		// Even wildcard case gets wrapped in Some for partial functions
		clause, resultType, err := t.transformPartialCaseClause(ccCtx, paramName, paramType)
		if err != nil {
			return nil, err
		}
		if clause != nil {
			clauses = append(clauses, clause)
		}
		if resultType != nil {
			resultTypes = append(resultTypes, resultType)
			casePatterns = append(casePatterns, fmt.Sprintf("case %s", patternText))
		}
	}

	// Infer common inner result type T from all branches
	innerResultType, err := t.inferCommonResultType(resultTypes, casePatterns)
	if err != nil {
		return nil, err
	}

	if innerResultType == nil || innerResultType.IsNil() {
		innerResultType = transpiler.BasicType{Name: "any"}
	}

	if t.typeHasUnresolvedParams(innerResultType) {
		innerResultType = transpiler.BasicType{Name: "any"}
	}

	// The final result type is Option[T]
	optionResultType := transpiler.GenericType{
		Base:   transpiler.NamedType{Package: transpiler.StdPackage, Name: transpiler.TypeOption},
		Params: []transpiler.Type{innerResultType},
	}

	// Generate default case: return None[T]()
	noneReturn := t.generateNoneReturn(innerResultType)

	// Build the function body
	var stmts []ast.Stmt
	stmts = append(stmts, clauses...)
	stmts = append(stmts, noneReturn)

	// Build function literal returning Option[T]
	t.needsStdImport = true
	funcLit := &ast.FuncLit{
		Type: &ast.FuncType{
			Params:  &ast.FieldList{List: []*ast.Field{{Names: []*ast.Ident{ast.NewIdent(paramName)}, Type: t.typeToExpr(paramType)}}},
			Results: &ast.FieldList{List: []*ast.Field{{Type: t.typeToExpr(optionResultType)}}},
		},
		Body: &ast.BlockStmt{List: stmts},
	}

	return funcLit, nil
}

// inferPartialFunctionParamType tries to infer the parameter type from the patterns
func (t *galaASTTransformer) inferPartialFunctionParamType(caseClauses []grammar.ICaseClauseContext) transpiler.Type {
	for _, cc := range caseClauses {
		ccCtx := cc.(*grammar.CaseClauseContext)
		patCtx := ccCtx.Pattern()

		// Check for typed pattern like "x: int"
		if tp, ok := patCtx.(*grammar.TypedPatternContext); ok {
			typeExpr, err := t.transformType(tp.Type_())
			if err == nil {
				return t.exprToType(typeExpr)
			}
		}

		// Check for extractor patterns like Some(n), Left(x), etc.
		if ep, ok := patCtx.(*grammar.ExpressionPatternContext); ok {
			patternText := ep.GetText()
			// Common Option patterns
			if strings.HasPrefix(patternText, "Some(") || patternText == "None()" {
				return transpiler.GenericType{
					Base:   transpiler.NamedType{Package: transpiler.StdPackage, Name: transpiler.TypeOption},
					Params: []transpiler.Type{transpiler.BasicType{Name: "any"}},
				}
			}
			// Common Either patterns
			if strings.HasPrefix(patternText, "Left(") || strings.HasPrefix(patternText, "Right(") {
				return transpiler.GenericType{
					Base:   transpiler.NamedType{Package: transpiler.StdPackage, Name: transpiler.TypeEither},
					Params: []transpiler.Type{transpiler.BasicType{Name: "any"}, transpiler.BasicType{Name: "any"}},
				}
			}
		}
	}
	return nil
}

// transformPartialCaseClause transforms a case clause for partial function
// The result expression is wrapped in Some(...)
func (t *galaASTTransformer) transformPartialCaseClause(ctx *grammar.CaseClauseContext, paramName string, matchedType transpiler.Type) (ast.Stmt, transpiler.Type, error) {
	t.pushScope()
	defer t.popScope()

	patCtx := ctx.Pattern()
	cond, bindings, err := t.transformPatternWithType(patCtx, ast.NewIdent(paramName), matchedType)
	if err != nil {
		return nil, nil, err
	}

	// Handle guard clause
	if ctx.GetGuard() != nil {
		guard, err := t.transformExpression(ctx.GetGuard())
		if err != nil {
			return nil, nil, err
		}
		cond = &ast.BinaryExpr{
			X:  cond,
			Op: token.LAND,
			Y:  guard,
		}
	}

	var body []ast.Stmt
	var resultType transpiler.Type

	if ctx.GetBodyBlock() != nil {
		b, err := t.transformBlock(ctx.GetBodyBlock().(*grammar.BlockContext))
		if err != nil {
			return nil, nil, err
		}
		// Wrap the last expression/return in Some
		body = t.wrapBlockReturnsInSome(b.List)
		// Infer type from the original (unwrapped) last statement
		if len(b.List) > 0 {
			if ret, ok := b.List[len(b.List)-1].(*ast.ReturnStmt); ok && len(ret.Results) > 0 {
				resultType = t.inferResultType(ret.Results[0])
			}
		}
	} else if ctx.GetBody() != nil {
		expr, err := t.transformExpression(ctx.GetBody())
		if err != nil {
			return nil, nil, err
		}
		// Wrap in Some(expr)
		someCall := t.wrapInSome(expr)
		body = []ast.Stmt{&ast.ReturnStmt{Results: []ast.Expr{someCall}}}
		resultType = t.inferResultType(expr)
	}

	bodyBlock := &ast.BlockStmt{List: body}
	ifStmt := &ast.IfStmt{
		Cond: cond,
		Body: bodyBlock,
	}

	if len(bindings) > 0 {
		return &ast.BlockStmt{
			List: append(bindings, ifStmt),
		}, resultType, nil
	}

	return ifStmt, resultType, nil
}

// wrapInSome generates: Some[T]{}.Apply(expr)
func (t *galaASTTransformer) wrapInSome(expr ast.Expr) ast.Expr {
	// Infer the type of expr for the type parameter
	exprType := t.getExprTypeNameManual(expr)
	if exprType == nil || exprType.IsNil() {
		exprType, _ = t.inferExprType(expr)
	}

	var someTypeExpr ast.Expr
	if exprType != nil && !exprType.IsNil() && exprType.String() != "any" {
		someTypeExpr = &ast.IndexExpr{
			X:     t.stdIdent("Some"),
			Index: t.typeToExpr(exprType),
		}
	} else {
		someTypeExpr = &ast.IndexExpr{
			X:     t.stdIdent("Some"),
			Index: ast.NewIdent("any"),
		}
	}

	// Generate: Some[T]{}.Apply(expr)
	return &ast.CallExpr{
		Fun: &ast.SelectorExpr{
			X:   &ast.CompositeLit{Type: someTypeExpr},
			Sel: ast.NewIdent("Apply"),
		},
		Args: []ast.Expr{expr},
	}
}

// generateNoneReturn generates: return None[T]{}.Apply()
func (t *galaASTTransformer) generateNoneReturn(innerType transpiler.Type) ast.Stmt {
	var noneTypeExpr ast.Expr
	if innerType != nil && !innerType.IsNil() && innerType.String() != "any" {
		noneTypeExpr = &ast.IndexExpr{
			X:     t.stdIdent("None"),
			Index: t.typeToExpr(innerType),
		}
	} else {
		noneTypeExpr = &ast.IndexExpr{
			X:     t.stdIdent("None"),
			Index: ast.NewIdent("any"),
		}
	}

	// Generate: return None[T]{}.Apply()
	noneCall := &ast.CallExpr{
		Fun: &ast.SelectorExpr{
			X:   &ast.CompositeLit{Type: noneTypeExpr},
			Sel: ast.NewIdent("Apply"),
		},
	}

	return &ast.ReturnStmt{Results: []ast.Expr{noneCall}}
}

// wrapBlockReturnsInSome wraps the final return/expression in a block with Some
func (t *galaASTTransformer) wrapBlockReturnsInSome(stmts []ast.Stmt) []ast.Stmt {
	if len(stmts) == 0 {
		return stmts
	}

	result := make([]ast.Stmt, len(stmts))
	copy(result, stmts)

	// Only wrap the last statement if it's a return
	last := result[len(result)-1]
	if ret, ok := last.(*ast.ReturnStmt); ok && len(ret.Results) > 0 {
		result[len(result)-1] = &ast.ReturnStmt{
			Results: []ast.Expr{t.wrapInSome(ret.Results[0])},
		}
	}

	return result
}

// isGenericMethodName checks if a method is marked as generic for a given type name
func (t *galaASTTransformer) isGenericMethodName(typeName, methodName string) bool {
	if typeName == "" {
		return false
	}
	return t.genericMethods[typeName] != nil && t.genericMethods[typeName][methodName]
}

// isGenericMethodWithImports checks if a method is generic, searching through all possible package lookups
func (t *galaASTTransformer) isGenericMethodWithImports(lookupBaseName, recvPkg, methodName string) bool {
	// First try the simple name
	if t.isGenericMethodName(lookupBaseName, methodName) {
		return true
	}
	// Try package-qualified name if receiver package is known
	if recvPkg != "" {
		if t.isGenericMethodName(recvPkg+"."+lookupBaseName, methodName) {
			return true
		}
	}
	// Search through dot-imported packages
	for _, dotPkg := range t.dotImports {
		if t.isGenericMethodName(dotPkg+"."+lookupBaseName, methodName) {
			return true
		}
	}
	// Search through all imported packages
	for alias := range t.imports {
		actualPkg := alias
		if actual, ok := t.importAliases[alias]; ok {
			actualPkg = actual
		}
		if t.isGenericMethodName(actualPkg+"."+lookupBaseName, methodName) {
			return true
		}
	}
	// Fallback: check typeMetas for methods with type parameters
	// This handles cases where genericMethods map wasn't fully populated
	if t.isMethodGenericViaTypeMeta(lookupBaseName, methodName) {
		return true
	}
	if recvPkg != "" {
		if t.isMethodGenericViaTypeMeta(recvPkg+"."+lookupBaseName, methodName) {
			return true
		}
	}
	for _, dotPkg := range t.dotImports {
		if t.isMethodGenericViaTypeMeta(dotPkg+"."+lookupBaseName, methodName) {
			return true
		}
	}
	return false
}

// isMethodGenericViaTypeMeta checks if a method has type parameters via typeMetas lookup
func (t *galaASTTransformer) isMethodGenericViaTypeMeta(typeName, methodName string) bool {
	if typeMeta, ok := t.typeMetas[typeName]; ok {
		if methodMeta, ok := typeMeta.Methods[methodName]; ok {
			return len(methodMeta.TypeParams) > 0 || methodMeta.IsGeneric
		}
	}
	return false
}
