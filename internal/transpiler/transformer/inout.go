package transformer

import (
	"fmt"
	"go/ast"
	"go/token"
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

// inoutCallee is a GALA function or method that declares an `inout`
// parameter: the parameters' `inout` flags and names, and its name. shift is
// 1 when the call passes the receiver as its first argument (a method
// lowered to a function).
type inoutCallee struct {
	inout  []bool
	params []string
	name   string
	shift  int
}

// inoutCalleeOf is the callee of call, the lowering of a call of base, when
// it declares an `inout` parameter; ok is false otherwise.
func (t *galaASTTransformer) inoutCalleeOf(base ast.Expr, call *ast.CallExpr) (c inoutCallee, ok bool) {
	receiver, method, _ := t.splitCallTarget(base)
	if receiver != nil && method != "" {
		if !t.inoutNames[method] {
			return c, false
		}
		recvType, key := t.resolveReceiverTypeAndLookupKey(receiver, method)
		if transpiler.IsUnusable(recvType) {
			return c, false
		}
		meta := t.getTypeMeta(key)
		if meta == nil || meta.Methods[method] == nil {
			return c, false
		}
		m := meta.Methods[method]
		c = inoutCallee{inout: m.InoutParams, params: m.ParamNames, name: method}
		if sel, isSel := call.Fun.(*ast.SelectorExpr); !isSel || sel.Sel.Name != method {
			c.shift = 1
		}
		return c, len(c.inout) > 0
	}
	name := t.extractFuncName(base)
	// A qualified call names `pkg.F`; the set holds F.
	if !t.inoutNames[name[strings.LastIndex(name, ".")+1:]] {
		return c, false
	}
	fm := t.getFunction(name)
	if fm == nil {
		return c, false
	}
	return inoutCallee{inout: fm.InoutParams, params: fm.ParamNames, name: fm.Name}, len(fm.InoutParams) > 0
}

// collectInoutNames is the names of the functions and methods of r that
// declare an `inout` parameter: the only callees checkInoutArgs resolves.
func collectInoutNames(r *transpiler.RichAST) map[string]bool {
	if r == nil {
		return nil
	}
	var names map[string]bool
	add := func(name string) {
		if names == nil {
			names = make(map[string]bool)
		}
		names[name] = true
	}
	for _, f := range r.Functions {
		if len(f.InoutParams) > 0 {
			add(f.Name)
		}
	}
	for _, typ := range r.Types {
		for _, m := range typ.Methods {
			if len(m.InoutParams) > 0 {
				add(m.Name)
			}
		}
	}
	return names
}

// checkInoutArgs checks the arguments call (the lowering of the call c of
// base) passes to the `inout` parameters of its callee: each must be `&` of
// a mutable place (isInoutPlace), or a Ptr the caller holds (GALA-E0111).
func (t *galaASTTransformer) checkInoutArgs(base ast.Expr, expr ast.Expr, c callSrc) error {
	call, ok := expr.(*ast.CallExpr)
	if !ok || len(call.Args) == 0 || len(t.inoutNames) == 0 {
		return nil
	}
	callee, ok := t.inoutCalleeOf(base, call)
	if !ok {
		return nil
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
