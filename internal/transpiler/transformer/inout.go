package transformer

import (
	"fmt"
	"go/ast"
	"go/token"
	"slices"
	"strings"

	"martianoff/gala/galaerr"
	"martianoff/gala/internal/transpiler"
)

// `inout` parameters and receivers (docs/OWNERSHIP.MD#inout-basics)
//
// `func f(inout x T)` lends the caller's place to f for the call. In Go it is
// the parameter `x *T`, and the caller passes `&place`: the Go a programmer
// writes with a `Ptr[T]` parameter today. In the body x is a mutable place of
// type T:
//
//   - x is bound with the type Ptr[T], the type of the Go parameter, and every
//     read of it is the dereference `(*x)` (inoutRead), whose type is T. So a
//     whole read copies the value, `x = e` writes `*x = e`, `x.f = e` writes
//     the field, and `&x` lends the place on as x itself (inoutAddr).
//   - A field read stays `(*x).f`: the dereference keeps its evaluation-order
//     mark (operandThroughPointer), and Go selects through `(*x)` whatever T
//     is (a type parameter or an interface, which `x.f` would not reach).
//
// `func (inout r T) m()` is a Go pointer-receiver method, `func (r *T) m()`.
// The analyzer records it as a Ptr receiver (MethodMetadata.InoutReceiver),
// so it is called as `v.m()` on a place, as a Ptr-receiver method is.
//
// A caller passes `&place` to an `inout` parameter, or a Ptr[T] it already
// holds (checkInoutArgs, GALA-E0111), and calls an `inout`-receiver method
// only on a place (checkInoutReceiver, GALA-E0114).

// addInout binds name, an `inout` parameter or receiver whose Go parameter
// has the type ptrType (a Ptr[T]). It is a mutable place, so it is
// reassignable like a `var`.
func (t *galaASTTransformer) addInout(name string, ptrType transpiler.Type) {
	t.inoutTypesSeen = true
	t.addVar(name, ptrType)
	t.markMutable(name)
	// Hover shows the binding as written: a T, not the pointer it reads through.
	if ptr, ok := ptrType.(transpiler.PointerType); ok {
		t.recordLSPVarType(name, ptr.Elem)
	}
	if t.currentScope == nil {
		return
	}
	if t.currentScope.inout == nil {
		t.currentScope.inout = make(map[string]bool)
	}
	t.currentScope.inout[name] = true
}

// inoutParamType is the Go type `*elem` of an `inout elem` parameter: the
// never-nil `*T` of a Ptr, marked so that the function type it is part of
// reads back as `func(inout T)` (inoutFuncParams).
func (t *galaASTTransformer) inoutParamType(elem ast.Expr) *ast.StarExpr {
	star := t.ptrStar(elem)
	t.markOperand(star, operandInoutParam)
	t.inoutTypesSeen = true
	return star
}

// inoutFuncParams is the Inout flags of a Go function type's parameters:
// true for each one inoutParamType made. Nil when none is.
func (t *galaASTTransformer) inoutFuncParams(fields *ast.FieldList) []bool {
	if fields == nil {
		return nil
	}
	var inout []bool
	i := 0
	for _, field := range fields.List {
		n := max(len(field.Names), 1)
		if t.marked(field.Type, operandInoutParam) {
			inout = append(inout, make([]bool, i+n-len(inout))...)
			for k := i; k < i+n; k++ {
				inout[k] = true
			}
		}
		i += n
	}
	return inout
}

// isInoutName reports whether name resolves to an `inout` binding, whose
// scope type is the Ptr[T] of its Go parameter rather than its value's T.
func (t *galaASTTransformer) isInoutName(name string) bool {
	s := t.bindingScope(name)
	return s != nil && s.inout[name]
}

// inoutRead is the read of the `inout` binding ident: the place its pointer
// addresses, `(*x)`.
func (t *galaASTTransformer) inoutRead(ident *ast.Ident) ast.Expr {
	star := t.markDeref(&ast.StarExpr{X: ident})
	t.markOperand(star, operandInoutRead)
	return &ast.ParenExpr{X: star}
}

// inoutAddr is `&operand` for an operand that reads an `inout` binding,
// `(*x)`: the pointer x itself. ok is false for any other operand.
func (t *galaASTTransformer) inoutAddr(operand ast.Expr) (ast.Expr, bool) {
	if star, ok := ast.Unparen(operand).(*ast.StarExpr); ok && t.marked(star, operandInoutRead) {
		return star.X, true
	}
	return nil, false
}

