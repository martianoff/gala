package transformer

import (
	"fmt"
	"go/ast"
	"go/token"
	"slices"

	"martianoff/gala/galaerr"
	"martianoff/gala/internal/parser/native"
	"martianoff/gala/internal/transpiler"
)

// The ownership checks of `inout` places (docs/OWNERSHIP.MD#the-rules-and-how-to-fix-each-error)
//
// An `inout` binding lends the caller's place for one call. Nothing may keep
// a reference to it past the call (no escape, GALA-E0113), no call may lend
// one place twice (exclusivity, GALA-E0112), and no panic Try catches may
// leave it half updated (GALA-E0115). Each check works on what the binding
// lowers to:
//
//   - a read of x is `(*x)` (inoutRead), so a capture is a read inside a
//     function literal that x is bound outside of (checkInoutCapture);
//   - `&x` is the pointer x, marked operandInoutLend (checkInoutLend); it is
//     legal only as the argument of an `inout` parameter (checkInoutLends);
//   - a method called on the place, or taken as a method value, may receive
//     its address (checkInoutReceiverUse).

// inoutCodedError is the error code at at.
func inoutCodedError(code galaerr.ErrorCode, at srcNode, msg, hint string) error {
	r := at.rng()
	return spanErrorAt(code, r, r, msg, hint)
}

// inoutRoot is the `inout` binding expr reads or lends a place of: x for
// `(*x)`, `(*x).f.g`, the lend `x` of `&x` and the lend `&(*x).f`; ok is
// false for anything else.
func (t *galaASTTransformer) inoutRoot(expr ast.Expr) (string, bool) {
	if ptr, ok := t.inoutAddr(expr); ok {
		return ptr.(*ast.Ident).Name, true
	}
	switch e := ast.Unparen(expr).(type) {
	case *ast.SelectorExpr:
		return t.inoutRoot(e.X)
	case *ast.UnaryExpr:
		if e.Op == token.AND {
			return t.inoutRoot(e.X)
		}
	case *ast.Ident:
		if t.marked(e, operandInoutLend) {
			return e.Name, true
		}
	}
	return "", false
}

// checkInoutCapture rejects a read of the `inout` binding name, bound in
// scope s and written at at, from inside a function literal declared in its
// scope: the literal could outlive the call (GALA-E0113), and inside the
// thunk of Try(...) a caught panic would leave the place half updated
// (GALA-E0115). captured and inTry say the read is already inside such a
// literal (a by-name argument, which has no scope of its own).
func (t *galaASTTransformer) checkInoutCapture(s *scope, name string, at srcNode, captured, inTry bool) error {
	for c := t.currentScope; c != nil && c != s; c = c.parent {
		captured = captured || c.lambda
		inTry = inTry || c.tryRegion
	}
	switch {
	case inTry:
		return inoutCodedError(galaerr.CodeInoutInTry, at,
			fmt.Sprintf("inout place %s is used inside Try: a panic Try catches would leave the caller's place half updated", name),
			fmt.Sprintf("copy it into a local `var` first, and assign %s back once Try has succeeded", name))
	case captured:
		return inoutCodedError(galaerr.CodeInoutEscape, at,
			fmt.Sprintf("inout place %s is captured by a lambda, which could outlive the call that lends it", name),
			fmt.Sprintf("pass the value instead (a copy), or give the lambda its own inout parameter and lend %s to it with `&%s`", name, name))
	}
	return nil
}

// checkInoutLend checks `&operand`, lowered to lowered and written at at,
// where operand is an `inout` place (inoutRoot): only an argument may lend
// it, and the call checks that the argument fills an `inout` parameter
// (checkInoutLends). It marks lowered as a lend.
func (t *galaASTTransformer) checkInoutLend(at srcNode, operand, lowered ast.Expr) error {
	name, ok := t.inoutRoot(operand)
	if !ok {
		return nil
	}
	if !isCallArgument(at.node) {
		return inoutCodedError(galaerr.CodeInoutEscape, at,
			fmt.Sprintf("&%s escapes: a pointer to the inout place %s may only be lent to an inout parameter of a call", name, name),
			fmt.Sprintf("read the value (`%s`) to keep a copy", name))
	}
	t.markOperand(lowered, operandInoutLend)
	t.inoutLends = true
	return nil
}

// isCallArgument reports whether n, an expression node, is a call
// argument by itself (`f(&x)`, `f(p = &x)`), not part of a larger one.
func isCallArgument(n native.GalaNodeRef) bool {
	for p := n.Parent(); p.Ok(); p = p.Parent() {
		if p.Kind() == native.GalaKIND_argument {
			return true
		}
		if p.ChildCount() != 1 {
			return false
		}
	}
	return false
}

