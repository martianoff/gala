package transformer

import (
	"fmt"
	"go/ast"
	"go/token"
	"maps"
	"slices"
	"strings"

	"github.com/antlr4-go/antlr/v4"

	"martianoff/gala/galaerr"
	"martianoff/gala/internal/parser/grammar"
	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/registry"
)

// This file contains function/method call transformation logic extracted from expressions.go
// Functions: applyCallSuffix, transformCallWithArgsCtx, handleNamedArgsCall,
//            lowerArg, transformArgument, inferTypeArgsFromApply,
//            isGenericMethodName, isGenericMethodWithImports, isMethodGenericViaTypeMeta

// forbiddenGoBuiltinSuggestions maps each bare Go builtin that GALA forbids in
// source to an actionable replacement. Bare builtins are the last symbols that
// resolve with no import and no GALA declaration; forbidding them removes that
// implicit Go-leakage special case and steers authors toward GALA-native
// idioms or the sanctioned interop wrappers.
var forbiddenGoBuiltinSuggestions = map[string]string{
	"len":     "use `.Size()` (logical size — characters for strings) or `.ByteSize()` (raw bytes) instead of `len(...)`",
	"append":  "use `go_interop.SliceAppend` / `SliceAppendAll`, or build an `Array`/`List` from collection_immutable",
	"make":    "use `go_interop.SliceWithSize` / `SliceWithCapacity` / `MapEmpty`, or an empty `Array`/`HashMap`",
	"new":     "use `go_interop.New[T]()` for a pointer, or a zero value / `Option[T]`",
	"cap":     "use `go_interop.SliceCap(...)`",
	"copy":    "use `go_interop.SliceCopy(...)`, or copy an `Array`",
	"delete":  "use `go_interop.MapDelete(...)`, or `HashMap.Remove(...)`",
	"close":   "use `go_interop.CloseChan(...)` (or `CloseSignal(...)` for a signal channel)",
	"complex": "use `go_interop.Complex(...)`",
	"real":    "use `go_interop.Real(...)`",
	"imag":    "use `go_interop.Imag(...)`",
	"panic":   "use `go_builtins.Panic(...)` — or prefer `Option` / `Try` / `Either` for recoverable failure",
	"recover": "`recover` is not available on the GALA surface; `Try` captures panics — use `Try(() => ...)` / `TryApply`",
}

// ForbiddenGoBuiltins returns the set of bare Go builtins that GALA forbids on
// its surface (GALA-E0035), keyed by name. It is the exported view of
// forbiddenGoBuiltinSuggestions so downstream tooling — notably the LSP
// completion provider — can avoid suggesting a builtin the compiler will
// reject, without re-declaring a list that could drift out of sync.
func ForbiddenGoBuiltins() map[string]bool {
	out := make(map[string]bool, len(forbiddenGoBuiltinSuggestions))
	for name := range forbiddenGoBuiltinSuggestions {
		out[name] = true
	}
	return out
}

// checkForbiddenGoBuiltinCall rejects a call to a bare Go builtin as a hard
// error (GALA-E0035). It is resolver-aware: the name is only forbidden when it
// is a bare identifier that does NOT resolve to a user-defined function, a
// local binding (val/var/param), or a declared type/struct. This keeps
// user-defined functions that happen to share a builtin's name legal — e.g.
// `func delete(...)` (examples/kvstore.gala) and `func copy(...)`
// (examples/method_default_params.gala) — while forbidding the builtins
// themselves. A selector call (`x.copy()`) is never a bare builtin and is not
// checked. Returns nil when the call is allowed.
// checkForbiddenGoBuiltinCall's line/col identify the callee identifier's start.
// When exactStart is true the position is the real primary-expression token
// (via primaryStartOf), so the diagnostic can carry an EXACT span covering the
// whole identifier — the caret in the rich CLI renderer then underlines `len`
// itself rather than a single derived character. When exactStart is false the
// caller only had an approximate position, so the span is left for the renderer
// to derive.
func (t *galaASTTransformer) checkForbiddenGoBuiltinCall(fun ast.Expr, line, col int, exactStart bool) error {
	id, ok := fun.(*ast.Ident)
	if !ok {
		return nil
	}
	suggestion, isBuiltin := forbiddenGoBuiltinSuggestions[id.Name]
	if !isBuiltin {
		return nil
	}
	// Resolver-aware guards: a name that resolves to something the author
	// declared is that declaration, not the builtin.
	if t.getFunction(id.Name) != nil { // user-defined function (delete/copy stay legal)
		return nil
	}
	// A local val/var/param, a type in scope, or a declared type / companion.
	if !t.getType(id.Name).IsNil() || t.getTypeMeta(id.Name) != nil {
		return nil
	}
	if _, ok := t.structFields[id.Name]; ok { // struct layout used as a constructor
		return nil
	}
	err := galaerr.NewCodedSemanticError(
		galaerr.CodeForbiddenGoBuiltin,
		line, col,
		fmt.Sprintf("bare Go builtin %q is not part of GALA's surface", id.Name+"(...)"),
		suggestion,
	)
	if exactStart {
		// The callee is a single identifier token on one line, so its exact
		// end is the start column plus the identifier's rune length.
		err = err.WithSpan(col + len([]rune(id.Name)))
	}
	return err
}

// primaryStartOf walks up from an ANTLR node inside a postfix call chain to the
// enclosing postfixExpr and returns the start token of its primary expression —
// i.e. the callee identifier (`len` in `len(s)`). This is the position the
// forbidden-builtin diagnostic should point at, rather than the argument list.
func primaryStartOf(node antlr.Tree) (line, col int, ok bool) {
	for n := node; n != nil; n = n.GetParent() {
		if pe, isPE := n.(*grammar.PostfixExprContext); isPE {
			if prim := pe.PrimaryExpr(); prim != nil {
				tok := prim.GetStart()
				return tok.GetLine(), tok.GetColumn(), true
			}
			return 0, 0, false
		}
	}
	return 0, 0, false
}

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
		if isStructMetaIntrinsic(t.getBaseTypeName(base)) {
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
						baseExpr := base
						if idx, ok := base.(*ast.IndexExpr); ok {
							baseExpr = idx.X
						} else if idxList, ok := base.(*ast.IndexListExpr); ok {
							baseExpr = idxList.X
						}

						// The base must be a type, not a variable.
						if t.isTypeBaseExpr(base) {
							// Zero-argument Apply method: TypeName[T]{}.Apply()
							receiverType := base
							// Downward inference: a sealed-variant zero-arg constructor
							// (e.g. `NoCmd()`) inside a context that expects the parent
							// sealed type (`Cmd[int]`) needs explicit type args injected
							// onto the variant — without them Go cannot pin the
							// vestigial type parameter from an empty composite literal.
							// Consume the top expected-type hint set by enclosing val
							// declarations / argument transforms (B1). It is the type
							// of the slot the constructor itself fills, its only
							// source: `None()` passed for `d Option[Drag]` in a
							// function returning `Option[int]` is `None[Drag]`.
							if pending := t.expectedArgTypes.peek(); pending != nil && !pending.IsNil() {
								if rewritten, ok := t.injectSealedVariantTypeArgs(base, pending); ok {
									receiverType = rewritten
									t.expectedArgTypes.consume()
								} else if baseExpr == base && len(typeMeta.TypeParams) > 0 {
									// Any other companion takes them from the
									// slot as one called with arguments does:
									// `MkTag()` as a `Tagged[int]`.
									slotArgs := t.resultSlotTypeArgs(methodMeta.ReturnType, typeMeta.TypeParams, pending)
									if instantiated, missing := t.completeTypeArgs(base, typeMeta.TypeParams, nil, slotArgs); missing == nil {
										receiverType = instantiated
										t.expectedArgTypes.consume()
									}
								}
							}
							// B6 fail-loud: if the slot hint did not resolve the
							// generic parameter, emitting an untyped `Variant{}`
							// would produce an obscure Go error far from the GALA
							// source. Surface as GALA-E0018 with a hint pointing at
							// the signals that resolve it (an annotated binding, the
							// slot it fills). Limited to sealed variants
							// of generic parents written without explicit type args
							// (`baseExpr == base`); explicit `Variant[T]()` shapes
							// fall through. Another generic companion is reported
							// the same way, without the E0018 code.
							if receiverType == base && baseExpr == base && len(typeMeta.TypeParams) > 0 {
								// The zero-arg call path derives typeName from
								// getBaseTypeName, which keeps the package selector for
								// dot-imported / std variants, but metadata is keyed by
								// the bare case name — so the qualifier is split off and
								// passed as the lookup scope (findSealedParentForVariant
								// also resolves import aliases against it). Comparing the
								// qualified `std.None` directly never matched, so this
								// guard used to miss `std.None()` and the transpiler
								// emitted an uninstantiated `std.None{}.Apply()` (invalid
								// Go) instead. The resolved parent is both the guard and
								// the hint's source for the annotation example, so it is
								// looked up once here rather than twice.
								if variant := t.sealedVariantOf(typeName); variant.parent != nil {
									return nil, t.uninferredVariantError(variant, "()", nil, nil, suffix.GetStart().GetLine(), suffix.GetStart().GetColumn())
								}
								// Any other companion would be emitted as an
								// uninstantiated `MkTag{}` too.
								if _, viaAlias := t.lookupTypeAlias(typeName); !viaAlias {
									return nil, t.uninferredTypeArgError(suffix.GetStart().GetLine(), suffix.GetStart().GetColumn(),
										base, valueYields(methodMeta.ReturnType), typeMeta.TypeParams, nil, typeMeta.TypeParams)
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
					} else if len(methodMeta.ParamTypes) == 1 && t.injectedMetaParam(methodMeta.ParamTypes[0]) != noInjectedMeta {
						// An Apply that takes nothing but an auto-injected
						// metadata parameter (StructMeta[T] / ValueMeta[T]) is
						// called with no arguments of its own:
						// `Value[Array[int]]()`. Route it through the same
						// companion-Apply path as a call with arguments, which
						// injects the metadata; otherwise it would read as
						// constructing the empty struct.
						handled, expr, err := t.tryTransformCompanionApplyOrStructCtor(base, typeName, nil, nil,
							suffix.GetStart().GetLine(), suffix.GetStart().GetColumn())
						if err != nil || handled {
							return expr, err
						}
					}
				}
			}
		}

		// Size()/ByteSize() sugar on Go primitives (string/slice/map). These are
		// zero-argument calls, so they must be intercepted here — the primary
		// dispatcher (transformCallWithArgsCtx) is only reached for calls that
		// carry an argument list. GALA collections keep their real Size() method
		// (tryTransformSizeSugar returns handled=false for non Go-primitive
		// receivers, so they fall through to the generic-method path below).
		if sel, ok := base.(*ast.SelectorExpr); ok &&
			(sel.Sel.Name == "Size" || sel.Sel.Name == "ByteSize") {
			if lowered, handled := t.tryTransformSizeSugar(sel); handled {
				return lowered, nil
			}
		}

		// Check for zero-argument generic method call (e.g., p.Swap())
		//
		// The resolved receiver type is hoisted so the unknown-method check at
		// the end of this branch can reuse it. Resolving it twice for the same
		// expression is not free: when manual inference cannot settle the type,
		// getExprTypeName falls through to the uncached inference pass, so the
		// expensive half would run again on the identical node.
		var zeroArgRecvType transpiler.Type = transpiler.NilType{}
		zeroArgLookupBase := ""
		if sel, ok := base.(*ast.SelectorExpr); ok {
			// Same normalization the call dispatcher uses — resolve the
			// receiver to its canonical form and derive the pointer-stripped
			// registry key — rather than repeating it inline.
			zeroArgRecvType, zeroArgLookupBase = t.resolveReceiverTypeAndLookupKey(sel.X, sel.Sel.Name)
		}
		// A generic method takes the same rewrite to a standalone function,
		// TypeName_Method[...](receiver), as a call with arguments: its
		// defaults, written type arguments and slot type included.
		if receiver, method, typeArgs := t.splitCallTarget(base); receiver != nil {
			recvType, lookupBaseName := zeroArgRecvType, zeroArgLookupBase
			if len(typeArgs) > 0 {
				recvType, lookupBaseName = t.resolveReceiverTypeAndLookupKey(receiver, method)
			}
			// splitCallTarget reads an index (`obj.handlers[0]()`) as type
			// arguments too, so only a generic method is rewritten.
			if t.isGenericMethodWithImports(lookupBaseName, recvType.GetPackage(), method) {
				// Taken before the defaults are lowered, as a call with
				// arguments takes it, so no default sees it.
				pending := t.expectedArgTypes.takeFor(callOwner(suffix))
				handled, expr, err := t.tryTransformGenericMethodAsFunction(nil, receiver, method, typeArgs, recvType, lookupBaseName, pending,
					suffix)
				if err != nil || handled {
					return expr, err
				}
			}
		}

		// Zero-argument call — check if function has default params that need injection
		if funcName := t.extractFuncName(base); funcName != "" {
			if funcMeta := t.getFunction(funcName); funcMeta != nil && len(funcMeta.DefaultExprs) > 0 && len(funcMeta.ParamTypes) > 0 {
				// A result-only type parameter comes from the slot, as for
				// any call (`parse()` for `parse[T any](s string = "")`).
				// The slot type is the call's, taken before its defaults are
				// lowered so none of them sees it.
				pending := t.expectedArgTypes.takeFor(callOwner(suffix))
				// Every argument is a default, so the type parameters the
				// defaults spell are bound by the call's explicit type
				// arguments (`describe[int]()`) and, for the rest, by the
				// slot (`val p Option[int] = pick()`).
				typeSubst := typeSubstStrings(t.resultSlotTypeArgs(funcMeta.ReturnType, funcMeta.TypeParams, pending))
				if explicit := explicitTypeArgSubst(funcMeta.TypeParams, t.extractFuncCallTypeArgs(base)); explicit != nil {
					if typeSubst == nil {
						typeSubst = explicit
					} else {
						maps.Copy(typeSubst, explicit)
					}
				}
				filled, err := t.fillDefaultArgs(nil, funcMeta, typeSubst, suffix.GetStart().GetLine(), suffix.GetStart().GetColumn())
				if err != nil {
					return nil, err
				}
				fun, err := t.injectFuncPhantomTypeArgs(base, funcMeta, filled, false, pending,
					suffix.GetStart().GetLine(), suffix.GetStart().GetColumn())
				if err != nil {
					return nil, err
				}
				return &ast.CallExpr{Fun: fun, Args: filled}, nil
			}
		}

		// Zero-argument method call — the same injection for a method whose
		// parameters all have defaults (`box.Scale()`). The argument-carrying
		// dispatcher fills defaults for under-filled calls, but a call with no
		// argument list never reaches it.
		if sel, ok := base.(*ast.SelectorExpr); ok && zeroArgLookupBase != "" {
			if typeMeta := t.getTypeMeta(zeroArgLookupBase); typeMeta != nil {
				if methodMeta := typeMeta.Methods[sel.Sel.Name]; methodMeta != nil && len(methodMeta.DefaultExprs) > 0 && len(methodMeta.ParamTypes) > 0 {
					// The call takes no type from its slot; its defaults
					// do not either.
					t.expectedArgTypes.takeFor(callOwner(suffix))
					filled, err := t.fillDefaultArgsMethod(sel.X, nil, methodMeta, zeroArgRecvType, nil, suffix.GetStart().GetLine(), suffix.GetStart().GetColumn())
					if err != nil {
						return nil, err
					}
					return &ast.CallExpr{Fun: base, Args: filled}, nil
				}
			}
		}

		// Zero-field shorthand struct constructed as `Foo()`. A bare `Foo()`
		// call lowers to a Go type conversion `Foo(...)`, which requires
		// exactly one argument ("missing argument in conversion to Foo"), so
		// emit a composite literal `Foo{}` instead. Non-empty shorthand
		// structs route through the positional-construction path (their argList
		// is non-nil); zero-field sealed variants carry a zero-arg Apply method
		// and are already handled above.
		if typeName := t.getBaseTypeName(base); typeName != "" {
			resolved := t.resolveStructTypeName(typeName)
			if fields, ok := t.structFields[resolved]; ok && t.isTypeBaseExpr(base) &&
				(len(fields) == 0 || t.isShorthandStruct(resolved)) {
				// A generic struct's type arguments come from the slot the
				// construction fills, as for one with arguments: no field
				// can bind them (`func p() Phantom[int] = Phantom()`).
				line, col := suffix.GetStart().GetLine(), suffix.GetStart().GetColumn()
				// Every field takes its default here, so another package's
				// struct with a private field has no such construction.
				if err := t.checkPrivateFieldCtor(base, t.getTypeMeta(resolved), fields, func(int, string) bool { return false }, suffix); err != nil {
					return nil, err
				}
				typed, err := t.structLiteralType(base, typeName, resolved, t.expectedArgTypes.peek(), line, col,
					func([]string, map[string]transpiler.Type) {})
				if err != nil {
					return nil, err
				}
				if typed != base {
					t.expectedArgTypes.consume()
					base = typed
				}
				if len(fields) == 0 {
					return &ast.CompositeLit{Type: base}, nil
				}
				// A shorthand struct called with no arguments is a real
				// construction meaning "all defaults". It has to be handled
				// here with the other zero-argument forms: `Cfg()` carries no
				// argument list, so it never reaches the positional dispatcher.
				//
				// Routing it through the same helper as every other
				// construction is what makes a struct with a required field
				// report that field by name, instead of falling through to Go
				// and coming back as "missing argument in conversion to Cfg" —
				// a message about a conversion the author never wrote.
				elts, derr := t.fillOmittedStructFields(
					typeName, resolved, fields,
					func(int, string) bool { return false },
					t.structTypeArgSubst(base, resolved),
					line, col)
				if derr != nil {
					return nil, derr
				}
				return &ast.CompositeLit{Type: base, Elts: elts}, nil
			}
		}
		// Zero-argument bare builtin (e.g. `recover()`) is forbidden too. Point
		// the diagnostic at the callee identifier (the primary expr) so the
		// caret underlines the builtin name, not the empty argument list.
		bl, bc, exact := primaryStartOf(suffix)
		if !exact {
			bl, bc = suffix.GetStart().GetLine(), suffix.GetStart().GetColumn()
		}
		if err := t.checkForbiddenGoBuiltinCall(base, bl, bc, exact); err != nil {
			return nil, err
		}
		// A val whose type has a zero-argument Apply method is called through
		// it, as section 12 of the dispatcher does for a call with arguments;
		// any other value that is not a function is an error.
		if expr, handled := t.tryTransformValWithApply(base, nil); handled {
			return expr, nil
		}
		if err := t.checkValueCalledAsFunction(base, suffix); err != nil {
			return nil, err
		}
		// Last stop for a zero-argument call: if the receiver's GALA type is
		// known and declares no such method, say so here rather than emitting
		// it and letting `go build` describe the generated expression.
		if err := t.checkUnknownMethodZeroArg(base, suffix, zeroArgRecvType, zeroArgLookupBase); err != nil {
			return nil, err
		}
		base, err := t.instantiateNullaryGenericCall(base, suffix.GetStart().GetLine(), suffix.GetStart().GetColumn())
		if err != nil {
			return nil, err
		}
		return &ast.CallExpr{Fun: base, Args: nil}, nil
	}

	return t.transformCallWithArgsCtx(base, argList.(*grammar.ArgumentListContext))
}

// resolveReceiverTypeAndLookupKey normalizes the inferred type of a call
// receiver into its canonical form (preserving generic type parameters when
// present) and returns both the resolved Type and the pointer-stripped base
// name used as a lookup key in t.genericMethods. method is the member the call
// selects, which decides how far an alias-typed receiver is resolved. When the
// receiver is nil (package-qualified call) the returned type is NilType and
// the key is "".
// Extracted from transformCallWithArgsCtx as part of A1.
func (t *galaASTTransformer) resolveReceiverTypeAndLookupKey(receiver ast.Expr, method string) (transpiler.Type, string) {
	recvType := t.getExprTypeName(receiver)
	// Normalize through a pointer receiver (`*Array[Row]`): getType never
	// resolves a `*`-prefixed name, so the pointee is qualified and re-wrapped.
	ptr, isPtr := recvType.(transpiler.PointerType)
	if isPtr {
		recvType = ptr.Elem
	}
	recvType = t.methodReceiverType(recvType, method)
	if gen, ok := recvType.(transpiler.GenericType); ok {
		if qBase := t.lookupTypeName(gen.Base.String()); !qBase.IsNil() {
			recvType = transpiler.GenericType{Base: qBase, Params: gen.Params}
		}
	} else if qName := t.lookupTypeName(recvType.BaseName()); !qName.IsNil() {
		recvType = qName
	}
	if isPtr {
		recvType = transpiler.PointerType{Elem: recvType}
	}
	// Strip pointer prefix for genericMethods lookup since methods are
	// registered under the base type name without the pointer marker.
	return recvType, strings.TrimPrefix(recvType.BaseName(), "*")
}

