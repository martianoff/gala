package transformer

import (
	"go/ast"
	"go/token"
	"strings"

	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/registry"
)

// This file contains type inference logic extracted from types.go
// Functions: getExprTypeNameManual, resolveType, substituteConcreteTypes, inferMethodTypeParamsFromArgs,
//            inferFuncTypeParamsFromArgs, unifyForInference, substituteInType, isTupleTypeName,
//            hasTupleTypePrefix, getTupleTypeFromName, getReceiverTypeArgs, getReceiverTypeArgStrings,
//            exprToTypeString, substituteTranspilerTypeParams

func (t *galaASTTransformer) getExprTypeNameManual(expr ast.Expr) transpiler.Type {
	if expr == nil {
		return transpiler.NilType{}
	}
	// Check cache first — AST node pointers are unique, so this is safe across scopes.
	if cached, ok := t.exprTypeCache[expr]; ok {
		return cached
	}
	result := t.getExprTypeNameManualUncached(expr)
	// Guard against nil interface value (not NilType{}) which can happen when
	// called functions return nil instead of NilType{}.
	if result == nil {
		return transpiler.NilType{}
	}
	// Only cache non-nil results; failed lookups may succeed later as more scope info becomes available.
	if !result.IsNil() {
		t.exprTypeCache[expr] = result
	}
	return result
}

func (t *galaASTTransformer) getExprTypeNameManualUncached(expr ast.Expr) transpiler.Type {
	switch e := expr.(type) {
	case *ast.BasicLit:
		switch e.Kind {
		case token.INT:
			return transpiler.BasicType{Name: "int"}
		case token.FLOAT:
			return transpiler.BasicType{Name: "float64"}
		case token.IMAG:
			return transpiler.BasicType{Name: "complex128"}
		case token.CHAR:
			return transpiler.BasicType{Name: "rune"}
		case token.STRING:
			return transpiler.BasicType{Name: "string"}
		}
	case *ast.Ident:
		if e.Name == "true" || e.Name == "false" {
			return transpiler.BasicType{Name: "bool"}
		}
		if typ := t.getType(e.Name); !typ.IsNil() {
			return typ
		}
		// Check if this is a reference to a known GALA function (e.g., passing getAnswer as func() int).
		if fm, ok := t.functions[e.Name]; ok && len(fm.TypeParams) == 0 {
			var params []transpiler.Type
			params = append(params, fm.ParamTypes...)
			var results []transpiler.Type
			if fm.ReturnType != nil && !fm.ReturnType.IsNil() {
				results = append(results, fm.ReturnType)
			}
			return transpiler.FuncType{Params: params, Results: results}
		}
		return transpiler.NilType{}
	case *ast.IndexExpr:
		xType := t.getExprTypeNameManual(e.X)
		if arr, ok := xType.(transpiler.ArrayType); ok {
			return arr.Elem
		}
		// Handle generic type expression like Option[int]
		return t.astTypeToTranspilerType(e)
	case *ast.IndexListExpr:
		// Handle generic type expression like Tuple[int, string]
		return t.astTypeToTranspilerType(e)
	case *ast.ParenExpr:
		return t.getExprTypeNameManual(e.X)
	case *ast.StarExpr:
		// Handle pointer dereference *x
		xType := t.getExprTypeNameManual(e.X)
		if ptr, ok := xType.(transpiler.PointerType); ok {
			return ptr.Elem
		}
		return transpiler.NilType{}
	case *ast.UnaryExpr:
		switch e.Op {
		case token.NOT:
			return transpiler.BasicType{Name: "bool"}
		case token.AND:
			return transpiler.PointerType{Elem: t.getExprTypeNameManual(e.X)}
		case token.MUL:
			xType := t.getExprTypeNameManual(e.X)
			if ptr, ok := xType.(transpiler.PointerType); ok {
				return ptr.Elem
			}
			return transpiler.NilType{}
		default:
			return t.getExprTypeNameManual(e.X)
		}
	case *ast.BinaryExpr:
		switch e.Op {
		case token.EQL, token.NEQ, token.LSS, token.LEQ, token.GTR, token.GEQ, token.LAND, token.LOR:
			return transpiler.BasicType{Name: "bool"}
		default:
			return t.getExprTypeNameManual(e.X)
		}
	case *ast.SelectorExpr:
		return t.inferSelectorExprType(e)
	case *ast.CallExpr:
		return t.inferCallExprType(e)
	case *ast.FuncLit:
		// Handle lambda expressions - extract their function type
		if e.Type != nil {
			var params []transpiler.Type
			var results []transpiler.Type
			if e.Type.Params != nil {
				for _, field := range e.Type.Params.List {
					paramType := t.astTypeToTranspilerType(field.Type)
					// If there are multiple names, repeat the type for each
					if len(field.Names) > 0 {
						for range field.Names {
							params = append(params, paramType)
						}
					} else {
						params = append(params, paramType)
					}
				}
			}
			if e.Type.Results != nil {
				for _, field := range e.Type.Results.List {
					resultType := t.astTypeToTranspilerType(field.Type)
					if len(field.Names) > 0 {
						for range field.Names {
							results = append(results, resultType)
						}
					} else {
						results = append(results, resultType)
					}
				}
			}
			return transpiler.FuncType{Params: params, Results: results}
		}
	case *ast.CompositeLit:
		// Use astTypeToTranspilerType to preserve generic type parameters
		typ := t.astTypeToTranspilerType(e.Type)
		if !typ.IsNil() {
			return typ
		}
		typeName := t.getBaseTypeName(e.Type)
		return t.resolveType(typeName)
	}
	return transpiler.NilType{}
}