// lendEscapeError is the GALA-E0113 of the lend arg, written at at, that
// fills anything but an `inout` parameter.
func (t *galaASTTransformer) lendEscapeError(arg ast.Expr, at srcNode) error {
	name, _ := t.inoutRoot(arg)
	return inoutCodedError(galaerr.CodeInoutEscape, at,
		fmt.Sprintf("&%s escapes: %s is lent to a parameter that is not inout, which may keep the pointer past the call", name, name),
		fmt.Sprintf("pass the value (`%s`) instead, or declare the parameter `inout`", name))
}

// checkInoutLends rejects a lend (`&x` of an `inout` place) that call passes
// to anything but an `inout` parameter of callee (GALA-E0113): a GALA
// parameter that is not `inout`, a Go function, a function value whose type
// does not say `inout`. callee is the zero inoutCallee when unknown. A lend
// it passes to an `inout` parameter is recorded as approved, so that an
// enclosing construct finds no stray one (checkStrayLends).
func (t *galaASTTransformer) checkInoutLends(call *ast.CallExpr, callee inoutCallee, c callSrc) error {
	if !t.inoutLends {
		return nil
	}
	for j, arg := range call.Args {
		if !t.marked(arg, operandInoutLend) {
			continue
		}
		i := j - callee.shift
		if i >= 0 && i < len(callee.inout) && callee.inout[i] {
			if t.inoutApproved == nil {
				t.inoutApproved = make(map[ast.Node]bool)
			}
			t.inoutApproved[arg] = true
			continue
		}
		at := c.at()
		if i >= 0 {
			at = t.inoutArgAt(c, callee, i)
		}
		return t.lendEscapeError(arg, at)
	}
	return t.checkStrayLends(call, c.at())
}

// checkStrayLends rejects a lend inside expr, a lowered call or
// construction, that no call passed to an `inout` parameter: one built into
// a literal, or nested where a call cannot see it (GALA-E0113).
func (t *galaASTTransformer) checkStrayLends(expr ast.Expr, at srcNode) error {
	if !t.inoutLends {
		return nil
	}
	var stray ast.Expr
	ast.Inspect(expr, func(n ast.Node) bool {
		if e, ok := n.(ast.Expr); ok && stray == nil && t.marked(e, operandInoutLend) && !t.inoutApproved[e] {
			stray = e
		}
		return stray == nil
	})
	if stray == nil {
		return nil
	}
	return t.lendEscapeError(stray, at)
}

// checkInoutReceiverUse rejects selecting method on base, an `inout` place
// (inoutRoot) of type xType, when that could keep the place's address past
// the call (GALA-E0113):
//
//   - a method value of a pointer-receiver method (`c.inc` not called) holds
//     the pointer;
//   - a call of a pointer-receiver method that is neither `inout` nor
//     borrowed (its ownership summary, BorrowSummary.ReceiverBorrowed) may
//     retain it, and so may a Go pointer method.
//
// A value-receiver method gets a copy and is always fine.
func (t *galaASTTransformer) checkInoutReceiverUse(base ast.Expr, xType transpiler.Type, method string, called bool, line, col int) error {
	if !t.inoutTypesSeen {
		return nil
	}
	name, ok := t.inoutRoot(base)
	if !ok {
		return nil
	}
	recvBase, meta, goData, _ := t.receiverTypeInfo(xType)
	var m *transpiler.MethodMetadata
	if meta != nil {
		m = meta.Methods[method]
	}
	goMethod := m == nil && goData != nil && goData.PointerMethods[method] || m != nil && m.GoDeclared && m.PointerReceiver
	pointer := m != nil && m.PointerReceiver || goMethod
	if called && pointer && !goMethod && m.Borrow == nil && t.richAST != nil && t.richAST.OwnBorrowSummaries != nil {
		// A method of this package: its summary is computed on demand.
		t.richAST.OwnBorrowSummaries()
	}
	switch {
	case !pointer:
		return nil
	case !called:
		return galaerr.NewCodedSemanticError(galaerr.CodeInoutEscape, line, col,
			fmt.Sprintf("method value %s.%s captures the inout place %s, which could outlive the call that lends it", name, method, name),
			fmt.Sprintf("call it on the spot (`%s.%s(...)`), or wrap the call in a function that takes %s inout", name, method, name))
	case m != nil && (m.InoutReceiver || m.Borrow != nil && m.Borrow.ReceiverBorrowed):
		return nil
	case goMethod && t.isNoRetainGoMethod(recvBase, method):
		return nil // on the compiler's no-retain allow-list
	}
	return galaerr.NewCodedSemanticError(galaerr.CodeInoutEscape, line, col,
		fmt.Sprintf("calling %s on the inout place %s lends its address to a method that may keep it past the call", method, name),
		fmt.Sprintf("declare %s's receiver `inout`, or copy the place first: `var tmp = %s`, call it on tmp, and assign %s = tmp", method, name, name))
}

