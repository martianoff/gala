package transformer

import (
	"go/ast"
	"go/token"
	"strings"

	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/registry"
)

// inferSelectorExprType infers the type of a selector expression (e.g., x.Field, pkg.Name).
// Extracted from getExprTypeNameManualUncached for readability.
func (t *galaASTTransformer) inferSelectorExprType(e *ast.SelectorExpr) transpiler.Type {
	xType := t.getExprTypeNameManual(e.X)
	xTypeName := xType.String()
	// Extract base type name (strip generic parameters + pointer prefix)
	baseTypeName := stripTypeNameDecorations(xTypeName)
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
		if fType := t.getGoFieldType(xTypeName, e.Sel.Name); !fType.IsNil() {
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
			// A bare reference to a function in another GALA package
			// (e.g. `helper.Shout` passed to Array.Map). The same-package
			// form is resolved from FunctionMetadata in the *ast.Ident case
			// of getExprTypeNameManualUncached; do the same here from the
			// imported package's metadata so the reference carries a
			// FuncType. Without it the reference resolves to a NamedType and
			// unification cannot bind the consuming method's type parameter,
			// leaving the method type-param sentinel in the emitted Go.
			// Generic functions are excluded for the same reason as the
			// Ident case: they need instantiation, not a raw signature whose
			// type variables would leak.
			if fm, ok := t.functions[qualName]; ok && fm != nil && len(fm.TypeParams) == 0 {
				return t.funcMetaToRawType(fm)
			}
			if t.goTypeInfo != nil {
				if constType, ok := t.goTypeInfo.Constants[qualName]; ok && constType != nil {
					return constType
				}
				if varType, ok := t.goTypeInfo.Variables[qualName]; ok && varType != nil {
					return varType
				}
				// Check if this is a Go function reference (e.g., os.TempDir without parens).
				// Return a FuncType so it can unify with expected func() T parameters.
				if sig := t.goTypeInfo.GetFuncSignature(qualName); sig != nil {
					var params []transpiler.Type
					for _, p := range sig.Params {
						params = append(params, p.Type)
					}
					return transpiler.FuncType{Params: params, Results: sig.Returns}
				}
			}
			return transpiler.NamedType{Package: pkgName, Name: e.Sel.Name}
		}
	}
	return transpiler.NilType{}
}

// inferCallExprType infers the return type of a call expression.
// Extracted from getExprTypeNameManualUncached for readability.
func (t *galaASTTransformer) inferCallExprType(e *ast.CallExpr) transpiler.Type {
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
		if result := t.inferCallSelectorType(e, sel, typeArgs); result != nil && !result.IsNil() {
			return result
		}
	}
	if id, ok := fun.(*ast.Ident); ok {
		if result := t.inferCallIdentType(e, id, typeArgs); result != nil && !result.IsNil() {
			return result
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
			// If the result is a named type (e.g., Filter, Handler), resolve through
			// type aliases to check if it's a function type alias.
			// This handles chained calls like Logger()(req, handler) where Logger()
			// returns Filter which is func(Request, Handler) Future[Response].
			if !funType.IsNil() {
				aliasKey := funType.BaseName()
				if _, ok := t.typeAliases[aliasKey]; !ok {
					if dotIdx := strings.LastIndex(aliasKey, "."); dotIdx != -1 {
						aliasKey = aliasKey[dotIdx+1:]
					}
				}
				if underlyingType, ok := t.typeAliases[aliasKey]; ok {
					if funcType, ok := underlyingType.(transpiler.FuncType); ok && len(funcType.Results) > 0 {
						t.traceType(e, funcType.Results[0], "chained-call-type-alias")
						return funcType.Results[0]
					}
				}
			}
		}
	}
	return transpiler.NilType{}
}