// methodReceiverType resolves the type of a receiver selecting method. A
// receiver typed by an alias has the methods of the type the alias names
// (`type Checked Try[Email]` has GetOrElse) as well as those declared on the
// alias itself (`func (c Coord) Sum()`), so the alias chain is followed until
// a type that declares method, or to its end.
func (t *galaASTTransformer) methodReceiverType(recv transpiler.Type, method string) transpiler.Type {
	if ptr, ok := recv.(transpiler.PointerType); ok {
		return transpiler.PointerType{Elem: t.methodReceiverType(ptr.Elem, method)}
	}
	return t.walkAliasChain(recv, func(typ transpiler.Type) bool {
		return t.typeHasMethod(typ.BaseName(), method)
	})
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
// argListCtx is nil for a call with no arguments; anchor is the argument
// list, or the `()` of such a call, where diagnostics about the call point.
// pendingExpected is the type of the slot the call fills, when it is the
// value that fills one (see consumesSlotType).
//
// Extracted from transformCallWithArgsCtx as part of A1 cont.
func (t *galaASTTransformer) tryTransformGenericMethodAsFunction(
	argListCtx *grammar.ArgumentListContext,
	receiver ast.Expr,
	method string,
	typeArgs []ast.Expr,
	recvType transpiler.Type,
	lookupBaseName string,
	pendingExpected transpiler.Type,
	anchor antlr.ParserRuleContext,
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
	// Parallel ImportPath-preserving substitution for the receiver type params.
	// The string form above drops ImportPath (via String()->ParseType), which is
	// fatal when a foreign type's package name collides with the current package
	// (io/fs vs GALA's own `fs`): the lambda param would emit a bare, colliding
	// name. Carrying the receiver's actual arg Types keeps the qualifier.
	typeSubstTypes := make(map[string]transpiler.Type)
	var recvTypeArgStrings []string
	if methodMeta != nil && typeMeta != nil {
		recvTypeArgStrings = t.getReceiverTypeArgStrings(recvType)
		recvTypeArgTypesFull := t.getReceiverTypeArgTypes(recvType)
		for i, tp := range typeMeta.TypeParams {
			if i < len(recvTypeArgStrings) {
				typeSubst[tp] = recvTypeArgStrings[i]
			}
			// Only override with the ImportPath-preserving Type for a foreign Go
			// type (one known to goTypeInfo). A local GALA type carries its own
			// module-path ImportPath which must NOT survive — the string form
			// blanks it so typeToExpr drops the current-package qualifier. Keeping
			// it would emit `<currentPackage>.LocalType` (undefined). The foreign
			// case (io/fs, whose name collides with the current `fs` package) is
			// exactly the one that needs its qualifier preserved.
			if i < len(recvTypeArgTypesFull) && t.isForeignGoType(recvTypeArgTypesFull[i]) {
				typeSubstTypes[tp] = recvTypeArgTypesFull[i]
			}
		}
		for i, tp := range methodMeta.TypeParams {
			if i < len(typeArgs) {
				typeSubst[tp] = t.exprToTypeString(typeArgs[i])
			}
			// Don't default to "any" — will try to infer from non-lambda args below.
		}
	}

	// Drive method-level type-param inference from the call's expected return
	// type (the immediately-enclosing typed slot — e.g. a named arg of a
	// surrounding constructor or a typed val initializer). When the lambda
	// body's own return type cannot be inferred locally — e.g. the body is a
	// method call on a Go universe type like `error`, whose `Error()` is not
	// resolvable through goTypeInfo — the lambda emits an `any` return,
	// unification leaves U=any, and the method's result type erases to
	// Option[any] / Try[any]. Unifying the method's declared return shape
	// (with receiver substitutions applied) against the call-site expected
	// type binds U directly from the surrounding context, making the lambda
	// see a concrete expected return type and emit `func(T) U` with the
	// correct U.
	//
	// A non-lambda argument of a known type binds its type parameters before
	// the slot does (FoldLeft's U is the type of its zero value, `Circle`, in
	// a `Shape` slot), since Go infers them from it too; an untyped constant
	// takes the slot's (`0` in an `int64` slot). The slot's bindings are
	// therefore kept apart (fromSlot) and only fill what the arguments leave.
	recvTypeArgTypes := make([]transpiler.Type, 0, len(recvTypeArgStrings))
	for _, a := range recvTypeArgStrings {
		recvTypeArgTypes = append(recvTypeArgTypes, transpiler.ParseType(a))
	}
	var fromSlot map[string]transpiler.Type
	slotTyped := map[string]bool{} // bound from the slot over an untyped constant argument
	if methodMeta != nil && typeMeta != nil && len(methodMeta.TypeParams) > 0 && !transpiler.IsUnusableOrAny(pendingExpected) {
		// The slot binds only what it can name: no type parameter left
		// unbound by another callee, no masked part (resultSlotTypeArgs).
		substitutedReturn := t.substituteConcreteTypes(methodMeta.ReturnType, typeMeta.TypeParams, recvTypeArgTypes)
		fromSlot = t.resultSlotTypeArgs(substitutedReturn, methodMeta.TypeParams, pendingExpected)
		maps.DeleteFunc(fromSlot, func(tp string, inferred transpiler.Type) bool {
			_, written := typeSubst[tp]
			return written || inferred.IsAny()
		})
	}

	// The arguments in parameter order: named ones moved to their parameter,
	// and a nil slot for each parameter left to its default.
	slots, err := bindMethodArguments(argListCtx, anchor, methodMeta)
	if err != nil {
		return true, nil, err
	}

	// Try to infer unresolved method type params from non-lambda arguments.
	// This enables FoldLeft(0, (acc, x) => acc + x) to infer U=int from the zero value 0.
	preTransformed := make(map[int]ast.Expr)
	if methodMeta != nil && typeMeta != nil && len(methodMeta.TypeParams) > 0 && len(typeArgs) < len(methodMeta.TypeParams) {
		for i, arg := range slots {
			if i >= len(methodMeta.ParamTypes) {
				break
			}
			if arg == nil {
				continue
			}
			exprCtx, lambdaCtx, _, extractErr := extractArgContent(arg)
			if extractErr != nil {
				continue
			}
			// Skip lambda/partial args — can't infer types from them.
			// Also skip placeholder-lambda expressions (L4): they contain `_`
			// identifiers that only make sense once rewritten as a lambda,
			// which happens later in transformArgument.
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
			substitutedParamType := t.substituteConcreteTypes(methodMeta.ParamTypes[i], typeMeta.TypeParams, recvTypeArgTypes)
			// Special case: bare reference to a generic GALA function. Without
			// instantiation, Go cannot infer the function's own type params at
			// the call site (and the method's result type-param U is also left
			// unresolved). Unify the function's raw signature against the
			// expected param type to bind both: method-level type params (e.g.
			// Map's U) AND the function's own type params (e.g. tickerAdvance's
			// T). If the function's params are fully bound, rewrite the AST to
			// an explicit instantiation `funcName[A, B, ...]` so Go gets a
			// concrete signature.
			if id, isIdent := expr.(*ast.Ident); isIdent && substitutedParamType != nil && !substitutedParamType.IsNil() {
				if fm, exists := t.functionByName(id.Name); exists && len(fm.TypeParams) > 0 {
					if expectedFT, isFT := substitutedParamType.(transpiler.FuncType); isFT {
						rawFT := t.funcMetaToRawType(fm)
						combined := append([]string{}, methodMeta.TypeParams...)
						combined = append(combined, fm.TypeParams...)
						combinedInferred := make(map[string]transpiler.Type)
						t.unifyForInference(expectedFT, rawFT, combined, combinedInferred)
						// If all function-level type params got bound, instantiate.
						funcTypeArgs := make([]transpiler.Type, 0, len(fm.TypeParams))
						allBound := true
						for _, fp := range fm.TypeParams {
							if v, ok := combinedInferred[fp]; ok && v != nil && !v.IsNil() {
								funcTypeArgs = append(funcTypeArgs, v)
							} else {
								allBound = false
								break
							}
						}
						if allBound {
							// Rewrite the AST to attach the inferred type args
							// to the function reference.
							var typeArgExprs []ast.Expr
							for _, ta := range funcTypeArgs {
								typeArgExprs = append(typeArgExprs, t.typeToExpr(ta))
							}
							if len(typeArgExprs) == 1 {
								expr = &ast.IndexExpr{X: id, Index: typeArgExprs[0]}
							} else {
								expr = &ast.IndexListExpr{X: id, Indices: typeArgExprs}
							}
							// Commit method-level inferences from the same unification.
							for _, mtp := range methodMeta.TypeParams {
								if v, ok := combinedInferred[mtp]; ok && v != nil && !v.IsNil() {
									if _, alreadySet := typeSubst[mtp]; !alreadySet {
										typeSubst[mtp] = v.String()
									}
								}
							}
						}
					}
				}
			}
			preTransformed[i] = expr
			argType := t.getExprTypeName(expr)
			if transpiler.IsUnusableOrAny(argType) {
				continue
			}
			inferredMap := make(map[string]transpiler.Type)
			t.unifyForInference(substitutedParamType, argType, methodMeta.TypeParams, inferredMap)
			// An untyped constant leaves the slot's binding in place.
			for tp, inferred := range inferredMap {
				if slot, slotted := fromSlot[tp]; slotted && t.isUntypedConstArg(expr) {
					// Go would infer the constant's default type, so the
					// slot's is spelled when it differs
					// (resultOnlyMethodTypeArgs).
					slotTyped[tp] = slot.String() != inferred.String()
					continue
				}
				if _, alreadySet := typeSubst[tp]; !alreadySet {
					typeSubst[tp] = inferred.String()
				}
			}
		}
	}
	for tp, inferred := range fromSlot {
		if _, alreadySet := typeSubst[tp]; !alreadySet {
			typeSubst[tp] = inferred.String()
		}
	}
	// Harvest type params from earlier lambdas to refine later ones.
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
	argListLine, argListCol := anchor.GetStart().GetLine(), anchor.GetStart().GetColumn()
	for i, arg := range slots {
		if arg == nil {
			expr, derr := t.methodDefaultArg(methodMeta, i, receiver, recvType, typeSubst, argListLine, argListCol)
			if derr != nil {
				return true, nil, derr
			}
			mArgs = append(mArgs, expr)
			continue
		}
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
		genMethodCtx.typeSubstTypes = typeSubstTypes
		if cerr := t.checkSendableArg(genMethodCtx, i, exprCtx, lambdaCtx); cerr != nil {
			return true, nil, cerr
		}
		expectedType := t.resolveExpectedArgType(genMethodCtx, i)
		expr, aerr := t.lowerArg(exprCtx, lambdaCtx, slot{typ: expectedType, open: len(genMethodCtx.typeSubst) > len(typeSubst)}, false)
		if aerr != nil {
			return true, nil, aerr
		}
		if lambdaCtx != nil {
			// Harvest newly-inferred method type params from the
			// lambda's actual result/param types and commit to typeSubst.
			if methodMeta != nil && typeMeta != nil && i < len(methodMeta.ParamTypes) {
				if paramFT, ok := methodMeta.ParamTypes[i].(transpiler.FuncType); ok {
					substitutedParamFT := t.substituteConcreteTypes(paramFT, typeMeta.TypeParams, recvTypeArgTypes)
					if actualFT, isFT := t.lambdaActualFuncType(expr).(transpiler.FuncType); isFT {
						inferredMap := make(map[string]transpiler.Type)
						t.unifyForInference(substitutedParamFT, actualFT, methodMeta.TypeParams, inferredMap)
						for tp, inferred := range inferredMap {
							if _, alreadySet := typeSubst[tp]; alreadySet {
								continue
							}
							if transpiler.IsUnusableOrAny(inferred) {
								continue
							}
							typeSubst[tp] = inferred.String()
						}
					}
				}
			}
		}
		mArgs = append(mArgs, expr)
	}

	// Record "any" for any method type param GALA could not resolve from the
	// arguments. This entry is a placeholder, not a committed output type: the
	// generic-method call below spells no type argument after the last
	// result-only one (resultOnlyMethodTypeArgs), so Go infers the param
	// from the concrete argument. The "any" only surfaces when building the
	// expected type for a *lambda* argument — for non-lambda callables (function
	// references like `xs.Map(step)`, placeholder lambdas like `xs.Map(_ * 2)`,
	// and partial-function literals like `xs.Collect({ case ... })`) it never
	// reaches the generated Go, because Go infers the param from the argument's
	// own type. A warning is emitted under GALA_WARN_TYPES so the unresolved site
	// is still visible.
	//
	// This is deliberately not a hard error: the cases above are valid programs
	// that compile via Go's inference. A method type param Go cannot infer
	// (one only the result mentions) is spelled out first instead, from the
	// slot the call fills, or reported (resultOnlyMethodTypeArgs).
	if methodMeta != nil && typeMeta != nil {
		yields := func() transpiler.Type {
			return t.substituteConcreteTypes(methodMeta.ReturnType, typeMeta.TypeParams, recvTypeArgTypes)
		}
		if typeArgs, err = t.resultOnlyMethodTypeArgs(anchor, methodMeta, typeArgs, typeSubst, slotTyped, yields, mArgs); err != nil {
			return true, nil, err
		}
	}
	if methodMeta != nil {
		for _, tp := range methodMeta.TypeParams {
			if _, ok := typeSubst[tp]; !ok {
				t.warnInference("method type parameter %q defaulted to `any` (unresolved from arguments)", tp)
				typeSubst[tp] = "any"
			}
		}
	}

	return true, t.emitGenericMethodFreeFunc(method, receiver, recvType, lookupBaseName, typeArgs, methodMeta, mArgs, hasSpread), nil
}

// bindMethodArguments lays a call's arguments out in the method's parameter
// order: positional arguments first, in order, then each named argument in its
// parameter's slot. A nil slot is a parameter the call omits, which must have a
// default.
//
// Without metadata, or for a call of only positional arguments that omits none
// — including one spreading into a variadic parameter — the arguments are
// returned as written. anchor (the argument list, or the `()` of a call with
// none) locates a diagnostic.
func bindMethodArguments(argListCtx *grammar.ArgumentListContext, anchor antlr.ParserRuleContext, methodMeta *transpiler.MethodMetadata) ([]*grammar.ArgumentContext, error) {
	var args []*grammar.ArgumentContext
	named := false
	if argListCtx != nil {
		for _, a := range argListCtx.AllArgument() {
			arg := a.(*grammar.ArgumentContext)
			args = append(args, arg)
			named = named || arg.Identifier() != nil
		}
	}
	if methodMeta == nil || (!named && len(args) >= len(methodMeta.ParamTypes)) {
		return args, nil
	}

	line, col := anchor.GetStart().GetLine(), anchor.GetStart().GetColumn()
	slots := make([]*grammar.ArgumentContext, len(methodMeta.ParamTypes))
	next := 0
	for _, arg := range args {
		if arg.Identifier() == nil {
			if next >= len(slots) {
				return nil, galaerr.NewSemanticErrorAt(line, col, fmt.Sprintf("too many arguments in call to %s", methodMeta.Name))
			}
			slots[next] = arg
			next++
			continue
		}
		name := arg.Identifier().GetText()
		idx := slices.Index(methodMeta.ParamNames, name)
		if idx < 0 || idx >= len(slots) {
			return nil, galaerr.NewSemanticErrorAt(line, col, fmt.Sprintf("unknown parameter %q in call to %s", name, methodMeta.Name))
		}
		if slots[idx] != nil {
			return nil, galaerr.NewSemanticErrorAt(line, col, fmt.Sprintf("parameter %q specified both positionally and by name in call to %s", name, methodMeta.Name))
		}
		slots[idx] = arg
	}
	for i, slot := range slots {
		if slot != nil {
			continue
		}
		if _, hasDefault := methodMeta.DefaultExprs[i]; !hasDefault {
			paramName := ""
			if i < len(methodMeta.ParamNames) {
				paramName = methodMeta.ParamNames[i]
			}
			return nil, galaerr.NewSemanticErrorAt(line, col, fmt.Sprintf("missing required argument %q (parameter %d) in call to %s", paramName, i+1, methodMeta.Name))
		}
	}
	return slots, nil
}

// methodDefaultArg is the default value of a method's i-th parameter at a call
// on callSiteReceiver, at line/col. typeSubst (may be nil) carries the type
// arguments the call has bound so far; a declared parameter type that still
// mentions an unbound type parameter is not threaded into the default.
func (t *galaASTTransformer) methodDefaultArg(methodMeta *transpiler.MethodMetadata, i int, callSiteReceiver ast.Expr, recvType transpiler.Type, typeSubst map[string]string, line, col int) (ast.Expr, error) {
	// A default may use the receiver (`f func(int) int = (x) => x * b.K`): it
	// is lowered with the receiver bound as in the method body, so `b.K`
	// resolves — and unwraps an immutable field — as it does there, and the
	// call-site receiver is then put in its place. A pointer receiver is bound
	// by its element type: field access reads the same through either, and the
	// element type is what the type metadata is keyed by.
	if ptr, isPtr := recvType.(transpiler.PointerType); isPtr {
		recvType = ptr.Elem
	}
	src := defaultSource{
		DefaultExpr: methodMeta.DefaultExprs[i],
		file:        methodMeta.DefinedIn,
		pkg:         methodMeta.Package,
		typeParams:  methodMeta.TypeParams,
		recv:        methodMeta.ReceiverName,
		recvType:    recvType,
		recvExpr:    callSiteReceiver,
	}
	// The receiver's own type arguments (`T` of a Box[int] receiver) are bound
	// by the receiver, whichever call form reached here.
	if recvMeta := t.getTypeMeta(t.resolveStructTypeName(recvType.BaseName())); recvMeta != nil && len(recvMeta.TypeParams) > 0 {
		typeSubst = receiverTypeSubst(recvMeta.TypeParams, recvType, typeSubst)
		src.typeParams = append(slices.Clone(recvMeta.TypeParams), src.typeParams...)
	}
	if i < len(methodMeta.ParamTypes) {
		t.substituteDeclared(&src, methodMeta.ParamTypes[i], parseTypeSubst(typeSubst))
	}
	return t.transformDefaultExpr(src, line, col)
}

// receiverTypeSubst maps a generic receiver type's parameters to the type
// arguments recvType carries (`T` → `int` for Box[int]), overlaid on extra
// (which is not modified). extra is returned as is when recvType carries none.
func receiverTypeSubst(typeParams []string, recvType transpiler.Type, extra map[string]string) map[string]string {
	generic, ok := recvType.(transpiler.GenericType)
	if !ok || len(generic.Params) != len(typeParams) {
		return extra
	}
	subst := make(map[string]string, len(typeParams)+len(extra))
	for k, v := range extra {
		subst[k] = v
	}
	for i, tp := range typeParams {
		if _, bound := subst[tp]; !bound && !transpiler.IsUnusable(generic.Params[i]) {
			subst[tp] = generic.Params[i].String()
		}
	}
	return subst
}

// replaceReceiver puts the call-site receiver in place of the method's receiver
// name in a lowered default. A function literal that binds that name itself — a
// lambda parameter or local called like the receiver — shadows it, so nothing
// inside it is replaced.
func replaceReceiver(expr ast.Expr, name string, with ast.Expr) ast.Expr {
	holder := &ast.ParenExpr{X: expr}
	ast.Inspect(holder, func(n ast.Node) bool {
		if n == nil || n == with {
			return false
		}
		if fl, isFuncLit := n.(*ast.FuncLit); isFuncLit && boundNames(fl)[name] {
			return false
		}
		for _, slot := range referenceSlots(n) {
			if id, isIdent := (*slot).(*ast.Ident); isIdent && id.Name == name {
				*slot = with
			}
		}
		return true
	})
	return holder.X
}

// emitGenericMethodFreeFunc builds the monomorphized free-function call that a
// generic method lowers to: `pkg.TypeName_Method[typeArgs...](receiver, args...)`
// (or unqualified `TypeName_Method[...]` for a non-std/current-package type).
// It is the shared emission step used both by tryTransformGenericMethodAsFunction
// (context-driven path) and by the `bind`/`also` desugaring, which synthesizes
// the call directly. `explicitTypeArgs` are the method-level type args (e.g. U in
// FlatMap[U]); the receiver's concrete type args (e.g. T of Try[T]) are appended
// when they are all of them, and left to Go when they are a prefix.
func (t *galaASTTransformer) emitGenericMethodFreeFunc(
	method string,
	receiver ast.Expr,
	recvType transpiler.Type,
	lookupBaseName string,
	explicitTypeArgs []ast.Expr,
	methodMeta *transpiler.MethodMetadata,
	mArgs []ast.Expr,
	hasSpread bool,
) ast.Expr {
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
		if ident, ok := arg.(*ast.Ident); ok && t.isUnboundTypeParam(ident.Name) {
			continue
		}
		concreteRecvTypeArgs = append(concreteRecvTypeArgs, arg)
	}

	// Decide whether to add type arguments:
	// - If method has its own type params (e.g., Map[U]) and no explicit type args: let Go infer.
	// - Otherwise: combine explicit type args with concrete receiver type args,
	//   which follow the method's own in the function's list; a shorter
	//   explicit list is a prefix Go completes from the arguments.
	shouldAddTypeArgs := len(explicitTypeArgs) > 0 || (methodMeta == nil || len(methodMeta.TypeParams) == 0)
	if shouldAddTypeArgs {
		allTypeArgs := explicitTypeArgs
		if methodMeta == nil || len(explicitTypeArgs) >= len(methodMeta.TypeParams) {
			allTypeArgs = append(slices.Clone(explicitTypeArgs), concreteRecvTypeArgs...)
		}
		if len(allTypeArgs) == 1 {
			funExpr = &ast.IndexExpr{X: funExpr, Index: allTypeArgs[0]}
		} else if len(allTypeArgs) > 1 {
			funExpr = &ast.IndexListExpr{X: funExpr, Indices: allTypeArgs}
		}
	}

	call := &ast.CallExpr{
		Fun:      funExpr,
		Args:     append([]ast.Expr{receiver}, mArgs...),
		Ellipsis: ellipsisPos(hasSpread),
	}
	if methodMeta != nil && methodMeta.GoResults != nil {
		t.recordGenericGoResultCall(call, recvType, lookupBaseName, methodMeta)
	}
	return call
}

// recordGenericGoResultCall remembers the signature of call, a generic method
// declaring a Go result list lowered to a free function, so that the call is
// lifted to one GALA value as other calls of such functions are: the
// receiver's type arguments substituted, and the receiver its first parameter.
func (t *galaASTTransformer) recordGenericGoResultCall(call *ast.CallExpr, recvType transpiler.Type, baseName string, m *transpiler.MethodMetadata) {
	subst := map[string]transpiler.Type{}
	if meta := t.getTypeMeta(baseName); meta != nil {
		args := t.getReceiverTypeArgTypes(recvType)
		for i, tp := range meta.TypeParams {
			if i < len(args) {
				subst[tp] = args[i]
			}
		}
	}
	if t.genericGoResultCalls == nil {
		t.genericGoResultCalls = make(map[*ast.CallExpr]*transpiler.GoFuncSignature)
	}
	params := append([]transpiler.Type{recvType}, t.substituteGoTypeParamsIn(m.ParamTypes, subst)...)
	t.genericGoResultCalls[call] = goResultsSignature(params, nil, t.substituteGoTypeParamsIn(m.GoResults, subst), m.TypeParams)
}

// resultOnlyMethodTypeArgs completes typeArgs, the type arguments written at a
// call of the generic method methodMeta, when the method has a type parameter
// only its result mentions (phantom, see phantomTypeParams: the `U` of
// `Convert[U any]() Option[U]`). Go infers no such parameter, so the call has
// to spell the method's type arguments up to the last such one; Go infers
// those after it from the arguments and the receiver. So does one slotTyped:
// bound from the slot over an untyped constant argument, which Go would give
// the constant's default type (`0` for a `U` in an `Option[int64]` slot).
// typeSubst holds those its arguments and the slot the call fills bound (see
// tryTransformGenericMethodAsFunction); yields gives its result type on this
// receiver, for the hint. One left open is GALA-E0067 at anchor (the call's
// argument list, or its `()`), as for a generic function (see
// injectFuncPhantomTypeArgs). typeArgs is returned as is when nothing needs
// spelling: Go infers every type argument from the arguments.
func (t *galaASTTransformer) resultOnlyMethodTypeArgs(anchor antlr.ParserRuleContext, methodMeta *transpiler.MethodMetadata, typeArgs []ast.Expr, typeSubst map[string]string, slotTyped map[string]bool, yields func() transpiler.Type, args []ast.Expr) ([]ast.Expr, error) {
	argBound, phantom := t.phantomTypeParams(methodMeta.TypeParams, methodMeta.ParamTypes)
	// The call spells a prefix of the type arguments, up to the last phantom
	// or slot-typed one; Go infers the rest from the arguments and the
	// receiver.
	end := 0
	for i, tp := range methodMeta.TypeParams {
		if slotTyped[tp] || slices.Contains(phantom, tp) {
			end = i + 1
		}
	}
	if len(typeArgs) >= end {
		return typeArgs, nil
	}
	full := slices.Clone(typeArgs)
	resolved := t.writtenTypeArgs(methodMeta.TypeParams, typeArgs)
	var missing []string
	for _, tp := range methodMeta.TypeParams[len(typeArgs):end] {
		if s, ok := typeSubst[tp]; ok {
			typ := transpiler.ParseType(s)
			resolved[tp] = typ
			full = append(full, t.typeToExpr(typ))
		} else {
			missing = append(missing, tp)
		}
	}
	if missing == nil {
		return full, nil
	}
	name := methodCallName(anchor, methodMeta.Name)
	line, col := anchor.GetStart().GetLine(), anchor.GetStart().GetColumn()
	// No slot fixes an argument whose type is unknown.
	always := slices.ContainsFunc(missing, func(tp string) bool { return argBound[tp] })
	if err := t.unknownArgTypeError(line, col, name, missing, args, always); err != nil {
		return nil, err
	}
	return nil, t.uninferredTypeArgErrorNamed(line, col, name, valueYields(yields()), methodMeta.TypeParams, resolved, missing)
}