// inoutError is a GALA-E0111 at at.
func inoutError(at srcNode, msg, hint string) error {
	r := at.rng()
	return spanErrorAt(galaerr.CodeInoutArgument, r, r, msg, hint)
}

// checkInoutParamDecl rejects an `inout` parameter or receiver, written at
// at with the lowered type typ, that would lend no place: a variadic one, one
// with a default, or one whose type is already a pointer (`inout p Ptr[T]`
// would be a `**T`) (GALA-E0111).
func checkInoutParamDecl(at srcNode, name string, typ ast.Expr, variadic, hasDefault bool) error {
	var why string
	hint := "take the values as an ordinary parameter, or lend each place with its own inout parameter"
	switch {
	case variadic:
		why = "variadic"
	case hasDefault:
		why = "given a default"
	default:
		if _, isPtr := typ.(*ast.StarExpr); !isPtr {
			return nil
		}
		why = "a pointer"
		hint = "declare it `inout " + name + " T` for the type T it points to; a caller lends a Ptr[T] it holds as it is"
	}
	return inoutError(at,
		fmt.Sprintf("inout parameter %s cannot be %s: an inout parameter lends one place of the caller", name, why),
		hint)
}

// inoutCallee is a GALA function or method that lends places: its
// parameters' `inout` flags and names, its name, and whether its receiver
// is `inout`. shift is 1 when the call passes the receiver as its first
// argument (a method lowered to a function).
type inoutCallee struct {
	inout     []bool
	params    []string
	name      string
	shift     int
	recvInout bool
	// method and recvType are the method called and its receiver's type; nil
	// for a function.
	method   *transpiler.MethodMetadata
	recvType transpiler.Type
	// declared is true for a declared function or method, false for a
	// function value.
	declared bool
	// fm is the declared function called; nil for a method or a function value.
	fm *transpiler.FunctionMetadata
	// paramTypes are the declared parameter types, an `inout` one as the
	// Ptr[T] it lowers to.
	paramTypes []transpiler.Type
	// typeArgs are the type arguments the call spells for the callee's own
	// type parameters: in the lowered call when it spells them all
	// (`put[Counter]`), else as written (`c.m[A, B]`). recvTypeParams are
	// the type parameters of a method's receiver type, which the receiver's
	// type arguments bind (T is Counter for a method of a Holds[Counter]).
	typeArgs       []ast.Expr
	recvTypeParams []string
}

// lends reports whether the callee lends a place: an `inout` parameter or
// receiver.
func (c inoutCallee) lends() bool { return c.recvInout || slices.Contains(c.inout, true) }

// inoutCalleeOf is the callee of call, the lowering of a call of base, when
// it lends a place: a GALA function or method with an `inout` parameter or
// receiver, or a function value whose type says `inout` (`f func(inout T)`);
// ok is false otherwise.
func (t *galaASTTransformer) inoutCalleeOf(base ast.Expr, call *ast.CallExpr) (c inoutCallee, ok bool) {
	receiver, method, _ := t.splitCallTarget(base)
	if receiver != nil && method != "" && t.inoutNames[method] {
		recvType, key := t.resolveReceiverTypeAndLookupKey(receiver, method)
		if !transpiler.IsUnusable(recvType) {
			if meta := t.getTypeMeta(key); meta != nil && meta.Methods[method] != nil {
				m := meta.Methods[method]
				c = inoutCallee{inout: m.InoutParams, params: m.ParamNames, name: method, recvInout: m.InoutReceiver, method: m, recvType: recvType, declared: true,
					paramTypes: m.ParamTypes, recvTypeParams: meta.TypeParams}
				_, c.typeArgs = splitCallFunTypeArgs(base)
				if sel, isSel := call.Fun.(*ast.SelectorExpr); !isSel || sel.Sel.Name != method {
					c.shift = 1
				}
				return c, c.lends()
			}
		}
	}
	if receiver == nil {
		name := t.extractFuncName(base)
		// A qualified call names `pkg.F`; the set holds F.
		if t.inoutNames[name[strings.LastIndex(name, ".")+1:]] {
			if fm := t.getFunction(name); fm != nil {
				c = inoutCallee{inout: fm.InoutParams, params: fm.ParamNames, name: fm.Name, declared: true, fm: fm,
					paramTypes: fm.ParamTypes}
				// The lowered call spells every type argument when GALA
				// settles them (written, or injected), else Go infers them.
				_, c.typeArgs = splitCallFunTypeArgs(call.Fun)
				if len(c.typeArgs) != len(fm.TypeParams) {
					_, c.typeArgs = splitCallFunTypeArgs(base)
				}
				return c, c.lends()
			}
		}
	}
	// A function value: a parameter, a val or a field of a function type.
	if !t.inoutTypesSeen || !t.mayBeFuncValue(base) {
		return c, false
	}
	if ft, isFunc := t.followAliasChain(t.probeExprType(base)).(transpiler.FuncType); isFunc && ft.HasInout() {
		name := t.extractFuncName(base)
		if name == "" {
			name = "the function"
		}
		return inoutCallee{inout: ft.Inout, name: name, paramTypes: ft.Params}, true
	}
	return c, false
}