// inferCallSelectorType handles type inference for call expressions with selector function (e.g., x.Method()).
func (t *galaASTTransformer) inferCallSelectorType(e *ast.CallExpr, sel *ast.SelectorExpr, typeArgs []transpiler.Type) transpiler.Type {
	// GALA's Println/Print are rewritten to fmt.Println/fmt.Print.
	// Treat them as void — the Go (int, error) return is an implementation detail.
	if pkgId, ok := sel.X.(*ast.Ident); ok && pkgId.Name == "fmt" {
		switch sel.Sel.Name {
		case "Println", "Print":
			return transpiler.VoidType{}
		}
	}

	// The string form of the `.Size()` sugar lowers to
	// `utf8.RuneCountInString(...)` (returns int). Like the `len(...)` rule in
	// inferCallIdentType, downstream inference over the emitted node — e.g. the
	// common-result-type of an if-expression whose branches are `s.Size()` —
	// must type it as int rather than NilType (the auto-injected `unicode/utf8`
	// import is not analyzed by goTypeInfo, so it does not resolve otherwise).
	if pkgId, ok := sel.X.(*ast.Ident); ok && pkgId.Name == "utf8" && sel.Sel.Name == "RuneCountInString" {
		return transpiler.BasicType{Name: "int"}
	}

	// Size()/ByteSize() sugar on Go primitives (string/slice/map) lowers to
	// len()/utf8.RuneCountInString(), both of which return int. Only claim the
	// result type when the receiver is actually a Go string/slice/map; GALA
	// collections have their own Size() method and must fall through to their
	// declared return type.
	if len(e.Args) == 0 && (sel.Sel.Name == "Size" || sel.Sel.Name == "ByteSize") {
		recvType := t.sizeSugarReceiverType(sel.X)
		if basic, ok := recvType.(transpiler.BasicType); ok && basic.Name == "string" {
			return transpiler.BasicType{Name: "int"}
		}
		if sel.Sel.Name == "Size" {
			switch recvType.(type) {
			case transpiler.ArrayType, transpiler.MapType:
				return transpiler.BasicType{Name: "int"}
			}
		}
	}

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
		// .Get() handler is always definitive — it always returns a result
		return t.inferGetMethodType(e, sel)
	}

	if sel.Sel.Name == transpiler.FuncNewImmutable || sel.Sel.Name == transpiler.TypeImmutable {
		if len(e.Args) > 0 {
			innerType := t.getExprTypeNameManual(e.Args[0])
			if t.isImmutableType(innerType) {
				t.raiseSemanticError("recursive Immutable wrapping is not allowed")
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
	// This ensures Left_Apply[int, string] uses [int, string] instead of [A, B] from metadata.
	//
	// This shortcut is only valid for *constructor* calls, where the explicit
	// type args ARE the parent type's params (e.g. `Left_Apply[int, string]` ->
	// `Either[int, string]`, `Some[int]` -> `Option[int]`). It must NOT fire for
	// monomorphized generic *method* calls like `Try_FlatMap[U, T]`: there the
	// type-arg list is `[methodU, receiverT]`, not the parent's params, so
	// splicing it as `Try[U, T]` over-arity'd the parent (`Try[int, int]`) and
	// the next chained call then leaked the extra arg. Generic methods fall
	// through to the Receiver_Method resolver below, which substitutes the
	// method's type args into its declared return type (`Try[U]`).
	if isStdQualified && len(typeArgs) > 0 && t.stdCallTypeArgsAreParentParams(sel.Sel.Name) {
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
				retType := fMeta.ReturnType
				// Substitute explicit type arguments if provided
				if len(typeArgs) > 0 && len(fMeta.TypeParams) > 0 {
					retType = t.substituteConcreteTypes(fMeta.ReturnType, fMeta.TypeParams, typeArgs)
				} else if len(fMeta.TypeParams) > 0 {
					// Try to infer type parameters from arguments
					inferredTypeArgs := t.inferFuncTypeParamsFromArgs(fMeta, e.Args, e.Ellipsis != token.NoPos)
					if len(inferredTypeArgs) > 0 {
						retType = t.substituteConcreteTypes(fMeta.ReturnType, fMeta.TypeParams, inferredTypeArgs)
					} else if t.currentFuncReturnType != nil && !t.currentFuncReturnType.IsNil() && t.hasTypeParams(retType) {
						// When argument-based inference fails (e.g., in multi-file batch mode
						// where argument types can't be resolved), try to infer type params by unifying
						// the function's return type pattern with the enclosing function's return type.
						// Example: When[T](found, v) returns Option[T], enclosing returns Option[string]
						//          -> unify Option[T] with Option[string] -> T=string -> Option[string]
						inferredMap := make(map[string]transpiler.Type)
						t.unifyForInference(retType, t.currentFuncReturnType, fMeta.TypeParams, inferredMap)
						if len(inferredMap) > 0 {
							fallbackArgs := make([]transpiler.Type, len(fMeta.TypeParams))
							allResolved := true
							for i, tp := range fMeta.TypeParams {
								if inferred, ok := inferredMap[tp]; ok {
									fallbackArgs[i] = inferred
								} else {
									allResolved = false
									break
								}
							}
							if allResolved {
								retType = t.substituteConcreteTypes(fMeta.ReturnType, fMeta.TypeParams, fallbackArgs)
							}
						}
					}
				}
				if retType == nil {
					return transpiler.NilType{}
				}
				return retType
			}
			// Check Go type info (stdlib, local Go files, third-party)
			if retType := t.getGoFuncReturnTypeForCall(fullName, e, typeArgs); !retType.IsNil() {
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
			if retType := t.getGoFuncReturnTypeForCall(fullName, e, typeArgs); !retType.IsNil() {
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
		wasPointer := false
		if ptr, ok := xType.(transpiler.PointerType); ok {
			underlyingType = ptr.Elem
			wasPointer = true
		}
		// Fallback: try base type name for generic types
		// e.g., for Pair[int, string].Swap(), try looking up Pair
		if genType, ok := underlyingType.(transpiler.GenericType); ok {
			baseTypeName := genType.Base.String()
			if result := t.resolveMethodCallTypeWithParams(baseTypeName, sel.Sel.Name, typeArgs, e.Args, -1, genType.Params); !result.IsNil() {
				return result
			}
		} else if wasPointer {
			// Fallback: for pointer to non-generic type, retry method lookup
			// against the unwrapped type name. Methods defined with pointer
			// receivers are registered under the base type name, not the
			// pointer type, so *Buffer.StyleAt → Buffer.StyleAt lookup.
			if result := t.resolveMethodCallType(underlyingType.String(), sel.Sel.Name, typeArgs, e.Args, -1); !result.IsNil() {
				return result
			}
		}
		// Fallback: try Go type info for method calls on Go types
		// e.g., scanner.Text() -> string, req.Header.Set() -> void
		if retType := t.getGoMethodReturnType(xTypeName, sel.Sel.Name); !retType.IsNil() {
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
	return transpiler.NilType{}
}

// inferCallIdentType handles type inference for call expressions with identifier function (e.g., funcName()).
func (t *galaASTTransformer) inferCallIdentType(e *ast.CallExpr, id *ast.Ident, typeArgs []transpiler.Type) transpiler.Type {
	if id.Name == transpiler.FuncNewImmutable || id.Name == transpiler.TypeImmutable {
		if len(e.Args) > 0 {
			innerType := t.getExprTypeNameManual(e.Args[0])
			if t.isImmutableType(innerType) {
				t.raiseSemanticError("recursive Immutable wrapping is not allowed")
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
		// `Type_Method` shaped names (e.g. `Try_Map`, `Option_FlatMap`) refer to
		// methods on a parent type whose return type carries the method's own
		// type-arg shape (e.g. `Map[U]` returns `Try[U]`). Returning the bare
		// parent named type here would drop the method's type argument and
		// produce uninstantiated `Try` / `Option` in the generated Go. Fall
		// through to the Receiver_Method resolver below, which substitutes
		// the inferred U into the method's declared return type. The early
		// return remains correct for direct constructor names that match the
		// parent type itself (e.g. `Left`, `Right` -> Either).
		if !strings.Contains(id.Name, "_") {
			return baseType
		}
	}
	// `len(...)` is inferred as int. Bare `len` in GALA *source* is a hard error
	// (checkForbiddenGoBuiltinCall, applied at transform time), but the
	// `.Size()`/`.ByteSize()` sugar EMITS `len(...)` as generated Go AST, and
	// downstream type inference — the common-result-type of an if-expression,
	// a binary operand like `xs.Size() - 1`, etc. — runs over that emitted node
	// via inferResultType. Without this rule the emitted `len(...)` resolves to
	// NilType and poisons the surrounding expression's type to `any`.
	if id.Name == "len" {
		return transpiler.BasicType{Name: "int"}
	}
	// Handle go_interop.SliceOf[T](elements ...T) []T
	// SliceOf is commonly used with dot imports, infer element type from arguments.
	// When explicit type args are provided (e.g., SliceOf[byte](...)),
	// use them instead of inferring from argument types. Without this fix,
	// SliceOf[byte](72, 101) would infer []int (from the int literal 72)
	// instead of []byte, causing downstream type errors like Array[int] instead of Array[byte].
	if id.Name == "SliceOf" {
		// Use explicit type argument if provided (e.g., SliceOf[byte](...))
		if len(typeArgs) > 0 {
			return transpiler.ArrayType{Elem: typeArgs[0]}
		}
		// Fall back to inferring from first argument
		if len(e.Args) > 0 {
			elemType := t.getExprTypeNameManual(e.Args[0])
			if !elemType.IsNil() {
				return transpiler.ArrayType{Elem: elemType}
			}
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
		retType := fMeta.ReturnType
		// Substitute type arguments if the function is generic
		if len(typeArgs) > 0 && len(fMeta.TypeParams) > 0 {
			retType = t.substituteConcreteTypes(fMeta.ReturnType, fMeta.TypeParams, typeArgs)
		} else if len(fMeta.TypeParams) > 0 {
			// Try to infer type parameters from arguments
			inferredTypeArgs := t.inferFuncTypeParamsFromArgs(fMeta, e.Args, e.Ellipsis != token.NoPos)
			if len(inferredTypeArgs) > 0 {
				retType = t.substituteConcreteTypes(fMeta.ReturnType, fMeta.TypeParams, inferredTypeArgs)
			} else if t.currentFuncReturnType != nil && !t.currentFuncReturnType.IsNil() && t.hasTypeParams(retType) {
				// Fallback — unify return type with enclosing function's return type
				inferredMap := make(map[string]transpiler.Type)
				t.unifyForInference(retType, t.currentFuncReturnType, fMeta.TypeParams, inferredMap)
				if len(inferredMap) > 0 {
					fallbackArgs := make([]transpiler.Type, len(fMeta.TypeParams))
					allResolved := true
					for i, tp := range fMeta.TypeParams {
						if inferred, ok := inferredMap[tp]; ok {
							fallbackArgs[i] = inferred
						} else {
							allResolved = false
							break
						}
					}
					if allResolved {
						retType = t.substituteConcreteTypes(fMeta.ReturnType, fMeta.TypeParams, fallbackArgs)
					}
				}
			}
		}
		if retType == nil {
			return transpiler.NilType{}
		}
		return retType
	}

	// Handle calling a variable of function type (e.g., thunk() where thunk is func() Stream[T])
	varType := t.getType(id.Name)
	if !varType.IsNil() {
		if funcType, ok := varType.(transpiler.FuncType); ok {
			if len(funcType.Results) > 0 {
				return funcType.Results[0]
			}
			// Void function (no return type) — e.g., func()
			return transpiler.VoidType{}
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
			if funcType, ok := underlyingType.(transpiler.FuncType); ok {
				if len(funcType.Results) > 0 {
					t.traceType(e, funcType.Results[0], "type-alias-call")
					return funcType.Results[0]
				}
				// Void function alias (no return type) — e.g., type Callback func()
				return transpiler.VoidType{}
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

	// Fallback: bare identifier that may name a Go-declared function in the
	// current package (e.g., `Make` from a hand-written `event.go` next to
	// `pipe.gala`). The analyzer registers same-directory Go functions in
	// goTypeInfo as `pkgName.FuncName`; resolve here via the current package
	// qualifier so chained generic-method type inference (ArrayFromSlice ->
	// FoldLeft lambda param type) sees a concrete element type instead of
	// the un-substituted type-parameter name.
	if t.packageName != "" {
		if retType := t.getGoFuncReturnTypeForCall(t.packageName+"."+id.Name, e, typeArgs); !retType.IsNil() {
			return retType
		}
	}

	// Fallback: a bare identifier naming a dot-imported Go function
	// (e.g. `ToRunes` from `import . "martianoff/gala/go_interop"`, which
	// returns `[]rune`). Resolve its return type through goTypeInfo so it
	// yields a concrete ArrayType/MapType/BasicType rather than NilType.
	// Without this, downstream consumers that key off the receiver type —
	// notably the `.Size()`/`.ByteSize()` sugar (via sizeSugarReceiverType)
	// and val-type inference for `val r = ToRunes(...)` — cannot fire and
	// fall back to a raw Go slice with no `.Size()` method. Mirrors the
	// dot-import resolution already done for parameter types in
	// resolveGoFuncParamTypes. `std` is dot-imported too but its functions
	// are GALA (resolved above via getFunction), so a `std.<name>` miss here
	// is harmless.
	for _, entry := range t.importManager.dotImports {
		if retType := t.getGoFuncReturnTypeForCall(entry.PkgName+"."+id.Name, e, typeArgs); !retType.IsNil() {
			return retType
		}
	}
	return transpiler.NilType{}
}

// inferGetMethodType handles type inference for .Get() calls, which have special semantics
// for vals (unwrapping Immutable), immutable struct fields, and generic types.
func (t *galaASTTransformer) inferGetMethodType(e *ast.CallExpr, sel *ast.SelectorExpr) transpiler.Type {
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
	if transpiler.IsUnusable(xType) {
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
	// For a non-generic named type that declares its own method (e.g. a user type
	// with a real `Get() Option[T]` accessor), return the method's declared return
	// type. Without this, the fallback below returns the receiver type itself,
	// erasing the real return type — which silently breaks downstream inference such
	// as a chained `Option.FlatMap`/`Map` on the result (it would be keyed off the
	// receiver type instead of Option, producing an undefined monomorphized helper).
	// The generic-type branches above already handle generic receivers with
	// substitution, so this only fills the non-generic named-type gap.
	if !transpiler.IsUnusable(xType) {
		if typeMeta := t.getTypeMeta(xBaseName); typeMeta != nil {
			if methodMeta, ok := typeMeta.Methods[sel.Sel.Name]; ok {
				return methodMeta.ReturnType
			}
		}
	}
	if xType == nil {
		return transpiler.NilType{}
	}
	return xType
}
