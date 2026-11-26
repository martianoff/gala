package transformer

import (
	"fmt"
	"go/ast"
	"go/token"
	"regexp"
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
		have, isPtr := t.heldPointer(arg)
		if isPtr && have.NonNull {
			// A Ptr the caller holds lends its pointee.
			if err := t.checkInoutLentPtr(arg, have, false, callee, i, c); err != nil {
				return err
			}
			continue
		}
		at := t.inoutArgAt(c, callee, i)
		text := at.sourceText()
		if isPtr && t.inoutTakesPointee(have, callee, i) {
			// A *T may be nil (GALA-E0080 for one GALA sees as it is).
			return inoutError(at, fmt.Sprintf("the argument %s of %s is a pointer that may be nil, passed to an inout parameter", text, callee.name),
				fmt.Sprintf("pass `RequirePtr(%s)`: a Ptr lends its pointee (RequirePtr panics on nil)", text))
		}
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
// GALA and Go aliases and Go defined pointer types (pointerView); ok is
// false when it holds none.
func (t *galaASTTransformer) heldPointer(expr ast.Expr) (ptr transpiler.PointerType, ok bool) {
	held, _ := t.pointerView(t.getExprTypeName(expr), nil)
	ptr, ok = held.(transpiler.PointerType)
	return ptr, ok
}

// pointerView is typ through GALA and Go aliases (unaliasedType), with a Go
// defined type of a pointer (`type NP *GoCounter`; a generic one
// instantiated with its type arguments) seen as that pointer: Go selects a
// field through it, `n.N` as `(*n).N`, and passes it where its underlying
// pointer is taken. Like any pointer from Go, it may be nil. pointerView
// is used wherever GALA asks whether a value is a pointer: for a field
// reached through it, the place it holds, or how many pointers deep a lend
// is. It is not the type's identity: a hint matching types (lendFits)
// keeps NP apart from *GoCounter. resolved is false when it stops at a Go
// alias or defined pointer type it cannot instantiate.
func (t *galaASTTransformer) pointerView(typ transpiler.Type, free []string) (_ transpiler.Type, resolved bool) {
	return t.walkGoTypes(typ, free, true)
}

// selectsThroughPointer reports whether a field selected on a value of type
// typ is reached through a pointer (pointerView): `x.f` of a Ptr, a *T, an
// alias of one or a Go defined pointer type is `(*x).f`, a field of the
// pointee, whatever x is bound by.
func (t *galaASTTransformer) selectsThroughPointer(typ transpiler.Type) bool {
	held, _ := t.pointerView(typ, nil)
	_, isPtr := held.(transpiler.PointerType)
	return isPtr
}

// unaliasedType follows typ through GALA aliases (`type PC Ptr[Counter]`)
// and Go aliases (`type GP = *GoCounter`, a generic `type A[T any] = *T`
// instantiated with its type arguments) to the type they name. resolved
// is false when it stops at a Go alias whose target does not resolve.
func (t *galaASTTransformer) unaliasedType(typ transpiler.Type, free []string) (_ transpiler.Type, resolved bool) {
	return t.walkGoTypes(typ, free, false)
}

// walkGoTypes is unaliasedType, and with definedPointers pointerView: one
// Go type-information lookup per step.
func (t *galaASTTransformer) walkGoTypes(typ transpiler.Type, free []string, definedPointers bool) (_ transpiler.Type, resolved bool) {
	// The hop bound stops an alias chain that refers back to itself.
	for hop := 0; hop < 64; hop++ {
		typ = t.followAliasChain(typ)
		key, data, args := t.goDeclaredType(typ, free)
		var next transpiler.Type
		switch {
		case data == nil:
			return typ, true
		case data.Kind == "alias":
			next = t.instantiateGoType(t.goTypeInfo.ResolveTypeAlias(key), data.TypeParams, args)
		case definedPointers:
			if _, isPtr := data.Underlying.(transpiler.PointerType); !isPtr {
				return typ, true
			}
			// One step: the pointer it is, not what that points to.
			if ptr := t.instantiateGoType(data.Underlying, data.TypeParams, args); ptr != nil {
				return ptr, true
			}
			return typ, false
		default:
			return typ, true
		}
		if next == nil {
			return typ, false
		}
		typ = next
	}
	return typ, false
}

// checkInoutLentPtr rejects a pointer of type have lent to the `inout`
// parameter i of callee when it certainly is not as many pointers deep as
// the parameter's type at this call (inoutLentType) takes (GALA-E0111,
// depthMismatch). With amp, arg is the place of `&place`, which lends the
// variable holding the pointer: `put[Counter](&p)` lends a `**Counter`
// where the parameter is a `*Counter`. Without, arg is a Ptr the caller
// holds, which lends its pointee: `put[Ptr[Counter]](p)` lends a
// `*Counter` where the parameter is a `**Counter`. Only the depth decides;
// the pointee's type is left to Go, and only picks the hint.
func (t *galaASTTransformer) checkInoutLentPtr(arg ast.Expr, have transpiler.PointerType, amp bool, callee inoutCallee, i int, c callSrc) error {
	want, free, known := t.inoutLentType(callee, i)
	if !known {
		return nil
	}
	wantDepth, wantExact := t.pointerDepth(want, free)
	haveDepth, haveExact := t.pointerDepth(have, nil)
	lent := haveDepth // the depth of what arg lends: the pointer, or its pointee
	if !amp {
		lent--
	}
	if !depthMismatch(lent, haveExact, wantDepth, wantExact) {
		return nil
	}
	at := t.inoutArgAt(c, callee, i)
	text := at.sourceText()
	takes := fmt.Sprintf("lend a place of type %s", t.lendTypeText(want))
	if amp {
		operand := strings.TrimSpace(strings.TrimPrefix(text, "&"))
		msg := fmt.Sprintf("cannot lend &%s to the inout parameter of %s: %s holds a %s", operand, callee.name, operand, t.heldText(have))
		if wantDepth != haveDepth-1 || !t.lendFits(have.Elem, want, free) {
			return inoutError(at, fmt.Sprintf("%s, and the parameter takes a %s", msg, t.lendTypeText(want)), takes)
		}
		hint := fmt.Sprintf("pass `%s` without `&`: a Ptr lends its pointee", operand)
		if !have.NonNull {
			hint = fmt.Sprintf("pass `RequirePtr(%s)` without `&`: a Ptr lends its pointee (RequirePtr panics on nil)", operand)
		}
		return inoutError(at, fmt.Sprintf("%s, and the parameter takes the %s it points to", msg, t.lendTypeText(want)), hint)
	}
	msg := fmt.Sprintf("cannot lend %s to the inout parameter of %s: %s holds a %s, and the parameter takes a %s",
		text, callee.name, text, t.heldText(have), t.lendTypeText(want))
	hint := takes
	if wantDepth == haveDepth && t.lendFits(have, want, free) {
		hint = fmt.Sprintf("lend the variable holding the Ptr with `&%s`; without `&`, a Ptr lends its pointee", text)
		if !t.isInoutPlace(arg) {
			hint = fmt.Sprintf("lend a `var` holding the Ptr: `var q = %s`, then pass `&q`; without `&`, a Ptr lends its pointee", text)
		}
	}
	return inoutError(at, msg, hint)
}

// inoutTakesPointee reports whether the `inout` parameter i of callee
// certainly takes what have, a held pointer, points to, one pointer less
// deep: then a *T that may be nil lends it through RequirePtr. Otherwise,
// as when the parameter takes the pointer itself (`inout x GP` for a Go
// `type GP = *T`), it is lent with `&`.
func (t *galaASTTransformer) inoutTakesPointee(have transpiler.PointerType, callee inoutCallee, i int) bool {
	want, free, known := t.inoutLentType(callee, i)
	if !known {
		return false
	}
	wantDepth, wantExact := t.pointerDepth(want, free)
	haveDepth, haveExact := t.pointerDepth(have, nil)
	return wantExact && haveExact && wantDepth == haveDepth-1
}

// heldText names have, a held pointer, in a message: its type (lendTypeText),
// or "pointer" when GALA does not know what it points to.
func (t *galaASTTransformer) heldText(have transpiler.PointerType) string {
	if transpiler.IsUnusable(have.Elem) {
		return "pointer"
	}
	return t.lendTypeText(have)
}

// lendTypeText names typ in a lend message the way this file names it: a
// type of this package unqualified, wherever it was resolved from (Go type
// information qualifies `main.GoCounter`), so both sides of a message read
// alike.
func (t *galaASTTransformer) lendTypeText(typ transpiler.Type) string {
	text := displayType(typ)
	if t.packageName == "" {
		return text
	}
	return regexp.MustCompile(`\b`+regexp.QuoteMeta(t.packageName)+`\.`).ReplaceAllString(text, "")
}

// depthMismatch reports whether what is lent, lent pointers deep, certainly
// does not fill a parameter want pointers deep. Each depth is exact, or a
// lower bound (pointerDepth): the mismatch is certain when both are exact
// and differ, when the lent depth already exceeds an exact parameter
// depth, or when an exact lent depth is short of the parameter's least.
func depthMismatch(lent int, lentExact bool, want int, wantExact bool) bool {
	switch {
	case lentExact && wantExact:
		return lent != want
	case wantExact:
		return lent > want
	case lentExact:
		return lent < want
	}
	return false
}

// lendFits reports whether a place of type have fills an inout parameter of
// type want, type arguments included, a type parameter of free (one Go
// infers) taking any type. It only picks a hint: one that suggests the
// place is given only when it fits.
func (t *galaASTTransformer) lendFits(have, want transpiler.Type, free []string) bool {
	have, _ = t.unaliasedType(have, nil)
	want, _ = t.unaliasedType(want, free)
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
// for a function value, whose type arguments the call does not spell.
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
	return t.substituteGoTypeParams(derefType(callee.paramTypes[i]), subst), free, true
}

// pointerDepth counts the pointers typ is, through GALA and Go aliases and
// Go defined pointer types (pointerView; `type P = *T` and `type NP *T`
// are one each). The count is exact when they lead
// to a type that is no pointer: a GALA type, a type parameter in scope, a
// type that is no named one, or a Go type that is no alias (or has no Go
// type information, as on master). It is only a lower bound when they
// lead to a type that may be a pointer: an unknown type, a type parameter
// of free (one Go infers), or a Go alias whose target does not resolve.
func (t *galaASTTransformer) pointerDepth(typ transpiler.Type, free []string) (depth int, exact bool) {
	// The hop bound stops a type that points to itself through aliases.
	for hop := 0; hop < 64; hop++ {
		var resolved bool
		typ, resolved = t.pointerView(typ, free)
		if ptr, isPtr := typ.(transpiler.PointerType); isPtr {
			depth++
			typ = ptr.Elem
			continue
		}
		return depth, resolved && !transpiler.IsUnusable(typ) && !isTypeParamNamed(typ, free)
	}
	return depth, false
}

// goDefinedUnderlying is the underlying type of typ, a type Go defines
// (goDeclaredType) or an instance of a generic Go alias, instantiated with
// typ's type arguments; nil for any other type. A defined Go struct's is the
// placeholder `any`, never a StructType; a generic alias of a struct gives nil.
func (t *galaASTTransformer) goDefinedUnderlying(typ transpiler.Type, free []string) transpiler.Type {
	_, data, args := t.goDeclaredType(typ, free)
	if data == nil {
		return nil
	}
	if data.Kind != "alias" {
		return t.instantiateGoType(data.Underlying, data.TypeParams, args)
	}
	// A generic alias (`type Handler[T any] = func(T) error`) reads as its
	// target's underlying type, the type arguments substituted, as a generic
	// defined type does: not a struct's (the placeholder), not one with a
	// part go/types could not resolve, and not one naming a generic Go type
	// the record spells without its type arguments (`chan Box[T]` recorded
	// as `chan Box`). A plain alias is left to unaliasedType.
	if len(data.TypeParams) == 0 || data.Underlying == nil || data.Underlying.IsAny() {
		return nil
	}
	if u := t.instantiateGoType(data.Underlying, data.TypeParams, args); !transpiler.ContainsUnusable(u) && !t.namesBareGoGeneric(u) {
		return u
	}
	return nil
}

// namesBareGoGeneric reports whether typ mentions a generic Go type with no
// type arguments, which names no type Go can spell.
func (t *galaASTTransformer) namesBareGoGeneric(typ transpiler.Type) bool {
	switch v := typ.(type) {
	case transpiler.GenericType:
		return slices.ContainsFunc(v.Params, t.namesBareGoGeneric)
	case transpiler.NamedType:
		data := t.goTypeInfo.GetTypeData(t.goTypeLookupName(v))
		return data != nil && len(data.TypeParams) > 0
	case transpiler.ArrayType:
		return t.namesBareGoGeneric(v.Elem)
	case transpiler.PointerType:
		return t.namesBareGoGeneric(v.Elem)
	case transpiler.ChanType:
		return t.namesBareGoGeneric(v.Elem)
	case transpiler.MapType:
		return t.namesBareGoGeneric(v.Key) || t.namesBareGoGeneric(v.Elem)
	case transpiler.FuncType:
		return slices.ContainsFunc(v.Params, t.namesBareGoGeneric) || slices.ContainsFunc(v.Results, t.namesBareGoGeneric)
	case transpiler.StructType, transpiler.InterfaceType:
		return slices.ContainsFunc(transpiler.MemberTypes(v), t.namesBareGoGeneric)
	}
	return false
}

// goUnderlying is typ through aliases (unaliasedType) and then, when that is
// a type Go defines or an instance of a generic Go alias, its underlying type
// (goDefinedUnderlying); defined reports the second step.
func (t *galaASTTransformer) goUnderlying(typ transpiler.Type) (under transpiler.Type, defined bool) {
	under, _ = t.unaliasedType(typ, nil)
	if u := t.goDefinedUnderlying(under, nil); u != nil {
		return u, true
	}
	return under, false
}

// goDeclaredType is the Go type information of typ, a type Go declares: a
// named type, or the basic type a GALA declaration names a Go type by,
// given type arguments (args) or not. data is nil when Go type information
// has no record of it, or typ is a GALA type or a type parameter (in
// scope, or of free): those are never looked up as Go types.
func (t *galaASTTransformer) goDeclaredType(typ transpiler.Type, free []string) (key string, data *transpiler.GoTypeData, args []transpiler.Type) {
	if gen, isGen := typ.(transpiler.GenericType); isGen {
		typ, args = gen.Base, gen.Params
	}
	var name string
	var qualified bool
	switch v := typ.(type) {
	case transpiler.NamedType:
		name, qualified = v.Name, v.Package != ""
	case transpiler.BasicType:
		name = v.Name
	default:
		return "", nil, nil
	}
	if t.getTypeMeta(typ.BaseName()) != nil || !qualified && (slices.Contains(free, name) || t.isActiveTypeParam(name)) {
		return "", nil, nil
	}
	key, data = t.goTypeDataFor(typ)
	return key, data, args
}

// instantiateGoType is typ, which a Go declaration with the type
// parameters params names, with args for them. It is nil when typ is, or
// when the number of type arguments does not match.
func (t *galaASTTransformer) instantiateGoType(typ transpiler.Type, params []string, args []transpiler.Type) transpiler.Type {
	if typ == nil || len(args) == 0 && len(params) == 0 {
		return typ
	}
	if len(args) != len(params) {
		return nil
	}
	subst := make(typeSubstMap, len(args))
	for k, tp := range params {
		subst[tp] = args[k]
	}
	return t.substituteGoTypeParams(typ, subst)
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