// isNoRetainGoMethod reports whether method of the Go type base is on the
// no-retain allow-list (transpiler.IsNoRetainMethod): a Go pointer method
// that keeps no reference to its receiver, verified against the Go
// compiler's escape analysis in CI.
func (t *galaASTTransformer) isNoRetainGoMethod(base transpiler.Type, method string) bool {
	named, isNamed := base.(transpiler.NamedType)
	if !isNamed {
		return false
	}
	path := named.ImportPath
	if path == "" {
		entry, ok := t.goImportForPathlessType(named)
		if !ok {
			return false
		}
		path = entry.Path
	}
	return transpiler.IsNoRetainMethod(path, named.Name, method)
}

// checkInoutThunk rejects a by-name argument (expr wrapped as a thunk) that
// reads an `inout` place: the thunk is a function literal, so the place is
// captured (GALA-E0113), or used inside Try (GALA-E0115).
func (t *galaASTTransformer) checkInoutThunk(expr ast.Expr, tryThunk bool, at srcNode) error {
	if !t.inoutTypesSeen {
		return nil
	}
	var name string
	ast.Inspect(expr, func(n ast.Node) bool {
		if e, ok := n.(ast.Expr); ok && name == "" {
			if root, isRoot := t.inoutRoot(e); isRoot {
				name = root
			}
		}
		return name == ""
	})
	if name == "" {
		return nil
	}
	return t.checkInoutCapture(t.bindingScope(name), name, at, true, tryThunk)
}

// checkInoutFuncSlot rejects a function value expr, written at at, filling
// a slot of the function type expected whose parameters differ from its
// own in being `inout` (GALA-E0113): a function whose parameter is not
// `inout` may keep the pointer an `inout` slot lends it, and a function
// whose parameter is `inout` relies on an exclusive place that a
// `func(Ptr[T])` caller does not promise.
func (t *galaASTTransformer) checkInoutFuncSlot(expr ast.Expr, expected transpiler.Type, at srcNode) error {
	want, ok := t.followAliasChain(expected).(transpiler.FuncType)
	if !ok || isNilIdent(expr) || !want.HasInout() && !t.inoutTypesSeen {
		return nil
	}
	var have transpiler.FuncType
	if lit, isLit := expr.(*ast.FuncLit); isLit {
		// A literal's own parameters say it; probing its type here would
		// cache it before its Go results are spread.
		have.Inout = t.inoutFuncParams(lit.Type.Params)
		for _, f := range lit.Type.Params.List {
			for range max(len(f.Names), 1) {
				have.Params = append(have.Params, t.astTypeToTranspilerType(f.Type))
			}
		}
	} else if have, ok = t.followAliasChain(t.probeExprType(expr)).(transpiler.FuncType); !ok {
		return nil
	}
	if len(have.Params) != len(want.Params) || !want.HasInout() && !have.HasInout() {
		return nil
	}
	for i := range want.Params {
		if want.IsInout(i) == have.IsInout(i) {
			continue
		}
		why, hint := "is not inout, so it may keep the pointer it is lent", "declare that parameter `inout` (a lambda: `(inout k) => …`)"
		if have.IsInout(i) {
			why, hint = "is inout, but the slot's callers may pass a pointer they share", "take a Ptr parameter, or declare the slot's parameter `inout`"
		}
		return inoutCodedError(galaerr.CodeInoutEscape, at,
			fmt.Sprintf("a %s cannot fill a %s: its parameter %d %s", displayType(have), displayType(want), i+1, why), hint)
	}
	return nil
}

