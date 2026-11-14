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

// `inout` parameters and receivers (docs/OWNERSHIP_DESIGN.MD, 3.2)
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
				c = inoutCallee{inout: m.InoutParams, params: m.ParamNames, name: method, recvInout: m.InoutReceiver, method: m, recvType: recvType, declared: true}
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
				c = inoutCallee{inout: fm.InoutParams, params: fm.ParamNames, name: fm.Name, declared: true, fm: fm}
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
		return inoutCallee{inout: ft.Inout, name: name}, true
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
			continue
		}
		if ptr, isPtr := t.getExprTypeName(arg).(transpiler.PointerType); isPtr && ptr.NonNull {
			continue // a Ptr the caller holds lends its pointee
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
		if _, isPtr := t.getExprTypeName(e.X).(transpiler.PointerType); isPtr {
			return true
		}
		return t.isInoutPlace(e.X)
	}
	return false
}