// methodCallName spells the callee of a method call for a diagnostic as the
// source writes it (`Cell(1).Convert`, see calleeText); anchor is the call's
// argument list or its `()`. A callee too long to quote is `(...).method`.
func methodCallName(anchor antlr.Tree, method string) string {
	for n := anchor; n != nil; n = n.GetParent() {
		if suffix, ok := n.(*grammar.PostfixSuffixContext); ok {
			if text := calleeText(suffix); strings.HasSuffix(text, "."+method) {
				return text
			}
			break
		}
	}
	return "(...)." + method
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
		// The receiver's type is known but declares no such method. When that
		// judgement can be made safely this is a GALA error (GALA-E0044) rather
		// than something to hand to `go build`, which would report it against
		// the generated expression. checkUnknownMethod stands down whenever the
		// receiver is not fully concrete or the name is transformer-generated;
		// see unknown_method.go.
		if err := t.checkUnknownMethod(argListCtx, typeMeta, method, recvType); err != nil {
			return nil, err
		}
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
			if t.isUnboundTypeParam(arg) {
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
	args := argListCtx.AllArgument()
	// The Go method's parameters, for passing a Go call's results through
	// and for naming a Try passed where its plain value is expected.
	var goSig *transpiler.GoFuncSignature
	if len(args) > 0 {
		goSig = t.lookupGoCallSignature(&ast.CallExpr{Fun: &ast.SelectorExpr{X: receiver, Sel: ast.NewIdent(method)}})
	}
	for i, argCtx := range args {
		arg := argCtx.(*grammar.ArgumentContext)
		exprCtx, lambdaCtx, isSpread, extractErr := extractArgContent(arg)
		if extractErr != nil {
			return nil, extractErr
		}
		if isSpread {
			hasSpread = true
		}
		positional := !isSpread && arg.Identifier() == nil
		// A lambda takes its parameter types from the Go method's parameter,
		// once the signature is instantiated (see goMethodSignature).
		expected := transpiler.Type(transpiler.NilType{})
		if positional && lambdaCtx != nil && goSig != nil && len(goSig.TypeParams) == 0 {
			expected = goSigParamType(goSig, i)
		} else if positional && lambdaCtx == nil {
			expected = t.goParamSlot(goSig, i, exprCtx)
		}
		expr, err := t.lowerArg(exprCtx, lambdaCtx, typedSlot(expected), false)
		if err != nil {
			return nil, err
		}
		if positional {
			expr = t.spreadGoResultArg(goSig, len(args), expr)
			if cerr := t.checkGoResultGoArg(goSig, i, expr, exprCtx); cerr != nil {
				return nil, cerr
			}
		}
		mArgs = append(mArgs, expr)
	}
	return &ast.CallExpr{
		Fun:      &ast.SelectorExpr{X: receiver, Sel: ast.NewIdent(method)},
		Args:     mArgs,
		Ellipsis: ellipsisPos(hasSpread),
	}, nil
}

// goParamSlot is the slot type the i-th argument exprCtx of a call to the Go
// function sig is lowered against, as an argument of a GALA function is: its
// parameter's type, for a value that takes its type arguments from the slot
// it fills (consumesSlotType) — `Id(5)` in `strconv.FormatInt(Id(5), 10)`
// is an `Id[int64]`. NilType for any other argument, a parameter of a generic
// Go function, or one of an interface type.
func (t *galaASTTransformer) goParamSlot(sig *transpiler.GoFuncSignature, i int, exprCtx grammar.IExpressionContext) transpiler.Type {
	if sig == nil || len(sig.TypeParams) > 0 {
		return transpiler.NilType{}
	}
	param := goSigParamType(sig, i)
	if transpiler.IsUnusableOrAny(param) || !t.consumesSlotType(exprCtx, t.followAliasChain(param)) {
		return transpiler.NilType{}
	}
	if _, iface := t.interfaceMethodNames(param); iface {
		return transpiler.NilType{}
	}
	return param
}

// genericGoLambdaSlot is the slot a lambda fills as the i-th argument of a call
// of fun, the generic Go function sig: the parameter's function type with the
// type parameters that explicit type arguments and the earlier arguments prior
// determine substituted, and each result that still names one left open for
// the lambda's body to give (`sync.OnceValues(() => strconv.Atoi(s))`). Go
// then infers the rest from the lambda. NilType when the parameter is not a
// function type, or when one of its parameters still names a type parameter,
// which the lambda's parameters cannot be typed by.
func (t *galaASTTransformer) genericGoLambdaSlot(sig *transpiler.GoFuncSignature, i int, prior []ast.Expr, fun ast.Expr) transpiler.Type {
	ft := t.resolveTranspilerTypeAsFuncType(goSigParamType(sig, i))
	if ft == nil {
		return transpiler.NilType{}
	}
	inst := *ft
	if subst := t.inferGoSignatureTypeArgs(sig, prior, t.callSiteTypeArgs(&ast.CallExpr{Fun: fun}), false); len(subst) > 0 {
		if sub, ok := t.substituteGoTypeParams(*ft, subst).(transpiler.FuncType); ok {
			inst = sub
		}
	}
	if funcTypeParamsMentionTypeParams(inst.Params, sig.TypeParams) {
		return transpiler.NilType{}
	}
	inst.Results = maskTypeParamResults(inst.Results, sig.TypeParams)
	return inst
}

// isGoResultsThunkSlot reports whether slot is a function type taking nothing
// and returning several results, the slot a bare expression fills as a thunk
// (see goResultsThunk); for a generic Go callee it is typed as a lambda's is,
// by genericGoLambdaSlot, so a result naming a type parameter is left open.
func isGoResultsThunkSlot(slot transpiler.Type) bool {
	ft, ok := slot.(transpiler.FuncType)
	return ok && len(ft.Params) == 0 && len(ft.Results) > 1
}

// goSigParamType is the type of the parameter the i-th positional argument of
// a call of sig fills, or NilType when there is none (sig may be nil). Every
// argument past the last parameter of a variadic signature fills that one,
// whose type is recorded element-wise: `...string` as string.
func goSigParamType(sig *transpiler.GoFuncSignature, i int) transpiler.Type {
	if sig == nil || len(sig.Params) == 0 {
		return transpiler.NilType{}
	}
	if i >= len(sig.Params) {
		if !sig.IsVariadic {
			return transpiler.NilType{}
		}
		i = len(sig.Params) - 1 // stored as the element type
	}
	if typ := sig.Params[i].Type; typ != nil {
		return typ
	}
	return transpiler.NilType{}
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
		expr, err := t.lowerArg(exprCtx, lambdaCtx, slot{typ: expectedType, open: true}, false)
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
			if cerr := t.checkSendableNamedArg(resolvedMethodCtx, argName, exprCtx, lambdaCtx); cerr != nil {
				return nil, cerr
			}
			expectedType := t.resolveNamedArgExpectedType(resolvedMethodCtx, argName)
			expr, err := t.lowerArg(exprCtx, lambdaCtx, typedSlot(expectedType), false)
			if err != nil {
				return nil, err
			}
			mNamedArgs[argName] = expr
		} else {
			resolvedMethodCtx := t.buildMethodCallContext(methodMeta, typeSubst, false)
			if cerr := t.checkSendableArg(resolvedMethodCtx, argIdx, exprCtx, lambdaCtx); cerr != nil {
				return nil, cerr
			}
			expectedType := t.resolveExpectedArgType(resolvedMethodCtx, argIdx)
			expr, err := t.lowerArg(exprCtx, lambdaCtx, typedSlot(expectedType), false)
			if err != nil {
				return nil, err
			}
			mArgs = append(mArgs, expr)
			argIdx++
		}
	}
	methodFun := &ast.SelectorExpr{X: receiver, Sel: ast.NewIdent(method)}
	if len(mNamedArgs) > 0 && len(methodMeta.ParamNames) > 0 {
		return t.handleNamedArgsMethodCall(methodFun, receiver, mArgs, mNamedArgs, methodMeta, recvType, typeSubst, argListCtx.GetStart().GetLine(), argListCtx.GetStart().GetColumn())
	}
	if len(methodMeta.DefaultExprs) > 0 && len(mArgs) < len(methodMeta.ParamTypes) {
		filled, err := t.fillDefaultArgsMethod(receiver, mArgs, methodMeta, recvType, typeSubst, argListCtx.GetStart().GetLine(), argListCtx.GetStart().GetColumn())
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
	slotType transpiler.Type,
	line, col int,
) (handled bool, result ast.Expr, err error) {
	// Tuple → TupleN arity rewrite for the bare positional constructor.
	// Routes through the unified helper (B2) so every site agrees. Without
	// this rewrite, `Tuple(a, b, c)` falls through to the Apply path and
	// emits a 2-field literal `Tuple{V1, V2}`, dropping the third arg.
	// Only applies when no explicit type args were given (bare ident or
	// qualified selector); explicit type args have already been resolved
	// to the right arity by transformType.
	if n := len(args); n >= 3 && n <= 10 && isStdTupleIdent(fun) {
		typeName, _ = transpiler.TupleArityName(n)
		fun = t.rewriteStdTupleIdent(fun, n)
	}

	origTypeName := typeName
	typeMeta, resolvedTypeMeta := t.getTypeMetaResolved(typeName)
	if typeMeta == nil {
		return false, nil, nil
	}
	// Update typeName to resolved name for subsequent lookups.
	typeName = resolvedTypeMeta

	resolvedTypeName := t.resolveStructTypeName(typeName)

	// A type alias naming a non-struct is a conversion target, not a
	// construction target: `Millis(v)` for `type Millis int64` emits the Go
	// conversion `Millis(v)`, the same form as `int64(v)`. An alias naming a
	// struct falls through to the construction paths below, since its resolved
	// name carries the struct's fields.
	if _, isAlias := t.lookupTypeAlias(typeName); isAlias && len(args) == 1 && len(t.structFields[resolvedTypeName]) == 0 {
		return true, &ast.CallExpr{Fun: fun, Args: args}, nil
	}
	// An alias called on a value of the type it names is a conversion too,
	// whatever that type is: `Figure(Dot())` for `type Figure Shape`. Read as
	// a construction it would map the value onto the struct's first field.
	if target, isAlias := t.lookupTypeAlias(origTypeName); isAlias && len(args) == 1 {
		want := stripPackagePrefix(t.followAliasChain(target).BaseName())
		if got := t.getExprTypeName(args[0]); !got.IsNil() && want != "" && stripPackagePrefix(got.BaseName()) == want {
			return true, &ast.CallExpr{Fun: fun, Args: args}, nil
		}
	}

	methodMeta, hasApply := typeMeta.Methods["Apply"]

	// Positional struct construction has priority over Apply: when arg count
	// equals field count, emit a struct literal directly.
	//
	// Exception: a sealed type's parent layout is synthetic (merged variant
	// fields plus a `_variant` discriminator) and is never a valid positional
	// construction target — callers construct sealed values through case
	// constructors or the companion Apply. Without this guard, calling a sealed
	// type that has an Apply method with an arg count that happens to equal the
	// synthetic field count (e.g. `Future[T](() => x, ec)` where the parent has
	// `state` + `_variant`) silently miscompiles into a wrong struct literal
	// instead of dispatching to Apply.
	if fields, structOk := t.structFields[resolvedTypeName]; structOk && len(args) == len(fields) &&
		t.positionalCallBuildsStructLiteral(typeMeta, fields, len(args)) {
		// Infer type args from positional arg types when the call site omitted
		// them. Without this, a generic struct like `Tuple(a, b)` emits
		// `Tuple{V1: a, V2: b}` — Go rejects the bare generic type.
		typedFun, err := t.inferTypeArgsFromPositionalArgs(fun, typeName, resolvedTypeName, fields, args, slotType, line, col)
		if err != nil {
			return true, nil, t.preferMissingFieldError(err, typeName, resolvedTypeName, fields, func(i int, _ string) bool { return i < len(args) }, line, col)
		}
		lit, err := t.buildStructLiteral(typedFun, typeName, resolvedTypeName, fields, args, false, line, col)
		return true, lit, err
	}

	if !hasApply {
		// No Apply method. Still emit a struct literal if this is a known
		// struct layout — callers may supply a subset of fields.
		//
		// Not, however, when those fields belong to another package and are
		// private to it: a subset construction would map the arguments onto a
		// prefix of fields the caller cannot even name. That is how
		// `Array(1, 2, 3)` used to become `Array{root: 1, length: 2, depth: 3}`
		// rather than being reported as GALA-E0043.
		// Subset construction fills fields left to right, so it applies only
		// while there are at least as many fields as arguments. A call with
		// more arguments than fields is left unhandled and reported by the
		// type-used-as-constructor check rather than dropping the surplus.
		if fields, ok := t.structFields[resolvedTypeName]; ok && t.positionalCallBuildsStructLiteral(typeMeta, fields, len(args)) {
			typedFun, err := t.inferTypeArgsFromPositionalArgs(fun, typeName, resolvedTypeName, fields, args, slotType, line, col)
			if err != nil {
				return true, nil, t.preferMissingFieldError(err, typeName, resolvedTypeName, fields, func(i int, _ string) bool { return i < len(args) }, line, col)
			}
			lit, err := t.buildStructLiteral(typedFun, typeName, resolvedTypeName, fields, args, true, line, col)
			return true, lit, err
		}
		return false, nil, nil
	}

	// Apply path: verify the base expression is a type (not a variable).
	baseExpr, written := splitCallFunTypeArgs(fun)
	typeArgs := t.qualifyTypeExprs(written)

	isType := false
	if id, ok := baseExpr.(*ast.Ident); ok {
		if !t.isVal(id.Name) && !t.isVar(id.Name) {
			if !t.lookupTypeName(id.Name).IsNil() {
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

	// Infer type args from argument types and the slot type. A partial
	// list (`Mk[int](2, "c")` for `Mk[A, B]`) binds its leading type
	// parameters as written and has the rest inferred the same way.
	// A generic alias's written arguments are its own, not the leading ones of
	// the type it names, so they are left as written.
	_, viaAlias := t.lookupTypeAlias(origTypeName)
	var uninferred error
	if len(typeArgs) < len(typeMeta.TypeParams) && !(viaAlias && len(typeArgs) > 0) {
		inferredMap := t.writtenTypeArgs(typeMeta.TypeParams, typeArgs)
		// The type of the slot the construction fills, matched against what
		// Apply returns, binds before the arguments do, so an untyped constant
		// takes the slot's type (`MkHalf(1)` as a `Half[int64, string]` is a
		// `MkHalf[int64, string]`), as for a struct (structLiteralType). A
		// result slot gives its type only to a construction that is the
		// result value (consumesSlotType), never to one bound to a `val` or
		// nested in the result.
		for tp, typ := range t.resultSlotTypeArgs(methodMeta.ReturnType, typeMeta.TypeParams, slotType) {
			if _, written := inferredMap[tp]; !written {
				inferredMap[tp] = typ
			}
		}
		// Then the Apply method's arguments.
		for i, arg := range args {
			if i < len(methodMeta.ParamTypes) {
				argType := t.getExprTypeName(arg)
				if argType != nil && !argType.IsNil() && !argType.IsAny() {
					t.unifyForInference(methodMeta.ParamTypes[i], argType, typeMeta.TypeParams, inferredMap)
				}
			}
		}
		// The variant lookup scans every type, so it runs only once a
		// parameter is left open.
		var variant sealedVariant
		if len(inferredMap) < len(typeMeta.TypeParams) {
			variant = t.sealedVariantOf(typeName)
		}
		instantiated, missing := t.completeTypeArgs(baseExpr, typeMeta.TypeParams, typeArgs, inferredMap)
		// What is left open is a GALA error: the receiver would be emitted as
		// an uninstantiated `MkTag{}` (or a partial `Mk[int]{}`), which Go
		// cannot infer type arguments for. Not through a generic alias, which
		// is left as written.
		partialList := len(typeArgs) > 0
		switch {
		case missing == nil:
			fun = instantiated
			_, typeArgs = splitCallFunTypeArgs(fun)
		case variant.parent != nil && (partialList || !viaAlias):
			return true, nil, t.uninferredVariantError(variant, "(...)", inferredMap, missing, line, col)
		case partialList:
			return true, nil, t.uninferredCallTypeArgError(line, col, baseExpr, methodMeta.ReturnType, typeMeta.TypeParams, inferredMap, missing, args)
		case !viaAlias:
			// Reported after the metadata injection below, whose error for a
			// codec call with no type argument (GALA-E0050) is the more
			// specific one.
			uninferred = t.uninferredCallTypeArgError(line, col, baseExpr, methodMeta.ReturnType, typeMeta.TypeParams, inferredMap, missing, args)
		}
	}

	// Auto-inject StructMeta[T] when first Apply param is StructMeta[T] (the
	// typed interface from std/meta.gala).
	// Codec[Person](SnakeCase()) → prepend _StructMeta_Person{} before SnakeCase()
	// ValueMeta[T] is injected the same way for a value of any codec shape:
	// Value[Array[int]]() → prepend _ValueMeta_Array_int{}.
	if len(methodMeta.ParamTypes) > 0 {
		if kind := t.injectedMetaParam(methodMeta.ParamTypes[0]); kind != noInjectedMeta {
			if len(typeArgs) == 0 {
				// No metadata can be generated for a type nobody named.
				return true, nil, t.codecError(&structMetaConfig{rootName: origTypeName, line: line, col: col},
					fmt.Sprintf("the type argument of %s is not given; write %s[T](...)", origTypeName, origTypeName))
			}
			metaArg, err := t.injectedMetaTypeArg(methodMeta.ParamTypes[0], typeMeta, typeArgs, origTypeName, line, col)
			if err != nil {
				return true, nil, err
			}
			var injected []ast.Expr
			if kind == injectedValueMeta {
				injected, err = t.autoInjectValueMeta(args, metaArg, line, col)
			} else {
				injected, err = t.autoInjectStructMeta(args, methodMeta, []ast.Expr{metaArg}, line, col)
			}
			if err != nil {
				return true, nil, err
			}
			args = injected
		}
	}
	if uninferred != nil {
		return true, nil, uninferred
	}

	if isGeneric {
		// Generic Apply method: use standalone function form.
		fullName := typeName + "_Apply"
		var funExpr ast.Expr
		isStdType := hasStdPrefix(typeName)
		if !isStdType {
			resolvedType := t.lookupTypeName(typeName)
			isStdType = !resolvedType.IsNil() && resolvedType.GetPackage() == registry.StdPackageName
		}
		if isStdType {
			funExpr = t.stdIdent(stripStdPrefix(fullName))
		} else {
			funExpr = t.ident(fullName)
		}
		return true, &ast.CallExpr{
			Fun:  withTypeArgs(funExpr, typeArgs),
			Args: append([]ast.Expr{&ast.CompositeLit{Type: fun}}, args...),
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

// positionalCallBuildsStructLiteral reports whether a call of the struct type
// with nargs positional arguments builds a struct literal rather than going
// to the companion Apply (or being rejected). A full call does, unless the
// type is sealed and has an Apply: a sealed parent's layout is synthetic. A
// call with fewer arguments than fields does only when there is no Apply, and
// then takes the omitted fields' defaults. Neither does when the fields are
// private to another package.
func (t *galaASTTransformer) positionalCallBuildsStructLiteral(typeMeta *transpiler.TypeMetadata, fields []string, nargs int) bool {
	if nargs == 0 || nargs > len(fields) || t.positionalCtorIsUnavailable(typeMeta, fields, nargs) {
		return false
	}
	_, hasApply := typeMeta.Methods["Apply"]
	if nargs == len(fields) {
		return !(typeMeta.IsSealed && hasApply)
	}
	return !hasApply
}

// buildStructLiteral emits a struct composite literal from positional args,
// wrapping immutable fields with NewImmutable as needed. When `truncate` is
// true, excess args are silently dropped (used when the arg count exceeds the
// field count); otherwise the caller is responsible for matching the counts.
// Extracted from transformCallWithArgsCtx as part of A1 cont.
func (t *galaASTTransformer) buildStructLiteral(typeExpr ast.Expr, typeName, resolvedTypeName string, fields []string, args []ast.Expr, truncate bool, line, col int) (ast.Expr, error) {
	immutFlags := t.structImmutFields[resolvedTypeName]
	fieldTypes := t.structFieldTypes[resolvedTypeName]
	typeArgSubst := t.structTypeArgSubst(typeExpr, resolvedTypeName)
	var elts []ast.Expr
	for i, fieldName := range fields {
		if i >= len(args) {
			break
		}
		var valExpr ast.Expr
		if immutFlags != nil && i < len(immutFlags) && immutFlags[i] {
			valExpr = t.wrapImmutableFieldValue(args[i], fieldTypes[fieldName], typeArgSubst)
		} else {
			valExpr = args[i]
		}
		elts = append(elts, &ast.KeyValueExpr{
			Key:   ast.NewIdent(fieldName),
			Value: valExpr,
		})
	}

	// Positional construction fills fields left to right, so anything past
	// len(args) was omitted. `truncate` marks the under-filled call: it takes
	// each omitted field's declared default, and reports the ones that have
	// none. A full call (len(args) == len(fields)) omits nothing and skips
	// this entirely. See struct_defaults.go.
	if truncate {
		defaulted, err := t.fillOmittedStructFields(
			typeName, resolvedTypeName, fields,
			func(i int, _ string) bool { return i < len(args) },
			typeArgSubst, line, col)
		if err != nil {
			return nil, err
		}
		elts = append(elts, defaulted...)
	}

	return &ast.CompositeLit{Type: typeExpr, Elts: elts}, nil
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
	args := argListCtx.AllArgument()

	// A Go callee's parameters, for passing a Go call's results through
	// (spreadGoResultArg) and for naming a Try or Tuple passed where its plain
	// value is expected (checkGoResultGoArg).
	var goSig *transpiler.GoFuncSignature
	if callCtx.funcMeta == nil && callCtx.applyMethodMeta == nil && len(callCtx.goFuncParamTypes) > 0 {
		goSig = t.lookupGoCallSignature(&ast.CallExpr{Fun: fun})
	}

	for _, argCtx := range args {
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
			// first (supports generic struct construction), then function metadata,
			// then sealed-variant cases. The line/col is for the dot-import
			// ambiguity diagnostic in findSealedVariant — point at the named arg
			// itself so the error pins the exact call-site token rather than
			// the surrounding statement.
			argName := arg.Identifier().GetText()
			argLine, argCol := arg.GetStart().GetLine(), arg.GetStart().GetColumn()
			namedExpectedType, expErr := t.resolveNamedArgExpectedFuncType(fun, argName, callCtx, argLine, argCol)
			if expErr != nil {
				return nil, nil, false, expErr
			}

			// Concurrency boundary: enforce capture-safety when this named
			// parameter is a Sendable[F].
			boundaryCtx := callContext{funcMeta: callCtx.funcMeta, applyMethodMeta: callCtx.applyMethodMeta}
			if cerr := t.checkSendableNamedArg(boundaryCtx, argName, exprCtx, lambdaCtx); cerr != nil {
				return nil, nil, false, cerr
			}

			expr, aerr := t.lowerFunctionArg(exprCtx, lambdaCtx, namedExpectedType, callCtx, false)
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
		funcCallCtx.applyTypeParams = callCtx.applyTypeParams
		funcCallCtx.unboundStructTypeParams = callCtx.unboundStructTypeParams()
		funcCallCtx.structLiteral = callCtx.structLiteral
		// Concurrency boundary: enforce capture-safety when this positional
		// parameter is a Sendable[F].
		if cerr := t.checkSendableArg(funcCallCtx, argIdx, exprCtx, lambdaCtx); cerr != nil {
			return nil, nil, false, cerr
		}
		expectedType := t.resolveExpectedArgType(funcCallCtx, argIdx)
		if transpiler.IsUnusable(expectedType) && !isSpreadAll {
			expectedType = t.goParamSlot(goSig, argIdx, exprCtx)
		}
		if goSig != nil && len(goSig.TypeParams) > 0 && (lambdaCtx != nil || isGoResultsThunkSlot(expectedType)) {
			expectedType = t.genericGoLambdaSlot(goSig, argIdx, positional, fun)
		}
		// The sole argument of Try(...) is its thunk: a Go call's error there is
		// the Failure itself (see tryThunkValue).
		tryThunk := len(args) == 1 && isTryThunkParam(funcCallCtx, argIdx)
		expr, aerr := t.lowerFunctionArg(exprCtx, lambdaCtx, expectedType, callCtx, tryThunk)
		if aerr != nil {
			return nil, nil, false, aerr
		}
		if !isSpreadAll {
			expr = t.spreadGoResultArg(goSig, len(args), expr)
			if cerr := t.checkGoResultGoArg(goSig, argIdx, expr, exprCtx); cerr != nil {
				return nil, nil, false, cerr
			}
		}
		positional = append(positional, expr)
		argIdx++
	}
	return positional, named, hasSpread, nil
}

// lowerFunctionArg lowers one argument of a regular call (see lowerArg). For a
// generic struct or sealed-variant constructor, an argument whose lowering
// depends on its slot type (a lambda, a placeholder lambda, or an if/match of
// lambdas) first has the constructor's still-unbound type parameters masked out
// of that type (see genericCtorLambdaExpectation).
func (t *galaASTTransformer) lowerFunctionArg(
	exprCtx grammar.IExpressionContext,
	lambdaCtx *grammar.LambdaExpressionContext,
	expected transpiler.Type,
	callCtx functionCallContext,
	tryThunk bool,
) (ast.Expr, error) {
	strict := false
	if len(callCtx.unboundStructTypeParams()) > 0 &&
		(lambdaCtx != nil || t.needsExpectedType(exprCtx) || t.isPlaceholderLambdaArg(exprCtx, expected)) {
		expected, strict = t.genericCtorLambdaExpectation(expected, callCtx)
	}
	return t.lowerArg(exprCtx, lambdaCtx, slot{typ: expected, open: callCtx.typeArgPlaceholders, tryThunk: tryThunk}, strict)
}

// isPlaceholderLambdaArg reports whether exprCtx, filling a slot of type
// slotType, lowers to a placeholder lambda (`_ * 10` against a function type,
// see tryRewriteAsPlaceholderLambda), so its slot type is a lambda's. Like
// transformArgument, a lambda or partial function in it takes precedence. Its
// `_` are counted as tryRewriteAsPlaceholderLambda counts them, nested call
// arguments included (`double(_)`): one a nested call's own function-typed slot
// takes (`compose(_ + 1, show)`) lowers there, and the slot's type arguments
// then bind from the value it produces.
func (t *galaASTTransformer) isPlaceholderLambdaArg(exprCtx grammar.IExpressionContext, slotType transpiler.Type) bool {
	return exprCtx != nil && t.resolveTranspilerTypeAsFuncType(slotType) != nil && countPlaceholderUnderscoresInExpr(exprCtx) > 0 &&
		t.findPartialFunctionInExpression(exprCtx) == nil && t.findLambdaInExpression(exprCtx) == nil
}

// resolveNamedArgExpectedFuncType looks up the expected type for a named
// argument so the value expression can be transformed with the right
// expectation. For lambda arguments this is what gives them their parameter
// types; for non-lambda arguments (e.g. nested generic method calls) the
// expected type drives downstream type-param inference via the
// expectedArgTypes stack — without it, a generic call like
// `errOpt.Map((e) => e.Error())` whose lambda body's return type is locally
// unresolvable falls back to U=any. Tries struct-field-type resolution first
// (with generic type-param substitution for generic struct construction),
// then falls back to function metadata param names, then to sealed-variant
// case constructors. Returns NilType when no expected type can be
// determined. The error return propagates an ambiguous-dot-import diagnostic
// from the sealed-variant lookup (see findSealedVariant); other failure
// modes here surface as NilType so the caller can still transform the
// value expression without an expected type. Extracted from
// transformCallWithArgsCtx as part of A1 cont.
func (t *galaASTTransformer) resolveNamedArgExpectedFuncType(fun ast.Expr, argName string, callCtx functionCallContext, line, col int) (transpiler.Type, error) {
	// Step 1: struct-field lookup — handles lambdas passed as named struct args
	// AND non-lambda named args (so the call-site expected type can drive
	// inference of nested generic method calls).
	if callCtx.structFieldExpectedTypes != nil {
		if funcName := t.extractFuncName(fun); funcName != "" {
			_, resolvedTypeName := t.getTypeMetaResolved(funcName)
			if resolvedTypeName != "" {
				resolved := t.resolveStructTypeName(resolvedTypeName)
				if fieldTypes, ok := t.structFieldTypes[resolved]; ok {
					if rawType, ok := fieldTypes[argName]; ok && rawType != nil && !rawType.IsNil() {
						// Generic struct: substitute the known type arguments.
						// As for a positional argument (resolveExpectedFuncArgType),
						// a field type still naming a type parameter the call
						// has not bound is passed down only for a lambda or
						// tuple literal; any other value lowered against it
						// would spell that parameter's bare name.
						slotType := t.substituteTranspilerTypeParams(rawType, callCtx.structTypeSubst)
						if t.isFuncOrTupleType(slotType) || !typeMentionsTypeParam(slotType, callCtx.unboundStructTypeParams()) {
							return slotType, nil
						}
						return transpiler.NilType{}, nil
					}
				}
			}
		}
	}

	// Step 1b: a Go struct's function-typed field (directly or through a Go
	// named function type), so a lambda passed for it gets its parameter
	// types. Only for a non-generic struct, whose field types need no type
	// arguments substituted.
	if td := callCtx.goStruct; td != nil && len(td.TypeParams) == 0 {
		ft := td.Fields[argName]
		if u, named := t.goNamedUnderlying(ft); named {
			ft = u
		}
		if fn, isFunc := ft.(transpiler.FuncType); isFunc {
			return fn, nil
		}
	}

	// Step 2: function metadata lookup — handles lambdas passed as named
	// function-call args when the function has named parameters. Returns the
	// declared param type with the call's type arguments substituted, as for a
	// positional argument.
	if callCtx.funcMeta != nil && len(callCtx.funcMeta.ParamNames) > 0 {
		for i, paramName := range callCtx.funcMeta.ParamNames {
			if paramName == argName && i < len(callCtx.funcMeta.ParamTypes) {
				return t.substituteTranspilerTypeParams(callCtx.funcMeta.ParamTypes[i], callCtx.inferredTypeSubst), nil
			}
		}
	}

	// Step 3: sealed-variant case constructor. Variants register an empty
	// companion struct, but the per-field types live on the parent sealed type's
	// SealedVariants; the call's type arguments (explicit or inferred, see
	// collectFunctionCallContext) are substituted as for a generic struct.
	if callCtx.variantErr != nil {
		return transpiler.NilType{}, callCtx.variantErr
	}
	if sv := callCtx.variant; sv != nil {
		for i, fieldName := range sv.FieldNames {
			if fieldName == argName && i < len(sv.FieldTypes) {
				if transpiler.IsUnusable(sv.FieldTypes[i]) {
					break
				}
				return t.substituteTranspilerTypeParams(sv.FieldTypes[i], callCtx.structTypeSubst), nil
			}
		}
	}

	return transpiler.NilType{}, nil
}

// sealedVariantForCall returns the sealed variant a call constructs, with its
// parent sealed type, or nils. The lookup is scoped to the variant's own
// package (from the call's qualified name) so a same-named variant in a
// sibling package cannot shadow the local one; see findSealedVariant.
func (t *galaASTTransformer) sealedVariantForCall(fun ast.Expr, line, col int) (*transpiler.SealedVariant, *transpiler.TypeMetadata, error) {
	typeName, qualifiedName := extractTypeNameFromExpr(fun)
	if typeName == "" {
		return nil, nil, nil
	}
	variantPkg := ""
	if qualifiedName != "" {
		resolvedTypeName := t.resolveStructTypeName(qualifiedName)
		if idx := strings.LastIndex(resolvedTypeName, "."); idx != -1 {
			variantPkg = resolvedTypeName[:idx]
		}
	}
	sv, err := t.findSealedVariant(typeName, variantPkg, line, col)
	if err != nil || sv == nil {
		return nil, nil, err
	}
	return sv, t.findSealedParentForVariant(typeName, variantPkg), nil
}

// findSealedVariant returns the SealedVariant metadata for a variant name by
// searching parent sealed types in typeMetas. The optional pkgQualifier
// restricts the search to a specific package; when empty, the current
// package is searched first (so a local variant always shadows a same-
// named variant in an imported sealed type) and dot-imported packages
// are searched as a deterministic fallback. Returns (variant, nil) when
// a match is found, (nil, nil) when no sealed parent contains the name,
// and (nil, err) when an unqualified call site matches in two or more
// dot-imported packages — mirrors the same package-aware lookup
// discipline as findSealedVariantFields so a same-named variant in a
// sibling package cannot shadow the local one through Go map iteration
// order.
//
// The expected-type propagation path in resolveNamedArgExpectedFuncType
// is what makes this lookup load-bearing: when a named arg's value is a
// nested generic call (e.g. `Wrap(Field = opt.Map((e) => e.Error()))`),
// the call-site's expected slot type must come from the local variant's
// FieldTypes — a wrong-package match would push the wrong type onto the
// expectedArgTypes stack and break type-param inference for the nested
// call, silently falling back to `any` in the generated Go.
func (t *galaASTTransformer) findSealedVariant(variantName, pkgQualifier string, line, col int) (*transpiler.SealedVariant, error) {
	// Resolve a possible import alias to the actual package name.
	actualPkg := pkgQualifier
	if pkgQualifier != "" {
		if resolved, ok := t.importManager.ResolveAlias(pkgQualifier); ok {
			actualPkg = resolved
		}
	}

	// First pass: explicitly named package, or current package when no
	// qualifier. Gives local declarations precedence.
	primaryPkg := actualPkg
	if primaryPkg == "" {
		primaryPkg = t.packageName
	}

	if primaryPkg != "" {
		for _, meta := range t.typeMetas {
			if !meta.IsSealed || meta.Package != primaryPkg {
				continue
			}
			for i := range meta.SealedVariants {
				if meta.SealedVariants[i].Name == variantName {
					return &meta.SealedVariants[i], nil
				}
			}
		}
	}

	// When the caller explicitly qualified the variant, do not fall
	// through to other packages — the qualifier is authoritative.
	if pkgQualifier != "" {
		return nil, nil
	}

	// Second pass: dot-imported packages. Collect every match so we can
	// reject ambiguity instead of silently picking by import order.
	type dotMatch struct {
		pkg     string
		variant *transpiler.SealedVariant
	}
	var matches []dotMatch
	for _, dotPkg := range t.importManager.GetDotImports() {
		if dotPkg == "" || dotPkg == t.packageName {
			continue
		}
		for _, meta := range t.typeMetas {
			if !meta.IsSealed || meta.Package != dotPkg {
				continue
			}
			for i := range meta.SealedVariants {
				if meta.SealedVariants[i].Name == variantName {
					matches = append(matches, dotMatch{pkg: dotPkg, variant: &meta.SealedVariants[i]})
				}
			}
		}
	}
	switch len(matches) {
	case 0:
		return nil, nil
	case 1:
		return matches[0].variant, nil
	default:
		pkgs := make([]string, len(matches))
		for i, m := range matches {
			pkgs[i] = m.pkg
		}
		return nil, galaerr.NewCodedSemanticError(
			galaerr.CodeAmbiguousSealedVariant,
			line, col,
			fmt.Sprintf("ambiguous sealed-variant reference: case %q is declared in multiple dot-imported packages (%s)", variantName, strings.Join(pkgs, ", ")),
			fmt.Sprintf("qualify the call site with the package name, e.g. `%s.%s(...)`", pkgs[0], variantName),
		)
	}
}

// functionCallContext bundles the expected-type lookups used when
// transforming a regular (non-method) function call's arguments. All fields
// may be nil/empty independently when the corresponding metadata is absent.
type functionCallContext struct {
	funcMeta *transpiler.FunctionMetadata
	// goFuncParamTypes are the callee's parameter types when it has no GALA
	// function metadata: a Go function or variable, a conversion to a named
	// function type, or a value of function type (see calleeFuncType).
	goFuncParamTypes         []transpiler.Type
	structFieldExpectedTypes []transpiler.Type
	inferredTypeSubst        map[string]string
	// typeArgPlaceholders: inferredTypeSubst fills some type parameter with
	// an `any` placeholder (see inferFuncTypeSubstFromArgs), so the call's
	// argument slots are open.
	typeArgPlaceholders bool
	// A generic struct / sealed-variant constructor's type parameters and the
	// type arguments known before its lambdas are lowered (structCtorTypeSubst).
	structTypeParams []string
	structTypeSubst  map[string]string
	// structLiteral: the call's positional arguments build a struct literal
	// (see positionalCallBuildsStructLiteral), so each fills a field.
	structLiteral bool
	// goStruct is the Go type info of the Go struct the call names when it
	// is not a GALA struct or function (see goStructTypeData), else nil.
	goStruct *transpiler.GoTypeData
	// The sealed variant the call constructs and its parent (nil otherwise),
	// and the lookup's ambiguity error, reported for a named argument.
	variant       *transpiler.SealedVariant
	variantParent *transpiler.TypeMetadata
	variantErr    error
	// slotType is the type of the slot the call's value fills, when pushed
	// (see lowerAgainst): it supplies a generic struct literal's type
	// arguments that its fields leave undetermined.
	slotType transpiler.Type
	// applyMethodMeta is set when the call site is of the form `Type[T](args)`
	// and Type has an Apply method. It lets the argument-transformation pass
	// see the Apply method's expected parameter types (with type-param
	// substitutions from `applyTypeSubst`) so lambda arguments can infer
	// concrete return / parameter types.  Without this, lambdas passed to
	// companion-Apply calls would be transformed before Section 10 resolves
	// them and would miss their expected type entirely.
	applyMethodMeta *transpiler.MethodMetadata
	applyTypeSubst  map[string]string
	// applyTypeParams carries the companion type's own type-param names so the
	// argument pass can mask unresolved Apply parameter result types (see
	// resolveExpectedArgType's companion-Apply branch).
	applyTypeParams []string
}

// collectFunctionCallContext handles Section 5 of the call dispatcher:
// gather all metadata used during argument transformation. slotType is the
// type of the slot the call fills, when pushed (see functionCallContext).
// Extracted from transformCallWithArgsCtx as part of A1 cont.
func (t *galaASTTransformer) collectFunctionCallContext(fun ast.Expr, argListCtx *grammar.ArgumentListContext, slotType transpiler.Type) functionCallContext {
	ctx := functionCallContext{slotType: slotType}

	// Look up GALA function metadata for expected parameter types
	// (enables void lambda detection and type-param inference).
	if funcName := t.extractFuncName(fun); funcName != "" {
		ctx.funcMeta = t.getFunction(funcName)
	}

	// A bare name bound in scope is that binding, as for getFunction: not a Go
	// function or a named function type that happens to share its name.
	shadowed := false
	if id, isIdent := fun.(*ast.Ident); isIdent {
		shadowed = t.shadowingScope(id.Name) != nil
	}

	// When GALA function metadata is not available, try Go type
	// info. Handles Go-defined functions and variables with function types
	// (e.g., concurrent.Spawn) called via dot-imports or qualified references.
	if ctx.funcMeta == nil && !shadowed && t.goTypeInfo != nil {
		if funcName := t.extractFuncName(fun); funcName != "" {
			ctx.goFuncParamTypes = t.resolveGoFuncParamTypes(funcName)
		}
	}

	// A conversion to a named function type (`type Handler func(int) int`,
	// then `Handler((x) => x)`) takes one argument of that function type, so
	// a lambda converted this way is typed by it. So is one converted to a Go
	// named function type (`http.HandlerFunc((w, r) => …)`), and one
	// converted to an instantiated generic alias (`Conv[int, string]((x) =>
	// …)`) by the alias's signature with the type arguments substituted.
	if ctx.funcMeta == nil && !shadowed && ctx.goFuncParamTypes == nil {
		if ft := t.conversionFuncType(fun); ft != nil {
			ctx.goFuncParamTypes = []transpiler.Type{*ft}
		}
	}

	// A call of a value of function type (a parameter, var, val or call
	// result) takes its parameter types from that value's type, a generic
	// alias's type arguments substituted (`visit Visitor[int]` for `type
	// Visitor[T any] func(func(T))` is a `func(func(int))`).
	if ctx.funcMeta == nil && ctx.goFuncParamTypes == nil {
		if ft := t.calleeFuncType(fun); ft != nil {
			ctx.goFuncParamTypes = ft.Params
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
					ctx.structLiteral = argListCtx != nil &&
						t.positionalCallBuildsStructLiteral(typeMeta, fields, len(argListCtx.AllArgument()))
					ctx.structFieldExpectedTypes = make([]transpiler.Type, len(fields))
					for i, fieldName := range fields {
						if ft, ok := fieldTypes[fieldName]; ok {
							ctx.structFieldExpectedTypes[i] = ft
						}
					}
					// Generic struct: a lambda for a `func(T) T` field must see
					// the call's type arguments, not the declared `T`.
					if structMeta := t.getTypeMeta(resolved); structMeta != nil && len(structMeta.TypeParams) > 0 {
						typeParams, ctorFun, fromSlot := t.structCtorSlotArgs(fun, funcName, resolved, typeMeta.TypeParams, slotType)
						ctx.structTypeParams = typeParams
						ctx.structTypeSubst = t.structCtorTypeSubst(ctorFun, typeParams, fields, ctx.structFieldExpectedTypes, argListCtx, fromSlot)
						for i, ft := range ctx.structFieldExpectedTypes {
							ctx.structFieldExpectedTypes[i] = t.substituteTranspilerTypeParams(ft, ctx.structTypeSubst)
						}
					}
				}
			}
		}
	}

	// A Go struct named-argument construction can build: one of the
	// package's own .go files, or of an imported Go package.
	if ctx.funcMeta == nil && ctx.structFieldExpectedTypes == nil && hasNamedArg(argListCtx) {
		ctx.goStruct = t.goStructTypeData(fun)
	}

	// For generic functions without explicit type args (e.g.,
	// Iterate(1, (x) => x * 2)), pre-scan non-lambda arguments to infer
	// type params so that lambda params get concrete types.
	if ctx.funcMeta != nil && len(ctx.funcMeta.TypeParams) > 0 {
		explicit := explicitTypeArgSubst(ctx.funcMeta.TypeParams, t.extractFuncCallTypeArgs(fun))
		if len(explicit) == len(ctx.funcMeta.TypeParams) {
			ctx.inferredTypeSubst = explicit
		} else {
			// None or only some written (`Using[Res](r, (x) => …)`): Go infers
			// the rest from the arguments, and so does this, so no lambda is
			// lowered against a bare type-parameter name.
			ctx.inferredTypeSubst, ctx.typeArgPlaceholders = t.inferFuncTypeSubstFromArgs(ctx.funcMeta, argListCtx, explicit)
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
					ctx.applyTypeParams = typeMeta.TypeParams
					ctx.applyTypeSubst = explicitTypeArgSubst(typeMeta.TypeParams, t.extractFuncCallTypeArgs(fun))
					// A partial list (`Mk[int](2, (x) => …)`) is completed
					// from the arguments as a generic function call's is, so
					// no argument is lowered against a bare type-parameter name.
					if n := len(ctx.applyTypeSubst); n > 0 && n < len(typeMeta.TypeParams) && argListCtx != nil {
						ctx.applyTypeSubst, ctx.typeArgPlaceholders = t.inferFuncTypeSubstFromArgs(&transpiler.FunctionMetadata{
							TypeParams: typeMeta.TypeParams,
							ParamTypes: applyMeta.ParamTypes,
							ParamNames: applyMeta.ParamNames,
						}, argListCtx, ctx.applyTypeSubst)
					}
				}
			}
		}
	}

	// Sealed-variant case constructor, looked up once per call (a function or
	// a struct with fields is never one). A generic sealed type gets the same
	// two-phase inference as a generic struct, over the variant's fields.
	if ctx.applyMethodMeta != nil && ctx.funcMeta == nil && len(ctx.structFieldExpectedTypes) == 0 {
		line, col := argListCtx.GetStart().GetLine(), argListCtx.GetStart().GetColumn()
		ctx.variant, ctx.variantParent, ctx.variantErr = t.sealedVariantForCall(fun, line, col)
	}
	if sv, parent := ctx.variant, ctx.variantParent; sv != nil && parent != nil && len(parent.TypeParams) > 0 {
		ctx.structTypeParams = parent.TypeParams
		ctx.structTypeSubst = t.structCtorTypeSubst(fun, parent.TypeParams, sv.FieldNames, sv.FieldTypes, argListCtx, nil)
		if len(ctx.applyTypeSubst) == 0 {
			ctx.applyTypeSubst = ctx.structTypeSubst
		}
	}

	return ctx
}

// structCtorSlotArgs returns, for the construction fun of the generic struct
// resolved named funcName, the type parameters its field types are written
// over, fun as structCtorTypeSubst reads its explicit type arguments, and the
// type arguments known from the slot it fills — bound as for the literal itself
// (see structLiteralType). typeParams are funcName's own. A generic alias
// (`Fn((x) => x + 1)` for `type Fn[U any] Box[U]`) is read as the struct it
// names: the fields are the struct's, so its type parameters are, with what
// the alias and its written type arguments fix of them; an alias of an
// instantiated generic (`IntH(...)`) is kept as written.
func (t *galaASTTransformer) structCtorSlotArgs(fun ast.Expr, funcName, resolved string, typeParams []string, slotType transpiler.Type) ([]string, ast.Expr, map[string]transpiler.Type) {
	target, isAlias := t.lookupTypeAlias(funcName)
	if !isAlias {
		return typeParams, fun, t.slotTypeArgs(slotType, resolved, typeParams)
	}
	structMeta := t.getTypeMeta(resolved)
	named, isGeneric := t.aliasedStructType(target, typeParams)
	if structMeta == nil || !isGeneric || len(named.Params) != len(structMeta.TypeParams) {
		return typeParams, fun, nil
	}
	base, written := splitCallFunTypeArgs(fun)
	if len(written) > len(typeParams) {
		// More type arguments than the alias takes: Go reports it at the call.
		return typeParams, fun, nil
	}
	fromSlot := t.aliasFixedStructArgs(named, structMeta.TypeParams, typeParams, t.writtenTypeArgs(typeParams, written))
	for tp, typ := range t.slotTypeArgs(t.followAliasChain(slotType), resolved, structMeta.TypeParams) {
		if _, bound := fromSlot[tp]; !bound {
			fromSlot[tp] = typ
		}
	}
	return structMeta.TypeParams, base, fromSlot
}

// explicitTypeArgSubst maps typeParams to a call's explicit type arguments, or
// returns nil when there are none.
func explicitTypeArgSubst(typeParams, typeArgs []string) map[string]string {
	if len(typeArgs) == 0 {
		return nil
	}
	subst := make(map[string]string, len(typeParams))
	for i, tp := range typeParams {
		if i < len(typeArgs) {
			subst[tp] = typeArgs[i]
		}
	}
	return subst
}

// typeSubstStrings converts an inferred substitution to the string form
// substituteTranspilerTypeParams takes; nil when empty.
func typeSubstStrings(inferred map[string]transpiler.Type) map[string]string {
	if len(inferred) == 0 {
		return nil
	}
	subst := make(map[string]string, len(inferred))
	for k, v := range inferred {
		subst[k] = v.String()
	}
	return subst
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

// tryTransformValWithApply handles Section 12: when `fun` reads a val/var —
// `name`, or an imported `pkg.Name` — whose type has an Apply method, rewrite
// `fun(args)` as `fun.Apply(args)`. This enables `val add5 = Adder(5); add5(10)`.
// Returns handled=false for all other shapes of `fun`.
// Extracted from transformCallWithArgsCtx as part of A1 cont.
func (t *galaASTTransformer) tryTransformValWithApply(fun ast.Expr, args []ast.Expr) (ast.Expr, bool) {
	b, ok := t.bindingRef(fun)
	if !ok || b.typ.IsNil() {
		return nil, false
	}
	varTypeName := b.typ.BaseName()
	typeMeta := t.getTypeMeta(varTypeName)
	if typeMeta == nil {
		return nil, false
	}
	apply, hasApply := typeMeta.Methods["Apply"]
	if !hasApply {
		return nil, false
	}
	// An empty call, `v()`, goes through Apply only when Apply takes no
	// parameters; otherwise it is left as written, for Go to report.
	if len(args) == 0 && apply != nil && len(apply.ParamTypes) > 0 {
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
//	Section 5.5 Value called as a function              — checkValueCalledAsFunction
//	Section 6  Argument transformation                  — transformFunctionArgs
//	Section 7  Named-args dispatch                      — handleNamedArgs(Func|)Call
//	Section 8  Default-arg injection                    — fillDefaultArgs
//	Section 9  StructMeta[T]() intrinsic                — transformStructMetaConstruction
//	Section 10 Companion Apply / struct construction    — tryTransformCompanionApplyOrStructCtor
//	Section 11 CompositeLit with Apply                  — tryTransformCompositeLitApply
//	Section 12 Variable with Apply method               — tryTransformValWithApply
//	Section 12.9 Type name called as a constructor      — checkTypeUsedAsConstructor
//	Section 13 Fallback: emit call verbatim             — inline
//
// When extending call-site behavior, add or modify a single section's helper
// rather than growing this dispatcher. Each helper is independently testable
// and carries its own doc comment describing the sub-path it handles.
func (t *galaASTTransformer) transformCallWithArgsCtx(fun ast.Expr, argListCtx *grammar.ArgumentListContext) (ast.Expr, error) {
	// Bare Go builtins (append, len, panic, ...) are a hard error: they are the
	// last symbols that resolve with no import and no GALA declaration. Reject
	// them here where the call target and the symbol tables are both available.
	// Report at the callee identifier (the primary expr) so the caret spans the
	// builtin name; fall back to the argument list start if the walk-up fails.
	fl, fc, exact := primaryStartOf(argListCtx)
	if !exact {
		fl, fc = argListCtx.GetStart().GetLine(), argListCtx.GetStart().GetColumn()
	}
	if err := t.checkForbiddenGoBuiltinCall(fun, fl, fc, exact); err != nil {
		return nil, err
	}

	// Consume the expected-type hint (pushed by lowerAgainst
	// for the immediately-enclosing call). Removed eagerly so nested arg
	// transforms inside this call don't pick up the outer call's expectation
	// (B1).
	pendingExpected := t.expectedArgTypes.consume()

	// Sealed-variant type-arg propagation: when the expected type is a
	// generic sealed parent (e.g. `Step[int]`) and `fun` names one of its
	// variants without explicit type args (`StepA(...)`), inject the parent's
	// type args into `fun` so downstream codegen emits `StepA[int]{}.Apply(...)`.
	if pendingExpected != nil && !pendingExpected.IsNil() {
		if rewritten, ok := t.injectSealedVariantTypeArgs(fun, pendingExpected); ok {
			fun = rewritten
		}
	}

	// --- Section 1: Copy method short-circuit ---
	if sel, ok := fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "Copy" {
		// Skip the struct-Copy short-circuit when the receiver is a package
		// identifier — `io.Copy(dst, src)` is a regular package-qualified
		// function call, not a struct `.Copy(field = value)` override.
		// Without this guard, the dispatcher tried to type-infer the package
		// name "io" as a struct receiver and bailed out with "type of
		// receiver unknown".
		if id, ok := sel.X.(*ast.Ident); ok && t.importManager.IsPackage(id.Name) {
			// Fall through to package-qualified function dispatch.
		} else {
			return t.transformCopyCall(sel.X, argListCtx)
		}
	}

	// --- Section 2: Method/function dispatch prelude ---
	// Classify `fun` as either a package-qualified function call (receiver==nil)
	// or a method call (receiver, method, typeArgs populated). Extracted into
	// splitCallTarget as part of A1.
	receiver, method, typeArgs := t.splitCallTarget(fun)

	// A1: resolve the receiver type to a canonical form and compute the
	// package-agnostic lookup key used by the generic-method registry.
	recvType, lookupBaseName := t.resolveReceiverTypeAndLookupKey(receiver, method)

	// Check for generic method - try all possible package lookups
	isGenericMethod := len(typeArgs) > 0 || t.isGenericMethodWithImports(lookupBaseName, recvType.GetPackage(), method)

	// --- Section 3: Generic method -> standalone function rewrite ---
	if receiver != nil && isGenericMethod {
		handled, expr, err := t.tryTransformGenericMethodAsFunction(argListCtx, receiver, method, typeArgs, recvType, lookupBaseName, pendingExpected,
			argListCtx)
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
	callCtx := t.collectFunctionCallContext(fun, argListCtx, pendingExpected)

	// --- Section 5.5: a value whose type is not a function, called anyway ---
	// Checked before the arguments are transformed, so named arguments or an
	// argument that fails to transform cannot preempt it with an error about
	// the arguments. A type with an Apply method is left to section 12. See
	// value_called_as_function.go.
	if err := t.checkValueCalledAsFunction(fun, argListCtx); err != nil {
		return nil, err
	}

	// --- Section 6: Argument transformation ---
	// Walks the argument list, classifying each arg as positional or named,
	// and resolves an expected type for each from the gathered callCtx.
	args, namedArgs, hasSpread, err := t.transformFunctionArgs(fun, argListCtx, callCtx)
	if err != nil {
		return nil, err
	}

	// --- Section 6.5: opaque-type conversion `UserID(x)` ---
	// A Go conversion; a direct conversion between two opaque types is
	// rejected (opaque.go). Any other arity is left to Go, which names it.
	if len(namedArgs) == 0 && !hasSpread {
		if target := t.opaqueConversionCallee(fun); target != nil {
			if len(args) == 1 {
				argCtx := argListCtx.Argument(0).(*grammar.ArgumentContext)
				if err := t.checkOpaqueConversion(target, t.astTypeToTranspilerType(fun), args[0], argCtx); err != nil {
					return nil, err
				}
			}
			return &ast.CallExpr{Fun: fun, Args: args}, nil
		}
	}

	// --- Section 7: Named-args dispatch ---
	if len(namedArgs) > 0 {
		if callCtx.funcMeta != nil && len(callCtx.funcMeta.ParamNames) > 0 {
			expr, err := t.handleNamedArgsFuncCall(fun, args, namedArgs, callCtx.funcMeta, callCtx.inferredTypeSubst, argListCtx.GetStart().GetLine(), argListCtx.GetStart().GetColumn())
			// A result-only type parameter comes from the slot, as for a
			// positional call (section 12.5), over the arguments in order.
			if call, ok := expr.(*ast.CallExpr); ok && err == nil {
				call.Fun, err = t.injectFuncPhantomTypeArgs(call.Fun, callCtx.funcMeta, call.Args, call.Ellipsis != token.NoPos, pendingExpected,
					argListCtx.GetStart().GetLine(), argListCtx.GetStart().GetColumn())
			}
			if err != nil {
				return nil, err
			}
			return expr, nil
		}
		return t.handleNamedArgsCall(fun, args, namedArgs, callCtx, argListCtx)
	}

	// --- Section 8: Default-arg injection for under-filled positional calls ---
	if callCtx.funcMeta != nil && len(callCtx.funcMeta.DefaultExprs) > 0 && len(args) < len(callCtx.funcMeta.ParamTypes) {
		filled, err := t.fillDefaultArgs(args, callCtx.funcMeta, callCtx.inferredTypeSubst, argListCtx.GetStart().GetLine(), argListCtx.GetStart().GetColumn())
		if err != nil {
			return nil, err
		}
		args = filled
	}

	// --- Section 9: StructMeta[T]() compiler intrinsic ---
	typeName := t.getBaseTypeName(fun)
	if isStructMetaIntrinsic(typeName) {
		return t.transformStructMetaConstruction(fun, argListCtx.GetStart().GetLine(), argListCtx.GetStart().GetColumn())
	}

	// --- Section 10: Companion Apply / positional struct construction ---
	if typeName != "" {
		handled, expr, err := t.tryTransformCompanionApplyOrStructCtor(fun, typeName, args, pendingExpected,
			argListCtx.GetStart().GetLine(), argListCtx.GetStart().GetColumn())
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

	// --- Section 12.5: Fill phantom return-only type params ---
	// A generic free function whose type param appears only in its return type
	// (not in any parameter) cannot have that param inferred by Go from the
	// call arguments. Emit explicit type args resolved from the type of the
	// slot the call fills, so the generated Go is concrete rather than an
	// uninstantiated `Fn(args)` that fails with "cannot infer".
	if fun, err = t.injectFuncPhantomTypeArgs(fun, callCtx.funcMeta, args, hasSpread, pendingExpected,
		argListCtx.GetStart().GetLine(), argListCtx.GetStart().GetColumn()); err != nil {
		return nil, err
	}

	// --- Section 12.9: a type name called as a constructor ---
	// Runs last, immediately before the verbatim fallback: every constructive
	// reading (struct ctor, companion Apply, sealed variant) is resolved in the
	// sections above and has already returned, so reaching here means the
	// callee names a type and nothing callable. See type_as_constructor.go.
	if err := t.checkTypeUsedAsConstructor(fun, fl, fc, exact); err != nil {
		return nil, err
	}

	// --- Section 13: Fallback — emit the call verbatim. ---
	// The go_builtins.Panic wrapper lowers to Go's builtin `panic` in EVERY
	// position (statement, match-arm tail, value-returning function body). A
	// void wrapper call is not a Go terminating statement, so a value/tail
	// position would fail with "missing return"; the builtin terminates. The
	// now-unused go_builtins import is pruned by the import cleanup pass. GALA
	// source never spells bare `panic` — only emitted Go does, exactly like the
	// `.Size()` sugar's `len()`.
	return t.lowerPanicWrapperToBuiltin(&ast.CallExpr{Fun: fun, Args: args, Ellipsis: ellipsisPos(hasSpread)}), nil
}

// handleNamedArgsCall is a thin dispatcher for named-argument calls. It
// classifies the call site (GALA struct, sealed variant companion, or
// Go-imported type) and delegates the heavy lifting to a section helper:
//
//  1. extractTypeNameFromExpr — decode `fun` into (typeName, qualifiedName)
//  2. findSealedVariantFields → buildSealedVariantApplyCall (fields=[])
//  3. buildStructLiteralWithNamedArgs (registered GALA struct)
//  4. buildGoCompositeLiteralWithNamedArgs (a Go struct of the package's own
//     .go files or of an imported Go package, checked against its fields;
//     else a Go-imported or dot-imported type with no type info)
//  5. fallthrough → coded semantic error
//
// Keeping the body a dispatcher matches the established A1 pattern for
// `transformCallWithArgsCtx` in this file: the numbered sections map to
// the helpers below and are easy to navigate.
func (t *galaASTTransformer) handleNamedArgsCall(fun ast.Expr, args []ast.Expr, namedArgs map[string]ast.Expr, callCtx functionCallContext, argListCtx *grammar.ArgumentListContext) (ast.Expr, error) {
	line, col := argListCtx.GetStart().GetLine(), argListCtx.GetStart().GetColumn()
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
			// Scope the sealed-parent lookup to the variant's own package.
			// resolvedTypeName is fully qualified (e.g. "pkg_a.B"), so the
			// segment before the last "." is the package the variant came
			// from — using it prevents a same-named variant in another
			// package from shadowing the local one via map-iteration order.
			variantPkg, _ := splitPackageQualifier(resolvedTypeName)
			variantFieldNames, found, ferr := t.findSealedVariantFields(typeName, variantPkg, line, col)
			if ferr != nil {
				return nil, ferr
			}
			if found {
				variantFun, err := t.inferVariantTypeArgs(fun, callCtx.variant, callCtx.variantParent, namedArgs, line, col)
				if err != nil {
					return nil, err
				}
				return buildSealedVariantApplyCall(variantFun, variantFieldNames, namedArgs), nil
			}
		}
		// 3. Regular GALA struct construction with named args. A named
		// argument matching no field is reported as itself: before the
		// missing-field check, which would otherwise name the field the author
		// thought they had just written, and before type-argument inference,
		// which would blame the type argument that field was to bind.
		if err := checkUnknownStructFields(qualifiedName, fields, argListCtx); err != nil {
			return nil, err
		}
		// Another package's struct is not constructible here when the literal
		// would set a field private to that package.
		provided := func(_ int, name string) bool { _, ok := namedArgs[name]; return ok }
		if err := t.checkPrivateFieldCtor(fun, t.getTypeMeta(resolvedTypeName), fields, provided, argListCtx); err != nil {
			return nil, err
		}
		return t.buildStructLiteralWithNamedArgs(fun, typeName, resolvedTypeName, fields, namedArgs, callCtx.slotType, line, col)
	}

	// 4. A Go struct the Go type info describes: one the package's own
	// hand-written .go files declare, or one from an imported Go package. Its
	// fields are plain Go fields, never Immutable-wrapped, so the named
	// arguments become a Go composite literal as they are.
	if td := callCtx.goStruct; td != nil {
		if err := checkUnknownStructFields(qualifiedName, td.FieldOrder, argListCtx); err != nil {
			return nil, err
		}
		if len(args) > 0 {
			return nil, galaerr.NewSemanticErrorAt(line, col, fmt.Sprintf(
				"%s is a Go struct: construct it with named arguments only, one per field", qualifiedName))
		}
		if _, written := splitCallFunTypeArgs(fun); len(written) < len(td.TypeParams) {
			return nil, galaerr.NewSemanticErrorAt(line, col, fmt.Sprintf(
				"%s is a generic Go struct: write its type arguments, e.g. `%s[%s](...)`",
				qualifiedName, qualifiedName, strings.Join(td.TypeParams, ", ")))
		}
		return buildGoCompositeLiteralWithNamedArgs(fun, namedArgs, t.sortKeyValueExprs), nil
	}
	// Otherwise a Go-imported type with no type info (direct or dot-imported).
	if t.isGoImportedType(fun) {
		return buildGoCompositeLiteralWithNamedArgs(fun, namedArgs, t.sortKeyValueExprs), nil
	}
	// Dot-imported types (bare or generic). The base of a generic
	// instantiation `Foo[A]` / `Foo[A, B]` is wrapped in IndexExpr /
	// IndexListExpr; unwrap to the underlying ident before checking the
	// dot-import set, otherwise generic Go-style block-form structs
	// declared in a dot-imported package fall through to the "named
	// arguments only supported..." error while their non-generic peers
	// succeed.
	if unwrapToBaseIdent(fun) != nil {
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

// checkUnknownStructFields reports named arguments that match no field of the
// struct being constructed (a shorthand or block-form GALA struct, or a Go
// struct the Go type info describes), pointing at the first one, and a field
// named twice. Unlike the required-field check it applies to every struct: the
// arguments are collected by name, so a second value for a field replaced the
// first, and for a GALA struct the literal is built from its fields, so an
// argument naming none of them was dropped.
func checkUnknownStructFields(qualifiedName string, fields []string, argListCtx *grammar.ArgumentListContext) error {
	var unknown []string
	var line, col int
	seen := make(map[string]bool)
	for _, argCtx := range argListCtx.AllArgument() {
		id := argCtx.(*grammar.ArgumentContext).Identifier()
		if id == nil {
			continue
		}
		name := id.GetText()
		if seen[name] {
			return galaerr.NewSemanticErrorAt(id.GetStart().GetLine(), id.GetStart().GetColumn(),
				fmt.Sprintf("field %q is given more than once in construction of %q", name, qualifiedName))
		}
		seen[name] = true
		if slices.Contains(fields, name) {
			continue
		}
		if len(unknown) == 0 {
			line, col = id.GetStart().GetLine(), id.GetStart().GetColumn()
		}
		unknown = append(unknown, name)
	}
	if len(unknown) == 0 {
		return nil
	}
	return unknownStructFieldError(qualifiedName, unknown, fields, line, col)
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

// inferVariantTypeArgs adds type arguments to a generic sealed variant built
// with named arguments and none or only its leading type arguments written,
// inferred from the argument values against the variant's field types. With
// none written it returns fun unchanged when inference cannot bind every type
// parameter; a partial list that cannot be completed is an error.
func (t *galaASTTransformer) inferVariantTypeArgs(fun ast.Expr, sv *transpiler.SealedVariant, parent *transpiler.TypeMetadata, namedArgs map[string]ast.Expr, line, col int) (ast.Expr, error) {
	base, written := splitCallFunTypeArgs(fun)
	switch base.(type) {
	case *ast.Ident, *ast.SelectorExpr:
	default:
		return fun, nil
	}
	if sv == nil || parent == nil || len(written) >= len(parent.TypeParams) {
		return fun, nil
	}
	inferred := t.writtenTypeArgs(parent.TypeParams, written)
	for i, fieldName := range sv.FieldNames {
		if val, ok := namedArgs[fieldName]; ok && i < len(sv.FieldTypes) {
			t.unifyFieldArgForInference(sv.FieldTypes[i], val, parent.TypeParams, inferred)
		}
	}
	instantiated, missing := t.completeTypeArgs(base, parent.TypeParams, written, inferred)
	switch {
	case missing == nil:
		return instantiated, nil
	case len(written) > 0:
		_, qualified := extractTypeNameFromExpr(base)
		pkgQualifier, bareName := splitPackageQualifier(qualified)
		return nil, t.uninferredVariantError(sealedVariant{parent, pkgQualifier, bareName}, "(...)", inferred, missing, line, col)
	}
	return fun, nil
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
	slotType transpiler.Type,
	line, col int,
) (ast.Expr, error) {
	immutFlags := t.structImmutFields[resolvedTypeName]
	fieldTypes := t.structFieldTypes[resolvedTypeName]

	provided := func(_ int, fieldName string) bool { _, ok := namedArgs[fieldName]; return ok }
	typeExpr, err := t.inferTypeArgsFromNamedArgs(fun, typeName, resolvedTypeName, fields, namedArgs, slotType, line, col)
	if err != nil {
		return nil, t.preferMissingFieldError(err, typeName, resolvedTypeName, fields, provided, line, col)
	}
	typeArgSubst := t.structTypeArgSubst(typeExpr, resolvedTypeName)

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
			valExpr = t.wrapImmutableFieldValue(val, fieldTypes[fieldName], typeArgSubst)
		}
		elts = append(elts, &ast.KeyValueExpr{Key: ast.NewIdent(fieldName), Value: valExpr})
	}

	// Fields the call site left out take their declared default; one with no
	// default is required, and omitting it is an error rather than a silent
	// Go zero value. See struct_defaults.go.
	defaulted, err := t.fillOmittedStructFields(typeName, resolvedTypeName, fields, provided, typeArgSubst, line, col)
	if err != nil {
		return nil, err
	}
	elts = append(elts, defaulted...)

	return &ast.CompositeLit{Type: typeExpr, Elts: elts}, nil
}

// inferTypeArgsFromPositionalArgs returns the literal type for a positional
// generic struct construction; see structLiteralType. fields gives the
// position → field-name mapping.
func (t *galaASTTransformer) inferTypeArgsFromPositionalArgs(
	fun ast.Expr,
	typeName, resolvedTypeName string,
	fields []string,
	args []ast.Expr,
	slotType transpiler.Type,
	line, col int,
) (ast.Expr, error) {
	fieldTypes := t.structFieldTypes[resolvedTypeName]
	return t.structLiteralType(fun, typeName, resolvedTypeName, slotType, line, col,
		func(typeParams []string, inferred map[string]transpiler.Type) {
			for i, fieldName := range fields {
				if i >= len(args) {
					break
				}
				if fieldType, ok := fieldTypes[fieldName]; ok {
					t.unifyFieldArgForInference(fieldType, args[i], typeParams, inferred)
				}
			}
		})
}

// inferTypeArgsFromNamedArgs returns the literal type for a named-argument
// generic struct construction; see structLiteralType.
func (t *galaASTTransformer) inferTypeArgsFromNamedArgs(
	fun ast.Expr,
	typeName, resolvedTypeName string,
	fields []string,
	namedArgs map[string]ast.Expr,
	slotType transpiler.Type,
	line, col int,
) (ast.Expr, error) {
	fieldTypes := t.structFieldTypes[resolvedTypeName]
	return t.structLiteralType(fun, typeName, resolvedTypeName, slotType, line, col,
		func(typeParams []string, inferred map[string]transpiler.Type) {
			for _, fieldName := range fields {
				if val, ok := namedArgs[fieldName]; ok {
					if fieldType, ok := fieldTypes[fieldName]; ok {
						t.unifyFieldArgForInference(fieldType, val, typeParams, inferred)
					}
				}
			}
		})
}

// preferMissingFieldError returns the omitted-required-field error for a
// construction whose type arguments could not be inferred, when there is one:
// the missing field is often the one that would have bound them. Otherwise it
// returns inferErr.
func (t *galaASTTransformer) preferMissingFieldError(
	inferErr error,
	typeName, resolvedTypeName string,
	fields []string,
	provided func(i int, fieldName string) bool,
	line, col int,
) error {
	if _, missingErr := t.fillOmittedStructFields(typeName, resolvedTypeName, fields, provided, nil, line, col); missingErr != nil {
		return missingErr
	}
	return inferErr
}

// structLiteralType adds type arguments to a generic struct literal's type
// when the call site wrote none: those the field values determine (bind), then
// any left from slotType when it names the same struct (`val t Tag[int] =
// Tag(N = 1)`). A type parameter still undetermined is an error rather than an
// uninstantiated `Box{...}` literal, which Go rejects. fun is returned as is
// when it already has type arguments, names a non-generic type, or is an alias
// of an instantiated generic (`type IntPair Pair[int]`).
func (t *galaASTTransformer) structLiteralType(
	fun ast.Expr,
	typeName, resolvedTypeName string,
	slotType transpiler.Type,
	line, col int,
	bind func(typeParams []string, inferred map[string]transpiler.Type),
) (ast.Expr, error) {
	base, written := splitCallFunTypeArgs(fun)
	switch base.(type) {
	case *ast.Ident, *ast.SelectorExpr:
	default:
		return fun, nil
	}
	target, isAlias := t.lookupTypeAlias(typeName)
	if isAlias && typeName == resolvedTypeName && len(t.structFields[resolvedTypeName]) == 0 {
		// An alias of a field-less struct resolves to its own (field-less)
		// entry; its type parameters are the struct's it names. (A name with
		// fields is a struct, whatever alias of that name an import declares.)
		resolvedTypeName = t.resolveStructTypeName(t.followAliasChain(target).BaseName())
	}
	typeMeta := t.getTypeMeta(resolvedTypeName)
	if typeMeta == nil {
		return fun, nil
	}
	if isAlias && typeName != resolvedTypeName {
		return t.aliasLiteralType(fun, typeName, target, resolvedTypeName, typeMeta.TypeParams, slotType, line, col, bind)
	}
	if len(written) >= len(typeMeta.TypeParams) {
		return fun, nil
	}

	inferred := t.structTypeArgs(typeMeta.TypeParams, t.writtenTypeArgs(typeMeta.TypeParams, written), resolvedTypeName, slotType, bind)
	instantiated, missing := t.completeTypeArgs(base, typeMeta.TypeParams, written, inferred)
	if missing != nil {
		return nil, t.uninferredTypeArgError(line, col, base, nil, typeMeta.TypeParams, inferred, missing)
	}
	return instantiated, nil
}

// structTypeArgs binds the type parameters of the generic struct
// resolvedTypeName that a construction determines: those in seed (the written
// ones, a leading part of the list as in `Pair[int](1, "a")` for `Pair[A,
// B]`), then the expected type's, then the fields' (bind). The expected type
// binds before the fields do, so an untyped constant takes the slot's type
// (`Box(1)` as a `Box[int64]` is a `Box[int64]`, not a `Box[int]`). seed is
// extended in place and returned.
func (t *galaASTTransformer) structTypeArgs(
	typeParams []string,
	seed map[string]transpiler.Type,
	resolvedTypeName string,
	slotType transpiler.Type,
	bind func(typeParams []string, inferred map[string]transpiler.Type),
) map[string]transpiler.Type {
	for tp, typ := range t.slotTypeArgs(slotType, resolvedTypeName, typeParams) {
		if _, bound := seed[tp]; !bound {
			seed[tp] = typ
		}
	}
	bind(typeParams, seed)
	return seed
}

// aliasLiteralType is structLiteralType for a construction fun through the
// alias typeName of the generic struct resolvedTypeName (whose type parameters
// are structParams), target being the type the alias names. An alias of an
// instantiated generic (`type IntPair Pair[int]`), or one whose type arguments
// are all written, is returned as is. A generic alias written without (all of)
// its type arguments (`Twin(1, 2)` for `type Twin[T any] Pair[T]`) takes them
// from the struct's: what the alias and its written type arguments fix of
// them, then the expected type and the fields, matched against the struct type
// the alias names, so `Twin(1, 2)` is a `Twin[int]`. An alias type parameter
// still undetermined is an error, as for the struct itself.
func (t *galaASTTransformer) aliasLiteralType(
	fun ast.Expr,
	typeName string,
	target transpiler.Type,
	resolvedTypeName string,
	structParams []string,
	slotType transpiler.Type,
	line, col int,
	bind func(typeParams []string, inferred map[string]transpiler.Type),
) (ast.Expr, error) {
	base, written := splitCallFunTypeArgs(fun)
	aliasMeta := t.getTypeMeta(typeName)
	if aliasMeta == nil || len(written) >= len(aliasMeta.TypeParams) {
		return fun, nil
	}
	aliasParams := aliasMeta.TypeParams
	inferred := t.writtenTypeArgs(aliasParams, written)

	// The struct type the alias names (`Pair[T]`) is the only route from the
	// struct's type arguments to the alias's: a parameter it does not mention
	// (`B` of `type Weird[A any, B any] Pair[A]`) must be written.
	named, isGeneric := t.aliasedStructType(target, aliasParams)
	if isGeneric && len(named.Params) == len(structParams) {
		fixed := t.aliasFixedStructArgs(named, structParams, aliasParams, inferred)
		structArgs := t.structTypeArgs(structParams, fixed, resolvedTypeName, t.followAliasChain(slotType), bind)
		// The first binding wins, as for the struct's own type parameters: a
		// later one that disagrees is left to Go's type check of the literal.
		for i, tp := range structParams {
			if typ, has := structArgs[tp]; has && !transpiler.IsUnusable(typ) {
				t.unifyForInference(named.Params[i], typ, aliasParams, inferred)
			}
		}
	}

	instantiated, missing := t.completeTypeArgs(base, aliasParams, written, inferred)
	if missing != nil {
		// An annotation spelled with the alias binds a missing type parameter
		// only through the struct type it names; for one that type does not
		// mention, the error asks for explicit type arguments instead.
		var yields transpiler.Type // nil: as for the struct itself
		if !isGeneric || !typeMentionsAllTypeParams(named, missing) {
			yields = transpiler.NilType{}
		}
		return nil, t.uninferredTypeArgError(line, col, base, yields, aliasParams, inferred, missing)
	}
	return instantiated, nil
}

// aliasedStructType is the generic type the alias target names, through any
// chain of aliases, over the alias's type parameters by their bare names (an
// imported alias's target spells them qualified, `shapes.T`).
func (t *galaASTTransformer) aliasedStructType(target transpiler.Type, aliasParams []string) (transpiler.GenericType, bool) {
	bare := make([]transpiler.Type, len(aliasParams))
	for i, tp := range aliasParams {
		bare[i] = transpiler.BasicType{Name: tp}
	}
	named, ok := t.substituteConcreteTypes(t.followAliasChain(target), aliasParams, bare).(transpiler.GenericType)
	return named, ok
}

// aliasFixedStructArgs returns what the alias fixes of its struct's type
// arguments (`int` of `type IntKeyed[V any] Entry[int, V]`), with the alias's
// type arguments bound so far substituted, keyed by the struct's type
// parameter. named is the struct type the alias names (aliasedStructType).
// Whether an argument is fixed is read before substituting: a bound type
// argument may itself be spelled with a type parameter of the enclosing
// function that shares an alias parameter's name (`Fn[T]` in `func g[T any]`).
func (t *galaASTTransformer) aliasFixedStructArgs(named transpiler.GenericType, structParams, aliasParams []string, bound map[string]transpiler.Type) map[string]transpiler.Type {
	unbound := make([]string, 0, len(aliasParams))
	for _, tp := range aliasParams {
		if _, has := bound[tp]; !has {
			unbound = append(unbound, tp)
		}
	}
	fixed := make(map[string]transpiler.Type)
	for i, tp := range structParams {
		if !typeMentionsTypeParam(named.Params[i], unbound) {
			fixed[tp] = t.substituteInType(named.Params[i], bound)
		}
	}
	return fixed
}

// typeMentionsAllTypeParams reports whether typ mentions every one of typeParams.
func typeMentionsAllTypeParams(typ transpiler.Type, typeParams []string) bool {
	for _, tp := range typeParams {
		if !typeMentionsTypeParam(typ, []string{tp}) {
			return false
		}
	}
	return true
}

// slotTypeArgs returns the type arguments slotType gives the generic struct
// resolvedTypeName (a structFields key) with typeParams, keyed by type
// parameter: none unless slotType instantiates that struct. A type argument
// naming a type parameter nothing has bound (the `B` of a callee's
// `Pair[A, B]`) gives nothing.
func (t *galaASTTransformer) slotTypeArgs(slotType transpiler.Type, resolvedTypeName string, typeParams []string) map[string]transpiler.Type {
	gen, ok := slotType.(transpiler.GenericType)
	if !ok || len(gen.Params) != len(typeParams) || t.resolveStructTypeName(gen.Base.String()) != resolvedTypeName {
		return nil
	}
	args := make(map[string]transpiler.Type, len(typeParams))
	for i, tp := range typeParams {
		if p := gen.Params[i]; t.slotTypeArgUsable(p) {
			args[tp] = p
		}
	}
	return args
}

// resultSlotTypeArgs is slotTypeArgs for a value whose type is result over
// typeParams — what a companion Apply or a generic function returns: the type
// arguments slotType gives typeParams where result matches it
// (`Pair[int, string]` binds both of a `Mk[A, B]` whose Apply returns
// `Pair[A, B]`).
func (t *galaASTTransformer) resultSlotTypeArgs(result transpiler.Type, typeParams []string, slotType transpiler.Type) map[string]transpiler.Type {
	if transpiler.IsUnusable(slotType) || transpiler.IsUnusable(result) {
		return nil
	}
	args := make(map[string]transpiler.Type, len(typeParams))
	t.unifyForInference(result, t.followAliasChain(slotType), typeParams, args)
	maps.DeleteFunc(args, func(_ string, p transpiler.Type) bool { return !t.slotTypeArgUsable(p) })
	return args
}

// slotTypeArgUsable reports whether p, a type argument an expected type
// gives, binds a type parameter: not one naming a type parameter nothing has
// bound (the `B` of a callee's `Pair[A, B]`).
func (t *galaASTTransformer) slotTypeArgUsable(p transpiler.Type) bool {
	return !typeHasMaskedPart(p) && !t.typeMentionsUnresolvedTypeParam(p)
}

// uninferredTypeArgError reports the type parameters of the generic
// construction base that neither its fields or arguments nor its expected
// type determine. yields is the type a companion construction's value has,
// over typeParams (what its Apply returns); nil means base is a struct,
// whose value is its own instantiation.
//
// The examples it prints are valid GALA where the call is: the constructor,
// and a type of its package, are named as they are reachable there
// (callSiteQualifier), and every type argument is the one the construction
// does fix, or the placeholder `int` for one it leaves open. The remedy is
// part of the message, so the error carries no separate hint (see
// isUninferredTypeArgError).
func (t *galaASTTransformer) uninferredTypeArgError(line, col int, base ast.Expr, yields transpiler.Type, typeParams []string, inferred map[string]transpiler.Type, missing []string) error {
	_, qualified := extractTypeNameFromExpr(base)
	return t.uninferredTypeArgErrorNamed(line, col, t.callSiteName(qualified), yields, typeParams, inferred, missing)
}

// uninferredTypeArgErrorNamed is uninferredTypeArgError for a callee spelled
// name at the call site.
func (t *galaASTTransformer) uninferredTypeArgErrorNamed(line, col int, name string, yields transpiler.Type, typeParams []string, inferred map[string]transpiler.Type, missing []string) error {
	args := t.hintTypeArgTypes(typeParams, inferred)
	typeArgs := joinDisplayTypes(args)
	what, valueType := "generic struct "+name+" from its fields", name+"["+typeArgs+"]"
	if yields != nil {
		what, valueType = name+" from its arguments", ""
		// An annotation binds the missing ones only through the type the
		// value has.
		if typeMentionsTypeParam(yields, missing) {
			valueType = displayType(t.reachedAs(t.substituteConcreteTypes(yields, typeParams, args)))
		}
	}
	return galaerr.NewCodedSemanticError(galaerr.CodeUninferredTypeArgument, line, col, fmt.Sprintf(
		"cannot infer type argument %s of %s or the expected type; %s",
		strings.Join(missing, ", "), what, inferenceRemedy(valueType, name, typeArgs, "(...)")), "")
}

// valueYields is ret, the type a companion Apply or generic function returns,
// as uninferredTypeArgError's yields: never nil, which would mean a struct.
func valueYields(ret transpiler.Type) transpiler.Type {
	if ret == nil {
		return transpiler.NilType{}
	}
	return ret
}

// inferenceRemedy is the remedy every "cannot infer type argument" hint
// gives for the constructor ctor, called with args (`(...)`, `()`): annotate
// the binding with valueType — omitted when "", as no annotation binds the
// missing type arguments — or pass typeArgs explicitly.
func inferenceRemedy(valueType, ctor, typeArgs, args string) string {
	explicit := fmt.Sprintf("pass type args explicitly (`%s[%s]%s`)", ctor, typeArgs, args)
	if valueType == "" {
		return explicit
	}
	return fmt.Sprintf("annotate the binding (e.g. `val x %s = %s%s`) or %s", valueType, ctor, args, explicit)
}

// reachedAs spells typ for a hint at the call site: every named type in it
// with the qualifier the file reaches its package by (see callSiteQualifier),
// bare for this package's own.
func (t *galaASTTransformer) reachedAs(typ transpiler.Type) transpiler.Type {
	switch ty := typ.(type) {
	case transpiler.GenericType:
		params := make([]transpiler.Type, len(ty.Params))
		for i, p := range ty.Params {
			params[i] = t.reachedAs(p)
		}
		return transpiler.GenericType{Base: t.reachedAs(ty.Base), Params: params}
	case transpiler.ArrayType:
		return transpiler.ArrayType{Elem: t.reachedAs(ty.Elem)}
	case transpiler.NamedType:
		if ty.Package == t.packageName {
			return transpiler.BasicType{Name: ty.Name}
		}
		if t.importManager != nil {
			if e, ok := t.importManager.GetByPkgName(ty.Package); ok {
				prefix := t.callSiteQualifier(e.Alias)
				// A bare name the package's own type, or a type parameter in
				// scope, shadows stays qualified.
				if prefix == "" && !e.IsDot {
					r := t.resolveTypeMetaName(ty.Name)
					if t.activeTypeParams[ty.Name] || (r != "" && r != ty.Package+"."+ty.Name) {
						prefix = e.Alias + "."
					}
				}
				return transpiler.BasicType{Name: prefix + ty.Name}
			}
		}
	}
	return typ
}

// hintTypeArgTypes is the type argument list of an inference hint: per type
// parameter, the type the call already fixes as the call site names it
// (reachedAs), or the placeholder `int`.
func (t *galaASTTransformer) hintTypeArgTypes(typeParams []string, inferred map[string]transpiler.Type) []transpiler.Type {
	args := make([]transpiler.Type, len(typeParams))
	for i, tp := range typeParams {
		args[i] = transpiler.BasicType{Name: "int"}
		if typ, ok := inferred[tp]; ok && !transpiler.ContainsUnusable(typ) {
			args[i] = t.reachedAs(typ)
		}
	}
	return args
}

// joinDisplayTypes spells types as a GALA type argument list.
func joinDisplayTypes(types []transpiler.Type) string {
	names := make([]string, len(types))
	for i, typ := range types {
		names[i] = displayType(typ)
	}
	return strings.Join(names, ", ")
}

// completeTypeArgs instantiates base with a type argument for every one of
// typeParams: those written at the call site, which keep their own spelling,
// then the inferred ones. It returns the type parameters left without one
// instead when inference did not determine them all.
func (t *galaASTTransformer) completeTypeArgs(base ast.Expr, typeParams []string, written []ast.Expr, inferred map[string]transpiler.Type) (ast.Expr, []string) {
	typeArgs := slices.Clone(written)
	var missing []string
	for _, tp := range typeParams[len(written):] {
		if typ, ok := inferred[tp]; ok && !transpiler.IsUnusable(typ) {
			typeArgs = append(typeArgs, t.typeToExpr(typ))
		} else {
			missing = append(missing, tp)
		}
	}
	if missing != nil {
		return nil, missing
	}
	return withTypeArgs(base, typeArgs), nil
}

// uninferredCallTypeArgError is uninferredTypeArgError for a call with
// arguments args whose value has type yields: a generic type called through
// its companion Apply, or a generic function. When an argument is a call into
// a Go package whose types were not loaded — the usual cause, as in
// `Try(term.MakeRaw(fd))` — the error names the call and the package instead
// of asking for a type argument, with a hint (see isUninferredTypeArgError:
// no other slot can fix it).
func (t *galaASTTransformer) uninferredCallTypeArgError(line, col int, base ast.Expr, yields transpiler.Type, typeParams []string, inferred map[string]transpiler.Type, missing []string, args []ast.Expr) error {
	if err := t.unknownArgTypeError(line, col, t.argCalleeName(base), missing, args, false); err != nil {
		return err
	}
	return t.uninferredTypeArgError(line, col, base, valueYields(yields), typeParams, inferred, missing)
}

// argCalleeName spells the callee base for unknownArgTypeError: `Try`, as
// written, not `std.Try`.
func (t *galaASTTransformer) argCalleeName(base ast.Expr) string {
	_, qualified := extractTypeNameFromExpr(base)
	return t.callSiteName(stripStdPrefix(qualified))
}

// unknownArgTypeError reports the type parameters missing of the callee
// spelled name, which its arguments args should have determined but whose
// types are unknown: an argument that calls into a Go package whose types
// were not loaded is named, with that package. Otherwise it returns nil, or
// with always, a generic form. Both carry a hint, which marks the error as one
// no other slot can fix (see isUninferredTypeArgError).
func (t *galaASTTransformer) unknownArgTypeError(line, col int, name string, missing []string, args []ast.Expr, always bool) error {
	for _, arg := range args {
		if callee, pkgPath, ok := t.unloadedGoPackageCall(arg); ok {
			return galaerr.NewCodedSemanticError(galaerr.CodeUninferredTypeArgument, line, col,
				fmt.Sprintf("cannot infer type argument %s of %s: the type of its argument `%s(...)` is unknown",
					strings.Join(missing, ", "), name, callee),
				fmt.Sprintf("the type information of Go package %q could not be loaded; require its module "+
					"in gala.mod (`gala mod add --go <module>`)", pkgPath))
		}
	}
	if !always {
		return nil
	}
	return galaerr.NewCodedSemanticError(galaerr.CodeUninferredTypeArgument, line, col,
		fmt.Sprintf("cannot infer type argument %s of %s: the type of an argument it depends on is unknown",
			strings.Join(missing, ", "), name),
		"bind the argument to a `val` with a declared type, or pass every type argument explicitly")
}

// unloadedGoPackageCall reports whether expr is an untyped call `pkg.F(...)`
// into an imported package this file has no type information for, returning
// the callee as written and the package's import path.
func (t *galaASTTransformer) unloadedGoPackageCall(expr ast.Expr) (callee, path string, ok bool) {
	call, isCall := expr.(*ast.CallExpr)
	if !isCall || t.richAST == nil {
		return "", "", false
	}
	fun, _ := splitCallFunTypeArgs(call.Fun)
	sel, isSel := fun.(*ast.SelectorExpr)
	if !isSel {
		return "", "", false
	}
	qualifier, isIdent := sel.X.(*ast.Ident)
	if !isIdent {
		return "", "", false
	}
	path, ok = t.importManager.PathForQualifier(qualifier.Name)
	if !ok || t.importPackageName(path) != "" {
		return "", "", false
	}
	// A standard-library package (no dot in its first element) is never
	// required in gala.mod; its types are missing only without a Go SDK.
	if first, _, _ := strings.Cut(path, "/"); !strings.Contains(first, ".") {
		return "", "", false
	}
	if typ := t.getExprTypeName(call); typ != nil && !typ.IsNil() && !transpiler.IsUnusable(typ) {
		return "", "", false
	}
	return formatExprForTrace(fun), path, true
}

// callSiteName spells a type name, as the transformer qualifies it, the way
// the call site can write it (see callSiteQualifier).
func (t *galaASTTransformer) callSiteName(qualified string) string {
	pkgQualifier, bareName := splitPackageQualifier(qualified)
	return t.callSiteQualifier(pkgQualifier) + bareName
}

// withTypeArgs instantiates base with typeArgs (none leaves it as is).
func withTypeArgs(base ast.Expr, typeArgs []ast.Expr) ast.Expr {
	switch len(typeArgs) {
	case 0:
		return base
	case 1:
		return &ast.IndexExpr{X: base, Index: typeArgs[0]}
	}
	return &ast.IndexListExpr{X: base, Indices: typeArgs}
}

// writtenTypeArgs binds the leading typeParams to the type arguments written
// at a call site (fewer than typeParams), seeding inference to complete a
// partial list; completeTypeArgs then emits them as written.
func (t *galaASTTransformer) writtenTypeArgs(typeParams []string, written []ast.Expr) map[string]transpiler.Type {
	bound := make(map[string]transpiler.Type, len(typeParams))
	for i, arg := range written {
		bound[typeParams[i]] = t.astTypeToTranspilerType(arg)
	}
	return bound
}

// unifyFieldArgForInference binds the struct type parameters that the value
// supplied for a field determines. The field type is matched structurally, so a
// type parameter reached only through a nested type (`Items Array[T]`) or a
// function type (`Make func() T`, given an already-lowered lambda) is bound as
// well as a bare `T` field.
func (t *galaASTTransformer) unifyFieldArgForInference(fieldType transpiler.Type, val ast.Expr, typeParams []string, inferred map[string]transpiler.Type) {
	valType := t.getExprTypeName(val)
	if transpiler.IsUnusable(valType) {
		return
	}
	t.unifyForInference(fieldType, valType, typeParams, inferred)
}

// orderedTypeArgExprs returns the type-argument expressions for typeParams in
// declaration order, or nil unless every one of them was inferred.
func (t *galaASTTransformer) orderedTypeArgExprs(typeParams []string, inferred map[string]transpiler.Type) []ast.Expr {
	if len(typeParams) == 0 {
		return nil
	}
	exprs := make([]ast.Expr, len(typeParams))
	for i, tp := range typeParams {
		typ, ok := inferred[tp]
		if !ok || transpiler.IsUnusable(typ) {
			return nil
		}
		exprs[i] = t.typeToExpr(typ)
	}
	return exprs
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

// unwrapToBaseIdent recurses through generic instantiation wrappers
// (IndexExpr for `Foo[A]`, IndexListExpr for `Foo[A, B]`) to return the
// underlying *ast.Ident base, or nil if the base is anything else (e.g.
// a SelectorExpr, which is the qualified `pkg.Type` shape handled by
// isGoImportedType). Used to recognize `Foo`, `Foo[A]`, and `Foo[A, B]`
// uniformly as dot-import candidates.
func unwrapToBaseIdent(expr ast.Expr) *ast.Ident {
	for {
		switch e := expr.(type) {
		case *ast.Ident:
			return e
		case *ast.IndexExpr:
			expr = e.X
		case *ast.IndexListExpr:
			expr = e.X
		default:
			return nil
		}
	}
}

// hasNamedArg reports whether a call passes any argument by name.
func hasNamedArg(argListCtx *grammar.ArgumentListContext) bool {
	return firstNamedArg(argListCtx) >= 0
}

// goStructTypeData returns the Go type info of the Go struct a call target
// names (`Bag`, `pkg.Bag`, `pkg.Box[int]`): a type the package's own
// hand-written .go files declare, or one an imported Go package does. It
// returns nil for anything else, including a type the Go type info does not
// describe.
func (t *galaASTTransformer) goStructTypeData(fun ast.Expr) *transpiler.GoTypeData {
	_, qualifiedName := extractTypeNameFromExpr(fun)
	if t.goTypeInfo == nil || qualifiedName == "" {
		return nil
	}
	if td := t.goTypeInfo.GetTypeData(t.goTypeKey(qualifiedName)); td != nil && td.Kind == "struct" {
		return td
	}
	return nil
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

// findSealedVariantFields looks up the field names for a sealed variant by
// searching parent sealed types in typeMetas. The optional pkgQualifier
// restricts the search to a specific package; when empty, the current
// package is searched first (so a local variant always shadows a same-
// named variant in an imported sealed type) and dot-imported packages
// are searched as a deterministic fallback. Returns (fields, true, nil)
// when a parent sealed type is found — the fields slice may be empty
// for zero-arg variants, which is distinct from "not found". Returns
// (nil, false, nil) when no sealed parent contains the name. Returns
// (nil, false, err) when an unqualified call site matches in two or
// more dot-imported packages: GALA mirrors Go's rule that an ambiguous
// name across imports is a compile error, not a silent first-wins.
//
// The package-aware search prevents a non-deterministic map-iteration
// match. Without it, two packages declaring same-named cases (e.g. a
// local `case B(P string)` and an imported `case B`) could resolve to
// either variant depending on Go's map iteration order; picking the
// wrong one drops field info and the caller emits a bare zero-value
// struct literal, losing the constructor args entirely. The fallback
// is intentionally limited to dot-imports because that is the only
// case where a same-named variant can legitimately appear at the call
// site without a qualifier — qualified references go through the
// pkgQualifier path above.
func (t *galaASTTransformer) findSealedVariantFields(variantName, pkgQualifier string, line, col int) ([]string, bool, error) {
	// Resolve a possible import alias to the actual package name.
	actualPkg := pkgQualifier
	if pkgQualifier != "" {
		if resolved, ok := t.importManager.ResolveAlias(pkgQualifier); ok {
			actualPkg = resolved
		}
	}

	// First pass: explicitly named package, or current package when no
	// qualifier. Gives local declarations precedence.
	primaryPkg := actualPkg
	if primaryPkg == "" {
		primaryPkg = t.packageName
	}

	if primaryPkg != "" {
		for _, meta := range t.typeMetas {
			if !meta.IsSealed || meta.Package != primaryPkg {
				continue
			}
			for _, sv := range meta.SealedVariants {
				if sv.Name == variantName {
					return sv.FieldNames, true, nil
				}
			}
		}
	}

	// When the caller explicitly qualified the variant, do not fall
	// through to other packages — the qualifier is authoritative and a
	// miss should propagate as such (the caller's struct-literal branch
	// handles the not-found case).
	if pkgQualifier != "" {
		return nil, false, nil
	}

	// Second pass: dot-imported packages. Collect every match so we can
	// reject ambiguity instead of silently picking by import order. The
	// dot-import slice itself is appended in declared order, so the walk
	// is deterministic regardless of typeMetas' map iteration order;
	// determinism only matters here for the error path's package list.
	type dotMatch struct {
		pkg    string
		fields []string
	}
	var matches []dotMatch
	for _, dotPkg := range t.importManager.GetDotImports() {
		if dotPkg == "" || dotPkg == t.packageName {
			continue
		}
		for _, meta := range t.typeMetas {
			if !meta.IsSealed || meta.Package != dotPkg {
				continue
			}
			for _, sv := range meta.SealedVariants {
				if sv.Name == variantName {
					matches = append(matches, dotMatch{pkg: dotPkg, fields: sv.FieldNames})
				}
			}
		}
	}
	switch len(matches) {
	case 0:
		return nil, false, nil
	case 1:
		return matches[0].fields, true, nil
	default:
		pkgs := make([]string, len(matches))
		for i, m := range matches {
			pkgs[i] = m.pkg
		}
		return nil, false, galaerr.NewCodedSemanticError(
			galaerr.CodeAmbiguousSealedVariant,
			line, col,
			fmt.Sprintf("ambiguous sealed-variant reference: case %q is declared in multiple dot-imported packages (%s)", variantName, strings.Join(pkgs, ", ")),
			fmt.Sprintf("qualify the call site with the package name, e.g. `%s.%s(...)`", pkgs[0], variantName),
		)
	}
}

// uninferredVariantError is GALA-E0018 for a call at line/col to the variant
// v. args is how the call is printed after the constructor (`()`, `(...)`);
// inferred holds the type parameters the call does fix, and missing, when
// known, names the ones left unbound.
func (t *galaASTTransformer) uninferredVariantError(v sealedVariant, args string, inferred map[string]transpiler.Type, missing []string, line, col int) error {
	params := ""
	if len(missing) > 0 {
		params = " " + strings.Join(missing, ", ")
	}
	return galaerr.NewCodedSemanticError(
		galaerr.CodeSealedVariantUninferred,
		line, col,
		fmt.Sprintf("cannot infer type parameter%s for sealed variant constructor %q", params, v.bareName+args),
		t.uninferredVariantHint(v.parent, v.pkgQualifier, v.bareName, args, inferred),
	)
}

// sealedVariant is a call target resolved to the generic sealed type it is a
// variant of; parent is nil when it is not one.
type sealedVariant struct {
	parent                 *transpiler.TypeMetadata
	pkgQualifier, bareName string
}

// sealedVariantOf resolves typeName, as a call site spells it (`std.Failure`),
// to the sealed type it is a variant of.
func (t *galaASTTransformer) sealedVariantOf(typeName string) sealedVariant {
	pkgQualifier, bareName := splitPackageQualifier(typeName)
	return sealedVariant{t.findSealedParentForVariant(bareName, pkgQualifier), pkgQualifier, bareName}
}

// uninferredVariantHint builds the GALA-E0018 remediation hint. args is the
// argument list the examples print after the constructor: `()` for a zero-arg
// variant, `(...)` for one called with arguments. Every example form it prints
// must be valid, copy-pasteable GALA at the offending call site. Three things
// decide that:
//
//   - a type annotation follows the binding name with NO colon
//     (`val x Box[int] = Empty()`);
//   - GALA's primitive spellings are lowercase (`int`, not `Int`); and
//   - both the parent type and the constructor must be spelled the way they
//     are actually reachable in this file.
//
// The parent sealed type comes from the metadata the caller already resolved to
// decide this diagnostic applies, so the annotation names a type that actually
// exists rather than a `ParentType` placeholder the user would have to
// translate.
//
// The qualifier is the subtle part. The call site's `typeName` reaches the
// caller already resolved, so its package selector may be one the user never
// typed: a bare `None()` lowers to `std.None` via the prelude. Printing that
// qualifier unconditionally yields `val x std.Option[int] = std.None()`, which
// does not compile in a file that never imports std under that name. Dropping
// it unconditionally is just as wrong the other way: for `cmdpkg.NoCmd()`
// behind a plain `import "…/cmdpkg"`, the bare `val x Cmd[int] = NoCmd()`
// fails with `undefined: NoCmd`.
//
// So the qualifier is printed exactly when it names a package this file can
// actually qualify with — an ordinary (non-dot) import, under whatever alias
// the file bound it to. Dot-imported packages and the std prelude bring their
// symbols into scope unqualified and are printed bare. An unnamed parent
// degrades the hint to the explicit-type-args form, which still carries the
// constructor exactly as the call site spells it — never to an example that
// would not compile.
func (t *galaASTTransformer) uninferredVariantHint(parent *transpiler.TypeMetadata, pkgQualifier, bareName, args string, inferred map[string]transpiler.Type) string {
	prefix := t.callSiteQualifier(pkgQualifier)

	// One type argument per type parameter of the parent: the one the call
	// already fixes, or a placeholder (`Either[string, int]` for `Left("x")`).
	typeArgs := "int"
	if parent != nil && len(parent.TypeParams) > 0 {
		typeArgs = joinDisplayTypes(t.hintTypeArgTypes(parent.TypeParams, inferred))
	}
	valueType := ""
	if parent != nil && parent.Name != "" {
		valueType = prefix + parent.Name + "[" + typeArgs + "]"
	}
	return inferenceRemedy(valueType, prefix+bareName, typeArgs, args)
}

// callSiteQualifier returns the `pkg.` prefix that a diagnostic should print
// in front of a symbol from pkgQualifier, or "" when the symbol is reachable
// unqualified in the file being transformed.
//
// It returns a prefix only for packages brought in by an ordinary import,
// using the alias the file actually bound (so `import c "…/cmdpkg"` yields
// `c.`). Dot imports and packages that are not imported here at all — most
// notably the std prelude, whose selector the resolver attaches to names the
// user wrote bare — yield "" because qualifying those would name something
// that is not in scope.
func (t *galaASTTransformer) callSiteQualifier(pkgQualifier string) string {
	if pkgQualifier == "" || t.importManager == nil {
		return ""
	}
	if !t.importManager.IsPackage(pkgQualifier) {
		// Not an ordinary import in this file: std prelude, same package, or
		// a dot import the resolver qualified behind the user's back.
		return ""
	}
	pkgName := pkgQualifier
	if resolved, ok := t.importManager.ResolveAlias(pkgQualifier); ok {
		pkgName = resolved
	}
	if t.importManager.IsDotImported(pkgName) {
		return ""
	}
	// A prelude package is an implicit dot import. Its entry here is one the
	// file did not write (seeded from the package's metadata), not an import
	// the user reaches it through.
	if e, ok := t.importManager.GetByAlias(pkgQualifier); ok && e.Implicit() && registry.Global.IsPreludePackage(pkgName) {
		return ""
	}
	return pkgQualifier + "."
}

// findSealedParentForVariant returns the parent sealed type's metadata for a
// given variant name, or nil if the name is not a known sealed variant.
//
// pkgQualifier is the optional package qualifier from the call site (e.g.
// "step" when the call target is `step.StepA(...)`). When non-empty it
// restricts the search to sealed parents whose package matches the
// qualifier; the qualifier is resolved through the import manager so an
// alias (`import alias "real/pkg"`) maps to the canonical package name
// before comparison.
func (t *galaASTTransformer) findSealedParentForVariant(variantName, pkgQualifier string) *transpiler.TypeMetadata {
	// Resolve a possible import alias to the actual package name; metadata
	// is registered under the real package, not the alias.
	actualPkg := pkgQualifier
	if pkgQualifier != "" {
		if resolved, ok := t.importManager.ResolveAlias(pkgQualifier); ok {
			actualPkg = resolved
		}
	}

	for _, meta := range t.typeMetas {
		if !meta.IsSealed {
			continue
		}
		if pkgQualifier != "" {
			// Restrict to the named package — variants from other packages
			// might happen to share a name (e.g. two `Some` variants), and
			// using one parent's type args on another parent's variant
			// would produce nonsense.
			if meta.Package != pkgQualifier && meta.Package != actualPkg {
				continue
			}
		}
		for _, sv := range meta.SealedVariants {
			if sv.Name == variantName {
				return meta
			}
		}
	}
	return nil
}

// injectSealedVariantTypeArgs rewrites a bare sealed-variant reference (an
// Ident or package-qualified SelectorExpr without explicit type args) into
// the equivalent generic instantiation when the expected type is the
// variant's parent sealed type with concrete type arguments.
//
// For same-package call sites (`ArrayOf[Step[int]](StepA(N=1))`), the
// argument's expected type is `Step[int]` and `fun` is a bare `*ast.Ident`;
// this helper rewrites it to `StepA[int]`. For cross-package call sites
// (`ArrayOf[step.Step[int]](step.StepA(N=1))`) the variant reference is a
// `*ast.SelectorExpr` and the rewrite wraps the whole selector in an
// `*ast.IndexExpr`, producing `step.StepA[int]`. Either shape lets the
// downstream sealed-variant codegen emit `<Variant>[int]{}.Apply(...)`
// instead of an uninstantiated `<Variant>{}.Apply(...)` — Go cannot infer
// the variant's vestigial type parameter from an empty composite literal
// whose `T` does not appear in any field.
//
// Returns the rewritten expression and true on success; returns the original
// expression and false when the rewrite does not apply.
func (t *galaASTTransformer) injectSealedVariantTypeArgs(fun ast.Expr, expected transpiler.Type) (ast.Expr, bool) {
	// Already has explicit type args — nothing to do.
	switch fun.(type) {
	case *ast.IndexExpr, *ast.IndexListExpr:
		return fun, false
	}

	// Resolve the bare variant name (and optional package qualifier) from
	// the call target. SelectorExpr corresponds to qualified references
	// like `step.StepA`; bare Ident covers same-package calls.
	var (
		variantName  string
		pkgQualifier string
	)
	switch f := fun.(type) {
	case *ast.Ident:
		variantName = f.Name
	case *ast.SelectorExpr:
		variantName = f.Sel.Name
		if pkgIdent, ok := f.X.(*ast.Ident); ok {
			pkgQualifier = pkgIdent.Name
		}
	default:
		return fun, false
	}
	if variantName == "" {
		return fun, false
	}

	parent := t.findSealedParentForVariant(variantName, pkgQualifier)
	if parent == nil || len(parent.TypeParams) == 0 {
		return fun, false
	}

	// Expected type must be the parent sealed generic with concrete params.
	// The expected type's base is package-qualified for cross-package types
	// (e.g. `step.Step`), but TypeMetadata stores Name and Package
	// separately. Compare both forms so same-package references (where
	// `gen.BaseName()` is just `Step`) still match.
	gen, ok := expected.(transpiler.GenericType)
	if !ok {
		return fun, false
	}
	expectedBase := gen.BaseName()
	parentSimple := parent.Name
	parentQualified := parent.Name
	if parent.Package != "" {
		parentQualified = parent.Package + "." + parent.Name
	}
	if expectedBase != parentSimple && expectedBase != parentQualified {
		return fun, false
	}
	if len(gen.Params) != len(parent.TypeParams) {
		return fun, false
	}

	typeArgs := make([]ast.Expr, len(gen.Params))
	for i, p := range gen.Params {
		if transpiler.IsUnusable(p) {
			return fun, false
		}
		typeArgs[i] = t.typeToExpr(p)
	}
	if len(typeArgs) == 1 {
		return &ast.IndexExpr{X: fun, Index: typeArgs[0]}, true
	}
	return &ast.IndexListExpr{X: fun, Indices: typeArgs}, true
}

// funcDefaultArg is the default value of a function's i-th parameter at a call
// at line/col. typeSubst (may be nil) carries the type arguments the call binds,
// explicitly or by inference from the arguments it does pass.
func (t *galaASTTransformer) funcDefaultArg(funcMeta *transpiler.FunctionMetadata, i int, typeSubst map[string]string, line, col int) (ast.Expr, error) {
	src := defaultSource{
		DefaultExpr: funcMeta.DefaultExprs[i],
		file:        funcMeta.DefinedIn,
		pkg:         funcMeta.Package,
		typeParams:  funcMeta.TypeParams,
	}
	if i < len(funcMeta.ParamTypes) {
		t.substituteDeclared(&src, funcMeta.ParamTypes[i], parseTypeSubst(typeSubst))
	}
	return t.transformDefaultExpr(src, line, col)
}

// fillDefaultArgs fills missing positional arguments with default values from function metadata.
// Called when a function has defaults and fewer args were provided than parameters.
func (t *galaASTTransformer) fillDefaultArgs(args []ast.Expr, funcMeta *transpiler.FunctionMetadata, typeSubst map[string]string, line, col int) ([]ast.Expr, error) {
	totalParams := len(funcMeta.ParamTypes)
	result := make([]ast.Expr, totalParams)

	// Copy provided positional args
	copy(result, args)

	// Fill missing positions with defaults
	for i := len(args); i < totalParams; i++ {
		if _, hasDefault := funcMeta.DefaultExprs[i]; !hasDefault {
			return nil, galaerr.NewSemanticErrorAt(line, col, fmt.Sprintf(
				"missing required argument %q (parameter %d) in call to %s",
				funcMeta.ParamNames[i], i+1, funcMeta.Name))
		}
		expr, err := t.funcDefaultArg(funcMeta, i, typeSubst, line, col)
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
	typeSubst map[string]string,
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
			if _, hasDefault := funcMeta.DefaultExprs[i]; !hasDefault {
				paramName := ""
				if i < len(funcMeta.ParamNames) {
					paramName = funcMeta.ParamNames[i]
				}
				return nil, galaerr.NewSemanticErrorAt(line, col, fmt.Sprintf(
					"missing required argument %q (parameter %d) in call to %s",
					paramName, i+1, funcMeta.Name))
			}
			expr, err := t.funcDefaultArg(funcMeta, i, typeSubst, line, col)
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
	recvType transpiler.Type,
	typeSubst map[string]string,
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
			if _, hasDefault := methodMeta.DefaultExprs[i]; !hasDefault {
				paramName := ""
				if i < len(methodMeta.ParamNames) {
					paramName = methodMeta.ParamNames[i]
				}
				return nil, galaerr.NewSemanticErrorAt(line, col, fmt.Sprintf("missing required argument %q (parameter %d) in call to %s", paramName, i+1, methodMeta.Name))
			}
			expr, err := t.methodDefaultArg(methodMeta, i, callSiteReceiver, recvType, typeSubst, line, col)
			if err != nil {
				return nil, err
			}
			result[i] = expr
		}
	}
	return &ast.CallExpr{Fun: fun, Args: result}, nil
}

// fillDefaultArgsMethod fills missing positional arguments with default values from method metadata.
// callSiteReceiver is the actual receiver expression at the call site.
func (t *galaASTTransformer) fillDefaultArgsMethod(callSiteReceiver ast.Expr, args []ast.Expr, methodMeta *transpiler.MethodMetadata, recvType transpiler.Type, typeSubst map[string]string, line, col int) ([]ast.Expr, error) {
	totalParams := len(methodMeta.ParamTypes)
	result := make([]ast.Expr, totalParams)
	copy(result, args)
	for i := len(args); i < totalParams; i++ {
		if _, hasDefault := methodMeta.DefaultExprs[i]; !hasDefault {
			return nil, galaerr.NewSemanticErrorAt(line, col, fmt.Sprintf("missing required argument %q (parameter %d) in call to %s", methodMeta.ParamNames[i], i+1, methodMeta.Name))
		}
		expr, err := t.methodDefaultArg(methodMeta, i, callSiteReceiver, recvType, typeSubst, line, col)
		if err != nil {
			return nil, err
		}
		result[i] = expr
	}
	return result, nil
}

// lowerArg lowers one call argument against its slot's expected type: a direct
// lambda (lambdaCtx, from extractArgContent) through
// transformLambdaWithExpectedType, anything else through transformArgument.
// strict is the untyped-lambda-parameter policy (see transformArgument).
func (t *galaASTTransformer) lowerArg(
	exprCtx grammar.IExpressionContext,
	lambdaCtx *grammar.LambdaExpressionContext,
	s slot,
	strict bool,
) (ast.Expr, error) {
	if lambdaCtx != nil {
		return t.lowerLambdaArg(lambdaCtx, s, strict)
	}
	return t.transformArgument(exprCtx, s, strict)
}

// lowerLambdaArg lowers a lambda standing in argument slot s, marking it as the
// Try thunk when the slot is one.
func (t *galaASTTransformer) lowerLambdaArg(lambdaCtx *grammar.LambdaExpressionContext, s slot, strict bool) (ast.Expr, error) {
	if s.tryThunk {
		prev := t.tryThunkLambda
		t.tryThunkLambda = lambdaCtx
		defer func() { t.tryThunkLambda = prev }()
	}
	expectedRetType, expectedParamTypes, isFunc := t.lambdaExpectation(s.typ)
	if !isFunc && s.typ != nil && (s.typ.IsAny() || s.typ.String() == "interface{}") {
		// A slot typed `any` (a Go `func Register(h any)`) takes the lambda as
		// it is: each parameter is `any`, as the callee declares.
		expectedParamTypes = slices.Repeat([]transpiler.Type{s.typ}, lambdaParamCount(lambdaCtx))
	}
	lit, err := t.transformLambdaWithExpectedType(lambdaCtx, expectedRetType, expectedParamTypes, strict)
	if err != nil {
		return nil, err
	}
	return t.spreadLambdaGoResults(lit, s.typ, lambdaCtx)
}

// goTypeKey returns the Go type info key of a type named as written: a bare
// name the package's own .go files declare, or `qualifier.Name` of an import.
func (t *galaASTTransformer) goTypeKey(name string) string {
	if qualifier, bare, ok := strings.Cut(name, "."); ok {
		return t.goQualifiedName(qualifier, bare)
	}
	return t.ownGoTypeKey(name)
}

// conversionFuncType returns the function type a call of fun converts to when
// fun names a function type rather than a function: a GALA alias (through a
// chain of them; a generic one instantiated as written, `Conv[int, string]`),
// a Go type of the package's own .go files, or an imported Go one
// (`http.HandlerFunc`). It returns nil otherwise, including for a generic
// alias written without its type arguments, which names no type yet, and for
// a generic Go named type, whose signature is not instantiated here.
func (t *galaASTTransformer) conversionFuncType(fun ast.Expr) *transpiler.FuncType {
	name := t.extractFuncName(fun)
	if name == "" {
		return nil
	}
	if _, isAlias := t.typeAliases[name]; isAlias {
		return t.resolveTranspilerTypeAsFuncType(t.astTypeToTranspilerType(fun))
	}
	return t.goNamedFuncType(name)
}

// goNamedFuncType returns the underlying function type of the non-generic Go
// named function type name (`http.HandlerFunc`, or a type of the package's own
// .go files), or nil.
func (t *galaASTTransformer) goNamedFuncType(name string) *transpiler.FuncType {
	if ft, generic := t.goNamedFuncSignature(name); !generic {
		return ft
	}
	return nil
}

// goNamedFuncSignature returns the underlying function type of the Go named
// function type name, generic or not, and whether it is generic; nil when
// name names no Go function type.
func (t *galaASTTransformer) goNamedFuncSignature(name string) (*transpiler.FuncType, bool) {
	if t.goTypeInfo == nil {
		return nil, false
	}
	if td := t.goTypeInfo.GetTypeData(t.goTypeKey(name)); td != nil && td.Kind == "named" {
		if ft, isFunc := td.Underlying.(transpiler.FuncType); isFunc {
			return &ft, len(td.TypeParams) > 0
		}
	}
	return nil, false
}

// calleeFuncType returns the function type of the value a call's callee reads
// when the callee is a value rather than a declared function: a val, var or
// parameter (the val through its `.Get()` unwrap), or the result of another
// call, parenthesized or not. A value typed by an alias of a function type has
// that function type, a generic alias's type arguments substituted, and one
// typed by a Go named function type has its underlying signature. It returns
// nil for any other callee, and for a value whose parameter types name a type
// parameter the code being transformed does not bind, such as the result of a
// generic function whose type arguments the call leaves undetermined: no lambda
// may be lowered against it.
func (t *galaASTTransformer) calleeFuncType(fun ast.Expr) *transpiler.FuncType {
	fun = ast.Unparen(fun)
	var typ transpiler.Type
	var calleeTypeParams []string
	if b, bound := t.bindingRef(fun); bound {
		if t.loweringDefault != nil && (b.pkg != "" || t.shadowingScope(b.name) == nil) {
			// A default is lowered at its use site but was written in its
			// own scope (see shadowingScope). A binding the default makes
			// itself is in scope; otherwise a foreign default's names are its
			// package's, and the use site's locals were not in scope: only
			// this package's own top-level vals type its own defaults.
			if t.loweringForeignDefault() || b.pkg == "" && !t.isTopLevelBinding(b.name) {
				return nil
			}
		}
		typ = b.typ
		if transpiler.IsUnusable(typ) {
			if _, isIdent := fun.(*ast.Ident); isIdent {
				// A bare name with no recorded type: inference would look the
				// name up as a type or function, not as this binding.
				return nil
			}
			// A val with no recorded type, read through its unwrap:
			// inference may still know it.
			typ = t.getExprTypeName(fun)
		}
	} else if call, isCall := fun.(*ast.CallExpr); isCall {
		typ = t.getExprTypeName(fun)
		if meta := t.getFunction(t.extractFuncName(call.Fun)); meta != nil {
			// The callee's own type parameters, which its result may still
			// name even where the caller declares a type of that name; one
			// the enclosing declaration also declares is that declaration's.
			for _, tp := range meta.TypeParams {
				if !t.activeTypeParams[tp] {
					calleeTypeParams = append(calleeTypeParams, tp)
				}
			}
		}
	} else {
		return nil
	}
	ft := t.resolveTranspilerTypeAsFuncType(typ)
	if named, isNamed := typ.(transpiler.NamedType); ft == nil && isNamed && !t.declaresType(named.Package, named.Name) {
		// A Go named function type, which no GALA type of that name hides.
		ft = t.goNamedFuncType(named.String())
	}
	if ft == nil || funcTypeParamsMentionTypeParams(ft.Params, calleeTypeParams) {
		return nil
	}
	for _, p := range ft.Params {
		if t.mentionsUnboundTypeParam(p) {
			return nil
		}
	}
	return ft
}

// lambdaParamCount is the number of parameters lambda declares.
func lambdaParamCount(lambda *grammar.LambdaExpressionContext) int {
	params, ok := lambda.Parameters().(*grammar.ParametersContext)
	if !ok || params.ParameterList() == nil {
		return 0
	}
	return len(params.ParameterList().(*grammar.ParameterListContext).AllParameter())
}

// transformArgument lowers an expression standing in argument slot s.
// strict is the untyped-lambda-parameter policy of transformLambdaWithExpectedType:
// a declared slot (a parameter or field default) passes true, so an unannotated
// lambda parameter the type does not cover is GALA-E0033; a call argument,
// whose expected type may still be partly inferred, passes false.
func (t *galaASTTransformer) transformArgument(exprCtx grammar.IExpressionContext, s slot, strict bool) (ast.Expr, error) {
	// A GALA alias of a function type (`f Conv[int, string]`) is a Go alias,
	// so the slot has that function type: every lowering below that keys on
	// a function type (lambdas, placeholders, partial functions, thunks)
	// sees through it.
	//
	// A Go named function type (`fs.WalkDirFunc`) is a distinct type: a
	// function literal written for it (a lambda, placeholder or partial
	// function) takes its signature, but any other value keeps the slot's
	// named type — it is never a by-name thunk.
	funcSlot := s.typ
	if _, isFunc := s.typ.(transpiler.FuncType); !isFunc {
		if ft := t.aliasedFuncType(s.typ); ft != nil {
			s.typ = *ft
			funcSlot = *ft
		} else if ft := t.resolveTranspilerTypeAsFuncType(s.typ); ft != nil {
			funcSlot = *ft
		}
	}
	expectedType := s.typ
	// Try to find a partial function literal in this expression
	if pfCtx := t.findPartialFunctionInExpression(exprCtx); pfCtx != nil {
		return t.transformPartialFunctionLiteral(pfCtx, funcSlot)
	}

	// Try to find a lambda in this expression
	if lambdaCtx := t.findLambdaInExpression(exprCtx); lambdaCtx != nil {
		return t.lowerLambdaArg(lambdaCtx, s, strict)
	}

	// L4: Try to rewrite as a placeholder lambda if the expected type is a
	// function type and the expression contains `_` identifiers.
	if expr, handled, err := t.tryRewriteAsPlaceholderLambda(exprCtx, funcSlot); err != nil {
		return nil, err
	} else if handled {
		return t.spreadLambdaGoResults(expr, funcSlot, exprCtx)
	}

	// Check mode: an if-expression or match lowers its branches against the
	// slot type; anything else sees it pushed for downward inference.
	expr, err := t.lowerAgainst(exprCtx, s, strict)
	if err != nil {
		return nil, err
	}

	// Lift bare T value to Immutable[T] when the expected param type is
	// Immutable[T] but the actual arg expression is a bare T (literal,
	// arithmetic, etc.). Without this, Go rejects the bare value against
	// the Immutable[T] parameter slot — a parameter explicitly typed
	// `Immutable[T]`, or a type parameter resolved to it from another arg.
	if expectedType != nil && !expectedType.IsNil() && t.isImmutableType(expectedType) {
		expr = t.liftToImmutableForArg(expr, expectedType)
	}

	// By-name / thunk sugar: when the expected parameter type is a zero-arg
	// function type, lift a plain expression argument into a thunk so
	// `Future(doSomething())` means `Future(() => doSomething())`. Lambdas and
	// placeholder lambdas are handled by the earlier branches, so only bare
	// expressions reach here.
	if wrapped, ok := t.wrapExprAsThunkIfNeeded(expr, expectedType, s.tryThunk); ok {
		expr = wrapped
	}

	return expr, nil
}

// wrapExprAsThunkIfNeeded implements by-name / thunk sugar. When the call-site
// expected parameter type is a zero-arg function type (`func() T`, or void
// `func()`) and `expr` is a plain expression rather than a function value, it
// lifts `expr` into `func() T { return expr }` (or `func() { expr }` for void).
// This lets
//
//	val f = Future(doSomething())
//
// mean the same as `Future(() => doSomething())`.
//
// The conversion is purely additive and never changes the meaning of an
// existing valid program: passing a bare `T` where a `func() T` is expected is
// otherwise a compile error, and an argument whose own type is already a
// function is passed through untouched (returns ok=false) so an existing thunk
// is never double-wrapped. Returns ok=false — leaving `expr` unchanged — when
// the sugar does not apply, including when the thunk's result type cannot be
// determined (so the normal type error surfaces instead of masking it).
//
// tryThunk marks the thunk of Try(...): a Go call there runs with its error
// turned into the panic Try catches (see tryThunkValue).
func (t *galaASTTransformer) wrapExprAsThunkIfNeeded(expr ast.Expr, expectedType transpiler.Type, tryThunk bool) (ast.Expr, bool) {
	if expr == nil || expectedType == nil || expectedType.IsNil() {
		return expr, false
	}
	// The expected type must be a zero-arg function type.
	ft, ok := expectedType.(transpiler.FuncType)
	if !ok || len(ft.Params) != 0 {
		return expr, false
	}

	// Already a function value: pass through untouched. This preserves the
	// pre-sugar behavior of handing an existing thunk (a `func() T` value or a
	// method/eta reference) directly to the parameter, and guarantees we never
	// re-wrap a valid program.
	exprType := t.getExprTypeName(expr)
	if _, isFunc := exprType.(transpiler.FuncType); isFunc {
		return expr, false
	}

	// Several results: the expression is their one GALA value, spread over
	// them as a lambda's is (see spreadLambdaGoResults).
	if len(ft.Results) > 1 {
		return t.goResultsThunk(expr, exprType, ft.Results)
	}

	// Void thunk: `func()` expecting no result. The body is the expression as a
	// statement, matching the void expression-lambda form `() => expr`.
	if len(ft.Results) == 0 {
		return &ast.FuncLit{
			Type: &ast.FuncType{Params: &ast.FieldList{}},
			Body: &ast.BlockStmt{List: []ast.Stmt{&ast.ExprStmt{X: expr}}},
		}, true
	}

	// Value thunk: `func() T`. The body mirrors the expression-lambda body
	// exactly, so the sugar is equivalent to writing `() => expr` by hand.
	resultType := ft.Results[0]
	concreteResult := !resultType.IsNil() && !transpiler.IsUnusable(resultType) && !t.hasTypeParams(resultType)

	// Try's thunk: a Go call returning `(T, error)` (or `(A, B, error)`, …, or
	// only `error`) runs with its error turned into the panic Try catches, so
	// `Try(strconv.Atoi(s))` is a Try[int] — the same as `Try(() =>
	// strconv.Atoi(s))` — not a Try of the Try the call converts to.
	if tryThunk {
		if thunkBody, thunkType, ok := t.tryThunkValue(expr); ok {
			retTypeExpr := thunkType
			if concreteResult {
				retTypeExpr = t.typeToExpr(resultType)
			}
			return thunkLit(thunkBody, retTypeExpr), true
		}
	}

	// Plain single-expression thunk. Determine T — prefer the concrete expected
	// result type, otherwise fall back to the argument expression's own inferred
	// type so a still-unresolved type parameter (e.g. Future's T) is bound
	// downstream from the thunk's declared result. Bail out when neither yields a
	// usable type.
	var retTypeExpr ast.Expr
	if concreteResult {
		retTypeExpr = t.typeToExpr(resultType)
	} else if !exprType.IsNil() && !transpiler.IsUnusable(exprType) && !t.hasTypeParams(exprType) {
		retTypeExpr = t.typeToExpr(exprType)
	}
	if retTypeExpr == nil {
		return expr, false
	}

	return &ast.FuncLit{
		Type: &ast.FuncType{
			Params:  &ast.FieldList{},
			Results: &ast.FieldList{List: []*ast.Field{{Type: retTypeExpr}}},
		},
		Body: &ast.BlockStmt{List: []ast.Stmt{&ast.ReturnStmt{Results: []ast.Expr{expr}}}},
	}, true
}

// liftToImmutableForArg wraps `expr` with `std.NewImmutable[T](expr)` when
// the call-site expected param type is `Immutable[T]` and `expr` is a bare
// `T` value (its inferred type does not already satisfy the Immutable
// shape). Returns `expr` unchanged when no wrap is needed — including:
//   - the expression already has type `Immutable[…]`,
//   - `expr` is the `nil` literal (Immutable[T] has no nil shape — let the
//     existing Some/None / Option diagnostics fire instead),
//   - the inner type `T` can't be resolved (let the Go compiler surface a
//     real mismatch rather than masking it).
func (t *galaASTTransformer) liftToImmutableForArg(expr ast.Expr, expectedType transpiler.Type) ast.Expr {
	if expr == nil {
		return expr
	}
	// Skip nil literal — wrapping nil in NewImmutable is never desirable.
	if id, ok := expr.(*ast.Ident); ok && id.Name == "nil" {
		return expr
	}
	// If the expression is already Immutable[…], no wrap is needed.
	exprType := t.getExprTypeName(expr)
	if exprType != nil && !exprType.IsNil() && t.isImmutableType(exprType) {
		return expr
	}
	// Resolve the inner type T from Immutable[T].
	gen, ok := expectedType.(transpiler.GenericType)
	if !ok || len(gen.Params) != 1 {
		return expr
	}
	inner := gen.Params[0]
	if transpiler.IsUnusable(inner) {
		return expr
	}
	innerExpr := t.typeToExpr(inner)
	if innerExpr == nil {
		return expr
	}
	return &ast.CallExpr{
		Fun: &ast.IndexExpr{
			X:     t.stdIdent("NewImmutable"),
			Index: innerExpr,
		},
		Args: []ast.Expr{expr},
	}
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
		if transpiler.IsUnusable(tp) {
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
// Used by the lambda inference refinement: after transforming each lambda arg,
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

// inferFuncTypeSubstFromArgs pre-scans non-lambda arguments of a generic function call
// to infer type parameter substitutions. For example, in Iterate(1, (x) => x * 2),
// it infers T = int from the first argument (1), enabling the lambda param x to be typed as int.
// placeholders reports that some type parameter was filled with `any`.
// preset holds the type arguments the call writes explicitly — a leading
// part of the list, as in `Using[Res](r, (x) => …)` — which win over inference.
func (t *galaASTTransformer) inferFuncTypeSubstFromArgs(funcMeta *transpiler.FunctionMetadata, argListCtx grammar.IArgumentListContext, preset map[string]string) (subst map[string]string, placeholders bool) {
	inferred := typeSubstStrings(t.inferTypeArgsFromNonLambdaArgs(funcMeta.TypeParams, funcMeta.ParamTypes, t.callArgs(argListCtx, funcMeta.ParamNames)))
	if len(inferred) == 0 && len(preset) == 0 {
		// Nothing determines any type parameter: a lambda over them has no
		// type to take, which is GALA-E0033 rather than an all-`any` guess.
		return nil, false
	}

	// Type params neither written nor bound by a non-lambda argument (e.g. `A`
	// in `body func(R) A`) become `any`, so the bound ones (`R`) still reach
	// the lambda. An `any` expected result means "infer from the body", and in
	// the emitted call Go infers the real type argument itself.
	subst = make(map[string]string, len(funcMeta.TypeParams))
	for _, tp := range funcMeta.TypeParams {
		if v, ok := preset[tp]; ok {
			subst[tp] = v
		} else if v, ok := inferred[tp]; ok {
			subst[tp] = v
		} else {
			subst[tp] = "any"
			placeholders = true
		}
	}
	return subst, placeholders
}

// callArg is one argument of a call: its expression (nil for a direct lambda),
// the lambda it is or holds (nil when neither), and the index of the parameter
// or field it fills (-1 when none).
type callArg struct {
	expr   grammar.IExpressionContext
	lambda *grammar.LambdaExpressionContext
	slot   int
}

// callArgs classifies a call's arguments. A named argument fills the slot of
// the same name in paramNames; a positional one fills the next position.
func (t *galaASTTransformer) callArgs(argListCtx grammar.IArgumentListContext, paramNames []string) []callArg {
	var args []callArg
	argIdx := 0
	for _, argCtx := range argListCtx.AllArgument() {
		arg := argCtx.(*grammar.ArgumentContext)
		var slot int
		if arg.Identifier() != nil {
			slot = slices.Index(paramNames, arg.Identifier().GetText())
		} else {
			slot = argIdx
			argIdx++
		}
		exprCtx, lambdaCtx, _, err := extractArgContent(arg)
		if err != nil {
			continue
		}
		if lambdaCtx == nil {
			lambdaCtx = t.findLambdaInExpression(exprCtx)
		}
		args = append(args, callArg{expr: exprCtx, lambda: lambdaCtx, slot: slot})
	}
	return args
}

// inferTypeArgsFromNonLambdaArgs is the first phase of lowering a generic call
// whose arguments include lambdas: it binds typeParams from the arguments that
// are not lambdas (explicit, or placeholder ones such as `_ * 2` in a
// function-typed slot), unifying each against the type of the slot it fills, so the
// lambdas can then be lowered against concrete types. Only type parameters an
// argument determines appear in the result.
func (t *galaASTTransformer) inferTypeArgsFromNonLambdaArgs(typeParams []string, paramTypes []transpiler.Type, args []callArg) map[string]transpiler.Type {
	inferredMap := make(map[string]transpiler.Type)
	for _, a := range args {
		if a.lambda != nil || a.slot < 0 || a.slot >= len(paramTypes) || t.isPlaceholderLambdaArg(a.expr, paramTypes[a.slot]) {
			continue
		}
		expr, err := t.transformExpression(a.expr)
		if err != nil {
			continue
		}
		argType := t.getExprTypeNameManual(expr)
		if transpiler.IsUnusable(argType) {
			argType, _ = t.inferExprType(expr)
		}
		if transpiler.IsUnusable(argType) {
			continue
		}
		t.unifyForInference(paramTypes[a.slot], argType, typeParams, inferredMap)
	}
	return inferredMap
}

// structCtorTypeSubst returns the type arguments of a generic struct (or sealed
// variant) constructor call known before its lambda arguments are lowered: the
// explicit ones, then those fromSlot (the expected type's, see slotTypeArgs)
// gives, then those the non-lambda arguments determine — the order
// structLiteralType binds them in. An undetermined type parameter is left out,
// not defaulted to `any`; see genericCtorLambdaExpectation.
func (t *galaASTTransformer) structCtorTypeSubst(
	fun ast.Expr,
	typeParams, fields []string,
	fieldTypes []transpiler.Type,
	argListCtx grammar.IArgumentListContext,
	fromSlot map[string]transpiler.Type,
) map[string]string {
	explicit := typeSubstStrings(fromSlot)
	if explicit == nil {
		explicit = map[string]string{}
	}
	maps.Copy(explicit, explicitTypeArgSubst(typeParams, t.extractFuncCallTypeArgs(fun)))
	if len(explicit) == len(typeParams) || argListCtx == nil {
		return explicit
	}
	// The pre-pass lowers the non-lambda arguments an extra time: run it only
	// when some argument's lowering depends on a generic function-typed slot.
	args := t.callArgs(argListCtx, fields)
	if !slices.ContainsFunc(args, func(a callArg) bool {
		if a.slot < 0 || a.slot >= len(fieldTypes) || !typeMentionsTypeParam(fieldTypes[a.slot], typeParams) {
			return false
		}
		slotType := fieldTypes[a.slot]
		return ((a.lambda != nil || t.needsExpectedType(a.expr)) && t.resolveTranspilerTypeAsFuncType(slotType) != nil) ||
			t.isPlaceholderLambdaArg(a.expr, slotType)
	}) {
		return explicit
	}
	// A partial explicit list binds its leading type parameters; the
	// arguments determine the rest.
	inferred := typeSubstStrings(t.inferTypeArgsFromNonLambdaArgs(typeParams, fieldTypes, args))
	if inferred == nil {
		return explicit
	}
	maps.Copy(inferred, explicit)
	return inferred
}

// unboundStructTypeParams returns the constructed struct's (or sealed
// parent's) type parameters that structCtorTypeSubst left unbound.
func (c functionCallContext) unboundStructTypeParams() []string {
	var unbound []string
	for _, tp := range c.structTypeParams {
		if _, ok := c.structTypeSubst[tp]; !ok {
			unbound = append(unbound, tp)
		}
	}
	return unbound
}

// genericCtorLambdaExpectation masks the type parameters structCtorTypeSubst
// left unbound out of a lambda argument's expected type. A masked result is
// inferred from the lambda's body (`Gen(Make = () => 5)` is `Gen[int]`); a
// masked parameter makes strict true, so an unannotated lambda parameter only
// that type parameter could type is GALA-E0033, not Go's "undefined: T".
func (t *galaASTTransformer) genericCtorLambdaExpectation(expected transpiler.Type, callCtx functionCallContext) (adjusted transpiler.Type, strict bool) {
	unbound := callCtx.unboundStructTypeParams()
	ft := t.resolveTranspilerTypeAsFuncType(expected)
	if len(unbound) == 0 || ft == nil {
		return expected, false
	}
	return transpiler.FuncType{
		Params:  maskTypeParamResults(ft.Params, unbound),
		Results: maskTypeParamResults(ft.Results, unbound),
	}, funcTypeParamsMentionTypeParams(ft.Params, unbound)
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

	// For bare names, try the package being compiled — a function of its own
	// hand-written .go files — then each dot-imported package as qualifier.
	qualifiers := make([]string, 0, len(t.importManager.dotImports)+1)
	if t.packageName != "" && !strings.Contains(funcName, ".") {
		qualifiers = append(qualifiers, t.packageName)
	}
	for _, entry := range t.importManager.dotImports {
		qualifiers = append(qualifiers, entry.PkgName)
	}
	for _, qualifier := range qualifiers {
		qualName := qualifier + "." + funcName
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
		if entry, _, ok := t.importForQualifier(alias); ok && entry.PkgName != alias {
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

// resolveGoCallSignature resolves the full Go function/method signature of a
// call expression via goTypeInfo, covering the shapes a `val a, b = goCall()`
// binding can take: a package-qualified function (`os.ReadDir`), a method on a
// package value (`base64.StdEncoding.DecodeString`), a method on a local
// expression, and a bare dot-imported function. It is the multi-return
// counterpart of resolveGoFuncParamTypes, used so each name in a destructuring
// binding gets its corresponding return type (enabling `.Size()` etc. on the
// value component) instead of NilType.
//
// A signature the call's arguments cannot fit is not the callee. The `.Get()`
// that reads a val field through its Immutable wrapper takes no arguments, and
// must not resolve to a Go method of the field's type that happens to be named
// Get (`(*http.Client).Get(url)` returns two values).
func (t *galaASTTransformer) resolveGoCallSignature(expr ast.Expr) *transpiler.GoFuncSignature {
	callExpr, ok := expr.(*ast.CallExpr)
	if !ok {
		return nil
	}
	if sig := t.lookupGoCallSignature(callExpr); sig != nil && goCallArgsFit(sig, callExpr) {
		return sig
	}
	return nil
}

// goCallArgsFit reports whether call's arguments can be passed to sig: one per
// parameter, at least all but the last for a variadic callee, or a sole
// argument that Go spreads over several parameters (`f(g())`).
func goCallArgsFit(sig *transpiler.GoFuncSignature, call *ast.CallExpr) bool {
	args, params := len(call.Args), len(sig.Params)
	switch {
	case args == params:
		return true
	case sig.IsVariadic:
		return args >= params-1
	default:
		return args == 1 && params > 1
	}
}

// lookupGoCallSignature finds the Go signature a call's callee names, without
// checking the call's arguments against it (see resolveGoCallSignature).
func (t *galaASTTransformer) lookupGoCallSignature(callExpr *ast.CallExpr) *transpiler.GoFuncSignature {
	if t.goTypeInfo == nil {
		return nil
	}
	funExpr, _ := splitCallFunTypeArgs(callExpr.Fun)
	switch fun := funExpr.(type) {
	case *ast.SelectorExpr:
		if id, ok := fun.X.(*ast.Ident); ok {
			if sig := t.goTypeInfo.GetFuncSignature(t.goQualifiedName(id.Name, fun.Sel.Name)); sig != nil {
				return sig
			}
		}
		// A method of the receiver's type, whatever expression yields it
		// (`exec.Command(...).Output()`).
		return t.goMethodSignature(t.getExprTypeNameManual(fun.X), fun.Sel.Name)
	case *ast.Ident:
		// A local binding of the name is the callee, not a Go function.
		if t.shadowingScope(fun.Name) != nil {
			return nil
		}
		if sig := t.ownGoFuncSignature(fun.Name); sig != nil {
			return sig
		}
		for _, entry := range t.importManager.dotImports {
			if sig := t.goTypeInfo.GetFuncSignature(entry.PkgName + "." + fun.Name); sig != nil {
				return sig
			}
		}
	}
	return nil
}

// ownGoFuncSignature finds the Go signature of a function a bare name calls in
// the package's own hand-written .go files — the declaring package's, while a
// default declared in another package is being lowered. Go type info is keyed
// by short package name alone, so the lookup misses rather than guesses when
// the name is a GALA function of the package, or when the file imports a Go
// package of the same name, whose functions share those keys.
func (t *galaASTTransformer) ownGoFuncSignature(name string) *transpiler.GoFuncSignature {
	pkg := t.packageName
	if t.loweringForeignDefault() {
		pkg = t.loweringDefault.pkg
	}
	if pkg == "" {
		return nil
	}
	sig := t.goTypeInfo.GetFuncSignature(pkg + "." + name)
	if sig == nil {
		return nil
	}
	if _, isGala := t.unshadowedFunctionByName(name); isGala {
		return nil
	}
	for _, entry := range t.importManager.All() {
		if entry.PkgName == pkg && !t.galaPkgPaths[entry.Path] {
			return nil
		}
	}
	return sig
}

// splitCallFunTypeArgs separates a call's function expression from any explicit
// type arguments written at the call site, so `go_interop.MapGet[string, int]`
// resolves against the same signature lookup as the bare `go_interop.MapGet`
// while the type arguments stay available for instantiation.
func splitCallFunTypeArgs(fun ast.Expr) (ast.Expr, []ast.Expr) {
	switch idx := fun.(type) {
	case *ast.IndexExpr:
		return idx.X, []ast.Expr{idx.Index}
	case *ast.IndexListExpr:
		return idx.X, idx.Indices
	}
	return fun, nil
}

// resolveGoCallReturnTypes resolves a Go call's return types with the callee's
// own type parameters instantiated to whatever the call binds them to. A
// generic callee's recorded signature still names its declared type parameters
// (`func MapGet[K comparable, V any](m map[K]V, k K) (V, bool)` returns `V`), so
// every consumer that writes a return type down — rather than merely passing it
// along — must go through here or it emits an identifier the user never wrote.
func (t *galaASTTransformer) resolveGoCallReturnTypes(expr ast.Expr) []transpiler.Type {
	callExpr, ok := expr.(*ast.CallExpr)
	if !ok {
		return nil
	}
	sig := t.resolveGoCallSignature(callExpr)
	if sig == nil {
		sig = t.declaredGoResultsSignature(callExpr)
	}
	if sig == nil {
		return nil
	}
	return t.instantiateGoSignatureReturns(sig, callExpr.Args, t.callSiteTypeArgs(callExpr), callExpr.Ellipsis != token.NoPos)
}

// callSiteTypeArgs converts a call's explicit type arguments to transpiler types.
func (t *galaASTTransformer) callSiteTypeArgs(callExpr *ast.CallExpr) []transpiler.Type {
	_, typeArgExprs := splitCallFunTypeArgs(callExpr.Fun)
	if len(typeArgExprs) == 0 {
		return nil
	}
	typeArgs := make([]transpiler.Type, 0, len(typeArgExprs))
	for _, ta := range typeArgExprs {
		typeArgs = append(typeArgs, t.astTypeToTranspilerType(ta))
	}
	return typeArgs
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