// resolveMethodCallType resolves the return type of a method call in Receiver_Method form.
// It handles type meta lookup, struct-level type param substitution from receiver,
// method-level type param substitution from explicit type args, and
// method-level type param inference from arguments.
//
// Parameters:
//   - receiverTypeName: the fully qualified or bare type name of the receiver (e.g., "Array", "std.Try")
//   - methodName: the method being called (e.g., "Zip", "FlatMap")
//   - typeArgs: explicit type arguments on the call (e.g., [string] for Zip[string])
//   - args: all arguments to the call expression
//   - receiverArgIndex: which arg is the receiver (-1 if the receiver is not among the args,
//     e.g., for receiver.Method() calls where receiver is sel.X not an arg)
func (t *galaASTTransformer) resolveMethodCallType(
	receiverTypeName string,
	methodName string,
	typeArgs []transpiler.Type,
	args []ast.Expr,
	receiverArgIndex int,
) transpiler.Type {
	return t.resolveMethodCallTypeWithParams(receiverTypeName, methodName, typeArgs, args, receiverArgIndex, nil)
}

// resolveMethodCallTypeWithParams is like resolveMethodCallType but accepts pre-resolved
// receiver generic type params. This is used when the receiver type is already known
// (e.g., from evaluating sel.X) rather than being derived from an argument.
func (t *galaASTTransformer) resolveMethodCallTypeWithParams(
	receiverTypeName string,
	methodName string,
	typeArgs []transpiler.Type,
	args []ast.Expr,
	receiverArgIndex int,
	preResolvedGenericParams []transpiler.Type,
) transpiler.Type {
	typeMeta := t.getTypeMeta(receiverTypeName)
	if typeMeta == nil {
		return transpiler.NilType{}
	}
	methodMeta, ok := typeMeta.Methods[methodName]
	if !ok {
		return transpiler.NilType{}
	}

	result := methodMeta.ReturnType

	// Determine the receiver's concrete generic type params for substitution.
	// Use pre-resolved params if provided, otherwise extract from the receiver argument.
	receiverGenericParams := preResolvedGenericParams
	if len(receiverGenericParams) == 0 && receiverArgIndex >= 0 && receiverArgIndex < len(args) {
		receiverArgType := t.getExprTypeNameManual(args[receiverArgIndex])
		if genType, ok := receiverArgType.(transpiler.GenericType); ok {
			receiverGenericParams = genType.Params
		}
	}

	// Substitute struct-level type params (e.g., T -> int for Array[int])
	if len(receiverGenericParams) > 0 && len(typeMeta.TypeParams) > 0 {
		result = t.substituteConcreteTypes(result, typeMeta.TypeParams, receiverGenericParams)
	}

	// Substitute method-level type params
	if len(methodMeta.TypeParams) > 0 {
		if len(typeArgs) > 0 {
			// Use explicit type args (e.g., Zip[string])
			result = t.substituteConcreteTypes(result, methodMeta.TypeParams, typeArgs)
		} else {
			// Try to infer method-level type params from arguments
			// For Receiver_Method calls, the method's regular params start after the receiver
			methodArgs := args
			if receiverArgIndex >= 0 && receiverArgIndex < len(args) {
				methodArgs = args[receiverArgIndex+1:]
			}
			inferredTypeArgs := t.inferMethodTypeParamsFromArgs(methodMeta, methodArgs, typeMeta.TypeParams, receiverGenericParams)
			if len(inferredTypeArgs) > 0 {
				result = t.substituteConcreteTypes(result, methodMeta.TypeParams, inferredTypeArgs)
			}
		}
	}

	return result
}

