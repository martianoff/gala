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

// tryTransformGenericMethodAsFunction handles Section 3 of the call dispatcher:
// when a receiver method has explicit or inferred type parameters, rewrite the
// call site as `TypeName_Method[typeArgs](receiver, args...)` so Go's type
// inference can handle it cleanly. Returns:
//
//	handled=false — the section decided it does not apply (e.g., the
//	                receiver is actually a package identifier). The caller
//	                should continue to Section 4.
//	handled=true  — the section produced an output expression; the caller
//	                must return `expr` verbatim.
//
// Extracted from transformCallWithArgsCtx as part of A1 cont.
func (t *galaASTTransformer) tryTransformGenericMethodAsFunction(
	argListCtx *grammar.ArgumentListContext,
	receiver ast.Expr,
	method string,
	typeArgs []ast.Expr,
	recvType transpiler.Type,
	lookupBaseName string,
) (handled bool, result ast.Expr, err error) {
	// Skip if the "receiver" is actually a package identifier — that is a
	// package-qualified function call and belongs to a later section.
	if id, ok := receiver.(*ast.Ident); ok && t.importManager.IsPackage(id.Name) {
		return false, nil, nil
	}

	// Look up method metadata for parameter types using unified resolution.
	typeMeta, resolvedName := t.getTypeMetaResolved(lookupBaseName)
	var methodMeta *transpiler.MethodMetadata
	if typeMeta != nil {
		methodMeta = typeMeta.Methods[method]
		// Update lookupBaseName to the resolved name for later use.
		lookupBaseName = resolvedName
	}

	// Build type argument substitution map: receiver type params + method type params.
	typeSubst := make(map[string]string)
	var recvTypeArgStrings []string
	if methodMeta != nil && typeMeta != nil {
		recvTypeArgStrings = t.getReceiverTypeArgStrings(recvType)
		for i, tp := range typeMeta.TypeParams {
			if i < len(recvTypeArgStrings) {
				typeSubst[tp] = recvTypeArgStrings[i]
			}
		}
		for i, tp := range methodMeta.TypeParams {
			if i < len(typeArgs) {
				typeSubst[tp] = t.exprToTypeString(typeArgs[i])
			}
			// Don't default to "any" — will try to infer from non-lambda args below.
		}
	}

	// Build the "fresh-named" method type params and lookup for safe unification.
	// We rename method type params to a unique prefixed form BEFORE substituting
	// receiver type arguments into the method signature. Without this, a method
	// type param that happens to share a letter with an outer/receiver type arg
	// (e.g., Array[T].SortBy[K] invoked on an Array[Tuple[K,V]] where outer K,V
	// are in scope) would be unified against the substituted outer K, producing
	// a bogus binding like `method_K -> outer_K`.
	freshMethodTypeParams := make([]string, len(methodMeta.TypeParams))
	for i, tp := range methodMeta.TypeParams {
		freshMethodTypeParams[i] = freshMethodParamPrefix + tp
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
			// Skip lambda/partial args — can't infer types from them.
			// Also skip placeholder-lambda expressions (L4): they contain `_`
			// identifiers that only make sense once rewritten as a lambda,
			// which happens later in transformArgumentWithExpectedType.
			if lambdaCtx != nil || t.findLambdaInExpression(exprCtx) != nil || t.findPartialFunctionInExpression(exprCtx) != nil {
				continue
			}
			if countPlaceholderUnderscoresInExpr(exprCtx) > 0 {
				continue
			}
			expr, txErr := t.transformExpression(exprCtx)
			if txErr != nil {
				continue
			}
			preTransformed[i] = expr
			argType := t.getExprTypeName(expr)
			if argType == nil || argType.IsNil() || argType.IsAny() {
				continue
			}
			// Rename method type params in the param type to fresh prefixed
			// names, then substitute receiver type args. Unify using the fresh
			// names so outer-scope names embedded in receiver args can't shadow
			// the method's own type params.
			renamedParamType := t.renameTypeParamsInType(methodMeta.ParamTypes[i], methodMeta.TypeParams)
			substitutedParamType := t.substituteConcreteTypes(renamedParamType, typeMeta.TypeParams, recvTypeArgTypes)
			inferredMap := make(map[string]transpiler.Type)
			t.unifyForInference(substitutedParamType, argType, freshMethodTypeParams, inferredMap)
			for freshTp, inferred := range inferredMap {
				tp := stripFreshMethodParamPrefix(freshTp)
				if tp == "" {
					continue
				}
				if _, alreadySet := typeSubst[tp]; !alreadySet {
					typeSubst[tp] = inferred.String()
				}
			}
		}
	}
	// Gap #9: harvest type params from earlier lambdas to refine later ones.
	// We transform args in declaration order. Each lambda is transformed with
	// a *view* of typeSubst in which still-unresolved method type params are
	// temporarily filled with "any" (so the lambda sees a concrete expected
	// FuncType and can emit concrete param/result types from its body).
	// After transformation, we unify the lambda's actual FuncType against the
	// method's declared param FuncType to discover concrete types for those
	// previously-unresolved params, and commit them to typeSubst so later
	// lambdas see the refinement.
	//
	// Example: arr.GroupMapReduce(
	//     (w) => w,        // keyFn: func(T) K    -> infers K=string
	//     (w) => 1,        // valueFn: func(T) V  -> infers V=int
	//     (a, b) => a + b, // reduce: func(V, V) V -> V is now int, not any
	// )
	var mArgs []ast.Expr
	hasSpread := false
	recvTypeArgTypes := make([]transpiler.Type, 0, len(recvTypeArgStrings))
	for _, a := range recvTypeArgStrings {
		recvTypeArgTypes = append(recvTypeArgTypes, transpiler.ParseType(a))
	}
	// Default-to-any view seen by buildMethodCallContext: resolved substitutions
	// pass through; unresolved params get "any" so the expected FuncType is
	// emittable. The real typeSubst may still gain entries via the refinement
	// step below; we only copy those into the final substitution if they
	// remain unresolved.
	anyView := func() map[string]string {
		view := make(map[string]string, len(typeSubst))
		for k, v := range typeSubst {
			view[k] = v
		}
		if methodMeta != nil {
			for _, tp := range methodMeta.TypeParams {
				if _, ok := view[tp]; !ok {
					view[tp] = "any"
				}
			}
		}
		return view
	}
	for i, argCtx := range argListCtx.AllArgument() {
		arg := argCtx.(*grammar.ArgumentContext)
		exprCtx, lambdaCtx, isSpread, extractErr := extractArgContent(arg)
		if extractErr != nil {
			return true, nil, extractErr
		}
		if isSpread {
			hasSpread = true
		}
		// Reuse pre-transformed expression if available.
		if expr, ok := preTransformed[i]; ok {
			mArgs = append(mArgs, expr)
			continue
		}
		genMethodCtx := t.buildMethodCallContext(methodMeta, anyView(), false)
		expectedType := t.resolveExpectedArgType(genMethodCtx, i)
		if lambdaCtx != nil {
			expr, lerr := t.transformLambdaArgWithExpectedType(lambdaCtx, expectedType)
			if lerr != nil {
				return true, nil, lerr
			}
			// Gap #9: harvest newly-inferred method type params from the
			// lambda's actual result/param types and commit to typeSubst.
			// Rename method type params to fresh names first (see the non-lambda
			// branch for rationale) to avoid name collision with outer-scope
			// identifiers embedded in receiver type arguments.
			if methodMeta != nil && typeMeta != nil && i < len(methodMeta.ParamTypes) {
				renamedParamFT := t.renameTypeParamsInType(methodMeta.ParamTypes[i], methodMeta.TypeParams)
				if paramFT, ok := renamedParamFT.(transpiler.FuncType); ok {
					substitutedParamFT := t.substituteConcreteTypes(paramFT, typeMeta.TypeParams, recvTypeArgTypes)
					if actualFT, isFT := t.lambdaActualFuncType(expr).(transpiler.FuncType); isFT {
						inferredMap := make(map[string]transpiler.Type)
						t.unifyForInference(substitutedParamFT, actualFT, freshMethodTypeParams, inferredMap)
						for freshTp, inferred := range inferredMap {
							tp := stripFreshMethodParamPrefix(freshTp)
							if tp == "" {
								continue
							}
							if _, alreadySet := typeSubst[tp]; alreadySet {
								continue
							}
							if inferred == nil || inferred.IsNil() || inferred.IsAny() {
								continue
							}
							typeSubst[tp] = inferred.String()
						}
					}
				}
			}
			mArgs = append(mArgs, expr)
		} else {
			expr, aerr := t.transformArgumentWithExpectedType(exprCtx, expectedType)
			if aerr != nil {
				return true, nil, aerr
			}
			mArgs = append(mArgs, expr)
		}
	}

	// Capture whether GALA's own inference resolved every method type parameter
	// with a concrete-enough type (i.e., not the "any" fallback). When true, we
	// emit those inferred type arguments explicitly on the generated call so we
	// don't depend on Go's own type inference (which can fail for receivers that
	// wrap the outer scope's type parameter, e.g., `Array[Future[T]].Collect`).
	allMethodTypeParamsInferred := methodMeta != nil && len(methodMeta.TypeParams) > 0 && len(typeArgs) == 0
	if allMethodTypeParamsInferred {
		for _, tp := range methodMeta.TypeParams {
			v, ok := typeSubst[tp]
			if !ok || v == "" || v == "any" {
				allMethodTypeParamsInferred = false
				break
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

	// Build the standalone function identifier: pkg.TypeName_Method or TypeName_Method.
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

	// Attach type arguments, filtering out unresolved type-param leaks.
	recvTypeArgs := t.getReceiverTypeArgs(recvType)
	var concreteRecvTypeArgs []ast.Expr
	for _, arg := range recvTypeArgs {
		if ident, ok := arg.(*ast.Ident); ok {
			if len(ident.Name) == 1 && ident.Name[0] >= 'A' && ident.Name[0] <= 'Z' {
				// Skip unresolved type params like T, U, K, V
				continue
			}
		}
		concreteRecvTypeArgs = append(concreteRecvTypeArgs, arg)
	}

	// Collect inferred method type args (in declaration order) when every param
	// was resolved by inference. `typeArgs` is empty here (user supplied none);
	// we reuse its slot so the downstream emission path stays uniform.
	inferredMethodTypeArgs := typeArgs
	if allMethodTypeParamsInferred {
		inferred := make([]ast.Expr, 0, len(methodMeta.TypeParams))
		for _, tp := range methodMeta.TypeParams {
			inferred = append(inferred, t.typeToExpr(transpiler.ParseType(typeSubst[tp])))
		}
		inferredMethodTypeArgs = inferred
	}

	// Decide whether to add type arguments:
	// - If method has its own type params (e.g., Map[U]) and no explicit type args
	//   and GALA could not infer all of them: let Go infer.
	// - Otherwise: combine explicit/inferred type args with concrete receiver
	//   type args.
	shouldAddTypeArgs := len(inferredMethodTypeArgs) > 0 || (methodMeta == nil || len(methodMeta.TypeParams) == 0)
	if shouldAddTypeArgs {
		allTypeArgs := append(inferredMethodTypeArgs, concreteRecvTypeArgs...)
		if len(allTypeArgs) == 1 {
			funExpr = &ast.IndexExpr{X: funExpr, Index: allTypeArgs[0]}
		} else if len(allTypeArgs) > 1 {
			funExpr = &ast.IndexListExpr{X: funExpr, Indices: allTypeArgs}
		}
	}

	return true, &ast.CallExpr{
		Fun:      funExpr,
		Args:     append([]ast.Expr{receiver}, mArgs...),
		Ellipsis: ellipsisPos(hasSpread),
	}, nil
}

// transformRegularMethodCall handles Section 4 of the call dispatcher: method
// calls on a receiver where the method is NOT generic (or the generic path
// declined). It has three sub-paths:
//
//  1. method metadata present + receiver has unresolved type params → emit a
//     simple method call, passing only void-function expected types so lambda
//     arguments still get their return-type stripped.
//  2. method metadata present + receiver is fully concrete → transform args
//     with named/positional handling and apply named-args / default-args
//     dispatch.
//  3. method metadata absent → emit the method call directly with
//     no expected-type threading.
//
// Extracted from transformCallWithArgsCtx as part of A1 cont.
func (t *galaASTTransformer) transformRegularMethodCall(
	argListCtx *grammar.ArgumentListContext,
	receiver ast.Expr,
	method string,
	recvType transpiler.Type,
	lookupBaseName string,
) (ast.Expr, error) {
	var methodMeta *transpiler.MethodMetadata
	// Look up method metadata for ALL types (not just generic ones) so that
	// non-generic wrapper types like Str can pass expected function types to
	// lambda arguments.
	typeMeta := t.getTypeMeta(lookupBaseName)
	if typeMeta != nil {
		methodMeta = typeMeta.Methods[method]
	}
	if methodMeta == nil {
		// method metadata unresolved → emit the method call directly.
		return t.emitDirectMethodCall(argListCtx, receiver, method)
	}

	// Build type substitution map from receiver's type arguments.
	typeSubst := make(map[string]string)
	recvTypeArgs := t.getReceiverTypeArgStrings(recvType)
	hasUnresolvedTypeParams := false
	for i, tp := range typeMeta.TypeParams {
		if i < len(recvTypeArgs) {
			arg := recvTypeArgs[i]
			if len(arg) == 1 && arg[0] >= 'A' && arg[0] <= 'Z' {
				hasUnresolvedTypeParams = true
				break
			}
			typeSubst[tp] = arg
		}
	}

	// Unresolved receiver type params: skip full expected-type inference but
	// still detect void function parameters for lambda return-type stripping.
	if hasUnresolvedTypeParams {
		return t.emitMethodCallWithVoidLambdaHint(argListCtx, receiver, method, methodMeta, typeSubst)
	}

	// Fully concrete receiver type: transform args (named + positional), then
	// dispatch through named-args / default-args fillers if needed.
	return t.emitMethodCallWithFullTypes(argListCtx, receiver, method, methodMeta, typeSubst, recvType)
}

// emitDirectMethodCall is the fallback used when the method's metadata
// cannot be resolved. It still generates a `receiver.method(args...)` call so
// the downstream compiler can report a meaningful error rather than having
// the receiver.method structure silently lost. Part of A1 cont.
func (t *galaASTTransformer) emitDirectMethodCall(argListCtx *grammar.ArgumentListContext, receiver ast.Expr, method string) (ast.Expr, error) {
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

// emitMethodCallWithVoidLambdaHint handles the unresolved-receiver-type-params
// sub-path of Section 4: the receiver's generic type params can't be resolved
// so we skip full expected-type threading, but we still pass void function
// expected types so lambda arguments can strip their return types. Part of A1 cont.
func (t *galaASTTransformer) emitMethodCallWithVoidLambdaHint(
	argListCtx *grammar.ArgumentListContext,
	receiver ast.Expr,
	method string,
	methodMeta *transpiler.MethodMetadata,
	typeSubst map[string]string,
) (ast.Expr, error) {
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
		// `true` here filters to void function types only, avoiding leaked
		// unresolved type params in return types.
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

// emitMethodCallWithFullTypes handles the concrete-receiver sub-path of
// Section 4: transform positional and named arguments with full expected-type
// threading, then dispatch through named-args / default-args fillers. Part of
// A1 cont.
func (t *galaASTTransformer) emitMethodCallWithFullTypes(
	argListCtx *grammar.ArgumentListContext,
	receiver ast.Expr,
	method string,
	methodMeta *transpiler.MethodMetadata,
	typeSubst map[string]string,
	recvType transpiler.Type,
) (ast.Expr, error) {
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

// tryTransformCompanionApplyOrStructCtor handles Section 10 of the call
// dispatcher. When the call target is a registered type with either:
//
//  1. field count matching positional args → emit a struct composite literal
//  2. an Apply method on its companion    → rewrite as either
//     `TypeName_Apply[typeArgs](receiver, args...)` (generic) or
//     `Type{}.Apply(args)` (non-generic)
//  3. no Apply but a struct layout        → emit a struct composite literal
//     with any extra positional args dropped
//
// Also handles type-parameter inference for generic Apply paths from both
// argument types (unification) and the enclosing function's return
// type. Returns handled=false if the type is not known, so the
// caller can fall through to the later sections.
//
// Extracted from transformCallWithArgsCtx as part of A1 cont.
func (t *galaASTTransformer) tryTransformCompanionApplyOrStructCtor(
	fun ast.Expr,
	typeName string,
	args []ast.Expr,
) (handled bool, result ast.Expr, err error) {
	typeMeta, resolvedTypeMeta := t.getTypeMetaResolved(typeName)
	if typeMeta == nil {
		return false, nil, nil
	}
	// Update typeName to resolved name for subsequent lookups.
	typeName = resolvedTypeMeta

	// Positional struct construction has priority over Apply: when arg count
	// equals field count, emit a struct literal directly.
	resolvedTypeName := t.resolveStructTypeName(typeName)
	if fields, structOk := t.structFields[resolvedTypeName]; structOk && len(args) > 0 && len(args) == len(fields) {
		return true, t.buildStructLiteral(fun, resolvedTypeName, fields, args, false), nil
	}

	methodMeta, hasApply := typeMeta.Methods["Apply"]
	if !hasApply {
		// No Apply method. Still emit a struct literal if this is a known
		// struct layout — callers may supply a subset of fields.
		if fields, ok := t.structFields[resolvedTypeName]; ok && len(args) > 0 {
			return true, t.buildStructLiteral(fun, resolvedTypeName, fields, args, true), nil
		}
		return false, nil, nil
	}

	// Apply path: verify the base expression is a type (not a variable).
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

	isType := false
	if id, ok := baseExpr.(*ast.Ident); ok {
		if !t.isVal(id.Name) && !t.isVar(id.Name) {
			if !t.getType(id.Name).IsNil() {
				isType = true
			}
		}
	} else if sel, ok := baseExpr.(*ast.SelectorExpr); ok {
		if id, ok := sel.X.(*ast.Ident); ok {
			if t.importManager.IsPackage(id.Name) || id.Name == registry.StdPackageName {
				isType = true
			}
		}
	}

	if !isType {
		return false, nil, nil
	}

	isGeneric := methodMeta.IsGeneric || len(methodMeta.TypeParams) > 0

	// Infer type args from argument types and enclosing return type.
	if !hasTypeArgs && len(typeMeta.TypeParams) > 0 {
		inferredMap := make(map[string]transpiler.Type)
		// Step 1: infer from Apply method arguments.
		for i, arg := range args {
			if i < len(methodMeta.ParamTypes) {
				argType := t.getExprTypeName(arg)
				if argType != nil && !argType.IsNil() && !argType.IsAny() {
					t.unifyForInference(methodMeta.ParamTypes[i], argType, typeMeta.TypeParams, inferredMap)
				}
			}
		}
		// Step 2: fall back to enclosing function's return type.
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

	// Auto-inject StructMeta[T] when first Apply param is StructMeta[T] (the
	// Option-C typed interface from std/meta.gala) or the legacy StructMetaOps
	// (the pre-migration non-generic shim in json/helpers.gala).
	// Codec[Person](SnakeCase()) → prepend _StructMeta_Person{} before SnakeCase()
	if hasTypeArgs && len(methodMeta.ParamTypes) > 0 {
		firstParamType := methodMeta.ParamTypes[0].BaseName()
		switch firstParamType {
		case "StructMeta", "std.StructMeta",
			"StructMetaOps", "json.StructMetaOps":
			args = t.autoInjectStructMeta(args, methodMeta, typeArgs)
		}
	}

	if isGeneric {
		// Generic Apply method: use standalone function form.
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
		return true, &ast.CallExpr{
			Fun:  funExpr,
			Args: append([]ast.Expr{receiver}, args...),
		}, nil
	}

	// Non-generic Apply method: call Apply on a freshly constructed instance.
	receiver := &ast.CompositeLit{Type: fun}
	return true, &ast.CallExpr{
		Fun: &ast.SelectorExpr{
			X:   receiver,
			Sel: ast.NewIdent("Apply"),
		},
		Args: args,
	}, nil
}

// buildStructLiteral emits a struct composite literal from positional args,
// wrapping immutable fields with NewImmutable as needed. When `truncate` is
// true, excess args are silently dropped (used when the arg count exceeds the
// field count); otherwise the caller is responsible for matching the counts.
// Extracted from transformCallWithArgsCtx as part of A1 cont.
func (t *galaASTTransformer) buildStructLiteral(typeExpr ast.Expr, resolvedTypeName string, fields []string, args []ast.Expr, truncate bool) ast.Expr {
	immutFlags := t.structImmutFields[resolvedTypeName]
	var elts []ast.Expr
	for i, fieldName := range fields {
		if i >= len(args) {
			if truncate {
				break
			}
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
	return &ast.CompositeLit{Type: typeExpr, Elts: elts}
}

// transformFunctionArgs handles Section 6 of the call dispatcher: walk the
// argument list, classify each argument as positional or named, and transform
// it with the correct expected type (for lambda parameter inference). Returns
// the positional args, named args map, and the hasSpread flag. Extracted from
// transformCallWithArgsCtx as part of A1 cont.
func (t *galaASTTransformer) transformFunctionArgs(
	fun ast.Expr,
	argListCtx *grammar.ArgumentListContext,
	callCtx functionCallContext,
) (positional []ast.Expr, named map[string]ast.Expr, hasSpread bool, err error) {
	named = make(map[string]ast.Expr)
	argIdx := 0

	for _, argCtx := range argListCtx.AllArgument() {
		arg := argCtx.(*grammar.ArgumentContext)
		exprCtx, lambdaCtx, isSpreadAll, extractErr := extractArgContent(arg)
		if extractErr != nil {
			return nil, nil, false, extractErr
		}
		if isSpreadAll {
			hasSpread = true
		}

		if arg.Identifier() != nil {
			// Named argument: resolve the expected type via struct-field lookup
			// first (supports generic struct construction), then function metadata.
			argName := arg.Identifier().GetText()
			namedExpectedType := t.resolveNamedArgExpectedFuncType(fun, argName, callCtx)

			var expr ast.Expr
			var aerr error
			if lambdaCtx != nil {
				expr, aerr = t.transformLambdaArgWithExpectedType(lambdaCtx, namedExpectedType)
			} else {
				expr, aerr = t.transformArgumentWithExpectedType(exprCtx, namedExpectedType)
			}
			if aerr != nil {
				return nil, nil, false, aerr
			}
			named[argName] = expr
			continue
		}

		// Positional argument — use unified function call context.
		funcCallCtx := t.buildFuncCallContext(callCtx.funcMeta, callCtx.inferredTypeSubst, callCtx.goFuncParamTypes, callCtx.structFieldExpectedTypes)
		funcCallCtx.applyMethodMeta = callCtx.applyMethodMeta
		funcCallCtx.applyTypeSubst = callCtx.applyTypeSubst
		expectedType := t.resolveExpectedArgType(funcCallCtx, argIdx)
		var expr ast.Expr
		var aerr error
		if lambdaCtx != nil {
			expr, aerr = t.transformLambdaArgWithExpectedType(lambdaCtx, expectedType)
		} else {
			expr, aerr = t.transformArgumentWithExpectedType(exprCtx, expectedType)
		}
		if aerr != nil {
			return nil, nil, false, aerr
		}
		positional = append(positional, expr)
		argIdx++
	}
	return positional, named, hasSpread, nil
}

// resolveNamedArgExpectedFuncType looks up the expected FuncType for a named
// argument so that lambda arguments can infer their parameter types. It tries
// struct-field-type resolution first (with generic type-param substitution for
// generic struct construction), then falls back to function metadata param
// names. Returns NilType if no FuncType can be resolved. Extracted from
// transformCallWithArgsCtx as part of A1 cont.
func (t *galaASTTransformer) resolveNamedArgExpectedFuncType(fun ast.Expr, argName string, callCtx functionCallContext) transpiler.Type {
	// Step 1: struct-field lookup — handles lambdas passed as named struct args.
	if callCtx.structFieldExpectedTypes != nil {
		if funcName := t.extractFuncName(fun); funcName != "" {
			typeMeta, resolvedTypeName := t.getTypeMetaResolved(funcName)
			if resolvedTypeName != "" {
				resolved := t.resolveStructTypeName(resolvedTypeName)
				if fieldTypes, ok := t.structFieldTypes[resolved]; ok {
					if ft, ok := fieldTypes[argName].(transpiler.FuncType); ok {
						expected := transpiler.Type(ft)
						// Apply generic type substitution for generic struct
						// construction: e.g., Wrapper[U](compute = ...) maps T -> U.
						if typeMeta != nil && len(typeMeta.TypeParams) > 0 {
							typeArgs := t.extractFuncCallTypeArgs(fun)
							if len(typeArgs) > 0 {
								typeSubst := make(map[string]string)
								for i, tp := range typeMeta.TypeParams {
									if i < len(typeArgs) {
										typeSubst[tp] = typeArgs[i]
									}
								}
								expected = t.substituteTranspilerTypeParams(expected, typeSubst)
							}
						}
						return expected
					}
				}
			}
		}
	}

	// Step 2: function metadata lookup — handles lambdas passed as named
	// function-call args when the function has named parameters.
	if callCtx.funcMeta != nil && len(callCtx.funcMeta.ParamNames) > 0 {
		for i, paramName := range callCtx.funcMeta.ParamNames {
			if paramName == argName && i < len(callCtx.funcMeta.ParamTypes) {
				if ft, ok := callCtx.funcMeta.ParamTypes[i].(transpiler.FuncType); ok {
					return ft
				}
				break
			}
		}
	}

	return transpiler.NilType{}
}

// functionCallContext bundles the expected-type lookups used when
// transforming a regular (non-method) function call's arguments. All fields
// may be nil/empty independently when the corresponding metadata is absent.
type functionCallContext struct {
	funcMeta                 *transpiler.FunctionMetadata
	goFuncParamTypes         []transpiler.Type
	structFieldExpectedTypes []transpiler.Type
	inferredTypeSubst        map[string]string
	// applyMethodMeta is set when the call site is of the form `Type[T](args)`
	// and Type has an Apply method. It lets the argument-transformation pass
	// see the Apply method's expected parameter types (with type-param
	// substitutions from `applyTypeSubst`) so lambda arguments can infer
	// concrete return / parameter types.  Without this, lambdas passed to
	// companion-Apply calls would be transformed before Section 10 resolves
	// them and would miss their expected type entirely.
	applyMethodMeta *transpiler.MethodMetadata
	applyTypeSubst  map[string]string
}

// collectFunctionCallContext handles Section 5 of the call dispatcher:
// gather all metadata used during argument transformation. Extracted from
// transformCallWithArgsCtx as part of A1 cont.
func (t *galaASTTransformer) collectFunctionCallContext(fun ast.Expr, argListCtx *grammar.ArgumentListContext) functionCallContext {
	var ctx functionCallContext

	// Look up GALA function metadata for expected parameter types
	// (enables void lambda detection and type-param inference).
	if funcName := t.extractFuncName(fun); funcName != "" {
		ctx.funcMeta = t.getFunction(funcName)
	}

	// When GALA function metadata is not available, try Go type
	// info. Handles Go-defined functions and variables with function types
	// (e.g., concurrent.Spawn) called via dot-imports or qualified references.
	if ctx.funcMeta == nil && t.goTypeInfo != nil {
		if funcName := t.extractFuncName(fun); funcName != "" {
			ctx.goFuncParamTypes = t.resolveGoFuncParamTypes(funcName)
		}
	}

	// Struct construction context: collect field types so lambdas passed
	// as positional struct args can infer their parameter types.
	if funcName := t.extractFuncName(fun); funcName != "" {
		typeMeta, resolvedTypeName := t.getTypeMetaResolved(funcName)
		if typeMeta != nil {
			resolved := t.resolveStructTypeName(resolvedTypeName)
			if fields, ok := t.structFields[resolved]; ok {
				if fieldTypes, ok := t.structFieldTypes[resolved]; ok {
					ctx.structFieldExpectedTypes = make([]transpiler.Type, len(fields))
					for i, fieldName := range fields {
						if ft, ok := fieldTypes[fieldName]; ok {
							ctx.structFieldExpectedTypes[i] = ft
						}
					}
				}
			}
		}
	}

	// For generic functions without explicit type args (e.g.,
	// Iterate(1, (x) => x * 2)), pre-scan non-lambda arguments to infer
	// type params so that lambda params get concrete types.
	if ctx.funcMeta != nil && len(ctx.funcMeta.TypeParams) > 0 {
		funcTypeArgs := t.extractFuncCallTypeArgs(fun)
		if len(funcTypeArgs) > 0 {
			// Explicit type args provided — use directly.
			ctx.inferredTypeSubst = make(map[string]string)
			for i, tp := range ctx.funcMeta.TypeParams {
				if i < len(funcTypeArgs) {
					ctx.inferredTypeSubst[tp] = funcTypeArgs[i]
				}
			}
		} else {
			// No explicit type args — infer from non-lambda arguments.
			ctx.inferredTypeSubst = t.inferFuncTypeSubstFromArgs(ctx.funcMeta, argListCtx)
		}
	}

	// Companion-Apply path: when `fun` is `Type[T]` (or `Type[T1, T2]`) whose
	// Type has an Apply method, extract the Apply method's metadata and
	// resolve any type-param substitutions so the argument-transformation
	// pass can propagate expected parameter types to lambda arguments.
	// Without this, calls like `Try[string](() => {...})` would transform
	// the lambda body with no expected return type and fail to emit the
	// `func() string` return signature.
	if ctx.funcMeta == nil {
		if funcName := t.extractFuncName(fun); funcName != "" {
			typeMeta, _ := t.getTypeMetaResolved(funcName)
			if typeMeta != nil {
				if applyMeta, hasApply := typeMeta.Methods["Apply"]; hasApply {
					ctx.applyMethodMeta = applyMeta
					funcTypeArgs := t.extractFuncCallTypeArgs(fun)
					if len(funcTypeArgs) > 0 {
						ctx.applyTypeSubst = make(map[string]string)
						for i, tp := range typeMeta.TypeParams {
							if i < len(funcTypeArgs) {
								ctx.applyTypeSubst[tp] = funcTypeArgs[i]
							}
						}
					}
				}
			}
		}
	}

	return ctx
}

// tryTransformCompositeLitApply handles Section 11: when `fun` is already a
// composite literal (e.g., `Append{...}` produced by an earlier partial
// application) whose type has an Apply method, rewrite `fun(args)` as
// `fun.Apply(args)`. Returns handled=false for all other shapes of `fun`.
// Extracted from transformCallWithArgsCtx as part of A1 cont.
func (t *galaASTTransformer) tryTransformCompositeLitApply(fun ast.Expr, args []ast.Expr) (ast.Expr, bool) {
	compLit, ok := fun.(*ast.CompositeLit)
	if !ok {
		return nil, false
	}
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
	if litTypeName == "" {
		return nil, false
	}
	typeMeta := t.getTypeMeta(litTypeName)
	if typeMeta == nil {
		return nil, false
	}
	if _, hasApply := typeMeta.Methods["Apply"]; !hasApply {
		return nil, false
	}
	return &ast.CallExpr{
		Fun:  &ast.SelectorExpr{X: compLit, Sel: ast.NewIdent("Apply")},
		Args: args,
	}, true
}

// tryTransformValWithApply handles Section 12: when `fun` is a variable
// (or val.Get() call) whose type has an Apply method, rewrite `fun(args)` as
// `fun.Apply(args)`. This enables `val add5 = Adder(5); add5(10)` to work.
// Returns handled=false for all other shapes of `fun`.
// Extracted from transformCallWithArgsCtx as part of A1 cont.
func (t *galaASTTransformer) tryTransformValWithApply(fun ast.Expr, args []ast.Expr) (ast.Expr, bool) {
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
	if valName == "" {
		return nil, false
	}
	varType := t.getType(valName)
	if varType.IsNil() {
		return nil, false
	}
	varTypeName := varType.BaseName()
	typeMeta := t.getTypeMeta(varTypeName)
	if typeMeta == nil {
		return nil, false
	}
	if _, hasApply := typeMeta.Methods["Apply"]; !hasApply {
		return nil, false
	}
	return &ast.CallExpr{
		Fun:  &ast.SelectorExpr{X: fun, Sel: ast.NewIdent("Apply")},
		Args: args,
	}, true
}

// transformCallWithArgsCtx is the primary entry point for transforming GALA
// call expressions (functions, methods, constructors, companion-object Apply,
// etc.) into Go AST call expressions.
//
// The body is a thin dispatcher. Each numbered section delegates to a focused
// helper and either returns eagerly or falls through to the next section.
// The dispatcher is navigated in strict order:
//
//	Section 1  Copy method short-circuit                — inline
//	Section 2  Dispatch prelude                         — splitCallTarget, resolveReceiverTypeAndLookupKey
//	Section 3  Generic method → standalone function     — tryTransformGenericMethodAsFunction
//	Section 4  Regular method call                      — transformRegularMethodCall
//	Section 5  Regular function-call context gather     — collectFunctionCallContext
//	Section 6  Argument transformation                  — transformFunctionArgs
//	Section 7  Named-args dispatch                      — handleNamedArgs(Func|)Call
//	Section 8  Default-arg injection                    — fillDefaultArgs
//	Section 9  StructMeta[T]() intrinsic                — transformStructMetaConstruction
//	Section 10 Companion Apply / struct construction    — tryTransformCompanionApplyOrStructCtor
//	Section 11 CompositeLit with Apply                  — tryTransformCompositeLitApply
//	Section 12 Variable with Apply method               — tryTransformValWithApply
//	Section 13 Fallback: emit call verbatim             — inline
//
// When extending call-site behavior, add or modify a single section's helper
// rather than growing this dispatcher. Each helper is independently testable
// and carries its own doc comment describing the sub-path it handles.
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

	// --- Section 3: Generic method -> standalone function rewrite ---
	if receiver != nil && isGenericMethod {
		handled, expr, err := t.tryTransformGenericMethodAsFunction(argListCtx, receiver, method, typeArgs, recvType, lookupBaseName)
		if err != nil {
			return nil, err
		}
		if handled {
			return expr, nil
		}
	}

	// --- Section 4: Regular method call (non-generic method or generic-method
	// lookup fell through). Covers both the path with method metadata and the
	// fallback when metadata cannot be resolved.
	if receiver != nil && !isGenericMethod && method != "" {
		return t.transformRegularMethodCall(argListCtx, receiver, method, recvType, lookupBaseName)
	}

	// --- Section 5: Regular function call context gathering ---
	callCtx := t.collectFunctionCallContext(fun, argListCtx)

	// --- Section 6: Argument transformation ---
	// Walks the argument list, classifying each arg as positional or named,
	// and resolves an expected type for each from the gathered callCtx.
	args, namedArgs, hasSpread, err := t.transformFunctionArgs(fun, argListCtx, callCtx)
	if err != nil {
		return nil, err
	}

	// --- Section 7: Named-args dispatch ---
	if len(namedArgs) > 0 {
		if callCtx.funcMeta != nil && len(callCtx.funcMeta.ParamNames) > 0 {
			return t.handleNamedArgsFuncCall(fun, args, namedArgs, callCtx.funcMeta, argListCtx.GetStart().GetLine(), argListCtx.GetStart().GetColumn())
		}
		return t.handleNamedArgsCall(fun, args, namedArgs, argListCtx.GetStart().GetLine(), argListCtx.GetStart().GetColumn())
	}

	// --- Section 8: Default-arg injection for under-filled positional calls ---
	if callCtx.funcMeta != nil && len(callCtx.funcMeta.DefaultExprs) > 0 && len(args) < len(callCtx.funcMeta.ParamTypes) {
		filled, err := t.fillDefaultArgs(args, callCtx.funcMeta, argListCtx.GetStart().GetLine(), argListCtx.GetStart().GetColumn())
		if err != nil {
			return nil, err
		}
		args = filled
	}

	// --- Section 9: StructMeta[T]() compiler intrinsic ---
	typeName := t.getBaseTypeName(fun)
	if typeName == "StructMeta" {
		return t.transformStructMetaConstruction(fun, argListCtx.GetStart().GetLine(), argListCtx.GetStart().GetColumn())
	}

	// --- Section 10: Companion Apply / positional struct construction ---
	if typeName != "" {
		handled, expr, err := t.tryTransformCompanionApplyOrStructCtor(fun, typeName, args)
		if err != nil {
			return nil, err
		}
		if handled {
			return expr, nil
		}
	}

	// --- Section 11: CompositeLit with Apply method ---
	// Handles `Append("cherry")("apple")` where the left side is already a
	// struct literal produced by an earlier partial-application step.
	if expr, handled := t.tryTransformCompositeLitApply(fun, args); handled {
		return expr, nil
	}

	// --- Section 12: Variable whose type has an Apply method ---
	// Handles `val add5 = Adder(5); add5(10)` → `add5.Apply(10)`.
	if expr, handled := t.tryTransformValWithApply(fun, args); handled {
		return expr, nil
	}

	// --- Section 13: Fallback — emit the call verbatim. ---
	return &ast.CallExpr{Fun: fun, Args: args, Ellipsis: ellipsisPos(hasSpread)}, nil
}

// handleNamedArgsCall is a thin dispatcher for named-argument calls. It
// classifies the call site (GALA struct, sealed variant companion, or
// Go-imported type) and delegates the heavy lifting to a section helper:
//
//  1. extractTypeNameFromExpr — decode `fun` into (typeName, qualifiedName)
//  2. findSealedVariantFields → buildSealedVariantApplyCall (fields=[])
//  3. buildStructLiteralWithNamedArgs (registered GALA struct)
//  4. buildGoCompositeLiteralWithNamedArgs (Go-imported or dot-imported type)
//  5. fallthrough → coded semantic error
//
// Keeping the body a dispatcher matches the established A1 pattern for
// `transformCallWithArgsCtx` in this file: the numbered sections map to
// the helpers below and are easy to navigate.
func (t *galaASTTransformer) handleNamedArgsCall(fun ast.Expr, args []ast.Expr, namedArgs map[string]ast.Expr, line, col int) (ast.Expr, error) {
	// 1. Extract the type name for struct field lookup.
	typeName, qualifiedName := extractTypeNameFromExpr(fun)

	// 2. GALA struct or sealed variant companion? Use qualifiedName so a
	// Go struct type (e.g., "go_struct_bridge.Cookie") does NOT resolve
	// to a same-named GALA type.
	resolvedTypeName := t.resolveStructTypeName(qualifiedName)
	if fields, ok := t.structFields[resolvedTypeName]; ok {
		// Sealed variant companion (empty struct with Apply method).
		// Variants are registered with nil fields because the companion
		// struct is empty; field info lives in the parent sealed type.
		if len(fields) == 0 && len(namedArgs) > 0 {
			if variantFieldNames := t.findSealedVariantFields(typeName); variantFieldNames != nil {
				return buildSealedVariantApplyCall(fun, variantFieldNames, namedArgs), nil
			}
		}
		// 3. Regular GALA struct construction with named args.
		return t.buildStructLiteralWithNamedArgs(fun, typeName, resolvedTypeName, fields, namedArgs, line, col)
	}

	// 4. Go-imported type (direct or dot-imported).
	if t.isGoImportedType(fun) {
		return buildGoCompositeLiteralWithNamedArgs(fun, namedArgs, t.sortKeyValueExprs), nil
	}
	if _, isIdent := fun.(*ast.Ident); isIdent {
		for _, pkg := range t.importManager.GetDotImports() {
			if pkg != "std" && pkg != t.packageName {
				return buildGoCompositeLiteralWithNamedArgs(fun, namedArgs, t.sortKeyValueExprs), nil
			}
		}
	}

	// 5. No match — the caller used named args against something we can't
	// construct with them. Emit a positioned semantic error.
	return nil, galaerr.NewSemanticErrorAt(line, col, fmt.Sprintf("named arguments only supported for Copy method or struct construction (type: %s)", typeName))
}

// extractTypeNameFromExpr decodes a call target into (typeName, qualifiedName).
// typeName is the bare identifier used for code generation; qualifiedName
// includes any package qualifier (e.g., "go_struct_bridge.Cookie") so
// downstream lookups can distinguish a Go type from a same-named GALA type.
// Handles Ident, IndexExpr, IndexListExpr, and SelectorExpr shapes.
func extractTypeNameFromExpr(fun ast.Expr) (typeName, qualifiedName string) {
	switch f := fun.(type) {
	case *ast.Ident:
		return f.Name, f.Name
	case *ast.IndexExpr:
		return decodeGenericBase(f.X)
	case *ast.IndexListExpr:
		return decodeGenericBase(f.X)
	case *ast.SelectorExpr:
		qn := f.Sel.Name
		if pkgId, ok := f.X.(*ast.Ident); ok {
			qn = pkgId.Name + "." + f.Sel.Name
		}
		return f.Sel.Name, qn
	}
	return "", ""
}

// decodeGenericBase is the Ident/SelectorExpr branch shared by IndexExpr
// and IndexListExpr in extractTypeNameFromExpr — the generic's base expr
// is either a bare identifier or a package-qualified selector.
func decodeGenericBase(x ast.Expr) (string, string) {
	switch b := x.(type) {
	case *ast.Ident:
		return b.Name, b.Name
	case *ast.SelectorExpr:
		qn := b.Sel.Name
		if pkgId, ok := b.X.(*ast.Ident); ok {
			qn = pkgId.Name + "." + b.Sel.Name
		}
		return b.Sel.Name, qn
	}
	return "", ""
}

// buildSealedVariantApplyCall generates `VariantName{}.Apply(args...)`
// with args reordered to match the Apply method's parameter order.
// Named args that don't appear in variantFieldNames are silently dropped
// (the parser should have caught that earlier).
func buildSealedVariantApplyCall(fun ast.Expr, variantFieldNames []string, namedArgs map[string]ast.Expr) ast.Expr {
	orderedArgs := make([]ast.Expr, 0, len(variantFieldNames))
	for _, fieldName := range variantFieldNames {
		if val, ok := namedArgs[fieldName]; ok {
			orderedArgs = append(orderedArgs, val)
		}
	}
	receiver := &ast.CompositeLit{Type: fun}
	return &ast.CallExpr{
		Fun:  &ast.SelectorExpr{X: receiver, Sel: ast.NewIdent("Apply")},
		Args: orderedArgs,
	}
}

// buildStructLiteralWithNamedArgs emits `TypeName[TypeArgs]{field: value, ...}`
// for a known GALA struct. It handles three concerns in order:
//
//   - type-parameter inference when the call site omitted them (e.g.
//     `Tuple(V1 = x, V2 = y)` → `Tuple[string, int]{V1: ..., V2: ...}`),
//   - nil-into-immutable-field rejection (an ergonomic error pointing
//     at Option[T]/None instead of a Go nil-deref at runtime),
//   - NewImmutable() wrapping for each immutable field value.
func (t *galaASTTransformer) buildStructLiteralWithNamedArgs(
	fun ast.Expr,
	typeName, resolvedTypeName string,
	fields []string,
	namedArgs map[string]ast.Expr,
	line, col int,
) (ast.Expr, error) {
	immutFlags := t.structImmutFields[resolvedTypeName]
	fieldTypes := t.structFieldTypes[resolvedTypeName]

	typeExpr := t.inferTypeArgsFromNamedArgs(fun, typeName, resolvedTypeName, namedArgs, fieldTypes)

	var elts []ast.Expr
	for i, fieldName := range fields {
		val, ok := namedArgs[fieldName]
		if !ok {
			continue
		}
		if immutFlags != nil && i < len(immutFlags) && immutFlags[i] {
			if ident, isIdent := val.(*ast.Ident); isIdent && ident.Name == "nil" {
				return nil, galaerr.NewSemanticErrorAt(line, col, fmt.Sprintf(
					"cannot assign nil to immutable field '%s' — use Option[T] with None() for optional values, or 'var %s' to make it mutable",
					fieldName, fieldName))
			}
		}

		valExpr := val
		if immutFlags != nil && i < len(immutFlags) && immutFlags[i] {
			valExpr = &ast.CallExpr{
				Fun:  t.stdIdent("NewImmutable"),
				Args: []ast.Expr{val},
			}
		}
		elts = append(elts, &ast.KeyValueExpr{Key: ast.NewIdent(fieldName), Value: valExpr})
	}
	return &ast.CompositeLit{Type: typeExpr, Elts: elts}, nil
}

// inferTypeArgsFromNamedArgs returns a type expression with all generic
// type parameters filled in from the types of the named-arg values, or
// returns `fun` verbatim if inference does not apply or cannot complete.
// Inference applies only when `fun` is a bare Ident or SelectorExpr (no
// explicit type args in the source), and the underlying type declares
// type parameters.
func (t *galaASTTransformer) inferTypeArgsFromNamedArgs(
	fun ast.Expr,
	typeName, resolvedTypeName string,
	namedArgs map[string]ast.Expr,
	fieldTypes map[string]transpiler.Type,
) ast.Expr {
	switch fun.(type) {
	case *ast.Ident, *ast.SelectorExpr:
		// Eligible for inference.
	default:
		return fun
	}

	typeMeta := t.getTypeMeta(resolvedTypeName)
	if typeMeta == nil || len(typeMeta.TypeParams) == 0 {
		return fun
	}

	inferredTypeArgs := make([]ast.Expr, len(typeMeta.TypeParams))
	typeParamIndices := make(map[string]int, len(typeMeta.TypeParams))
	for i, tp := range typeMeta.TypeParams {
		typeParamIndices[tp] = i
	}

	for fieldName, fieldType := range fieldTypes {
		val, ok := namedArgs[fieldName]
		if !ok {
			continue
		}
		valType := t.getExprTypeName(val)
		if valType == nil || valType.IsNil() {
			continue
		}
		if idx, isTypeParam := typeParamIndices[fieldType.String()]; isTypeParam {
			if inferredTypeArgs[idx] == nil {
				inferredTypeArgs[idx] = t.typeToExpr(valType)
			}
		}
	}

	for _, arg := range inferredTypeArgs {
		if arg == nil {
			return fun // incomplete inference — let downstream stages handle it
		}
	}
	if len(inferredTypeArgs) == 0 {
		return fun
	}

	var baseExpr ast.Expr
	if sel, isSel := fun.(*ast.SelectorExpr); isSel {
		baseExpr = &ast.SelectorExpr{X: sel.X, Sel: ast.NewIdent(typeName)}
	} else {
		baseExpr = ast.NewIdent(typeName)
	}
	if len(inferredTypeArgs) == 1 {
		return &ast.IndexExpr{X: baseExpr, Index: inferredTypeArgs[0]}
	}
	return &ast.IndexListExpr{X: baseExpr, Indices: inferredTypeArgs}
}

// buildGoCompositeLiteralWithNamedArgs emits a plain Go composite literal
// for a Go-imported or dot-imported type. No Immutable wrapping — the
// underlying type is outside GALA's immutable model. Fields are sorted
// alphabetically for deterministic output.
func buildGoCompositeLiteralWithNamedArgs(
	fun ast.Expr,
	namedArgs map[string]ast.Expr,
	sortFn func([]ast.Expr),
) ast.Expr {
	elts := make([]ast.Expr, 0, len(namedArgs))
	for fieldName, val := range namedArgs {
		elts = append(elts, &ast.KeyValueExpr{Key: ast.NewIdent(fieldName), Value: val})
	}
	sortFn(elts)
	return &ast.CompositeLit{Type: fun, Elts: elts}
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

	// L4: Try to rewrite as a placeholder lambda if the expected type is a
	// function type and the expression contains `_` identifiers.
	if expr, handled, err := t.tryRewriteAsPlaceholderLambda(exprCtx, expectedType); err != nil {
		return nil, err
	} else if handled {
		return expr, nil
	}

	// Not a lambda or partial function, transform normally
	return t.transformExpression(exprCtx)
}

// transformLambdaArgWithExpectedType transforms a direct lambda argument.
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

// lambdaActualFuncType extracts the FuncType from a transformed lambda argument.
// The result captures the lambda's ACTUAL inferred param and result types, which
// may differ from the expected types (e.g., a lambda declared with expected result
// `V` may actually have result `int` once its body is typed). Returns NilType when
// expr is not a function literal with a usable Type field.
//
// Used by the gap #9 inference refinement: after transforming each lambda arg,
// we unify its actual type against the method's declared param FuncType to
// propagate inferred type parameters to later args.
func (t *galaASTTransformer) lambdaActualFuncType(expr ast.Expr) transpiler.Type {
	fnLit, ok := expr.(*ast.FuncLit)
	if !ok || fnLit.Type == nil {
		return transpiler.NilType{}
	}
	var params []transpiler.Type
	if fnLit.Type.Params != nil {
		for _, field := range fnLit.Type.Params.List {
			paramType := t.astTypeToTranspilerType(field.Type)
			if len(field.Names) == 0 {
				params = append(params, paramType)
				continue
			}
			for range field.Names {
				params = append(params, paramType)
			}
		}
	}
	var results []transpiler.Type
	if fnLit.Type.Results != nil {
		for _, field := range fnLit.Type.Results.List {
			resultType := t.astTypeToTranspilerType(field.Type)
			if len(field.Names) == 0 {
				results = append(results, resultType)
				continue
			}
			for range field.Names {
				results = append(results, resultType)
			}
		}
	}
	return transpiler.FuncType{Params: params, Results: results}
}

// inferZeroArgTypeParams infers type parameters for a zero-argument sealed variant
// constructor (e.g., None()) when a concrete instantiation of the variant's parent
// sealed type is available in the surrounding context. It checks if the constructor
// is a companion of a sealed type whose instantiation is available and extracts the
// type parameters from that instantiation.
// Returns a typed AST expression (e.g., None[User]) or nil if inference fails.
//
// Context sources tried, in order:
//  1. currentMatchSubjectType — `x match { case _ => None() }` where the subject is
//     Option[T]. (Companion match required.)
//  2. currentFuncReturnType — gap #11: `func find(id int) Option[User] { ... None() ... }`
//     widens the zero-arg constructor from the enclosing function's return type.
func (t *galaASTTransformer) inferZeroArgTypeParams(typeName string, typeMeta *transpiler.TypeMetadata) ast.Expr {
	// Look up companion relationship for this type
	companion := t.lookupCompanion(typeName)
	if companion == nil {
		return nil
	}
	targetBaseName := stripPackagePrefix(companion.TargetType)

	// Try each context source in priority order: match subject first (most
	// specific), then enclosing function return type (gap #11 widening).
	sources := []transpiler.Type{t.currentMatchSubjectType, t.currentFuncReturnType}
	for _, src := range sources {
		if src == nil || src.IsNil() {
			continue
		}
		// The context type's base must match the companion's target type.
		if stripPackagePrefix(src.BaseName()) != targetBaseName {
			continue
		}
		// Extract concrete type params from the context type.
		gen, ok := src.(transpiler.GenericType)
		if !ok || len(gen.Params) == 0 {
			continue
		}
		// Reject if any param is itself an unresolved type parameter (e.g., still T).
		hasUnresolved := false
		for _, p := range gen.Params {
			if t.isActiveTypeParam(p.String()) {
				hasUnresolved = true
				break
			}
		}
		if hasUnresolved {
			continue
		}

		// Build the typed expression: e.g., None[User]
		baseExpr := t.typeToExpr(transpiler.BasicType{Name: typeName})
		if len(gen.Params) == 1 {
			return &ast.IndexExpr{
				X:     baseExpr,
				Index: t.typeToExpr(gen.Params[0]),
			}
		}
		indices := make([]ast.Expr, len(gen.Params))
		for i, p := range gen.Params {
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