// mayBeFuncValue reports whether base, a callee, can be a function value: a
// local binding, or a selector that is not a package's member (a field).
func (t *galaASTTransformer) mayBeFuncValue(base ast.Expr) bool {
	switch b := ast.Unparen(base).(type) {
	case *ast.Ident:
		return t.bindingScope(b.Name) != nil
	case *ast.SelectorExpr:
		pkg, isIdent := b.X.(*ast.Ident)
		return !isIdent || t.bindingScope(pkg.Name) != nil || !t.importManager.IsPackage(pkg.Name)
	}
	return false
}

// collectInoutNames is the names of the functions and methods of r that
// lend a place (an `inout` parameter or receiver): the only declared callees
// checkInoutArgs resolves. hasFuncTypes reports whether a signature or field
// of r has a `func(inout T)` type, so that calls through function values are
// checked too.
func collectInoutNames(r *transpiler.RichAST) (names map[string]bool, hasFuncTypes bool) {
	if r == nil {
		return nil, false
	}
	add := func(name string) {
		if names == nil {
			names = make(map[string]bool)
		}
		names[name] = true
	}
	inoutFunc := func(types []transpiler.Type) bool { return slices.ContainsFunc(types, typeHasInoutFunc) }
	for _, f := range r.Functions {
		if slices.Contains(f.InoutParams, true) {
			add(f.Name)
		}
		hasFuncTypes = hasFuncTypes || inoutFunc(f.ParamTypes)
	}
	for _, typ := range r.Types {
		for _, m := range typ.Methods {
			if m.InoutReceiver || slices.Contains(m.InoutParams, true) {
				add(m.Name)
			}
			hasFuncTypes = hasFuncTypes || inoutFunc(m.ParamTypes)
		}
		for _, field := range typ.Fields {
			hasFuncTypes = hasFuncTypes || typeHasInoutFunc(field)
		}
	}
	return names, hasFuncTypes || len(names) > 0
}

// typeHasInoutFunc reports whether typ is, or holds, a `func(inout T)`.
func typeHasInoutFunc(typ transpiler.Type) bool {
	switch v := typ.(type) {
	case transpiler.FuncType:
		return v.HasInout() || slices.ContainsFunc(v.Params, typeHasInoutFunc) || slices.ContainsFunc(v.Results, typeHasInoutFunc)
	case transpiler.GenericType:
		return slices.ContainsFunc(v.Params, typeHasInoutFunc)
	case transpiler.PointerType:
		return typeHasInoutFunc(v.Elem)
	case transpiler.ArrayType:
		return typeHasInoutFunc(v.Elem)
	}
	return false
}