// resolveStdConstructorParentType checks if the given name is a std constructor or has a
// std type/constructor prefix, and returns the parent type name (e.g., "Option", "Either", "Try").
// Returns empty string if the name doesn't match any known std constructor pattern.
//
// When includeDirectConstructors is true, all known direct constructor names (Some, None, Left,
// Right, Success, Failure) are matched. When false, only Left and Right are matched as direct
// names — the others are handled by function metadata lookup (getFunction) for more accurate
// type parameter inference from arguments.
//
// In both modes, prefixed patterns (Option_*, Some_*, Either_*, Left_*, etc.) and
// tuple types/prefixes are always matched.
func (t *galaASTTransformer) resolveStdConstructorParentType(name string, includeDirectConstructors bool) string {
	// Check direct constructor names
	if includeDirectConstructors {
		if rule, ok := registry.StdConstructorRule(name); ok {
			return rule.ParentType
		}
	} else {
		// Only match Left and Right as direct names
		if name == transpiler.FuncLeft || name == transpiler.FuncRight {
			return transpiler.TypeEither
		}
	}
	// Check prefixed patterns (Option_*, Some_*, Either_*, Left_*, etc.)
	if parentType, ok := registry.StdParentTypeForPrefix(name); ok {
		return parentType
	}
	// Check tuple types and tuple prefixes
	if t.isTupleTypeName(name) || t.hasTupleTypePrefix(name) {
		return t.getTupleTypeFromName(name)
	}
	return ""
}

func (t *galaASTTransformer) resolveType(name string) transpiler.Type {
	if name == "" {
		return transpiler.NilType{}
	}
	return transpiler.ParseType(name)
}

// substituteConcreteTypes substitutes type parameters in a type with concrete types.
// For example, if returnType is Pair[B, A], typeParams is ["A", "B"], and concreteTypes is [int, string],
// the result will be Pair[string, int].
func (t *galaASTTransformer) substituteConcreteTypes(returnType transpiler.Type, typeParams []string, concreteTypes []transpiler.Type) transpiler.Type {
	if returnType == nil || returnType.IsNil() {
		return returnType
	}

	// Build a mapping from type parameter names to concrete types
	paramMap := make(map[string]transpiler.Type)
	for i, param := range typeParams {
		if i < len(concreteTypes) {
			paramMap[param] = concreteTypes[i]
		}
	}

	return t.substituteInType(returnType, paramMap)
}

