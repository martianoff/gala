package transformer

import (
	"fmt"
	"go/ast"
	"strings"

	"github.com/antlr4-go/antlr/v4"

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
	// Rewrite Println/Print to fmt.Println/fmt.Print (auto-imported)
	base = t.rewriteBuiltinPrintFuncs(base)

	// When making a function call with type arguments (e.g., Unfold[int, Tuple[int, int]](...)),
	// the type arguments need to be qualified with std. prefix if they are std types.
	// This is because at parse time we don't know if T[A, B] is a type instantiation or array access.
	base = t.qualifyTypeArgsInExpr(base)

	argList := suffix.ArgumentList()
	if argList == nil {
		// Check for compiler intrinsic: StructMeta[T]()
		if t.getBaseTypeName(base) == "StructMeta" {
			return t.transformStructMetaConstruction(base, suffix.GetStart().GetLine(), suffix.GetStart().GetColumn())
		}
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
							// Zero-argument Apply method: TypeName[T]{}.Apply()
							receiverType := base
							// If the type is generic but no explicit type args were provided,
							// try to infer them from the match subject type via companion relationship.
							// e.g., None() inside `Option[int] match { ... }` → None[int]{}.Apply()
							if len(typeMeta.TypeParams) > 0 && baseExpr == base {
								if inferredBase := t.inferZeroArgTypeParams(typeName, typeMeta); inferredBase != nil {
									receiverType = inferredBase
								}
							}
							receiver := &ast.CompositeLit{Type: receiverType}
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
						if recvPkg == registry.StdPackageName || hasStdPrefix(lookupBaseName) {
							baseName := stripStdPrefix(lookupBaseName)
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

		// Zero-argument call — check if function has default params that need injection
		if funcName := t.extractFuncName(base); funcName != "" {
			if funcMeta := t.getFunction(funcName); funcMeta != nil && len(funcMeta.DefaultExprs) > 0 && len(funcMeta.ParamTypes) > 0 {
				filled, err := t.fillDefaultArgs(nil, funcMeta, suffix.GetStart().GetLine(), suffix.GetStart().GetColumn())
				if err != nil {
					return nil, err
				}
				return &ast.CallExpr{Fun: base, Args: filled}, nil
			}
		}
		return &ast.CallExpr{Fun: base, Args: nil}, nil
	}

	return t.transformCallWithArgsCtx(base, argList.(*grammar.ArgumentListContext))
}

// resolveReceiverTypeAndLookupKey normalizes the inferred type of a call
// receiver into its canonical form (preserving generic type parameters when
// present) and returns both the resolved Type and the pointer-stripped base
// name used as a lookup key in t.genericMethods. When the receiver is nil
// (package-qualified call) the returned type is NilType and the key is "".
// Extracted from transformCallWithArgsCtx as part of A1.
func (t *galaASTTransformer) resolveReceiverTypeAndLookupKey(receiver ast.Expr) (transpiler.Type, string) {
	recvType := t.getExprTypeName(receiver)
	if gen, ok := recvType.(transpiler.GenericType); ok {
		if qBase := t.getType(gen.Base.String()); !qBase.IsNil() {
			recvType = transpiler.GenericType{Base: qBase, Params: gen.Params}
		}
	} else if qName := t.getType(recvType.BaseName()); !qName.IsNil() {
		recvType = qName
	}
	// Strip pointer prefix for genericMethods lookup since methods are
	// registered under the base type name without the pointer marker.
	return recvType, strings.TrimPrefix(recvType.BaseName(), "*")
}

// splitCallTarget classifies a call expression `fun` as either:
//   - a package-qualified function call (returns receiver=nil, method="", typeArgs=nil)
//   - a method call (returns the receiver expression, the method name, and any
//     explicit type arguments from an IndexExpr/IndexListExpr wrapper)
//
// Extracted from transformCallWithArgsCtx as part of A1. A receiver whose
// first identifier is a known package (imported or std) is always treated as
// a package-qualified function call, never a method call.
func (t *galaASTTransformer) splitCallTarget(fun ast.Expr) (receiver ast.Expr, method string, typeArgs []ast.Expr) {
	isPkgHead := func(x ast.Expr) bool {
		id, ok := x.(*ast.Ident)
		return ok && (t.importManager.IsPackage(id.Name) || id.Name == registry.StdPackageName)
	}

	switch f := fun.(type) {
	case *ast.SelectorExpr:
		if isPkgHead(f.X) {
			return nil, "", nil
		}
		return f.X, f.Sel.Name, nil
	case *ast.IndexExpr:
		sel, ok := f.X.(*ast.SelectorExpr)
		if !ok || isPkgHead(sel.X) {
			return nil, "", nil
		}
		return sel.X, sel.Sel.Name, []ast.Expr{t.qualifyTypeExpr(f.Index)}
	case *ast.IndexListExpr:
		sel, ok := f.X.(*ast.SelectorExpr)
		if !ok || isPkgHead(sel.X) {
			return nil, "", nil
		}
		return sel.X, sel.Sel.Name, t.qualifyTypeExprs(f.Indices)
	}
	return nil, "", nil
}

// transformCallWithArgsCtx is the primary entry point for transforming GALA call
// expressions (functions, methods, constructors, companion-object Apply, etc.) into
// Go AST call expressions.
//
// TODO(B1): this function has grown to ~850 lines and interleaves several concerns:
//
//	Section 1  (~175-210) Copy method short-circuit
//	Section 2  (~180-225) Method/function dispatch prelude (receiver, method, typeArgs)
//	Section 3  (~225-315) Generic method -> standalone function rewrite + type-param
//	                      substitution (recv type args, method type args, argument-based
//	                      inference, "any" fallback)
//	Section 4  (~315-400) Regular method-call branch with lambda expected-type passing
//	Section 5  (~400-540) Regular function call: arg transformation, function metadata
//	                      lookup, Go type-info fallback
//	Section 6  (~540-620) Struct construction path: resolve struct field types as
//	                      expected types for lambda arguments before transformation
//	Section 7  (~620-720) Generic-function type-arg pre-inference from non-lambda args
//	Section 8  (~720-780) Named-args path (handleNamedArgsCall)
//	Section 9  (~780-850) Default-arg injection when positional count is short
//	Section 10 (~850-920) Compiler intrinsics (StructMeta[T]())
//	Section 11 (~920-1000) Companion-object Apply: Some[A](v) -> Some[A]{}.Apply(v)
//	Section 12 (~1000-1020) Variable-with-Apply-method: add5(10) -> add5.Apply(10)
//
// The planned refactor is to (a) introduce a `callResolution` struct carrying the
// mutable state (receiver, method, typeArgs, recvType, typeSubst, preTransformed),
// (b) extract each section into a method of the form
// `(cr *callResolution) tryFoo(ctx *argListCtx) (expr, bool, error)` returning a
// sentinel when the section handled the call, and (c) keep this function as a
// thin dispatcher that walks the sections in order. Doing the split in this PR
// alongside B2-B15 would be high-risk; it is intentionally deferred to a
// follow-up so each extraction can be tested in isolation.
//
// Until then: treat the section markers above as navigation anchors when
// editing, and avoid piling new logic into the middle of an existing section.
func (t *galaASTTransformer) transformCallWithArgsCtx(fun ast.Expr, argListCtx *grammar.ArgumentListContext) (ast.Expr, error) {
	// --- Section 1: Copy method short-circuit ---
	if sel, ok := fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "Copy" {
		return t.transformCopyCall(sel.X, argListCtx)
	}

	// --- Section 2: Method/function dispatch prelude ---
	// Classify `fun` as either a package-qualified function call (receiver==nil)
	// or a method call (receiver, method, typeArgs populated). Extracted into
	// splitCallTarget as part of A1.
	receiver, method, typeArgs := t.splitCallTarget(fun)

	// A1: resolve the receiver type to a canonical form and compute the
	// package-agnostic lookup key used by the generic-method registry.
	recvType, lookupBaseName := t.resolveReceiverTypeAndLookupKey(receiver)

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
			typeMeta, resolvedName := t.getTypeMetaResolved(lookupBaseName)
			var methodMeta *transpiler.MethodMetadata
			if typeMeta != nil {
				methodMeta = typeMeta.Methods[method]
				// Update lookupBaseName to the resolved name for later use
				lookupBaseName = resolvedName
			}

			// Build type argument substitution map
			typeSubst := make(map[string]string)
			var recvTypeArgStrings []string
			if methodMeta != nil && typeMeta != nil {
				// Add receiver's type args (e.g., T -> int)
				recvTypeArgStrings = t.getReceiverTypeArgStrings(recvType)
				for i, tp := range typeMeta.TypeParams {
					if i < len(recvTypeArgStrings) {
						typeSubst[tp] = recvTypeArgStrings[i]
					}
				}
				// Add method's explicit type args (e.g., U -> string)
				for i, tp := range methodMeta.TypeParams {
					if i < len(typeArgs) {
						typeSubst[tp] = t.exprToTypeString(typeArgs[i])
					}
					// Don't default to "any" — will try to infer from non-lambda args below
				}
			}

			// Try to infer unresolved method type params from non-lambda arguments.
			// This enables FoldLeft(0, (acc, x) => acc + x) to infer U=int from the zero value 0.
			preTransformed := make(map[int]ast.Expr)
			if methodMeta != nil && typeMeta != nil && len(methodMeta.TypeParams) > 0 && len(typeArgs) < len(methodMeta.TypeParams) {
				recvTypeArgTypes := make([]transpiler.Type, 0, len(recvTypeArgStrings))
				for _, a := range recvTypeArgStrings {
					recvTypeArgTypes = append(recvTypeArgTypes, transpiler.ParseType(a))
				}
				for i, argCtx := range argListCtx.AllArgument() {
					if i >= len(methodMeta.ParamTypes) {
						break
					}
					arg := argCtx.(*grammar.ArgumentContext)
					exprCtx, lambdaCtx, _, extractErr := extractArgContent(arg)
					if extractErr != nil {
						continue
					}
					// Skip lambda and partial function arguments — can't infer types from them
					if lambdaCtx != nil || t.findLambdaInExpression(exprCtx) != nil || t.findPartialFunctionInExpression(exprCtx) != nil {
						continue
					}
					expr, err := t.transformExpression(exprCtx)
					if err != nil {
						continue
					}
					preTransformed[i] = expr
					argType := t.getExprTypeName(expr)
					if argType == nil || argType.IsNil() || argType.IsAny() {
						continue
					}
					substitutedParamType := t.substituteConcreteTypes(methodMeta.ParamTypes[i], typeMeta.TypeParams, recvTypeArgTypes)
					inferredMap := make(map[string]transpiler.Type)
					t.unifyForInference(substitutedParamType, argType, methodMeta.TypeParams, inferredMap)
					for tp, inferred := range inferredMap {
						if _, alreadySet := typeSubst[tp]; !alreadySet {
							typeSubst[tp] = inferred.String()
						}
					}
				}
			}
			// Default remaining unresolved method type params to "any".
			// NOTE: This violates project rule #3 (never emit `any` implicitly). It is
			// retained as a fallback because removing it breaks code where the type
			// parameter is genuinely unconstrained by the call site (e.g., a phantom
			// type param used only in the return type with no constraining argument).
			// A warning is emitted when GALA_WARN_TYPES=1 so authors can surface and
			// annotate these sites. TODO(B5): replace with a hard error once all
			// inference paths (call-site context, expected return type) are wired up.
			if methodMeta != nil {
				for _, tp := range methodMeta.TypeParams {
					if _, ok := typeSubst[tp]; !ok {
						t.warnInference("method type parameter %q defaulted to `any` (unresolved from arguments)", tp)
						typeSubst[tp] = "any"
					}
				}
			}

			var mArgs []ast.Expr
			hasSpread := false
			for i, argCtx := range argListCtx.AllArgument() {
				arg := argCtx.(*grammar.ArgumentContext)
				exprCtx, lambdaCtx, isSpread, extractErr := extractArgContent(arg)
				if extractErr != nil {
					return nil, extractErr
				}
				if isSpread {
					hasSpread = true
				}

				// Reuse pre-transformed expression if available (already processed during type inference)
				if expr, ok := preTransformed[i]; ok {
					mArgs = append(mArgs, expr)
					continue
				}

				// Get expected parameter type if available, with type substitution
				genMethodCtx := t.buildMethodCallContext(methodMeta, typeSubst, false)
				expectedType := t.resolveExpectedArgType(genMethodCtx, i)

				if lambdaCtx != nil {
					// Direct lambda argument (FIX-050)
					expr, err := t.transformLambdaArgWithExpectedType(lambdaCtx, expectedType)
					if err != nil {
						return nil, err
					}
					mArgs = append(mArgs, expr)
				} else {
					expr, err := t.transformArgumentWithExpectedType(exprCtx, expectedType)
					if err != nil {
						return nil, err
					}
					mArgs = append(mArgs, expr)
				}
			}

			var funExpr ast.Expr
			if !recvType.IsNil() {
				recvPkg := recvType.GetPackage()
				if recvPkg == registry.StdPackageName || hasStdPrefix(lookupBaseName) {
					baseName := stripStdPrefix(lookupBaseName)
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
				Fun:      funExpr,
				Args:     append([]ast.Expr{receiver}, mArgs...),
				Ellipsis: ellipsisPos(hasSpread),
			}, nil
		}
	}

	// Handle regular method calls on generic types (methods without type params on receiver types with type params)
	// These should remain as method calls but still need expected types for lambda arguments
	if receiver != nil && !isGenericMethod && method != "" {
		var methodMeta *transpiler.MethodMetadata
		// Use unified resolution to find type metadata
		// Look up method metadata for ALL types (not just generic ones) so that
		// non-generic wrapper types like Str can pass expected function types to lambda arguments.
		typeMeta := t.getTypeMeta(lookupBaseName)
		if typeMeta != nil {
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

			// If receiver has unresolved type params, skip full expected type inference
			// but still detect void function parameters for lambda return type stripping
			if hasUnresolvedTypeParams {
				var mArgs []ast.Expr
				hasSpread := false
				for i, argCtx := range argListCtx.AllArgument() {
					arg := argCtx.(*grammar.ArgumentContext)
					exprCtx, lambdaCtx, isSpread, extractErr := extractArgContent(arg)
					if extractErr != nil {
						return nil, extractErr
					}
					if isSpread {
						hasSpread = true
					}
					// Only pass void function types (avoids unresolved type params in return types)
					unresolvedCtx := t.buildMethodCallContext(methodMeta, typeSubst, true)
					expectedType := t.resolveExpectedArgType(unresolvedCtx, i)
					var expr ast.Expr
					var err error
					if lambdaCtx != nil {
						expr, err = t.transformLambdaArgWithExpectedType(lambdaCtx, expectedType)
					} else {
						expr, err = t.transformArgumentWithExpectedType(exprCtx, expectedType)
					}
					if err != nil {
						return nil, err
					}
					mArgs = append(mArgs, expr)
				}
				return &ast.CallExpr{
					Fun:      &ast.SelectorExpr{X: receiver, Sel: ast.NewIdent(method)},
					Args:     mArgs,
					Ellipsis: ellipsisPos(hasSpread),
				}, nil
			}

			// Transform arguments with expected types, handling named args and defaults
			var mArgs []ast.Expr
			mNamedArgs := make(map[string]ast.Expr)
			hasSpread := false
			argIdx := 0
			for _, argCtx := range argListCtx.AllArgument() {
				arg := argCtx.(*grammar.ArgumentContext)
				exprCtx, lambdaCtx, isSpreadAll, extractErr := extractArgContent(arg)
				if extractErr != nil {
					return nil, extractErr
				}
				if isSpreadAll {
					hasSpread = true
				}
				if arg.Identifier() != nil {
					argName := arg.Identifier().GetText()
					resolvedMethodCtx := t.buildMethodCallContext(methodMeta, typeSubst, false)
					expectedType := t.resolveNamedArgExpectedType(resolvedMethodCtx, argName)
					var expr ast.Expr
					var err error
					if lambdaCtx != nil {
						expr, err = t.transformLambdaArgWithExpectedType(lambdaCtx, expectedType)
					} else {
						expr, err = t.transformArgumentWithExpectedType(exprCtx, expectedType)
					}
					if err != nil {
						return nil, err
					}
					mNamedArgs[argName] = expr
				} else {
					resolvedMethodCtx := t.buildMethodCallContext(methodMeta, typeSubst, false)
					expectedType := t.resolveExpectedArgType(resolvedMethodCtx, argIdx)
					var expr ast.Expr
					var err error
					if lambdaCtx != nil {
						expr, err = t.transformLambdaArgWithExpectedType(lambdaCtx, expectedType)
					} else {
						expr, err = t.transformArgumentWithExpectedType(exprCtx, expectedType)
					}
					if err != nil {
						return nil, err
					}
					mArgs = append(mArgs, expr)
					argIdx++
				}
			}
			methodFun := &ast.SelectorExpr{X: receiver, Sel: ast.NewIdent(method)}
			recvTypeName := recvType.BaseName()
			if len(mNamedArgs) > 0 && len(methodMeta.ParamNames) > 0 {
				return t.handleNamedArgsMethodCall(methodFun, receiver, mArgs, mNamedArgs, methodMeta, recvTypeName, argListCtx.GetStart().GetLine(), argListCtx.GetStart().GetColumn())
			}
			if len(methodMeta.DefaultExprs) > 0 && len(mArgs) < len(methodMeta.ParamTypes) {
				filled, err := t.fillDefaultArgsMethod(receiver, mArgs, methodMeta, recvTypeName, argListCtx.GetStart().GetLine(), argListCtx.GetStart().GetColumn())
				if err != nil {
					return nil, err
				}
				return &ast.CallExpr{Fun: methodFun, Args: filled, Ellipsis: ellipsisPos(hasSpread)}, nil
			}
			return &ast.CallExpr{Fun: methodFun, Args: mArgs, Ellipsis: ellipsisPos(hasSpread)}, nil
		}

		// FIX-042: When method metadata is not found (receiver type unresolvable),
		// still generate the method call directly instead of falling through to the
		// regular function call handler which would lose the receiver.method structure.
		var mArgs []ast.Expr
		hasSpread := false
		for _, argCtx := range argListCtx.AllArgument() {
			arg := argCtx.(*grammar.ArgumentContext)
			exprCtx, lambdaCtx, isSpread, extractErr := extractArgContent(arg)
			if extractErr != nil {
				return nil, extractErr
			}
			if isSpread {
				hasSpread = true
			}
			var expr ast.Expr
			var err error
			if lambdaCtx != nil {
				expr, err = t.transformLambdaArgWithExpectedType(lambdaCtx, transpiler.NilType{})
			} else {
				expr, err = t.transformArgumentWithExpectedType(exprCtx, transpiler.NilType{})
			}
			if err != nil {
				return nil, err
			}
			mArgs = append(mArgs, expr)
		}
		return &ast.CallExpr{
			Fun:      &ast.SelectorExpr{X: receiver, Sel: ast.NewIdent(method)},
			Args:     mArgs,
			Ellipsis: ellipsisPos(hasSpread),
		}, nil
	}

	// Regular function call - transform arguments
	// Look up function metadata for expected parameter types (enables void lambda detection)
	var funcMeta *transpiler.FunctionMetadata
	if funcName := t.extractFuncName(fun); funcName != "" {
		funcMeta = t.getFunction(funcName)
	}

	// FIX-075: When GALA function metadata is not available, try Go type info.
	// This handles Go-defined functions and variables with function types (e.g., concurrent.Spawn)
	// that are called from GALA code via dot imports or qualified references.
	var goFuncParamTypes []transpiler.Type
	if funcMeta == nil && t.goTypeInfo != nil {
		if funcName := t.extractFuncName(fun); funcName != "" {
			goFuncParamTypes = t.resolveGoFuncParamTypes(funcName)
		}
	}

	// Check if this call is struct construction — if so, use struct field types as expected types
	// for lambda arguments. This must happen BEFORE argument transformation so that lambdas
	// can infer parameter types from the struct field definitions.
	var structFieldExpectedTypes []transpiler.Type
	if funcName := t.extractFuncName(fun); funcName != "" {
		typeMeta, resolvedTypeName := t.getTypeMetaResolved(funcName)
		if typeMeta != nil {
			resolved := t.resolveStructTypeName(resolvedTypeName)
			if fields, ok := t.structFields[resolved]; ok {
				if fieldTypes, ok := t.structFieldTypes[resolved]; ok {
					structFieldExpectedTypes = make([]transpiler.Type, len(fields))
					for i, fieldName := range fields {
						if ft, ok := fieldTypes[fieldName]; ok {
							structFieldExpectedTypes[i] = ft
						}
					}
				}
			}
		}
	}

	// For generic functions without explicit type args (e.g., Iterate(1, (x) => x * 2)),
	// pre-scan non-lambda arguments to infer type params so that lambda params get concrete types.
	var inferredTypeSubst map[string]string
	if funcMeta != nil && len(funcMeta.TypeParams) > 0 {
		funcTypeArgs := t.extractFuncCallTypeArgs(fun)
		if len(funcTypeArgs) > 0 {
			// Explicit type args provided — use directly
			inferredTypeSubst = make(map[string]string)
			for i, tp := range funcMeta.TypeParams {
				if i < len(funcTypeArgs) {
					inferredTypeSubst[tp] = funcTypeArgs[i]
				}
			}
		} else {
			// No explicit type args — infer from non-lambda arguments
			inferredTypeSubst = t.inferFuncTypeSubstFromArgs(funcMeta, argListCtx)
		}
	}

	var args []ast.Expr
	namedArgs := make(map[string]ast.Expr)
	hasSpread := false

	argIdx := 0
	for _, argCtx := range argListCtx.AllArgument() {
		arg := argCtx.(*grammar.ArgumentContext)
		exprCtx, lambdaCtx, isSpreadAll, extractErr := extractArgContent(arg)
		if extractErr != nil {
			return nil, extractErr
		}
		if isSpreadAll {
			hasSpread = true
		}

		// Check for named argument
		if arg.Identifier() != nil {
			// This is a named argument
			argName := arg.Identifier().GetText()
			// Look up expected type from struct field types for lambda inference
			var namedExpectedType transpiler.Type = transpiler.NilType{}
			if structFieldExpectedTypes != nil {
				if funcName := t.extractFuncName(fun); funcName != "" {
					typeMeta, resolvedTypeName := t.getTypeMetaResolved(funcName)
					if resolvedTypeName != "" {
						resolved := t.resolveStructTypeName(resolvedTypeName)
						if fieldTypes, ok := t.structFieldTypes[resolved]; ok {
							if ft, ok := fieldTypes[argName].(transpiler.FuncType); ok {
								namedExpectedType = ft
								// Apply generic type substitution if this is a generic struct construction
								// e.g., Wrapper[U](compute = ...) maps T -> U
								if typeMeta != nil && len(typeMeta.TypeParams) > 0 {
									typeArgs := t.extractFuncCallTypeArgs(fun)
									if len(typeArgs) > 0 {
										typeSubst := make(map[string]string)
										for i, tp := range typeMeta.TypeParams {
											if i < len(typeArgs) {
												typeSubst[tp] = typeArgs[i]
											}
										}
										namedExpectedType = t.substituteTranspilerTypeParams(namedExpectedType, typeSubst)
									}
								}
							}
						}
					}
				}
			}
			// Also check function metadata for named args in function calls
			if namedExpectedType.IsNil() && funcMeta != nil && len(funcMeta.ParamNames) > 0 {
				for i, paramName := range funcMeta.ParamNames {
					if paramName == argName && i < len(funcMeta.ParamTypes) {
						if ft, ok := funcMeta.ParamTypes[i].(transpiler.FuncType); ok {
							namedExpectedType = ft
						}
						break
					}
				}
			}
			var expr ast.Expr
			var err error
			if lambdaCtx != nil {
				expr, err = t.transformLambdaArgWithExpectedType(lambdaCtx, namedExpectedType)
			} else {
				expr, err = t.transformArgumentWithExpectedType(exprCtx, namedExpectedType)
			}
			if err != nil {
				return nil, err
			}
			namedArgs[argName] = expr
		} else {
			// Positional argument - use expected type if available
			// Resolve expected type using unified function call context
			funcCallCtx := t.buildFuncCallContext(funcMeta, inferredTypeSubst, goFuncParamTypes, structFieldExpectedTypes)
			expectedType := t.resolveExpectedArgType(funcCallCtx, argIdx)
			var expr ast.Expr
			var err error
			if lambdaCtx != nil {
				expr, err = t.transformLambdaArgWithExpectedType(lambdaCtx, expectedType)
			} else {
				expr, err = t.transformArgumentWithExpectedType(exprCtx, expectedType)
			}
			if err != nil {
				return nil, err
			}
			args = append(args, expr)
			argIdx++
		}
	}

	// If we have named args, try function call with named args + defaults first
	if len(namedArgs) > 0 {
		if funcMeta != nil && len(funcMeta.ParamNames) > 0 {
			return t.handleNamedArgsFuncCall(fun, args, namedArgs, funcMeta, argListCtx.GetStart().GetLine(), argListCtx.GetStart().GetColumn())
		}
		return t.handleNamedArgsCall(fun, args, namedArgs, argListCtx.GetStart().GetLine(), argListCtx.GetStart().GetColumn())
	}

	// If fewer positional args than expected and function has defaults, inject default values
	if funcMeta != nil && len(funcMeta.DefaultExprs) > 0 && len(args) < len(funcMeta.ParamTypes) {
		filled, err := t.fillDefaultArgs(args, funcMeta, argListCtx.GetStart().GetLine(), argListCtx.GetStart().GetColumn())
		if err != nil {
			return nil, err
		}
		args = filled
	}

	// Check for compiler intrinsic: StructMeta[T]()
	typeName := t.getBaseTypeName(fun)
	if typeName == "StructMeta" {
		return t.transformStructMetaConstruction(fun, argListCtx.GetStart().GetLine(), argListCtx.GetStart().GetColumn())
	}

	// Check if the function being called is a type with an Apply method
	// This handles companion object calls like Some[A](value) -> Some[A]{}.Apply(value)
	if typeName != "" {
		// Use unified resolution to find type metadata
		typeMeta, resolvedTypeMeta := t.getTypeMetaResolved(typeName)
		if typeMeta != nil {
			// Update typeName to resolved name for subsequent lookups
			typeName = resolvedTypeMeta
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
					// and/or from the enclosing function's return type context (FIX-040).
					if !hasTypeArgs && len(typeMeta.TypeParams) > 0 {
						// Build a map from type param name to inferred concrete type
						inferredMap := make(map[string]transpiler.Type)

						// Step 1: Try to infer from Apply method arguments using unification.
						// This handles both simple cases (param is T, arg is string -> T=string)
						// and complex cases (param is func() T, arg is func() string -> T=string).
						// FIX-044: Use unifyForInference instead of direct string comparison
						// so that FuncType params like func() T can be matched against lambda arg types.
						if len(args) > 0 {
							for i, arg := range args {
								if i < len(methodMeta.ParamTypes) {
									argType := t.getExprTypeName(arg)
									if argType != nil && !argType.IsNil() && !argType.IsAny() {
										t.unifyForInference(methodMeta.ParamTypes[i], argType, typeMeta.TypeParams, inferredMap)
									}
								}
							}
						}

						// Step 2 (FIX-040): For any type params still unresolved, try to infer
						// from the enclosing function's return type. For example, if we're in
						// a function returning Option[string] and calling Some(v), unify the
						// Apply method's return type (Option[T]) with Option[string] to get T=string.
						if len(inferredMap) < len(typeMeta.TypeParams) && t.currentFuncReturnType != nil && !t.currentFuncReturnType.IsNil() {
							if methodMeta.ReturnType != nil && !methodMeta.ReturnType.IsNil() {
								returnInferred := make(map[string]transpiler.Type)
								t.unifyForInference(methodMeta.ReturnType, t.currentFuncReturnType, typeMeta.TypeParams, returnInferred)
								for tp, inferred := range returnInferred {
									if _, alreadySet := inferredMap[tp]; !alreadySet {
										inferredMap[tp] = inferred
									}
								}
							}
						}

						// Build the inferred type args list
						if len(inferredMap) == len(typeMeta.TypeParams) {
							inferredTypeArgs := make([]ast.Expr, len(typeMeta.TypeParams))
							allResolved := true
							for i, tp := range typeMeta.TypeParams {
								if inferred, ok := inferredMap[tp]; ok {
									inferredTypeArgs[i] = t.typeToExpr(inferred)
								} else {
									allResolved = false
									break
								}
							}
							if allResolved {
								typeArgs = inferredTypeArgs
								hasTypeArgs = true
								if len(typeArgs) == 1 {
									fun = &ast.IndexExpr{X: baseExpr, Index: typeArgs[0]}
								} else if len(typeArgs) > 1 {
									fun = &ast.IndexListExpr{X: baseExpr, Indices: typeArgs}
								}
							}
						}
					}

					// Auto-inject StructMeta[T] when first param is StructMetaOps.
					// Codec[Person](SnakeCase()) → prepend _StructMeta_Person{} before SnakeCase()
					if hasTypeArgs && len(methodMeta.ParamTypes) > 0 {
						firstParamType := methodMeta.ParamTypes[0].BaseName()
						if firstParamType == "StructMetaOps" || firstParamType == "json.StructMetaOps" {
							args = t.autoInjectStructMeta(args, methodMeta, typeArgs)
						}
					}

					if isGeneric {
						// Generic Apply method: use standalone function
						fullName := typeName + "_Apply"
						var funExpr ast.Expr
						isStdType := hasStdPrefix(typeName)
						if !isStdType {
							resolvedType := t.getType(typeName)
							isStdType = !resolvedType.IsNil() && resolvedType.GetPackage() == registry.StdPackageName
						}
						if isStdType {
							funExpr = t.stdIdent(stripStdPrefix(fullName))
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

	return &ast.CallExpr{Fun: fun, Args: args, Ellipsis: ellipsisPos(hasSpread)}, nil
}

func (t *galaASTTransformer) handleNamedArgsCall(fun ast.Expr, args []ast.Expr, namedArgs map[string]ast.Expr, line, col int) (ast.Expr, error) {
	// Extract the type name for struct field lookup.
	// typeName is the bare name (e.g., "Cookie") used for code generation.
	// qualifiedName includes the package qualifier (e.g., "go_struct_bridge.Cookie")
	// for resolveStructTypeName to distinguish Go types from same-named GALA types.
	var typeName string
	var qualifiedName string
	switch f := fun.(type) {
	case *ast.Ident:
		typeName = f.Name
		qualifiedName = f.Name
	case *ast.IndexExpr:
		if id, ok := f.X.(*ast.Ident); ok {
			typeName = id.Name
			qualifiedName = id.Name
		} else if sel, ok := f.X.(*ast.SelectorExpr); ok {
			typeName = sel.Sel.Name
			if pkgId, ok := sel.X.(*ast.Ident); ok {
				qualifiedName = pkgId.Name + "." + sel.Sel.Name
			} else {
				qualifiedName = sel.Sel.Name
			}
		}
	case *ast.IndexListExpr:
		if id, ok := f.X.(*ast.Ident); ok {
			typeName = id.Name
			qualifiedName = id.Name
		} else if sel, ok := f.X.(*ast.SelectorExpr); ok {
			typeName = sel.Sel.Name
			if pkgId, ok := sel.X.(*ast.Ident); ok {
				qualifiedName = pkgId.Name + "." + sel.Sel.Name
			} else {
				qualifiedName = sel.Sel.Name
			}
		}
	case *ast.SelectorExpr:
		typeName = f.Sel.Name
		if pkgId, ok := f.X.(*ast.Ident); ok {
			qualifiedName = pkgId.Name + "." + f.Sel.Name
		} else {
			qualifiedName = f.Sel.Name
		}
	}

	// Check if this is a known struct type.
	// Use qualifiedName so resolveStructTypeName can detect when a Go struct type
	// (e.g., "go_struct_bridge.Cookie") should NOT resolve to a same-named GALA type.
	resolvedTypeName := t.resolveStructTypeName(qualifiedName)
	if fields, ok := t.structFields[resolvedTypeName]; ok {
		// Check if this is a sealed variant companion (empty struct with Apply method)
		// Sealed variants are registered with nil fields because the companion struct is empty.
		// The actual field info lives in the parent sealed type's SealedVariants metadata.
		if len(fields) == 0 && len(namedArgs) > 0 {
			if variantFieldNames := t.findSealedVariantFields(typeName); variantFieldNames != nil {
				// Reorder named args to match the Apply method's parameter order
				orderedArgs := make([]ast.Expr, 0, len(variantFieldNames))
				for _, fieldName := range variantFieldNames {
					if val, ok := namedArgs[fieldName]; ok {
						orderedArgs = append(orderedArgs, val)
					}
				}
				// Generate: VariantName{}.Apply(args...)
				receiver := &ast.CompositeLit{Type: fun}
				return &ast.CallExpr{
					Fun: &ast.SelectorExpr{
						X:   receiver,
						Sel: ast.NewIdent("Apply"),
					},
					Args: orderedArgs,
				}, nil
			}
		}

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
				// Reject nil assignment to immutable (val) fields — use Option[T] instead
				if immutFlags != nil && i < len(immutFlags) && immutFlags[i] {
					if ident, isIdent := val.(*ast.Ident); isIdent && ident.Name == "nil" {
						return nil, galaerr.NewSemanticErrorAt(line, col, fmt.Sprintf(
							"cannot assign nil to immutable field '%s' — use Option[T] with None() for optional values, or 'var %s' to make it mutable",
							fieldName, fieldName))
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

	// Fallback: if the type is not a known GALA struct, check if it's a Go-imported type.
	// Go structs from bridge/external packages can use GALA named-arg syntax: Type(Field = value)
	// which generates a plain Go composite literal: Type{Field: value}.
	if t.isGoImportedType(fun) {
		var elts []ast.Expr
		for fieldName, val := range namedArgs {
			elts = append(elts, &ast.KeyValueExpr{
				Key:   ast.NewIdent(fieldName),
				Value: val,
			})
		}
		// Sort fields alphabetically for deterministic output
		t.sortKeyValueExprs(elts)
		return &ast.CompositeLit{Type: fun, Elts: elts}, nil
	}

	// FIX-USR004: Fallback for dot-imported Go types.
	// When a type is not in structFields (not a GALA struct) and isGoImportedType returns false
	// (because the name is unqualified due to dot import), check if any non-std dot-imported
	// package exists. If so, generate a plain Go composite literal without Immutable wrapping.
	if _, isIdent := fun.(*ast.Ident); isIdent {
		for _, pkg := range t.importManager.GetDotImports() {
			if pkg != "std" && pkg != t.packageName {
				var elts []ast.Expr
				for fieldName, val := range namedArgs {
					elts = append(elts, &ast.KeyValueExpr{
						Key:   ast.NewIdent(fieldName),
						Value: val,
					})
				}
				t.sortKeyValueExprs(elts)
				return &ast.CompositeLit{Type: fun, Elts: elts}, nil
			}
		}
	}

	return nil, galaerr.NewSemanticErrorAt(line, col, fmt.Sprintf("named arguments only supported for Copy method or struct construction (type: %s)", typeName))
}



// isGoImportedType checks if an expression refers to a Go-imported type (not a GALA struct).
// This is used to determine if named-arg syntax should generate a plain Go composite literal.
func (t *galaASTTransformer) isGoImportedType(expr ast.Expr) bool {
	switch e := expr.(type) {
	case *ast.SelectorExpr:
		// pkg.Type — check if 'pkg' is an imported package
		if id, ok := e.X.(*ast.Ident); ok {
			return t.importManager.IsPackage(id.Name)
		}
	case *ast.IndexExpr:
		// Type[T] or pkg.Type[T] — recurse into the base
		return t.isGoImportedType(e.X)
	case *ast.IndexListExpr:
		// Type[T1, T2] or pkg.Type[T1, T2] — recurse into the base
		return t.isGoImportedType(e.X)
	}
	return false
}

// sortKeyValueExprs sorts a slice of ast.Expr (expected to be *ast.KeyValueExpr) by key name
// for deterministic output ordering.
func (t *galaASTTransformer) sortKeyValueExprs(elts []ast.Expr) {
	for i := 1; i < len(elts); i++ {
		for j := i; j > 0; j-- {
			a := elts[j-1].(*ast.KeyValueExpr).Key.(*ast.Ident).Name
			b := elts[j].(*ast.KeyValueExpr).Key.(*ast.Ident).Name
			if a > b {
				elts[j-1], elts[j] = elts[j], elts[j-1]
			}
		}
	}
}

// findSealedVariantFields looks up the field names for a sealed variant by searching
// parent sealed types in typeMetas. Returns nil if the variant is not found.
func (t *galaASTTransformer) findSealedVariantFields(variantName string) []string {
	for _, meta := range t.typeMetas {
		if meta.IsSealed {
			for _, sv := range meta.SealedVariants {
				if sv.Name == variantName {
					return sv.FieldNames
				}
			}
		}
	}
	return nil
}

// parseDefaultExpr parses a GALA expression string (from a default parameter value)
// into an ANTLR expression context that can be transformed by the normal pipeline.
func (t *galaASTTransformer) parseDefaultExpr(exprText string) (grammar.IExpressionContext, error) {
	input := antlr.NewInputStream(exprText)
	lexer := grammar.NewgalaLexer(input)
	stream := antlr.NewCommonTokenStream(lexer, antlr.TokenDefaultChannel)
	p := grammar.NewgalaParser(stream)
	p.RemoveErrorListeners()
	return p.Expression(), nil
}

// transformDefaultExpr parses and transforms a default expression string into a Go AST expression.
func (t *galaASTTransformer) transformDefaultExpr(exprText string) (ast.Expr, error) {
	exprCtx, err := t.parseDefaultExpr(exprText)
	if err != nil {
		return nil, fmt.Errorf("failed to parse default expression %q: %w", exprText, err)
	}
	return t.transformExpression(exprCtx)
}

// fillDefaultArgs fills missing positional arguments with default values from function metadata.
// Called when a function has defaults and fewer args were provided than parameters.
func (t *galaASTTransformer) fillDefaultArgs(args []ast.Expr, funcMeta *transpiler.FunctionMetadata, line, col int) ([]ast.Expr, error) {
	totalParams := len(funcMeta.ParamTypes)
	result := make([]ast.Expr, totalParams)

	// Copy provided positional args
	copy(result, args)

	// Fill missing positions with defaults
	for i := len(args); i < totalParams; i++ {
		defaultExprText, hasDefault := funcMeta.DefaultExprs[i]
		if !hasDefault {
			return nil, galaerr.NewSemanticErrorAt(line, col, fmt.Sprintf(
				"missing required argument %q (parameter %d) in call to %s",
				funcMeta.ParamNames[i], i+1, funcMeta.Name))
		}
		expr, err := t.transformDefaultExpr(defaultExprText)
		if err != nil {
			return nil, err
		}
		result[i] = expr
	}

	return result, nil
}

// handleNamedArgsFuncCall handles function calls with named arguments and default parameter values.
// Reorders named args to match parameter order and fills gaps with defaults.
func (t *galaASTTransformer) handleNamedArgsFuncCall(
	fun ast.Expr,
	positionalArgs []ast.Expr,
	namedArgs map[string]ast.Expr,
	funcMeta *transpiler.FunctionMetadata,
	line, col int,
) (ast.Expr, error) {
	totalParams := len(funcMeta.ParamTypes)
	result := make([]ast.Expr, totalParams)

	// Place positional args first
	for i, arg := range positionalArgs {
		if i >= totalParams {
			return nil, galaerr.NewSemanticErrorAt(line, col, fmt.Sprintf(
				"too many arguments in call to %s: expected %d, got %d positional + %d named",
				funcMeta.Name, totalParams, len(positionalArgs), len(namedArgs)))
		}
		result[i] = arg
	}

	// Place named args at their correct positions
	for name, expr := range namedArgs {
		idx := -1
		for i, pName := range funcMeta.ParamNames {
			if pName == name {
				idx = i
				break
			}
		}
		if idx < 0 {
			return nil, galaerr.NewSemanticErrorAt(line, col, fmt.Sprintf(
				"unknown parameter %q in call to %s", name, funcMeta.Name))
		}
		if result[idx] != nil {
			return nil, galaerr.NewSemanticErrorAt(line, col, fmt.Sprintf(
				"parameter %q specified both positionally and by name in call to %s",
				name, funcMeta.Name))
		}
		result[idx] = expr
	}

	// Fill remaining gaps with defaults
	for i, slot := range result {
		if slot == nil {
			defaultExprText, hasDefault := funcMeta.DefaultExprs[i]
			if !hasDefault {
				paramName := ""
				if i < len(funcMeta.ParamNames) {
					paramName = funcMeta.ParamNames[i]
				}
				return nil, galaerr.NewSemanticErrorAt(line, col, fmt.Sprintf(
					"missing required argument %q (parameter %d) in call to %s",
					paramName, i+1, funcMeta.Name))
			}
			expr, err := t.transformDefaultExpr(defaultExprText)
			if err != nil {
				return nil, err
			}
			result[i] = expr
		}
	}

	return &ast.CallExpr{Fun: fun, Args: result}, nil
}

// handleNamedArgsMethodCall handles method calls with named arguments and default parameter values.
// callSiteReceiver is the actual receiver expression at the call site (e.g., config.Get()).
func (t *galaASTTransformer) handleNamedArgsMethodCall(
	fun ast.Expr,
	callSiteReceiver ast.Expr,
	positionalArgs []ast.Expr,
	namedArgs map[string]ast.Expr,
	methodMeta *transpiler.MethodMetadata,
	recvTypeName string,
	line, col int,
) (ast.Expr, error) {
	totalParams := len(methodMeta.ParamTypes)
	result := make([]ast.Expr, totalParams)
	for i, arg := range positionalArgs {
		if i >= totalParams {
			return nil, galaerr.NewSemanticErrorAt(line, col, fmt.Sprintf("too many arguments in call to %s", methodMeta.Name))
		}
		result[i] = arg
	}
	for name, expr := range namedArgs {
		idx := -1
		for i, pName := range methodMeta.ParamNames {
			if pName == name {
				idx = i
				break
			}
		}
		if idx < 0 {
			return nil, galaerr.NewSemanticErrorAt(line, col, fmt.Sprintf("unknown parameter %q in call to %s", name, methodMeta.Name))
		}
		if result[idx] != nil {
			return nil, galaerr.NewSemanticErrorAt(line, col, fmt.Sprintf("parameter %q specified both positionally and by name in call to %s", name, methodMeta.Name))
		}
		result[idx] = expr
	}
	for i, slot := range result {
		if slot == nil {
			defaultExprText, hasDefault := methodMeta.DefaultExprs[i]
			if !hasDefault {
				paramName := ""
				if i < len(methodMeta.ParamNames) {
					paramName = methodMeta.ParamNames[i]
				}
				return nil, galaerr.NewSemanticErrorAt(line, col, fmt.Sprintf("missing required argument %q (parameter %d) in call to %s", paramName, i+1, methodMeta.Name))
			}
			expr, err := t.transformDefaultExpr(defaultExprText)
			if err != nil {
				return nil, err
			}
			// Substitute receiver references and unwrap immutable field accesses.
			// Only substitute when the call-site receiver differs from the method's
			// receiver name — when they match, transformDefaultExpr already handles
			// the unwrapping via the current scope.
			if methodMeta.ReceiverName != "" && !isIdentNamed(callSiteReceiver, methodMeta.ReceiverName) {
				expr = t.substituteReceiverInDefault(expr, methodMeta.ReceiverName, callSiteReceiver, recvTypeName)
			}
			result[i] = expr
		}
	}
	return &ast.CallExpr{Fun: fun, Args: result}, nil
}

// fillDefaultArgsMethod fills missing positional arguments with default values from method metadata.
// callSiteReceiver is the actual receiver expression at the call site.
func (t *galaASTTransformer) fillDefaultArgsMethod(callSiteReceiver ast.Expr, args []ast.Expr, methodMeta *transpiler.MethodMetadata, recvTypeName string, line, col int) ([]ast.Expr, error) {
	totalParams := len(methodMeta.ParamTypes)
	result := make([]ast.Expr, totalParams)
	copy(result, args)
	for i := len(args); i < totalParams; i++ {
		defaultExprText, hasDefault := methodMeta.DefaultExprs[i]
		if !hasDefault {
			return nil, galaerr.NewSemanticErrorAt(line, col, fmt.Sprintf("missing required argument %q (parameter %d) in call to %s", methodMeta.ParamNames[i], i+1, methodMeta.Name))
		}
		expr, err := t.transformDefaultExpr(defaultExprText)
		if err != nil {
			return nil, err
		}
		// Same as above — only substitute when receivers differ
		if methodMeta.ReceiverName != "" && !isIdentNamed(callSiteReceiver, methodMeta.ReceiverName) {
			expr = t.substituteReceiverInDefault(expr, methodMeta.ReceiverName, callSiteReceiver, recvTypeName)
		}
		result[i] = expr
	}
	return result, nil
}

// isIdentNamed checks if an expression is a simple identifier with the given name.
func isIdentNamed(expr ast.Expr, name string) bool {
	if id, ok := expr.(*ast.Ident); ok {
		return id.Name == name
	}
	return false
}

// substituteReceiver replaces all occurrences of the receiver identifier in a
// transformed default expression with the actual call-site receiver expression.
// For example, if the method is `func (c Config) copy(host string = c.host, ...)`
// and the call site is `config.Get().copy(port = 8080)`, then `c.host` in the
// default expression becomes `config.Get().host.Get()`.
//
// structImmutFields maps struct type names to their field immutability flags.
// When a field access on the receiver is detected and the field is immutable,
// `.Get()` is appended to unwrap the Immutable[T] wrapper.
func (t *galaASTTransformer) substituteReceiverInDefault(expr ast.Expr, receiverName string, callSiteReceiver ast.Expr, recvTypeName string) ast.Expr {
	switch e := expr.(type) {
	case *ast.Ident:
		if e.Name == receiverName {
			return callSiteReceiver
		}
		return e
	case *ast.SelectorExpr:
		newX := t.substituteReceiverInDefault(e.X, receiverName, callSiteReceiver, recvTypeName)
		result := &ast.SelectorExpr{X: newX, Sel: e.Sel}
		// If this is a field access on the receiver (c.field), check if the field is immutable
		// and add .Get() to unwrap Immutable[T]
		if t.isReceiverFieldAccess(e.X, receiverName) {
			resolvedTypeName := t.resolveStructTypeName(recvTypeName)
			if fields, ok := t.structFields[resolvedTypeName]; ok {
				immutFlags := t.structImmutFields[resolvedTypeName]
				for i, fieldName := range fields {
					if fieldName == e.Sel.Name && immutFlags != nil && i < len(immutFlags) && immutFlags[i] {
						// Field is immutable — add .Get() unwrap
						return &ast.CallExpr{
							Fun: &ast.SelectorExpr{X: result, Sel: ast.NewIdent("Get")},
						}
					}
				}
			}
		}
		return result
	case *ast.CallExpr:
		newFun := t.substituteReceiverInDefault(e.Fun, receiverName, callSiteReceiver, recvTypeName)
		newArgs := make([]ast.Expr, len(e.Args))
		for i, arg := range e.Args {
			newArgs[i] = t.substituteReceiverInDefault(arg, receiverName, callSiteReceiver, recvTypeName)
		}
		return &ast.CallExpr{Fun: newFun, Args: newArgs, Ellipsis: e.Ellipsis}
	case *ast.IndexExpr:
		return &ast.IndexExpr{
			X:     t.substituteReceiverInDefault(e.X, receiverName, callSiteReceiver, recvTypeName),
			Index: t.substituteReceiverInDefault(e.Index, receiverName, callSiteReceiver, recvTypeName),
		}
	case *ast.UnaryExpr:
		return &ast.UnaryExpr{Op: e.Op, X: t.substituteReceiverInDefault(e.X, receiverName, callSiteReceiver, recvTypeName)}
	case *ast.BinaryExpr:
		return &ast.BinaryExpr{
			X:  t.substituteReceiverInDefault(e.X, receiverName, callSiteReceiver, recvTypeName),
			Op: e.Op,
			Y:  t.substituteReceiverInDefault(e.Y, receiverName, callSiteReceiver, recvTypeName),
		}
	case *ast.ParenExpr:
		return &ast.ParenExpr{X: t.substituteReceiverInDefault(e.X, receiverName, callSiteReceiver, recvTypeName)}
	default:
		return expr
	}
}

// isReceiverFieldAccess checks if an expression is the receiver identifier (or receiver.Get()).
func (t *galaASTTransformer) isReceiverFieldAccess(expr ast.Expr, receiverName string) bool {
	// Direct: c.field
	if id, ok := expr.(*ast.Ident); ok && id.Name == receiverName {
		return true
	}
	return false
}

func (t *galaASTTransformer) transformArgumentWithExpectedType(exprCtx grammar.IExpressionContext, expectedType transpiler.Type) (ast.Expr, error) {
	// Try to find a partial function literal in this expression
	if pfCtx := t.findPartialFunctionInExpression(exprCtx); pfCtx != nil {
		return t.transformPartialFunctionLiteral(pfCtx, expectedType)
	}

	// Try to find a lambda in this expression
	if lambdaCtx := t.findLambdaInExpression(exprCtx); lambdaCtx != nil {
		// Extract the expected return type and parameter types from the function type
		var expectedRetType ast.Expr
		var expectedParamTypes []transpiler.Type
		if funcType, ok := expectedType.(transpiler.FuncType); ok {
			if len(funcType.Results) > 0 {
				expectedRetType = t.typeToExpr(funcType.Results[0])
			} else {
				// Void function - use sentinel value
				expectedRetType = ExpectedVoid
			}
			expectedParamTypes = funcType.Params
		}
		return t.transformLambdaWithExpectedType(lambdaCtx, expectedRetType, expectedParamTypes)
	}
	// Not a lambda or partial function, transform normally
	return t.transformExpression(exprCtx)
}

// transformLambdaArgWithExpectedType transforms a direct lambda argument (FIX-050).
// When the grammar's argument rule matches lambdaExpression directly instead of going through
// pattern -> expression -> primaryExpr -> lambdaExpression, we get the lambda context directly.
func (t *galaASTTransformer) transformLambdaArgWithExpectedType(lambdaCtx *grammar.LambdaExpressionContext, expectedType transpiler.Type) (ast.Expr, error) {
	var expectedRetType ast.Expr
	var expectedParamTypes []transpiler.Type
	if funcType, ok := expectedType.(transpiler.FuncType); ok {
		if len(funcType.Results) > 0 {
			expectedRetType = t.typeToExpr(funcType.Results[0])
		} else {
			expectedRetType = ExpectedVoid
		}
		expectedParamTypes = funcType.Params
	}
	return t.transformLambdaWithExpectedType(lambdaCtx, expectedRetType, expectedParamTypes)
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

// extractFuncName extracts the base function name from a call expression's Fun node.
// Handles: Ident (f), SelectorExpr (pkg.f), IndexExpr (f[T] or pkg.f[T]),
// IndexListExpr (f[T, U] or pkg.f[T, U]).
func (t *galaASTTransformer) extractFuncName(fun ast.Expr) string {
	switch f := fun.(type) {
	case *ast.Ident:
		return f.Name
	case *ast.SelectorExpr:
		// Package-qualified function call: pkg.Func
		if id, ok := f.X.(*ast.Ident); ok {
			return id.Name + "." + f.Sel.Name
		}
	case *ast.IndexExpr:
		if id, ok := f.X.(*ast.Ident); ok {
			return id.Name
		}
		// Package-qualified generic function call: pkg.Func[T]
		if sel, ok := f.X.(*ast.SelectorExpr); ok {
			if id, ok := sel.X.(*ast.Ident); ok {
				return id.Name + "." + sel.Sel.Name
			}
		}
	case *ast.IndexListExpr:
		if id, ok := f.X.(*ast.Ident); ok {
			return id.Name
		}
		// Package-qualified generic function call: pkg.Func[T, U]
		if sel, ok := f.X.(*ast.SelectorExpr); ok {
			if id, ok := sel.X.(*ast.Ident); ok {
				return id.Name + "." + sel.Sel.Name
			}
		}
	}
	return ""
}

// extractFuncCallTypeArgs extracts explicit type argument strings from a generic
// function call expression. For New[U] returns ["U"], for Func[A, B] returns ["A", "B"].
func (t *galaASTTransformer) extractFuncCallTypeArgs(fun ast.Expr) []string {
	switch f := fun.(type) {
	case *ast.IndexExpr:
		return []string{t.exprToTypeString(f.Index)}
	case *ast.IndexListExpr:
		var args []string
		for _, idx := range f.Indices {
			args = append(args, t.exprToTypeString(idx))
		}
		return args
	}
	return nil
}

// inferZeroArgTypeParams infers type parameters for a zero-argument sealed variant
// constructor (e.g., None()) when inside a match expression. It checks if the
// constructor is a companion of the match subject's sealed type and extracts the
// type parameters from the match subject type.
// Returns a typed AST expression (e.g., None[int]) or nil if inference fails.
func (t *galaASTTransformer) inferZeroArgTypeParams(typeName string, typeMeta *transpiler.TypeMetadata) ast.Expr {
	if t.currentMatchSubjectType == nil || t.currentMatchSubjectType.IsNil() {
		return nil
	}

	// Look up companion relationship for this type
	companion := t.lookupCompanion(typeName)
	if companion == nil {
		return nil
	}

	// Check if the companion's target type matches the match subject's base type
	subjectBaseName := stripPackagePrefix(t.currentMatchSubjectType.BaseName())
	targetBaseName := stripPackagePrefix(companion.TargetType)
	if subjectBaseName != targetBaseName {
		return nil
	}

	// Extract type params from the match subject type
	var subjectTypeParams []transpiler.Type
	if gen, ok := t.currentMatchSubjectType.(transpiler.GenericType); ok {
		subjectTypeParams = gen.Params
	} else {
		return nil
	}

	// Build the typed expression: e.g., None[int]
	baseExpr := t.typeToExpr(transpiler.BasicType{Name: typeName})
	if len(subjectTypeParams) == 1 {
		return &ast.IndexExpr{
			X:     baseExpr,
			Index: t.typeToExpr(subjectTypeParams[0]),
		}
	} else if len(subjectTypeParams) > 1 {
		indices := make([]ast.Expr, len(subjectTypeParams))
		for i, p := range subjectTypeParams {
			indices[i] = t.typeToExpr(p)
		}
		return &ast.IndexListExpr{
			X:       baseExpr,
			Indices: indices,
		}
	}

	return nil
}

// inferFuncTypeSubstFromArgs pre-scans non-lambda arguments of a generic function call
// to infer type parameter substitutions. For example, in Iterate(1, (x) => x * 2),
// it infers T = int from the first argument (1), enabling the lambda param x to be typed as int.
func (t *galaASTTransformer) inferFuncTypeSubstFromArgs(funcMeta *transpiler.FunctionMetadata, argListCtx grammar.IArgumentListContext) map[string]string {
	inferredMap := make(map[string]transpiler.Type)

	argIdx := 0
	for _, argCtx := range argListCtx.AllArgument() {
		arg := argCtx.(*grammar.ArgumentContext)
		if arg.Identifier() != nil {
			continue // skip named args
		}
		exprCtx, lambdaCtx, _, extractErr := extractArgContent(arg)
		if extractErr != nil {
			argIdx++
			continue
		}

		// Skip lambda arguments — we're inferring type params FOR them
		if lambdaCtx != nil || t.findLambdaInExpression(exprCtx) != nil {
			argIdx++
			continue
		}

		if argIdx >= len(funcMeta.ParamTypes) {
			argIdx++
			continue
		}

		// Transform the expression to get its Go AST, then infer its type
		expr, err := t.transformExpression(exprCtx)
		if err != nil {
			argIdx++
			continue
		}

		argType := t.getExprTypeNameManual(expr)
		if argType == nil || argType.IsNil() {
			argType, _ = t.inferExprType(expr)
		}
		if argType == nil || argType.IsNil() {
			argIdx++
			continue
		}

		paramType := funcMeta.ParamTypes[argIdx]
		t.unifyForInference(paramType, argType, funcMeta.TypeParams, inferredMap)
		argIdx++
	}

	if len(inferredMap) == 0 {
		return nil
	}

	// Only return substitutions when ALL type params are resolved.
	// Partial inference (e.g., T resolved but U not) would leave U as a literal
	// type name in generated Go code, which is undefined.
	for _, tp := range funcMeta.TypeParams {
		if _, ok := inferredMap[tp]; !ok {
			return nil
		}
	}

	// Convert transpiler.Type map to string map for substituteTranspilerTypeParams
	result := make(map[string]string, len(inferredMap))
	for k, v := range inferredMap {
		result[k] = v.String()
	}
	return result
}

// resolveGoFuncParamTypes resolves parameter types for a Go-defined function or
// function-typed variable using GoTypeInfo. This is used when GALA function metadata
// is not available (e.g., Go functions/vars in mixed GALA+Go packages like concurrent.Spawn).
// It checks GoTypeInfo.Functions first, then GoTypeInfo.Variables for function-typed vars.
// For bare names (from dot imports), it tries each dot-imported package as a qualifier.
func (t *galaASTTransformer) resolveGoFuncParamTypes(funcName string) []transpiler.Type {
	if t.goTypeInfo == nil {
		return nil
	}

	// Helper: extract param types from a Go function signature
	sigToParams := func(sig *transpiler.GoFuncSignature) []transpiler.Type {
		params := make([]transpiler.Type, len(sig.Params))
		for i, p := range sig.Params {
			params[i] = p.Type
		}
		return params
	}

	// Try direct lookup (qualified name like pkg.Func)
	if sig := t.goTypeInfo.GetFuncSignature(funcName); sig != nil {
		return sigToParams(sig)
	}
	// Try as a variable with function type
	if varType, ok := t.goTypeInfo.Variables[funcName]; ok {
		if ft, ok := varType.(transpiler.FuncType); ok {
			return ft.Params
		}
	}

	// For bare names, try each dot-imported package as qualifier
	for _, entry := range t.importManager.dotImports {
		qualName := entry.PkgName + "." + funcName
		if sig := t.goTypeInfo.GetFuncSignature(qualName); sig != nil {
			return sigToParams(sig)
		}
		if varType, ok := t.goTypeInfo.Variables[qualName]; ok {
			if ft, ok := varType.(transpiler.FuncType); ok {
				return ft.Params
			}
		}
	}

	// For qualified calls (alias.Func), resolve the alias to the actual package name
	if parts := splitQualifiedName(funcName); len(parts) == 2 {
		alias, name := parts[0], parts[1]
		if entry, ok := t.importManager.GetByAlias(alias); ok && entry.PkgName != alias {
			qualName := entry.PkgName + "." + name
			if sig := t.goTypeInfo.GetFuncSignature(qualName); sig != nil {
				return sigToParams(sig)
			}
			if varType, ok := t.goTypeInfo.Variables[qualName]; ok {
				if ft, ok := varType.(transpiler.FuncType); ok {
					return ft.Params
				}
			}
		}
	}

	return nil
}

// splitQualifiedName splits "pkg.Name" into ["pkg", "Name"], or returns nil for bare names.
func splitQualifiedName(name string) []string {
	for i, c := range name {
		if c == '.' {
			return []string{name[:i], name[i+1:]}
		}
	}
	return nil
}