// checkInoutArgs checks the call expr, the lowering of the call c of base,
// for the places it lends:
//
//   - an argument of an `inout` parameter is `&` of a mutable place
//     (isInoutPlace), or a Ptr the caller holds (GALA-E0111);
//   - `&x` of an `inout` place x fills nothing but an `inout` parameter
//     (checkInoutLends, GALA-E0113);
//   - no two places it lends overlap (checkInoutOverlap, GALA-E0112).
func (t *galaASTTransformer) checkInoutArgs(base ast.Expr, expr ast.Expr, c callSrc) error {
	if len(t.inoutNames) == 0 && !t.inoutTypesSeen && !t.inoutLends {
		return nil
	}
	call, ok := expr.(*ast.CallExpr)
	if !ok {
		// A construction lowered to a literal fills fields, never inout
		// parameters.
		return t.checkStrayLends(expr, c.at())
	}
	callee, ok := t.inoutCalleeOf(base, call)
	if err := t.checkInoutLends(call, callee, c); err != nil || !ok {
		return err
	}
	for i, isInout := range callee.inout {
		if !isInout || i+callee.shift >= len(call.Args) {
			continue
		}
		arg := call.Args[i+callee.shift]
		if addr, isAddr := arg.(*ast.UnaryExpr); isAddr && addr.Op == token.AND {
			if !t.isInoutPlace(addr.X) {
				at := t.inoutArgAt(c, callee, i)
				operand := strings.TrimSpace(strings.TrimPrefix(at.sourceText(), "&"))
				return inoutError(at, fmt.Sprintf("cannot lend %s to the inout parameter of %s: it is not a mutable place", operand, callee.name),
					"an inout argument is `&` of a `var`, an inout parameter, or a `var` field of one; copy a value first: `var c = v`, then pass `&c`")
			}
			if have, holds := t.heldPointer(addr.X); holds {
				if err := t.checkInoutLentPtr(addr.X, have, true, callee, i, c); err != nil {
					return err
				}
			}
			continue
		}
		if have, isPtr := t.heldPointer(arg); isPtr && have.NonNull {
			// A Ptr the caller holds lends its pointee.
			if err := t.checkInoutLentPtr(arg, have, false, callee, i, c); err != nil {
				return err
			}
			continue
		}
		at := t.inoutArgAt(c, callee, i)
		text := at.sourceText()
		return inoutError(at, fmt.Sprintf("the argument %s of %s is passed to an inout parameter without `&`", text, callee.name),
			fmt.Sprintf("lend the place with `&%s`; an inout argument is marked at the call site", text))
	}
	if err := t.checkInoutOverlap(call, callee, c); err != nil {
		return err
	}
	t.inoutBodyCall(call, callee)
	return nil
}

// heldPointer is the pointer type (a Ptr[T] or a *T) expr holds, through
// GALA aliases (`type PC Ptr[Counter]`); ok is false when it holds none.
func (t *galaASTTransformer) heldPointer(expr ast.Expr) (ptr transpiler.PointerType, ok bool) {
	ptr, ok = t.followAliasChain(t.getExprTypeName(expr)).(transpiler.PointerType)
	return ptr, ok
}

// checkInoutLentPtr rejects a pointer of type have lent to the `inout`
// parameter i of callee when it is not as many pointers deep as the
// parameter's type at this call (inoutLentType) takes (GALA-E0111). With
// amp, arg is the place of `&place`, which lends the variable holding the
// pointer: `put[Counter](&p)` lends a `**Counter` where the parameter is a
// `*Counter`. Without, arg is a Ptr the caller holds, which lends its
// pointee: `put[Ptr[Counter]](p)` lends a `*Counter` where the parameter
// is a `**Counter`. Only the depth decides; the pointee's type is left to
// Go, and only picks the hint.
func (t *galaASTTransformer) checkInoutLentPtr(arg ast.Expr, have transpiler.PointerType, amp bool, callee inoutCallee, i int, c callSrc) error {
	want, free, known := t.inoutLentType(callee, i)
	if !known {
		return nil
	}
	wantDepth, known := t.pointerDepth(want)
	if !known {
		return nil
	}
	haveDepth, known := t.pointerDepth(have)
	if !known {
		return nil
	}
	lent := haveDepth // the depth of what arg lends: the pointer, or its pointee
	if !amp {
		lent--
	}
	if lent == wantDepth {
		return nil
	}
	at := t.inoutArgAt(c, callee, i)
	text := at.sourceText()
	takes := fmt.Sprintf("lend a place of type %s", displayType(want))
	if amp {
		operand := strings.TrimSpace(strings.TrimPrefix(text, "&"))
		msg := fmt.Sprintf("cannot lend &%s to the inout parameter of %s: %s holds a %s", operand, callee.name, operand, displayType(have))
		if wantDepth != haveDepth-1 || !t.lendFits(have.Elem, want, free) {
			return inoutError(at, fmt.Sprintf("%s, and the parameter takes a %s", msg, displayType(want)), takes)
		}
		hint := fmt.Sprintf("pass `%s` without `&`: a Ptr lends its pointee", operand)
		if !have.NonNull {
			hint = fmt.Sprintf("pass `RequirePtr(%s)` without `&`: a Ptr lends its pointee (RequirePtr panics on nil)", operand)
		}
		return inoutError(at, fmt.Sprintf("%s, and the parameter takes the %s it points to", msg, displayType(want)), hint)
	}
	msg := fmt.Sprintf("cannot lend %s to the inout parameter of %s: %s holds a %s, and the parameter takes a %s",
		text, callee.name, text, displayType(have), displayType(want))
	hint := takes
	if wantDepth == haveDepth && t.lendFits(have, want, free) {
		hint = fmt.Sprintf("lend the variable holding the Ptr with `&%s`; without `&`, a Ptr lends its pointee", text)
		if !t.isInoutPlace(arg) {
			hint = fmt.Sprintf("lend a `var` holding the Ptr: `var q = %s`, then pass `&q`; without `&`, a Ptr lends its pointee", text)
		}
	}
	return inoutError(at, msg, hint)
}

