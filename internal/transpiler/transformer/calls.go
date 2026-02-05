package transformer

import (
	"fmt"
	"go/ast"
	"strings"

	"martianoff/gala/galaerr"
	"martianoff/gala/internal/parser/grammar"
	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/registry"
)

// This file contains function/method call transformation logic extracted from expressions.go
// Functions: applyCallSuffix, transformCallWithArgsCtx, handleNamedArgsCall,
//            transformArgumentWithExpectedType, inferTypeArgsFromApply,
//            isGenericMethodName, isGenericMethodWithImports, isMethodGenericViaTypeMeta

func (t *galaASTTransformer) applyCallSuffix(base ast.Expr, suffix *grammar.PostfixSuffixContext) (ast.Expr, error) {
	// When making a function call with type arguments (e.g., Unfold[int, Tuple[int, int]](...)),
	// the type arguments need to be qualified with std. prefix if they are std types.
	// This is because at parse time we don't know if T[A, B] is a type instantiation or array access.
	base = t.qualifyTypeArgsInExpr(base)

	argList := suffix.ArgumentList()
	if argList == nil {
		// Empty argument list - check for zero-argument Apply method
		typeName := t.getBaseTypeName(base)
		if typeName != "" {
			// Use unified resolution to find type metadata
			typeMeta := t.getTypeMeta(typeName)
			if typeMeta != nil {
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
								if t.importManager.IsPackage(id.Name) || id.Name == registry.StdPackageName {
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
					if t.importManager.IsPackage(id.Name) {
						isPkg = true
					}
				}

				if !isPkg {
					// Transform to standalone function call: TypeName_Method[T](receiver)
					var funExpr ast.Expr
					if !recvType.IsNil() {
						recvPkg := recvType.GetPackage()
						if recvPkg == registry.StdPackageName || strings.HasPrefix(lookupBaseName, "std.") {
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
		if id, ok := sel.X.(*ast.Ident); ok && id.Name == registry.StdPackageName {
			// Not a method call
		} else {
			receiver = sel.X
			method = sel.Sel.Name
		}
	} else if idx, ok := fun.(*ast.IndexExpr); ok {
		if sel, ok := idx.X.(*ast.SelectorExpr); ok {
			if id, ok := sel.X.(*ast.Ident); ok && id.Name == registry.StdPackageName {
				// Not a method call
			} else {
				receiver = sel.X
				method = sel.Sel.Name
				typeArgs = []ast.Expr{t.qualifyTypeExpr(idx.Index)}
			}
		}
	} else if idxList, ok := fun.(*ast.IndexListExpr); ok {
		if sel, ok := idxList.X.(*ast.SelectorExpr); ok {
			if id, ok := sel.X.(*ast.Ident); ok && id.Name == registry.StdPackageName {
				// Not a method call
			} else {
				receiver = sel.X
				method = sel.Sel.Name
				typeArgs = t.qualifyTypeExprs(idxList.Indices)
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
			if t.importManager.IsPackage(id.Name) {
				isPkg = true
			}
		}

		if !isPkg {
			// Transform generic method call to standalone function call
			// Get method metadata for parameter types using unified resolution
			typeMeta := t.getTypeMeta(lookupBaseName)
			var methodMeta *transpiler.MethodMetadata
			if typeMeta != nil {
				methodMeta = typeMeta.Methods[method]
				// Update lookupBaseName to the resolved name for later use
				if resolved := t.resolveTypeMetaName(lookupBaseName); resolved != "" {
					lookupBaseName = resolved
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
				if recvPkg == registry.StdPackageName || strings.HasPrefix(lookupBaseName, "std.") {
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
		// Use unified resolution to find type metadata
		typeMeta := t.getTypeMeta(lookupBaseName)
		if typeMeta != nil && len(typeMeta.TypeParams) > 0 {
			methodMeta = typeMeta.Methods[method]
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
		// Use unified resolution to find type metadata
		typeMeta := t.getTypeMeta(typeName)
		if typeMeta != nil {
			// Update typeName to resolved name for subsequent lookups
			if resolved := t.resolveTypeMetaName(typeName); resolved != "" {
				typeName = resolved
			}
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
					typeArgs = []ast.Expr{t.qualifyTypeExpr(idx.Index)}
				} else if idxList, ok := fun.(*ast.IndexListExpr); ok {
					baseExpr = idxList.X
					hasTypeArgs = true
					typeArgs = t.qualifyTypeExprs(idxList.Indices)
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
						if t.importManager.IsPackage(id.Name) || id.Name == registry.StdPackageName {
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
							isStdType = !resolvedType.IsNil() && resolvedType.GetPackage() == registry.StdPackageName
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
			if typeMeta := t.getTypeMeta(litTypeName); typeMeta != nil {
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
			if typeMeta := t.getTypeMeta(varTypeName); typeMeta != nil {
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
			if typeMeta := t.getTypeMeta(resolvedTypeName); typeMeta != nil && len(typeMeta.TypeParams) > 0 {
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
				// Check for nil assignment to immutable pointer field
				if immutFlags != nil && i < len(immutFlags) && immutFlags[i] {
					if fieldType, hasType := fieldTypes[fieldName]; hasType {
						if _, isPtr := fieldType.(transpiler.PointerType); isPtr {
							if ident, isIdent := val.(*ast.Ident); isIdent && ident.Name == "nil" {
								return nil, galaerr.NewSemanticError(fmt.Sprintf(
									"cannot assign nil to immutable pointer field '%s' - use 'var %s' to make it mutable",
									fieldName, fieldName))
							}
						}
					}
				}

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

func (t *galaASTTransformer) transformArgumentWithExpectedType(exprCtx grammar.IExpressionContext, expectedType transpiler.Type) (ast.Expr, error) {
	// Try to find a partial function literal in this expression
	if pfCtx := t.findPartialFunctionInExpression(exprCtx); pfCtx != nil {
		return t.transformPartialFunctionLiteral(pfCtx, expectedType)
	}

	// Try to find a lambda in this expression
	if lambdaCtx := t.findLambdaInExpression(exprCtx); lambdaCtx != nil {
		// Extract the expected return type from the function type
		var expectedRetType ast.Expr
		if funcType, ok := expectedType.(transpiler.FuncType); ok {
			if len(funcType.Results) > 0 {
				expectedRetType = t.typeToExpr(funcType.Results[0])
			} else {
				// Void function - use sentinel value
				expectedRetType = ExpectedVoid
			}
		}
		return t.transformLambdaWithExpectedType(lambdaCtx, expectedRetType)
	}
	// Not a lambda or partial function, transform normally
	return t.transformExpression(exprCtx)
}

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
	// Search through all imported packages (dot and non-dot)
	for _, entry := range t.importManager.All() {
		if t.isGenericMethodName(entry.PkgName+"."+lookupBaseName, methodName) {
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
	for _, entry := range t.importManager.All() {
		if entry.IsDot {
			if t.isMethodGenericViaTypeMeta(entry.PkgName+"."+lookupBaseName, methodName) {
				return true
			}
		}
	}
	return false
}

// isMethodGenericViaTypeMeta checks if a method has type parameters via typeMetas lookup
func (t *galaASTTransformer) isMethodGenericViaTypeMeta(typeName, methodName string) bool {
	if typeMeta := t.getTypeMeta(typeName); typeMeta != nil {
		if methodMeta, ok := typeMeta.Methods[methodName]; ok {
			return len(methodMeta.TypeParams) > 0 || methodMeta.IsGeneric
		}
	}
	return false
}