// inoutPlacePath is the place lent by arg, an argument (or receiver) of a
// call: its root binding and the field path from it (`&p.lex` is p, [lex];
// a lend of an inout x is x, []). ok is false for anything that names no
// place by a path: a call result, an element, a Ptr computed by a call.
func (t *galaASTTransformer) inoutPlacePath(arg ast.Expr) (root string, path []string, ok bool) {
	e := ast.Unparen(arg)
	if addr, isAddr := e.(*ast.UnaryExpr); isAddr && addr.Op == token.AND {
		e = ast.Unparen(addr.X)
	}
	switch v := e.(type) {
	case *ast.Ident:
		return v.Name, nil, true
	case *ast.StarExpr:
		if id, isID := v.X.(*ast.Ident); isID {
			return id.Name, nil, true
		}
	case *ast.SelectorExpr:
		if root, path, ok := t.inoutPlacePath(v.X); ok {
			return root, append(slices.Clone(path), v.Sel.Name), true
		}
	}
	return "", nil, false
}

// lentPlace is one place a call lends, for checkInoutOverlap.
type lentPlace struct {
	root string
	path []string
	text string // as written, for the message
}

// overlaps reports whether p and q are the same place or one holds the other.
func (p lentPlace) overlaps(q lentPlace) bool {
	if p.root != q.root {
		return false
	}
	n := min(len(p.path), len(q.path))
	return slices.Equal(p.path[:n], q.path[:n])
}

// checkInoutOverlap rejects a call that lends one place twice
// (GALA-E0112): two `inout` arguments, or an `inout` receiver and an `inout`
// argument, naming overlapping places, or a lambda argument reading a place
// the call lends.
func (t *galaASTTransformer) checkInoutOverlap(call *ast.CallExpr, callee inoutCallee, c callSrc) error {
	var lent []lentPlace
	add := func(arg ast.Expr, text string, at srcNode) error {
		root, path, ok := t.inoutPlacePath(arg)
		if !ok {
			return nil
		}
		p := lentPlace{root: root, path: path, text: text}
		for _, q := range lent {
			if p.overlaps(q) {
				return inoutCodedError(galaerr.CodeInoutOverlap, at, fmt.Sprintf("%s and %s overlap: a call cannot lend one place twice", q.text, p.text),
					"lend disjoint places, or copy one of them into a local `var` first")
			}
		}
		lent = append(lent, p)
		return nil
	}
	if callee.recvInout {
		recv := ast.Expr(nil)
		if sel, ok := call.Fun.(*ast.SelectorExpr); ok && callee.shift == 0 {
			recv = sel.X
		} else if callee.shift == 1 && len(call.Args) > 0 {
			recv = call.Args[0]
		}
		if recv != nil {
			if err := add(recv, "the receiver", c.at()); err != nil {
				return err
			}
		}
	}
	for i, isInout := range callee.inout {
		j := i + callee.shift
		if !isInout || j >= len(call.Args) {
			continue
		}
		at := t.inoutArgAt(c, callee, i)
		if err := add(call.Args[j], at.sourceText(), at); err != nil {
			return err
		}
	}
	for _, arg := range call.Args {
		lit, ok := arg.(*ast.FuncLit)
		if !ok || len(lent) == 0 {
			continue
		}
		if p, reads := lambdaReadsPlace(lit, lent); reads {
			return inoutCodedError(galaerr.CodeInoutOverlap, c.at(), fmt.Sprintf("a lambda argument reads %s, which the same call lends", p.root),
				"compute the value before the call, or pass it to the lambda as a parameter")
		}
	}
	return nil
}

// lambdaReadsPlace reports whether the function literal lit reads the root
// of a place in lent by name: a reference that is not a field selector, in a
// literal none of whose parameters takes that name. A local of the literal
// with the same name counts too, which can only make the check stricter.
func lambdaReadsPlace(lit *ast.FuncLit, lent []lentPlace) (lentPlace, bool) {
	params := map[string]bool{}
	for _, f := range lit.Type.Params.List {
		for _, n := range f.Names {
			params[n.Name] = true
		}
	}
	var hit lentPlace
	found := false
	ast.Inspect(lit.Body, func(n ast.Node) bool {
		if found {
			return false
		}
		switch e := n.(type) {
		case *ast.SelectorExpr:
			ast.Inspect(e.X, func(m ast.Node) bool {
				if id, ok := m.(*ast.Ident); ok && !found {
					hit, found = readsLent(id.Name, params, lent)
				}
				return !found
			})
			return false
		case *ast.Ident:
			hit, found = readsLent(e.Name, params, lent)
		}
		return !found
	})
	return hit, found
}

// readsLent reports the place of lent whose root is name, unless a
// parameter of the reading literal takes that name.
func readsLent(name string, params map[string]bool, lent []lentPlace) (lentPlace, bool) {
	if params[name] {
		return lentPlace{}, false
	}
	for _, p := range lent {
		if p.root == name {
			return p, true
		}
	}
	return lentPlace{}, false
}