// lendFits reports whether a place of type have fills an inout parameter of
// type want, type arguments included, a type parameter of free (one Go
// infers) taking any type. It only picks a hint: one that suggests the
// place is given only when it fits.
func (t *galaASTTransformer) lendFits(have, want transpiler.Type, free []string) bool {
	have, want = t.followAliasChain(have), t.followAliasChain(want)
	if isTypeParamNamed(want, free) {
		return true
	}
	hp, havePtr := have.(transpiler.PointerType)
	wp, wantPtr := want.(transpiler.PointerType)
	if havePtr || wantPtr {
		return havePtr && wantPtr && t.lendFits(hp.Elem, wp.Elem, free)
	}
	hg, haveGen := have.(transpiler.GenericType)
	wg, wantGen := want.(transpiler.GenericType)
	if haveGen && wantGen {
		if len(hg.Params) != len(wg.Params) || !t.typesCompatible(hg.Base, wg.Base) {
			return false
		}
		for k := range hg.Params {
			if !t.lendFits(hg.Params[k], wg.Params[k], free) {
				return false
			}
		}
		return true
	}
	return t.typesCompatible(have, want)
}

// inoutLentType is the type the `inout` parameter i of a declared callee
// lends at this call: its declared type with the type arguments the call
// spells and, for a method, those of the receiver's type. free is the
// callee's type parameters neither binds, which Go infers. known is false
// for a function value, whose type arguments the call does not spell, and
// when the type is, or points to, a type parameter in free, which may be a
// pointer itself.
func (t *galaASTTransformer) inoutLentType(callee inoutCallee, i int) (want transpiler.Type, free []string, known bool) {
	var own []string
	switch {
	case callee.fm != nil:
		own = callee.fm.TypeParams
	case callee.method != nil:
		own = callee.method.TypeParams
	default:
		return nil, nil, false
	}
	if i >= len(callee.paramTypes) || callee.paramTypes[i] == nil {
		return nil, nil, false
	}
	subst := t.writtenTypeArgs(own, callee.typeArgs[:min(len(callee.typeArgs), len(own))])
	if callee.method != nil {
		subst = receiverTypeSubst(callee.recvTypeParams, derefType(callee.recvType), subst)
	}
	for _, params := range [][]string{own, callee.recvTypeParams} {
		for _, tp := range params {
			if _, bound := subst[tp]; !bound {
				free = append(free, tp)
			}
		}
	}
	declared := derefType(callee.paramTypes[i])
	core := t.followAliasChain(declared)
	for ptr, isPtr := core.(transpiler.PointerType); isPtr; ptr, isPtr = core.(transpiler.PointerType) {
		core = t.followAliasChain(ptr.Elem)
	}
	if isTypeParamNamed(core, free) {
		return nil, nil, false
	}
	return t.substituteGoTypeParams(declared, subst), free, true
}