// inferMethodTypeParamsFromArgs attempts to infer method-level type parameters from call arguments.
// For example, for FlatMap[U](f func(T) Try[U]) where the argument is a lambda returning Try[User],
// this function infers U = User.
func (t *galaASTTransformer) inferMethodTypeParamsFromArgs(methodMeta *transpiler.MethodMetadata, args []ast.Expr, structTypeParams []string, structTypeArgs []transpiler.Type) []transpiler.Type {
	if len(methodMeta.TypeParams) == 0 || len(methodMeta.ParamTypes) == 0 || len(args) == 0 {
		return nil
	}

	// First substitute struct-level type params in method param types
	// e.g., for Try[User].FlatMap, substitute T -> User in func(T) Try[U] to get func(User) Try[U]
	substitutedParamTypes := make([]transpiler.Type, len(methodMeta.ParamTypes))
	for i, pt := range methodMeta.ParamTypes {
		substitutedParamTypes[i] = t.substituteConcreteTypes(pt, structTypeParams, structTypeArgs)
	}

	// Build a mapping from method type param names to inferred concrete types
	inferredMap := make(map[string]transpiler.Type)

	// Try to infer type params from each argument
	for i, arg := range args {
		if i >= len(substitutedParamTypes) {
			break
		}
		paramType := substitutedParamTypes[i]

		// Get the actual type of the argument
		argType := t.getExprTypeNameManual(arg)
		if argType == nil || argType.IsNil() {
			argType, _ = t.inferExprType(arg)
		}
		if argType == nil || argType.IsNil() {
			continue
		}

		// Try to unify paramType with argType to find type param substitutions
		t.unifyForInference(paramType, argType, methodMeta.TypeParams, inferredMap)
	}

	// Build result in order of type params
	if len(inferredMap) == 0 {
		return nil
	}

	result := make([]transpiler.Type, len(methodMeta.TypeParams))
	for i, paramName := range methodMeta.TypeParams {
		if inferredType, ok := inferredMap[paramName]; ok {
			result[i] = inferredType
		} else {
			// Couldn't infer this type param
			return nil
		}
	}

	return result
}

// inferFuncTypeParamsFromArgs attempts to infer type parameters for standalone function calls.
// For example, for ArrayOf[T any](elements ...T) Array[T] where the arguments are [1, 2, 3],
// this function infers T = int.
func (t *galaASTTransformer) inferFuncTypeParamsFromArgs(fMeta *transpiler.FunctionMetadata, args []ast.Expr, hasEllipsis bool) []transpiler.Type {
	if len(fMeta.TypeParams) == 0 || len(args) == 0 {
		return nil
	}

	// Build a mapping from type param names to inferred concrete types
	inferredMap := make(map[string]transpiler.Type)

	// Try to infer type params from each argument
	for i, arg := range args {
		var paramType transpiler.Type
		if i < len(fMeta.ParamTypes) {
			paramType = fMeta.ParamTypes[i]
		} else if len(fMeta.ParamTypes) > 0 {
			// For variadic functions, the last param type applies to remaining args
			lastParamType := fMeta.ParamTypes[len(fMeta.ParamTypes)-1]
			// Unwrap slice type for variadic parameters (e.g., ...T becomes T for each arg)
			if arrType, ok := lastParamType.(transpiler.ArrayType); ok {
				paramType = arrType.Elem
			} else {
				paramType = lastParamType
			}
		}
		if paramType == nil {
			continue
		}

		// Get the actual type of the argument
		argType := t.getExprTypeNameManual(arg)
		if argType == nil || argType.IsNil() {
			argType, _ = t.inferExprType(arg)
		}
		if argType == nil || argType.IsNil() {
			continue
		}

		// When spread operator is used (e.g., ArrayOf(keys...)), the arg type is []T
		// but the variadic param type has already been unwrapped to T, so we need to
		// unwrap the arg type as well to get the element type.
		if hasEllipsis {
			if arrType, ok := argType.(transpiler.ArrayType); ok {
				argType = arrType.Elem
			}
		}

		// Try to unify paramType with argType to find type param substitutions
		t.unifyForInference(paramType, argType, fMeta.TypeParams, inferredMap)
	}

	// Build result in order of type params
	if len(inferredMap) == 0 {
		return nil
	}

	result := make([]transpiler.Type, len(fMeta.TypeParams))
	for i, paramName := range fMeta.TypeParams {
		if inferredType, ok := inferredMap[paramName]; ok {
			result[i] = inferredType
		} else {
			// Couldn't infer this type param
			return nil
		}
	}

	return result
}

