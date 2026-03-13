package transformer

import (
	"go/ast"
	"go/token"
	"strings"

	"martianoff/gala/galaerr"
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
		return t.getType(e.Name)
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
		xType := t.getExprTypeNameManual(e.X)
		xTypeName := xType.String()
		// Extract base type name (strip generic parameters like List[T] -> List)
		baseTypeName := xTypeName
		if idx := strings.Index(xTypeName, "["); idx != -1 {
			baseTypeName = xTypeName[:idx]
		}
		// Strip pointer prefix for struct field lookup
		baseTypeName = strings.TrimPrefix(baseTypeName, "*")
		// Resolve to fully qualified name for map lookup
		resolvedTypeName := t.resolveStructTypeName(baseTypeName)
		if !xType.IsNil() && t.structFieldTypes[resolvedTypeName] != nil {
			if fType, ok := t.structFieldTypes[resolvedTypeName][e.Sel.Name]; ok && !fType.IsNil() {
				// If xType is a generic type, substitute type parameters in the field type
				// e.g., for acc.V1 where acc is Tuple[HashMap[K,V], HashMap[K,V]],
				// the field V1 has declared type Immutable[A], substitute A -> HashMap[K,V]
				// Unwrap pointer type if needed (e.g., for *Container[T].value)
				underlyingType := xType
				if ptr, ok := xType.(transpiler.PointerType); ok {
					underlyingType = ptr.Elem
				}
				if genType, ok := underlyingType.(transpiler.GenericType); ok {
					if typeMeta := t.getTypeMeta(resolvedTypeName); typeMeta != nil && len(typeMeta.TypeParams) > 0 {
						return t.substituteConcreteTypes(fType, typeMeta.TypeParams, genType.Params)
					}
				}
				return fType
			}
		}
		// Try Go type info for struct field access and method calls on Go types
		if !xType.IsNil() {
			if fType := t.getGoFieldType(xTypeName, e.Sel.Name); fType != nil {
				return fType
			}
		}
		// It might be a package-qualified name
		if x, ok := e.X.(*ast.Ident); ok {
			if t.importManager.IsPackage(x.Name) {
				pkgName := x.Name
				if actual, ok := t.importManager.ResolveAlias(pkgName); ok {
					pkgName = actual
				}
				// Check if this is a Go constant or variable (not a type)
				// e.g., runtime.GOOS is a string constant, not a type
				qualName := pkgName + "." + e.Sel.Name
				if t.goTypeInfo != nil {
					if constType, ok := t.goTypeInfo.Constants[qualName]; ok {
						return constType
					}
					if varType, ok := t.goTypeInfo.Variables[qualName]; ok {
						return varType
					}
				}
				return transpiler.NamedType{Package: pkgName, Name: e.Sel.Name}
			}
		}
	case *ast.CallExpr:
		// Handle IIFE (used by if/match expressions)
		if fl, ok := e.Fun.(*ast.FuncLit); ok {
			if fl.Type != nil && fl.Type.Results != nil && len(fl.Type.Results.List) > 0 {
				return t.astTypeToTranspilerType(fl.Type.Results.List[0].Type)
			}
		}

		// Handle b.Get() or std.Some()
		// Capture type arguments from generic calls like Tuple[int, string](...)
		fun := e.Fun
		var typeArgs []transpiler.Type
		if idx, ok := fun.(*ast.IndexExpr); ok {
			fun = idx.X
			typeArgs = []transpiler.Type{t.astTypeToTranspilerType(idx.Index)}
		} else if idxList, ok := fun.(*ast.IndexListExpr); ok {
			fun = idxList.X
			for _, idx := range idxList.Indices {
				typeArgs = append(typeArgs, t.astTypeToTranspilerType(idx))
			}
		}

		if sel, ok := fun.(*ast.SelectorExpr); ok {
			// Handle Apply method on composite literal: Some[int]{}.Apply(value) -> Option[int]
			if sel.Sel.Name == "Apply" {
				if compLit, ok := sel.X.(*ast.CompositeLit); ok {
					typeName := t.getBaseTypeName(compLit.Type)
					if typeName != "" {
						// Use unified resolution to find type metadata
						typeMeta := t.getTypeMeta(typeName)
						if typeMeta != nil {
							if methodMeta, hasApply := typeMeta.Methods["Apply"]; hasApply {
								// Get type args from the composite literal type
								var litTypeArgs []transpiler.Type
								if idx, ok := compLit.Type.(*ast.IndexExpr); ok {
									litTypeArgs = []transpiler.Type{t.astTypeToTranspilerType(idx.Index)}
								} else if idxList, ok := compLit.Type.(*ast.IndexListExpr); ok {
									for _, idxExpr := range idxList.Indices {
										litTypeArgs = append(litTypeArgs, t.astTypeToTranspilerType(idxExpr))
									}
								}
								// Substitute type parameters in return type
								if len(litTypeArgs) > 0 && len(typeMeta.TypeParams) > 0 {
									return t.substituteConcreteTypes(methodMeta.ReturnType, typeMeta.TypeParams, litTypeArgs)
								}
								return methodMeta.ReturnType
							}
						}
					}
				}
			}
			if sel.Sel.Name == transpiler.MethodGet {
				// Get the type of x in x.Get()
				var xType transpiler.Type
				var isVal bool
				var isImmutableFieldAccess bool
				if id, ok := sel.X.(*ast.Ident); ok {
					if t.isVal(id.Name) {
						isVal = true
						// For vals, the stored type is already the inner type (e.g., Array[int] not Immutable[Array[int]])
						// So x.Get() returns the stored type directly
						xType = t.getType(id.Name)
					}
				}
				// Check if sel.X is an immutable struct field access (e.g., c.value where value is an immutable field)
				// In this case, the .Get() is unwrapping the implicit Immutable wrapper,
				// and xType from getExprTypeNameManual will be the declared field type (e.g., Option[int])
				// which is what we should return (not the result of calling Option.Get() which would be int)
				if innerSel, ok := sel.X.(*ast.SelectorExpr); ok {
					// Get the type of the receiver (e.g., for c.value, get type of c)
					recvType := t.getExprTypeNameManual(innerSel.X)
					if !recvType.IsNil() {
						baseRecvTypeName := recvType.String()
						if idx := strings.Index(baseRecvTypeName, "["); idx != -1 {
							baseRecvTypeName = baseRecvTypeName[:idx]
						}
						baseRecvTypeName = strings.TrimPrefix(baseRecvTypeName, "*")
						resolvedTypeName := t.resolveStructTypeName(baseRecvTypeName)
						// Check if this field is immutable
						if fields, ok := t.structFields[resolvedTypeName]; ok {
							for i, f := range fields {
								if f == innerSel.Sel.Name {
									if i < len(t.structImmutFields[resolvedTypeName]) && t.structImmutFields[resolvedTypeName][i] {
										isImmutableFieldAccess = true
									}
									break
								}
							}
						}
					}
				}
				if xType == nil || xType.IsNil() {
					xType = t.getExprTypeNameManual(sel.X)
				}
				// For vals and immutable field access, .Get() unwraps the implicit Immutable wrapper
				// and returns the stored type directly - BUT only when there are no arguments.
				// If there are arguments (like runes.Get(i)), this is a method call on the stored type,
				// not an Immutable unwrap - fall through to generic method lookup below.
				if (isVal || isImmutableFieldAccess) && xType != nil && !xType.IsNil() && len(e.Args) == 0 {
					return xType
				}
				xBaseName := xType.BaseName()
				// For Immutable[T].Get(), return the inner type T
				if xBaseName == transpiler.TypeImmutable || xBaseName == withStdPrefix(transpiler.TypeImmutable) {
					if gen, ok := xType.(transpiler.GenericType); ok && len(gen.Params) > 0 {
						return gen.Params[0]
					}
				}
				// For other types, use generic method lookup via typeMetas
				// This handles Array[T].Get() -> T, List[T].Get() -> T, etc.
				if genType, ok := xType.(transpiler.GenericType); ok {
					baseTypeName := genType.Base.String()
					if typeMeta := t.getTypeMeta(baseTypeName); typeMeta != nil {
						if methodMeta, ok := typeMeta.Methods[sel.Sel.Name]; ok {
							return t.substituteConcreteTypes(methodMeta.ReturnType, typeMeta.TypeParams, genType.Params)
						}
					}
				}
				// Handle pointer-wrapped generic types (e.g., *Future[Array[int]].Get())
				// Only when all type params are concrete (not unresolved like *List[T])
				if ptrType, ok := xType.(transpiler.PointerType); ok {
					if genType, ok := ptrType.Elem.(transpiler.GenericType); ok && !t.hasTypeParams(genType) {
						baseTypeName := genType.Base.String()
						if typeMeta := t.getTypeMeta(baseTypeName); typeMeta != nil {
							if methodMeta, ok := typeMeta.Methods[sel.Sel.Name]; ok {
								return t.substituteConcreteTypes(methodMeta.ReturnType, typeMeta.TypeParams, genType.Params)
							}
						}
					}
				}
				return xType
			}

			if sel.Sel.Name == transpiler.FuncNewImmutable || sel.Sel.Name == transpiler.TypeImmutable {
				if len(e.Args) > 0 {
					innerType := t.getExprTypeNameManual(e.Args[0])
					if t.isImmutableType(innerType) {
						panic(galaerr.NewSemanticError("recursive Immutable wrapping is not allowed"))
					}
					return transpiler.GenericType{
						Base:   transpiler.NamedType{Package: registry.StdPackageName, Name: transpiler.TypeImmutable},
						Params: []transpiler.Type{innerType},
					}
				}
			}

			// Check if sel.X is actually the std package before matching std-specific selector names
			isStdQualified := false
			if stdId, ok := sel.X.(*ast.Ident); ok && stdId.Name == registry.StdPackageName {
				isStdQualified = true
			}

			// IMPORTANT: Check for explicit type args BEFORE looking up metadata return types
			// This ensures Left_Apply[int, string] uses [int, string] instead of [A, B] from metadata
			if isStdQualified && len(typeArgs) > 0 {
				if parentType := t.resolveStdConstructorParentType(sel.Sel.Name, true); parentType != "" {
					return transpiler.GenericType{
						Base:   transpiler.NamedType{Package: registry.StdPackageName, Name: parentType},
						Params: typeArgs,
					}
				}
			}

			if id, ok := sel.X.(*ast.Ident); ok {
				if t.importManager.IsPackage(id.Name) {
					pkgName := id.Name
					if actual, ok := t.importManager.ResolveAlias(pkgName); ok {
						pkgName = actual
					}
					fullName := pkgName + "." + sel.Sel.Name
					if fMeta, ok := t.functions[fullName]; ok {
						// Substitute explicit type arguments if provided
						if len(typeArgs) > 0 && len(fMeta.TypeParams) > 0 {
							return t.substituteConcreteTypes(fMeta.ReturnType, fMeta.TypeParams, typeArgs)
						}
						// Try to infer type parameters from arguments
						if len(fMeta.TypeParams) > 0 {
							inferredTypeArgs := t.inferFuncTypeParamsFromArgs(fMeta, e.Args)
							if len(inferredTypeArgs) > 0 {
								return t.substituteConcreteTypes(fMeta.ReturnType, fMeta.TypeParams, inferredTypeArgs)
							}
						}
						return fMeta.ReturnType
					}
					// Check Go type info (stdlib, local Go files, third-party)
					if retType := t.getGoFuncReturnType(fullName); retType != nil {
						return retType
					}
					// Handle Receiver_Method (e.g., std.Some_Apply, std.Try_FlatMap)
					// Special handling for Some_Apply to infer type parameter from argument
					if sel.Sel.Name == transpiler.FuncSome+"_Apply" && len(e.Args) >= 2 {
						argType := t.getExprTypeNameManual(e.Args[1])
						if !argType.IsNil() && !argType.IsAny() {
							return transpiler.GenericType{
								Base:   transpiler.NamedType{Package: registry.StdPackageName, Name: transpiler.TypeOption},
								Params: []transpiler.Type{argType},
							}
						}
					}
					// Try all possible underscore split points to find valid type + method
					for offset := strings.Index(sel.Sel.Name, "_"); offset != -1; {
						receiverType := pkgName + "." + sel.Sel.Name[:offset]
						methodName := sel.Sel.Name[offset+1:]
						if result := t.resolveMethodCallType(receiverType, methodName, typeArgs, e.Args, 0); !result.IsNil() {
							return result
						}
						// Try next underscore position
						next := strings.Index(sel.Sel.Name[offset+1:], "_")
						if next == -1 {
							break
						}
						offset = offset + 1 + next
					}
					if _, ok := t.structFields[fullName]; ok {
						return transpiler.NamedType{Package: pkgName, Name: sel.Sel.Name}
					}
				} else {
					// For external Go packages not in t.imports, check Go type info
					fullName := id.Name + "." + sel.Sel.Name
					if retType := t.getGoFuncReturnType(fullName); retType != nil {
						return retType
					}
				}
			}

			xType := t.getExprTypeNameManual(sel.X)
			xTypeName := xType.String()
			if !xType.IsNil() {
				// Try exact type name first (non-generic types)
				if result := t.resolveMethodCallType(xTypeName, sel.Sel.Name, typeArgs, e.Args, -1); !result.IsNil() {
					return result
				}
				// Unwrap pointer types to get to the underlying type for method lookup
				// e.g., for *Array[int].Find(), unwrap to Array[int]
				underlyingType := xType
				if ptr, ok := xType.(transpiler.PointerType); ok {
					underlyingType = ptr.Elem
				}
				// Fallback: try base type name for generic types
				// e.g., for Pair[int, string].Swap(), try looking up Pair
				if genType, ok := underlyingType.(transpiler.GenericType); ok {
					baseTypeName := genType.Base.String()
					if result := t.resolveMethodCallTypeWithParams(baseTypeName, sel.Sel.Name, typeArgs, e.Args, -1, genType.Params); !result.IsNil() {
						return result
					}
				}
				// Fallback: try Go type info for method calls on Go types
				// e.g., scanner.Text() -> string, req.Header.Set() -> void
				if retType := t.getGoMethodReturnType(xTypeName, sel.Sel.Name); retType != nil {
					return retType
				}
			}

			if isStdQualified {
				if parentType := t.resolveStdConstructorParentType(sel.Sel.Name, false); parentType != "" {
					baseType := transpiler.NamedType{Package: registry.StdPackageName, Name: parentType}
					if len(typeArgs) > 0 {
						return transpiler.GenericType{Base: baseType, Params: typeArgs}
					}
					// For Left_Apply/Right_Apply, infer type parameters from the first argument (the type hint)
					// Left_Apply(std.Left[int, string]{}, value) -> Either[int, string]
					if (sel.Sel.Name == transpiler.FuncLeft+"_Apply" || sel.Sel.Name == transpiler.FuncRight+"_Apply") && len(e.Args) >= 1 {
						firstArgType := t.getExprTypeNameManual(e.Args[0])
						if genType, ok := firstArgType.(transpiler.GenericType); ok && len(genType.Params) > 0 {
							return transpiler.GenericType{Base: baseType, Params: genType.Params}
						}
					}
					// For Some_Apply, infer the type parameter from the second argument (the value)
					// Some_Apply(std.Some{}, value) -> Option[typeof(value)]
					if sel.Sel.Name == transpiler.FuncSome+"_Apply" && len(e.Args) >= 2 {
						argType := t.getExprTypeNameManual(e.Args[1])
						if !argType.IsNil() && !argType.IsAny() {
							return transpiler.GenericType{Base: baseType, Params: []transpiler.Type{argType}}
						}
					}
					return baseType
				}
			}
			if _, ok := t.structFields[sel.Sel.Name]; ok {
				return transpiler.BasicType{Name: sel.Sel.Name}
			}
		}
		if id, ok := fun.(*ast.Ident); ok {
			if id.Name == transpiler.FuncNewImmutable || id.Name == transpiler.TypeImmutable {
				if len(e.Args) > 0 {
					innerType := t.getExprTypeNameManual(e.Args[0])
					if t.isImmutableType(innerType) {
						panic(galaerr.NewSemanticError("recursive Immutable wrapping is not allowed"))
					}
					return transpiler.GenericType{
						Base:   transpiler.NamedType{Package: registry.StdPackageName, Name: transpiler.TypeImmutable},
						Params: []transpiler.Type{innerType},
					}
				}
			}
			if parentType := t.resolveStdConstructorParentType(id.Name, false); parentType != "" {
				baseType := transpiler.NamedType{Package: registry.StdPackageName, Name: parentType}
				if len(typeArgs) > 0 {
					return transpiler.GenericType{Base: baseType, Params: typeArgs}
				}
				// For Option_* methods without explicit type args, don't return early.
				// Fall through to Receiver_Method handling below to infer type params.
				// For all other std constructors/prefixes, return the base type directly.
				if parentType != transpiler.TypeOption {
					return baseType
				}
			}
			if id.Name == "len" {
				return transpiler.BasicType{Name: "int"}
			}
			// Handle go_interop.SliceOf[T](elements ...T) []T
			// SliceOf is commonly used with dot imports, infer element type from arguments
			if id.Name == "SliceOf" && len(e.Args) > 0 {
				elemType := t.getExprTypeNameManual(e.Args[0])
				if !elemType.IsNil() {
					return transpiler.ArrayType{Elem: elemType}
				}
			}
			// Handle type conversions like uint32(x), int64(y), string(z)
			// When a primitive type name is used as a function call, it's a type conversion
			if isPrimitiveType(id.Name) {
				return transpiler.BasicType{Name: id.Name}
			}
			if _, ok := t.structFields[id.Name]; ok {
				return transpiler.BasicType{Name: id.Name}
			}
			if fMeta := t.getFunction(id.Name); fMeta != nil {
				// Substitute type arguments if the function is generic
				if len(typeArgs) > 0 && len(fMeta.TypeParams) > 0 {
					return t.substituteConcreteTypes(fMeta.ReturnType, fMeta.TypeParams, typeArgs)
				}
				// Try to infer type parameters from arguments
				if len(fMeta.TypeParams) > 0 {
					inferredTypeArgs := t.inferFuncTypeParamsFromArgs(fMeta, e.Args)
					if len(inferredTypeArgs) > 0 {
						return t.substituteConcreteTypes(fMeta.ReturnType, fMeta.TypeParams, inferredTypeArgs)
					}
				}
				return fMeta.ReturnType
			}

			// Handle calling a variable of function type (e.g., thunk() where thunk is func() Stream[T])
			varType := t.getType(id.Name)
			if !varType.IsNil() {
				if funcType, ok := varType.(transpiler.FuncType); ok && len(funcType.Results) > 0 {
					return funcType.Results[0]
				}
				// If the variable has a named type (e.g., Handler), check if it's a type alias
				// for a function type and use the underlying function type's return type.
				typeName := varType.BaseName()
				// Try full name first (e.g., "Handler"), then strip package prefix
				// (e.g., "server.Handler" -> "Handler") since type aliases from siblings
				// are stored by simple name.
				aliasKey := typeName
				if _, ok := t.typeAliases[aliasKey]; !ok {
					if dotIdx := strings.LastIndex(typeName, "."); dotIdx != -1 {
						aliasKey = typeName[dotIdx+1:]
					}
				}
				if underlyingType, ok := t.typeAliases[aliasKey]; ok {
					if funcType, ok := underlyingType.(transpiler.FuncType); ok && len(funcType.Results) > 0 {
						t.traceType(e, funcType.Results[0], "type-alias-call")
						return funcType.Results[0]
					}
				}
			}

			// Handle generic methods transformed to standalone functions: Receiver_Method
			// e.g., Array_Zip[string](nums.Get(), strs.Get())
			// The first argument is the receiver (nums.Get() -> Array[int])
			// typeArgs are the method's explicit type arguments ([string])
			// Try all possible underscore split points to find valid type + method
			for offset := strings.Index(id.Name, "_"); offset != -1; {
				receiverType := id.Name[:offset]
				methodName := id.Name[offset+1:]
				resolvedRecvType := t.getType(receiverType)
				resolvedRecvTypeName := resolvedRecvType.String()
				if resolvedRecvType.IsNil() {
					resolvedRecvTypeName = receiverType
				}
				if result := t.resolveMethodCallType(resolvedRecvTypeName, methodName, typeArgs, e.Args, 0); !result.IsNil() {
					return result
				}
				// Try next underscore position
				next := strings.Index(id.Name[offset+1:], "_")
				if next == -1 {
					break
				}
				offset = offset + 1 + next
			}
		}
		// Handle chained function calls: when fun is itself a call expression
		// e.g., handler.Get()() where handler.Get() returns a function type
		// Resolve the type of the fun expression; if it's a FuncType, return its result type.
		if _, alreadySel := fun.(*ast.SelectorExpr); !alreadySel {
			if _, alreadyIdent := fun.(*ast.Ident); !alreadyIdent {
				funType := t.getExprTypeNameManual(e.Fun)
				if funcType, ok := funType.(transpiler.FuncType); ok && len(funcType.Results) > 0 {
					return funcType.Results[0]
				}
			}
		}
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
func (t *galaASTTransformer) inferFuncTypeParamsFromArgs(fMeta *transpiler.FunctionMetadata, args []ast.Expr) []transpiler.Type {
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

	// Check if both are function types
	patternFunc, patternIsFunc := pattern.(transpiler.FuncType)
	concreteFunc, concreteIsFunc := concrete.(transpiler.FuncType)
	if patternIsFunc && concreteIsFunc {
		// Try to unify result types
		// This handles cases like func(T) Try[U] with func(User) Try[User]
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
func (t *galaASTTransformer) substituteTranspilerTypeParams(typ transpiler.Type, subst map[string]string) transpiler.Type {
	if typ == nil || typ.IsNil() || len(subst) == 0 {
		return typ
	}
	switch ty := typ.(type) {
	case transpiler.BasicType:
		if replacement, ok := subst[ty.Name]; ok {
			return transpiler.ParseType(replacement)
		}
		return ty
	case transpiler.NamedType:
		// Check if the full name or just the Name needs substitution
		if replacement, ok := subst[ty.Name]; ok {
			return transpiler.ParseType(replacement)
		}
		return ty
	case transpiler.PointerType:
		return transpiler.PointerType{Elem: t.substituteTranspilerTypeParams(ty.Elem, subst)}
	case transpiler.ArrayType:
		return transpiler.ArrayType{Elem: t.substituteTranspilerTypeParams(ty.Elem, subst)}
	case transpiler.GenericType:
		newParams := make([]transpiler.Type, len(ty.Params))
		for i, p := range ty.Params {
			newParams[i] = t.substituteTranspilerTypeParams(p, subst)
		}
		return transpiler.GenericType{Base: t.substituteTranspilerTypeParams(ty.Base, subst), Params: newParams}
	case transpiler.FuncType:
		newParams := make([]transpiler.Type, len(ty.Params))
		for i, p := range ty.Params {
			newParams[i] = t.substituteTranspilerTypeParams(p, subst)
		}
		newResults := make([]transpiler.Type, len(ty.Results))
		for i, r := range ty.Results {
			newResults[i] = t.substituteTranspilerTypeParams(r, subst)
		}
		return transpiler.FuncType{Params: newParams, Results: newResults}
	case transpiler.MapType:
		return transpiler.MapType{
			Key:  t.substituteTranspilerTypeParams(ty.Key, subst),
			Elem: t.substituteTranspilerTypeParams(ty.Elem, subst),
		}
	}
	return typ
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