// pointerDepth is how many pointers typ is, through GALA aliases and Go
// aliases (`type P = *T`). known is false only when that is not certain:
// typ is unknown, or a Go alias whose target does not resolve. A type
// parameter in scope, and a Go named type with no Go type information,
// are no pointer.
func (t *galaASTTransformer) pointerDepth(typ transpiler.Type) (depth int, known bool) {
	// The hop bound stops an alias chain that refers back to itself.
	for hop := 0; hop < 64; hop++ {
		typ = t.followAliasChain(typ)
		if ptr, isPtr := typ.(transpiler.PointerType); isPtr {
			depth++
			typ = ptr.Elem
			continue
		}
		if transpiler.IsUnusable(typ) {
			return depth, false
		}
		named, isNamed := receiverBase(typ).(transpiler.NamedType)
		if !isNamed || t.getTypeMeta(named.BaseName()) != nil || named.Package == "" && t.isActiveTypeParam(named.Name) {
			return depth, true
		}
		target, isAlias := t.goAliasTarget(named)
		if !isAlias {
			return depth, true
		}
		if target == nil {
			return depth, false
		}
		typ = target
	}
	return depth, false
}

// goAliasTarget is the type named, a Go type, is an alias of, pointers
// kept (`type P = *T` is a *T). isAlias is false when Go declares named as
// other than an alias, or Go type information has no record of it; target
// is nil for an alias whose target does not resolve.
func (t *galaASTTransformer) goAliasTarget(named transpiler.NamedType) (target transpiler.Type, isAlias bool) {
	if t.goTypeInfo == nil {
		return nil, false
	}
	key := t.goTypeLookupName(named)
	data := t.goTypeInfo.GetTypeData(key)
	if name, bare := bareTypeName(named); data == nil && bare {
		if key = t.dotImportedGoTypeKey(name); key != "" {
			data = t.goTypeInfo.GetTypeData(key)
		}
	}
	if data == nil || data.Kind != "alias" {
		return nil, false
	}
	return t.goTypeInfo.ResolveTypeAlias(key), true
}

// inoutArgAt is where the value for parameter i of callee is written in the
// call c: the argument named for it, or the one at its position. A default
// filling the parameter is written at the call.
func (t *galaASTTransformer) inoutArgAt(c callSrc, callee inoutCallee, i int) srcNode {
	positional := 0
	for _, a := range c.args.list {
		name, _ := a.name()
		if name == "" && positional != i || name != "" && (i >= len(callee.params) || name != callee.params[i]) {
			if name == "" {
				positional++
			}
			continue
		}
		if v, err := t.argContent(a); err == nil {
			return v.at()
		}
		return a.at()
	}
	return c.at()
}

// checkInoutReceiver rejects calling m, a method with an `inout` receiver,
// on recv when recv is not a mutable place (GALA-E0114). Unlike a Ptr
// receiver, whose read-only method may run on a copy (std.AddrOfCopy), an
// `inout` receiver always lends a place.
func (t *galaASTTransformer) checkInoutReceiver(recv ast.Expr, recvType transpiler.Type, m *transpiler.MethodMetadata, line, col int) error {
	if m == nil || !m.InoutReceiver || t.isInoutPlace(recv) {
		return nil
	}
	typeName := displayType(receiverBase(recvType))
	return galaerr.NewCodedSemanticError(galaerr.CodeInoutReceiver, line, col,
		fmt.Sprintf("cannot call %s on this %s: %s has an inout receiver, and this %s is not a mutable place", m.Name, typeName, m.Name, typeName),
		"call it on a `var`, an inout parameter or a `Ptr`; to change a copy, copy the value into a `var` first")
}

// isInoutPlace reports whether expr, a lowered expression, is a mutable
// place an `inout` parameter may borrow: a `var` (local, `var` parameter or
// package-level), the pointee of a pointer (an `inout` binding, `*p`), or a
// field of one. A val, a parameter not declared `var`, a val field (read
// through Get()), a call result, a literal and a collection element are not.
func (t *galaASTTransformer) isInoutPlace(expr ast.Expr) bool {
	switch e := expr.(type) {
	case *ast.ParenExpr:
		return t.isInoutPlace(e.X)
	case *ast.StarExpr:
		return true
	case *ast.Ident:
		return t.isMutableVar(e.Name) || t.isPackageVar(e.Name)
	case *ast.SelectorExpr:
		if pkg, ok := e.X.(*ast.Ident); ok && t.bindingScope(pkg.Name) == nil && t.importManager.IsPackage(pkg.Name) {
			pv := t.importedPackageVal(pkg.Name, e.Sel.Name)
			return pv != nil && !pv.IsVal
		}
		if _, isPtr := t.heldPointer(e.X); isPtr {
			return true
		}
		return t.isInoutPlace(e.X)
	}
	return false
}