// unifyForInference attempts to unify a pattern type with a concrete type to infer type parameters.
// This is used to infer method-level type params from call arguments.
func (t *galaASTTransformer) unifyForInference(pattern, concrete transpiler.Type, typeParams []string, inferredMap map[string]transpiler.Type) bool {
	if pattern == nil || concrete == nil || pattern.IsNil() || concrete.IsNil() {
		return false
	}

	// Check if pattern is one of the type parameters we're looking for
	patternStr := pattern.String()
	// Also try without package prefix (e.g., "collection_immutable.T" -> "T")
	patternStrNoPackage := stripPackagePrefix(patternStr)
	for _, tp := range typeParams {
		if patternStr == tp || patternStrNoPackage == tp {
			// Found a type parameter - record the inferred type
			if existing, ok := inferredMap[tp]; ok {
				// Already have an inference - check consistency
				return existing.String() == concrete.String()
			}
			inferredMap[tp] = concrete
			return true
		}
	}

	// Check if both are array types
	patternArr, patternIsArr := pattern.(transpiler.ArrayType)
	concreteArr, concreteIsArr := concrete.(transpiler.ArrayType)
	if patternIsArr && concreteIsArr {
		return t.unifyForInference(patternArr.Elem, concreteArr.Elem, typeParams, inferredMap)
	}

	// Check if both are pointer types
	patternPtr, patternIsPtr := pattern.(transpiler.PointerType)
	concretePtr, concreteIsPtr := concrete.(transpiler.PointerType)
	if patternIsPtr && concreteIsPtr {
		return t.unifyForInference(patternPtr.Elem, concretePtr.Elem, typeParams, inferredMap)
	}

	// Check if both are map types
	patternMap, patternIsMap := pattern.(transpiler.MapType)
	concreteMap, concreteIsMap := concrete.(transpiler.MapType)
	if patternIsMap && concreteIsMap {
		t.unifyForInference(patternMap.Key, concreteMap.Key, typeParams, inferredMap)
		t.unifyForInference(patternMap.Elem, concreteMap.Elem, typeParams, inferredMap)
		return true
	}

	// Check if both are function types
	patternFunc, patternIsFunc := pattern.(transpiler.FuncType)
	concreteFunc, concreteIsFunc := concrete.(transpiler.FuncType)
	if patternIsFunc && concreteIsFunc {
		// Unify parameter types
		for i, pParam := range patternFunc.Params {
			if i < len(concreteFunc.Params) {
				t.unifyForInference(pParam, concreteFunc.Params[i], typeParams, inferredMap)
			}
		}
		// Unify result types
		for i, pResult := range patternFunc.Results {
			if i < len(concreteFunc.Results) {
				t.unifyForInference(pResult, concreteFunc.Results[i], typeParams, inferredMap)
			}
		}
		return true
	}

	// Check if both are generic types
	patternGen, patternIsGen := pattern.(transpiler.GenericType)
	concreteGen, concreteIsGen := concrete.(transpiler.GenericType)
	if patternIsGen && concreteIsGen {
		// Check if base types are compatible
		if stripPackagePrefix(patternGen.Base.BaseName()) != stripPackagePrefix(concreteGen.Base.BaseName()) {
			return false
		}
		// Unify type parameters
		for i := range patternGen.Params {
			if i < len(concreteGen.Params) {
				t.unifyForInference(patternGen.Params[i], concreteGen.Params[i], typeParams, inferredMap)
			}
		}
		return true
	}

	// Check if named types match (handling package prefixes)
	if stripPackagePrefix(pattern.BaseName()) == stripPackagePrefix(concrete.BaseName()) {
		return true
	}

	return false
}

