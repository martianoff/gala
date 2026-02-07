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
		return t.exprToType(e)
	case *ast.IndexListExpr:
		// Handle generic type expression like Tuple[int, string]
		return t.exprToType(e)
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
		// It might be a package-qualified name
		if x, ok := e.X.(*ast.Ident); ok {
			if t.importManager.IsPackage(x.Name) {
				pkgName := x.Name
				if actual, ok := t.importManager.ResolveAlias(pkgName); ok {
					pkgName = actual
				}
				return transpiler.NamedType{Package: pkgName, Name: e.Sel.Name}
			}
		}
	case *ast.CallExpr:
		// Handle IIFE (used by if/match expressions)
		if fl, ok := e.Fun.(*ast.FuncLit); ok {
			if fl.Type != nil && fl.Type.Results != nil && len(fl.Type.Results.List) > 0 {
				return t.exprToType(fl.Type.Results.List[0].Type)
			}
		}

		// Handle b.Get() or std.Some()
		// Capture type arguments from generic calls like Tuple[int, string](...)
		fun := e.Fun
		var typeArgs []transpiler.Type
		if idx, ok := fun.(*ast.IndexExpr); ok {
			fun = idx.X
			typeArgs = []transpiler.Type{t.exprToType(idx.Index)}
		} else if idxList, ok := fun.(*ast.IndexListExpr); ok {
			fun = idxList.X
			for _, idx := range idxList.Indices {
				typeArgs = append(typeArgs, t.exprToType(idx))
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
									litTypeArgs = []transpiler.Type{t.exprToType(idx.Index)}
								} else if idxList, ok := compLit.Type.(*ast.IndexListExpr); ok {
									for _, idxExpr := range idxList.Indices {
										litTypeArgs = append(litTypeArgs, t.exprToType(idxExpr))
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
				if sel.Sel.Name == transpiler.FuncLeft || sel.Sel.Name == transpiler.FuncRight ||
					strings.HasPrefix(sel.Sel.Name, transpiler.FuncLeft+"_") || strings.HasPrefix(sel.Sel.Name, transpiler.FuncRight+"_") ||
					strings.HasPrefix(sel.Sel.Name, transpiler.TypeEither+"_") {
					return transpiler.GenericType{
						Base:   transpiler.NamedType{Package: registry.StdPackageName, Name: transpiler.TypeEither},
						Params: typeArgs,
					}
				}
				if sel.Sel.Name == transpiler.FuncSome || sel.Sel.Name == transpiler.FuncNone ||
					strings.HasPrefix(sel.Sel.Name, transpiler.FuncSome+"_") || strings.HasPrefix(sel.Sel.Name, transpiler.FuncNone+"_") ||
					strings.HasPrefix(sel.Sel.Name, transpiler.TypeOption+"_") {
					return transpiler.GenericType{
						Base:   transpiler.NamedType{Package: registry.StdPackageName, Name: transpiler.TypeOption},
						Params: typeArgs,
					}
				}
				if t.isTupleTypeName(sel.Sel.Name) || t.hasTupleTypePrefix(sel.Sel.Name) {
					tupleType := t.getTupleTypeFromName(sel.Sel.Name)
					return transpiler.GenericType{
						Base:   transpiler.NamedType{Package: registry.StdPackageName, Name: tupleType},
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
					// Check for known Go stdlib functions (e.g., fmt.Sprintf -> string)
					if retType := getKnownGoStdlibReturnType(fullName); retType != nil {
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
						if typeMeta := t.getTypeMeta(receiverType); typeMeta != nil {
							if methodMeta, ok := typeMeta.Methods[methodName]; ok {
								// For Receiver_Method calls, the first arg is the receiver
								// Get the receiver's type to substitute struct-level type params
								result := methodMeta.ReturnType
								if len(e.Args) > 0 {
									receiverArgType := t.getExprTypeNameManual(e.Args[0])
									if genRecv, ok := receiverArgType.(transpiler.GenericType); ok {
										// Substitute struct-level type params (e.g., T -> User for Try[User])
										result = t.substituteConcreteTypes(result, typeMeta.TypeParams, genRecv.Params)
										// For methods with their own type params (e.g., FlatMap[U])
										// Try to infer them from the other arguments
										if len(methodMeta.TypeParams) > 0 && len(typeArgs) == 0 {
											// Arguments after the receiver are the method's regular params
											methodArgs := e.Args[1:]
											inferredTypeArgs := t.inferMethodTypeParamsFromArgs(methodMeta, methodArgs, typeMeta.TypeParams, genRecv.Params)
											if len(inferredTypeArgs) > 0 {
												result = t.substituteConcreteTypes(result, methodMeta.TypeParams, inferredTypeArgs)
											}
										} else if len(typeArgs) > 0 {
											result = t.substituteConcreteTypes(result, methodMeta.TypeParams, typeArgs)
										}
									}
								}
								return result
							}
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
					// For external Go packages not in t.imports, still check known stdlib functions
					fullName := id.Name + "." + sel.Sel.Name
					if retType := getKnownGoStdlibReturnType(fullName); retType != nil {
						return retType
					}
				}
			}

			xType := t.getExprTypeNameManual(sel.X)
			xTypeName := xType.String()
			if !xType.IsNil() {
				if typeMeta := t.getTypeMeta(xTypeName); typeMeta != nil {
					if methodMeta, ok := typeMeta.Methods[sel.Sel.Name]; ok {
						return methodMeta.ReturnType
					}
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
					if typeMeta := t.getTypeMeta(baseTypeName); typeMeta != nil {
						if methodMeta, ok := typeMeta.Methods[sel.Sel.Name]; ok {
							// Substitute type parameters in return type
							// First, substitute struct-level type params (e.g., T -> int for Array[int])
							result := t.substituteConcreteTypes(methodMeta.ReturnType, typeMeta.TypeParams, genType.Params)
							// Then, substitute method-level type params (e.g., U -> string for Zip[string])
							if len(methodMeta.TypeParams) > 0 {
								if len(typeArgs) > 0 {
									result = t.substituteConcreteTypes(result, methodMeta.TypeParams, typeArgs)
								} else {
									// Try to infer method-level type params from function arguments
									// e.g., for FlatMap[U](f func(T) Try[U]), infer U from the lambda's return type
									inferredTypeArgs := t.inferMethodTypeParamsFromArgs(methodMeta, e.Args, typeMeta.TypeParams, genType.Params)
									if len(inferredTypeArgs) > 0 {
										result = t.substituteConcreteTypes(result, methodMeta.TypeParams, inferredTypeArgs)
									}
								}
							}
							return result
						}
					}
				}
			}

			if isStdQualified {
				if sel.Sel.Name == transpiler.FuncLeft || sel.Sel.Name == transpiler.FuncRight {
					baseType := transpiler.NamedType{Package: registry.StdPackageName, Name: transpiler.TypeEither}
					if len(typeArgs) > 0 {
						return transpiler.GenericType{Base: baseType, Params: typeArgs}
					}
					return baseType
				}
				if t.isTupleTypeName(sel.Sel.Name) {
					tupleType := t.getTupleTypeFromName(sel.Sel.Name)
					baseType := transpiler.NamedType{Package: registry.StdPackageName, Name: tupleType}
					if len(typeArgs) > 0 {
						return transpiler.GenericType{Base: baseType, Params: typeArgs}
					}
					return baseType
				}
				if strings.HasPrefix(sel.Sel.Name, transpiler.TypeEither+"_") || strings.HasPrefix(sel.Sel.Name, transpiler.FuncLeft+"_") || strings.HasPrefix(sel.Sel.Name, transpiler.FuncRight+"_") {
					baseType := transpiler.NamedType{Package: registry.StdPackageName, Name: transpiler.TypeEither}
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
					return baseType
				}
				if strings.HasPrefix(sel.Sel.Name, transpiler.TypeOption+"_") || strings.HasPrefix(sel.Sel.Name, transpiler.FuncSome+"_") || strings.HasPrefix(sel.Sel.Name, transpiler.FuncNone+"_") {
					// For Some_Apply, infer the type parameter from the second argument (the value)
					// Some_Apply(std.Some{}, value) -> Option[typeof(value)]
					if sel.Sel.Name == transpiler.FuncSome+"_Apply" && len(e.Args) >= 2 {
						argType := t.getExprTypeNameManual(e.Args[1])
						if !argType.IsNil() && !argType.IsAny() {
							return transpiler.GenericType{
								Base:   transpiler.NamedType{Package: registry.StdPackageName, Name: transpiler.TypeOption},
								Params: []transpiler.Type{argType},
							}
						}
					}
					return transpiler.NamedType{Package: registry.StdPackageName, Name: transpiler.TypeOption}
				}
				if t.hasTupleTypePrefix(sel.Sel.Name) {
					tupleType := t.getTupleTypeFromName(sel.Sel.Name)
					baseType := transpiler.NamedType{Package: registry.StdPackageName, Name: tupleType}
					if len(typeArgs) > 0 {
						return transpiler.GenericType{Base: baseType, Params: typeArgs}
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
			if id.Name == transpiler.FuncLeft || id.Name == transpiler.FuncRight {
				baseType := transpiler.NamedType{Package: registry.StdPackageName, Name: transpiler.TypeEither}
				if len(typeArgs) > 0 {
					return transpiler.GenericType{Base: baseType, Params: typeArgs}
				}
				return baseType
			}
			if t.isTupleTypeName(id.Name) {
				tupleType := t.getTupleTypeFromName(id.Name)
				baseType := transpiler.NamedType{Package: registry.StdPackageName, Name: tupleType}
				if len(typeArgs) > 0 {
					return transpiler.GenericType{Base: baseType, Params: typeArgs}
				}
				return baseType
			}
			if strings.HasPrefix(id.Name, transpiler.TypeEither+"_") || strings.HasPrefix(id.Name, transpiler.FuncLeft+"_") || strings.HasPrefix(id.Name, transpiler.FuncRight+"_") {
				baseType := transpiler.NamedType{Package: registry.StdPackageName, Name: transpiler.TypeEither}
				if len(typeArgs) > 0 {
					return transpiler.GenericType{Base: baseType, Params: typeArgs}
				}
				return baseType
			}
			// Handle Option_* methods - don't return early, let the Receiver_Method handling below infer type params
			// This only handles cases where we have explicit type args
			if strings.HasPrefix(id.Name, transpiler.TypeOption+"_") || strings.HasPrefix(id.Name, transpiler.FuncSome+"_") || strings.HasPrefix(id.Name, transpiler.FuncNone+"_") {
				baseType := transpiler.NamedType{Package: registry.StdPackageName, Name: transpiler.TypeOption}
				if len(typeArgs) > 0 {
					return transpiler.GenericType{Base: baseType, Params: typeArgs}
				}
				// Don't return early - fall through to Receiver_Method handling to infer type params
			}
			if t.hasTupleTypePrefix(id.Name) {
				tupleType := t.getTupleTypeFromName(id.Name)
				baseType := transpiler.NamedType{Package: registry.StdPackageName, Name: tupleType}
				if len(typeArgs) > 0 {
					return transpiler.GenericType{Base: baseType, Params: typeArgs}
				}
				return baseType
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
				if meta := t.getTypeMeta(resolvedRecvTypeName); meta != nil {
					if mMeta, ok := meta.Methods[methodName]; ok {
						result := mMeta.ReturnType
						// Substitute receiver's type params from first argument
						// e.g., Array_Zip[string](nums.Get(), ...) where nums.Get() is Array[int]
						// needs to substitute T -> int from the first arg's generic type
						var receiverTypeParams []transpiler.Type
						if len(e.Args) > 0 {
							firstArgType := t.getExprTypeNameManual(e.Args[0])
							if genType, ok := firstArgType.(transpiler.GenericType); ok && len(meta.TypeParams) > 0 {
								receiverTypeParams = genType.Params
								result = t.substituteConcreteTypes(result, meta.TypeParams, genType.Params)
							}
						}
						// Substitute method's type params from explicit type args
						// e.g., Array_Zip[string] needs to substitute U -> string
						if len(typeArgs) > 0 && len(mMeta.TypeParams) > 0 {
							result = t.substituteConcreteTypes(result, mMeta.TypeParams, typeArgs)
						} else if len(mMeta.TypeParams) > 0 && len(e.Args) > 1 {
							// Try to infer method-level type params from function arguments
							// e.g., for Option_Map(opt, func(v int) int {...}), infer U=int from lambda return type
							methodArgs := e.Args[1:] // Arguments after the receiver
							inferredTypeArgs := t.inferMethodTypeParamsFromArgs(mMeta, methodArgs, meta.TypeParams, receiverTypeParams)
							if len(inferredTypeArgs) > 0 {
								result = t.substituteConcreteTypes(result, mMeta.TypeParams, inferredTypeArgs)
							}
						}
						return result
					}
				}
				// Try next underscore position
				next := strings.Index(id.Name[offset+1:], "_")
				if next == -1 {
					break
				}
				offset = offset + 1 + next
			}
		}
	case *ast.FuncLit:
		// Handle lambda expressions - extract their function type
		if e.Type != nil {
			var params []transpiler.Type
			var results []transpiler.Type
			if e.Type.Params != nil {
				for _, field := range e.Type.Params.List {
					paramType := t.exprToType(field.Type)
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
					resultType := t.exprToType(field.Type)
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
		// Use exprToType to preserve generic type parameters
		typ := t.exprToType(e.Type)
		if !typ.IsNil() {
			return typ
		}
		typeName := t.getBaseTypeName(e.Type)
		return t.resolveType(typeName)
	}
	return transpiler.NilType{}
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

// knownGoStdlibReturnTypes maps common Go stdlib functions to their return types.
// This helps type inference for calls like fmt.Sprintf, strings.Join, etc.
var knownGoStdlibReturnTypes = map[string]transpiler.Type{
	// fmt package
	"fmt.Sprintf":  transpiler.BasicType{Name: "string"},
	"fmt.Sprint":   transpiler.BasicType{Name: "string"},
	"fmt.Sprintln": transpiler.BasicType{Name: "string"},
	// strings package
	"strings.Join":       transpiler.BasicType{Name: "string"},
	"strings.Replace":    transpiler.BasicType{Name: "string"},
	"strings.ReplaceAll": transpiler.BasicType{Name: "string"},
	"strings.ToLower":    transpiler.BasicType{Name: "string"},
	"strings.ToUpper":    transpiler.BasicType{Name: "string"},
	"strings.TrimSpace":  transpiler.BasicType{Name: "string"},
	"strings.Trim":       transpiler.BasicType{Name: "string"},
	"strings.TrimPrefix": transpiler.BasicType{Name: "string"},
	"strings.TrimSuffix": transpiler.BasicType{Name: "string"},
	"strings.Contains":   transpiler.BasicType{Name: "bool"},
	"strings.HasPrefix":  transpiler.BasicType{Name: "bool"},
	"strings.HasSuffix":  transpiler.BasicType{Name: "bool"},
	"strings.Index":      transpiler.BasicType{Name: "int"},
	"strings.Count":      transpiler.BasicType{Name: "int"},
	// strconv package
	"strconv.Itoa":       transpiler.BasicType{Name: "string"},
	"strconv.FormatInt":  transpiler.BasicType{Name: "string"},
	"strconv.FormatBool": transpiler.BasicType{Name: "string"},
	// math package
	"math.Abs":   transpiler.BasicType{Name: "float64"},
	"math.Max":   transpiler.BasicType{Name: "float64"},
	"math.Min":   transpiler.BasicType{Name: "float64"},
	"math.Floor": transpiler.BasicType{Name: "float64"},
	"math.Ceil":  transpiler.BasicType{Name: "float64"},
	"math.Round": transpiler.BasicType{Name: "float64"},
	"math.Sqrt":  transpiler.BasicType{Name: "float64"},
	// len is a builtin, but handle it if needed
}

// getKnownGoStdlibReturnType returns the return type for a known Go stdlib function.
func getKnownGoStdlibReturnType(fullName string) transpiler.Type {
	if retType, ok := knownGoStdlibReturnTypes[fullName]; ok {
		return retType
	}
	return nil
}