// substituteInType recursively substitutes type parameters in a type
func (t *galaASTTransformer) substituteInType(typ transpiler.Type, paramMap map[string]transpiler.Type) transpiler.Type {
	if typ == nil || typ.IsNil() {
		return typ
	}

	switch v := typ.(type) {
	case transpiler.BasicType:
		if concrete, ok := paramMap[v.Name]; ok {
			return concrete
		}
		return v
	case transpiler.NamedType:
		if concrete, ok := paramMap[v.Name]; ok {
			return concrete
		}
		return v
	case transpiler.GenericType:
		newParams := make([]transpiler.Type, len(v.Params))
		for i, param := range v.Params {
			newParams[i] = t.substituteInType(param, paramMap)
		}
		newBase := t.substituteInType(v.Base, paramMap)
		if namedBase, ok := newBase.(transpiler.NamedType); ok {
			return transpiler.GenericType{
				Base:   namedBase,
				Params: newParams,
			}
		}
		return transpiler.GenericType{
			Base:   v.Base,
			Params: newParams,
		}
	case transpiler.ArrayType:
		return transpiler.ArrayType{Elem: t.substituteInType(v.Elem, paramMap)}
	case transpiler.PointerType:
		return transpiler.PointerType{Elem: t.substituteInType(v.Elem, paramMap)}
	case transpiler.MapType:
		return transpiler.MapType{
			Key:  t.substituteInType(v.Key, paramMap),
			Elem: t.substituteInType(v.Elem, paramMap),
		}
	case transpiler.FuncType:
		newParams := make([]transpiler.Type, len(v.Params))
		for i, p := range v.Params {
			newParams[i] = t.substituteInType(p, paramMap)
		}
		newResults := make([]transpiler.Type, len(v.Results))
		for i, r := range v.Results {
			newResults[i] = t.substituteInType(r, paramMap)
		}
		return transpiler.FuncType{Params: newParams, Results: newResults}
	default:
		return typ
	}
}

// isTupleTypeName checks if a name is exactly a TupleN type name
// Handles both prefixed (std.Tuple3) and unprefixed (Tuple3) names
func (t *galaASTTransformer) isTupleTypeName(name string) bool {
	// Strip std. prefix if present
	normalizedName := name
	if hasStdPrefix(name) {
		normalizedName = stripStdPrefix(name)
	}
	switch normalizedName {
	case transpiler.TypeTuple, transpiler.TypeTuple3, transpiler.TypeTuple4,
		transpiler.TypeTuple5, transpiler.TypeTuple6, transpiler.TypeTuple7,
		transpiler.TypeTuple8, transpiler.TypeTuple9, transpiler.TypeTuple10:
		return true
	}
	return false
}

// hasTupleTypePrefix checks if a name has a TupleN_ prefix
func (t *galaASTTransformer) hasTupleTypePrefix(name string) bool {
	tupleTypes := []string{
		transpiler.TypeTuple10, transpiler.TypeTuple9, transpiler.TypeTuple8,
		transpiler.TypeTuple7, transpiler.TypeTuple6, transpiler.TypeTuple5,
		transpiler.TypeTuple4, transpiler.TypeTuple3, transpiler.TypeTuple,
	}
	for _, tt := range tupleTypes {
		if strings.HasPrefix(name, tt+"_") {
			return true
		}
	}
	return false
}

// getTupleTypeFromName extracts the TupleN type name from a name that starts with a tuple type
func (t *galaASTTransformer) getTupleTypeFromName(name string) string {
	// Check in order of longest to shortest to handle Tuple10 before Tuple
	tupleTypes := []string{
		transpiler.TypeTuple10, transpiler.TypeTuple9, transpiler.TypeTuple8,
		transpiler.TypeTuple7, transpiler.TypeTuple6, transpiler.TypeTuple5,
		transpiler.TypeTuple4, transpiler.TypeTuple3, transpiler.TypeTuple,
	}
	for _, tt := range tupleTypes {
		if name == tt || strings.HasPrefix(name, tt+"_") {
			return tt
		}
	}
	return transpiler.TypeTuple
}

// getReceiverTypeArgs extracts type arguments from a receiver type and converts them to ast.Expr.
// For example, for *Array[int] or Array[int], it returns [int] as []ast.Expr.
func (t *galaASTTransformer) getReceiverTypeArgs(recvType transpiler.Type) []ast.Expr {
	if recvType == nil || recvType.IsNil() {
		return nil
	}
	// Unwrap pointer type
	if ptr, ok := recvType.(transpiler.PointerType); ok {
		return t.getReceiverTypeArgs(ptr.Elem)
	}
	// Extract type params from generic type
	if gen, ok := recvType.(transpiler.GenericType); ok {
		var args []ast.Expr
		for _, param := range gen.Params {
			args = append(args, t.typeToExpr(param))
		}
		return args
	}
	return nil
}

// getReceiverTypeArgStrings extracts type arguments from a receiver type as strings.
// For example, for *Container[int], it returns ["int"].
func (t *galaASTTransformer) getReceiverTypeArgStrings(recvType transpiler.Type) []string {
	if recvType == nil || recvType.IsNil() {
		return nil
	}
	// Unwrap pointer type
	if ptr, ok := recvType.(transpiler.PointerType); ok {
		return t.getReceiverTypeArgStrings(ptr.Elem)
	}
	// Extract type params from generic type
	if gen, ok := recvType.(transpiler.GenericType); ok {
		var args []string
		for _, param := range gen.Params {
			args = append(args, param.String())
		}
		return args
	}
	return nil
}

// exprToTypeString converts an ast.Expr to a type string.
func (t *galaASTTransformer) exprToTypeString(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.Ident:
		return e.Name
	case *ast.SelectorExpr:
		if x, ok := e.X.(*ast.Ident); ok {
			return x.Name + "." + e.Sel.Name
		}
	case *ast.StarExpr:
		return "*" + t.exprToTypeString(e.X)
	case *ast.IndexExpr:
		return t.exprToTypeString(e.X) + "[" + t.exprToTypeString(e.Index) + "]"
	case *ast.IndexListExpr:
		var params []string
		for _, idx := range e.Indices {
			params = append(params, t.exprToTypeString(idx))
		}
		return t.exprToTypeString(e.X) + "[" + strings.Join(params, ", ") + "]"
	}
	return ""
}

// substituteTranspilerTypeParams substitutes type parameters in a type with their concrete values.
// Delegates to substituteInType after converting the string-keyed map to a Type-keyed map.
func (t *galaASTTransformer) substituteTranspilerTypeParams(typ transpiler.Type, subst map[string]string) transpiler.Type {
	if typ == nil || typ.IsNil() || len(subst) == 0 {
		return typ
	}
	paramMap := make(map[string]transpiler.Type, len(subst))
	for k, v := range subst {
		paramMap[k] = transpiler.ParseType(v)
	}
	return t.substituteInType(typ, paramMap)
}

// getGoFuncReturnType returns the first return type of a Go function using GoTypeInfo.
// Handles package-qualified function calls like fmt.Sprintf, strings.Split, etc.
func (t *galaASTTransformer) getGoFuncReturnType(qualifiedName string) transpiler.Type {
	if t.goTypeInfo == nil {
		return nil
	}
	return t.goTypeInfo.GetFuncReturnType(qualifiedName)
}

// getGoMethodReturnType returns the first return type of a method on a Go type.
// Handles calls like scanner.Text(), req.Header.Set(), etc.
// The typeName may be package-qualified (e.g., "bufio.Scanner") or a pointer type.
func (t *galaASTTransformer) getGoMethodReturnType(typeName, methodName string) transpiler.Type {
	if t.goTypeInfo == nil {
		return nil
	}
	// Strip pointer prefix
	cleanType := strings.TrimPrefix(typeName, "*")

	// Try direct lookup
	if retType := t.goTypeInfo.GetMethodReturnType(cleanType, methodName); retType != nil {
		return retType
	}

	// If the type is a Go type alias, resolve and try the underlying type's methods
	if aliasedType := t.goTypeInfo.ResolveTypeAlias(cleanType); aliasedType != nil {
		aliasedName := aliasedType.String()
		if retType := t.goTypeInfo.GetMethodReturnType(aliasedName, methodName); retType != nil {
			return retType
		}
	}

	return nil
}

// getGoFieldType returns the type of a field on a Go struct type.
// Handles field access like req.Header, resp.StatusCode, etc.
func (t *galaASTTransformer) getGoFieldType(typeName, fieldName string) transpiler.Type {
	if t.goTypeInfo == nil {
		return nil
	}
	// Strip pointer prefix
	cleanType := strings.TrimPrefix(typeName, "*")

	// Try direct lookup
	if fType := t.goTypeInfo.GetFieldType(cleanType, fieldName); fType != nil {
		return fType
	}

	// If the type is a Go type alias, resolve and try the underlying type's fields
	if aliasedType := t.goTypeInfo.ResolveTypeAlias(cleanType); aliasedType != nil {
		aliasedName := aliasedType.String()
		if fType := t.goTypeInfo.GetFieldType(aliasedName, fieldName); fType != nil {
			return fType
		}
	}

	return nil
}
