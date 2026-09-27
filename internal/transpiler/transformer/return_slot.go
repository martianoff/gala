package transformer

import (
	"go/ast"

	"martianoff/gala/internal/parser/grammar"
	"martianoff/gala/internal/transpiler"
)

// returnSlot is the result type that a `return`, a `bind` block and the
// return-type fallbacks see in the body being lowered: the result type of the
// innermost function, lambda, or construct lowered to an IIFE.
//
// A lambda has its own slot, never the enclosing function's. When its result
// type is known (an annotation, or a concrete expected type) typ holds it. When
// it is not, the slot is fillable: the body's own values fill it, in any order.
// A `return` whose value has a settled type fills the slot; one whose value
// does not (`return None()`, `return Failure(e)`) is deferred and lowered again
// once the whole body is lowered (see settleReturnSlot).
type returnSlot struct {
	typ      transpiler.Type
	fillable bool
	body     *grammar.BlockContext // a fillable lambda's block body: its trailing value is a result value
	deferred []deferredReturn
	guesses  []ast.Expr // result values typed from a guess (the match subject): they never fill the slot
}

// deferredReturn is a result value (a `return` value or the body's trailing
// value) in a fillable slot that had no settled type when it was reached.
// value is the expression emitted for it so far, replaced wherever it appears
// once the value is settled; scope and subject are the lexical scope and match
// subject it is lowered in again; err is the first lowering's error.
type deferredReturn struct {
	value   ast.Expr
	exprCtx grammar.IExpressionContext
	scope   *scope
	subject transpiler.Type
	err     error
}

// enterReturnSlot makes s the current return slot and returns the function
// that restores the previous one, for `defer t.enterReturnSlot(s)()`.
func (t *galaASTTransformer) enterReturnSlot(s returnSlot) func() {
	prev := t.returnSlot
	t.returnSlot = s
	return func() { t.returnSlot = prev }
}

// enterIIFEReturnSlot is enterReturnSlot for a value-position construct
// lowered to an IIFE (a match or an if-expression), whose value fills a slot
// of type typ. A `return` in it leaves only the IIFE, so it sees typ, and it
// never fills or defers into an enclosing lambda's fillable slot.
func (t *galaASTTransformer) enterIIFEReturnSlot(typ transpiler.Type) func() {
	switch {
	case !transpiler.IsUnusable(typ):
		return t.enterReturnSlot(returnSlot{typ: typ})
	case t.returnSlot.fillable:
		return t.enterReturnSlot(returnSlot{})
	}
	return func() {}
}

// isSettledType reports whether typ can fix a lambda's result slot: it is
// concrete and fully resolved — no masked part, not any or void, and no leaf
// naming a type parameter that is not in scope (the `T` of `Failure(e)`'s
// `Try[T]` before T is known).
func (t *galaASTTransformer) isSettledType(typ transpiler.Type) bool {
	return !typeHasMaskedPart(typ) && !typ.IsAny() && !typ.IsVoid() && !t.typeMentionsUnresolvedTypeParam(typ)
}

// typeMentionsUnresolvedTypeParam reports whether typ has a leaf that is
// neither a primitive, a known type, nor a type parameter of the declaration
// being lowered: a type parameter of some callee left unbound.
func (t *galaASTTransformer) typeMentionsUnresolvedTypeParam(typ transpiler.Type) bool {
	switch v := typ.(type) {
	case transpiler.BasicType:
		return t.isUnresolvedTypeParamName(v.Name)
	case transpiler.NamedType:
		return v.Package == "" && t.isUnresolvedTypeParamName(v.Name)
	case transpiler.GenericType:
		for _, p := range v.Params {
			if t.typeMentionsUnresolvedTypeParam(p) {
				return true
			}
		}
		return false
	case transpiler.ArrayType:
		return t.typeMentionsUnresolvedTypeParam(v.Elem)
	case transpiler.PointerType:
		return t.typeMentionsUnresolvedTypeParam(v.Elem)
	case transpiler.MapType:
		return t.typeMentionsUnresolvedTypeParam(v.Key) || t.typeMentionsUnresolvedTypeParam(v.Elem)
	case transpiler.FuncType:
		for _, p := range v.Params {
			if t.typeMentionsUnresolvedTypeParam(p) {
				return true
			}
		}
		for _, r := range v.Results {
			if t.typeMentionsUnresolvedTypeParam(r) {
				return true
			}
		}
	}
	return false
}

func (t *galaASTTransformer) isUnresolvedTypeParamName(name string) bool {
	if name == "" || transpiler.IsPrimitiveType(name) || t.activeTypeParams[name] {
		return false
	}
	return t.lookupTypeName(name).IsNil()
}

// tryFillReturnSlot fills a fillable, still empty slot with typ when typ is
// settled. It reports whether the slot now has a type.
func (t *galaASTTransformer) tryFillReturnSlot(typ transpiler.Type) bool {
	s := &t.returnSlot
	if !transpiler.IsUnusable(s.typ) {
		return true
	}
	if !s.fillable || transpiler.IsUnusable(typ) || !t.isSettledType(typ) {
		return false
	}
	s.typ = typ
	return true
}

// lowerReturnValue lowers the value of a `return` statement against the
// current slot; in a fillable slot that is still empty, through
// lowerFillingValue.
func (t *galaASTTransformer) lowerReturnValue(exprCtx grammar.IExpressionContext) (*ast.ReturnStmt, error) {
	if t.returnSlotPending() {
		return &ast.ReturnStmt{Results: []ast.Expr{t.lowerFillingValue(exprCtx, true)}}, nil
	}
	expr, err := t.lowerAgainst(exprCtx, resultSlot(t.returnSlot.typ), false)
	if err != nil {
		return nil, err
	}
	return &ast.ReturnStmt{Results: []ast.Expr{t.unwrapImmutable(expr)}}, nil
}

// returnSlotPending reports whether the current slot is fillable and still
// empty: its lambda's result values are lowered through lowerFillingValue.
func (t *galaASTTransformer) returnSlotPending() bool {
	return t.returnSlot.fillable && transpiler.IsUnusable(t.returnSlot.typ)
}

// lowerFillingValue lowers a result value of a lambda whose slot is pending —
// a `return` value, or the body's trailing value — on its own. A settled type
// fills the slot. A value that fails to lower is deferred until the body is
// lowered (see settleReturnSlot) and emitted as a placeholder meanwhile, so a
// `return` still reads as a value return (a statement-position match whose
// arm holds it is inlined, not lowered to a void IIFE). A `return` value whose
// type is not settled is deferred too (deferUnsettled); a trailing value is
// not, since it may legitimately be void or have a type the transpiler cannot
// name.
//
// Only a value's own type may fill the slot, never a guess: the enclosing
// match subject, which a zero-arg constructor such as `None()` falls back to,
// is hidden while the value is lowered here.
func (t *galaASTTransformer) lowerFillingValue(exprCtx grammar.IExpressionContext, deferUnsettled bool) ast.Expr {
	subject := t.currentMatchSubjectType
	expr, err := t.lowerOwnValue(exprCtx)
	if err == nil {
		expr = t.unwrapImmutable(expr)
		if t.tryFillReturnSlot(t.getExprTypeName(expr)) || !deferUnsettled {
			return expr
		}
	} else {
		expr = ast.NewIdent("nil")
	}
	t.returnSlot.deferred = append(t.returnSlot.deferred, deferredReturn{
		value: expr, exprCtx: exprCtx, scope: t.currentScope, subject: subject, err: err,
	})
	return expr
}

// lowerOwnValue lowers exprCtx with the enclosing match subject hidden, so the
// value's type is its own and not a guess from the subject.
func (t *galaASTTransformer) lowerOwnValue(exprCtx grammar.IExpressionContext) (ast.Expr, error) {
	subject := t.currentMatchSubjectType
	t.currentMatchSubjectType = nil
	defer func() { t.currentMatchSubjectType = subject }()
	return t.transformExpression(exprCtx)
}

// settleReturnSlot finishes a fillable slot once the lambda body is lowered.
// If no `return` filled it, the body's other result values (the promoted
// trailing value, a `bind` chain) are tried. Each deferred return is then
// lowered again in its own scope: against the filled slot, or — when nothing
// filled it — with its match-subject context back, where a value that still
// has no settled type is an error.
func (t *galaASTTransformer) settleReturnSlot(body *ast.BlockStmt) error {
	s := &t.returnSlot
	if !s.fillable || len(s.deferred) == 0 {
		return nil
	}
	// Values that must not fill the slot: the deferred ones, and guesses.
	deferred := make(map[ast.Expr]bool, len(s.deferred)+len(s.guesses))
	for _, d := range s.deferred {
		deferred[d.value] = true
	}
	for _, g := range s.guesses {
		deferred[g] = true
	}
	if transpiler.IsUnusable(s.typ) {
		ast.Inspect(body, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.FuncLit:
				return false // a nested function's returns are its own
			case *ast.ReturnStmt:
				if len(n.Results) == 1 && !deferred[n.Results[0]] {
					t.tryFillReturnSlot(t.getExprTypeName(n.Results[0]))
				}
			}
			return transpiler.IsUnusable(s.typ)
		})
	}
	filled := !transpiler.IsUnusable(s.typ)
	settled := make(map[ast.Expr]ast.Expr, len(s.deferred))
	outerScope, outerSubject := t.currentScope, t.currentMatchSubjectType
	defer func() { t.currentScope, t.currentMatchSubjectType = outerScope, outerSubject }()
	for _, d := range s.deferred {
		t.currentScope, t.currentMatchSubjectType = d.scope, d.subject
		expr, err := t.lowerAgainst(d.exprCtx, resultSlot(s.typ), false)
		if err != nil {
			return err
		}
		expr = t.unwrapImmutable(expr)
		if !filled && !t.isSettledType(t.getExprTypeName(expr)) {
			return t.semanticErrorAt(d.exprCtx, "cannot infer the result type of this lambda: no `return` or result value has a fully known type — annotate the lambda's result type (e.g. `(x int) Option[int] => { ... }`)")
		}
		settled[d.value] = expr
	}
	// Put each settled value where its placeholder was emitted: a return
	// (possibly inside a bind continuation) or the trailing statement.
	placed := make(map[ast.Expr]bool, len(settled))
	place := func(slot *ast.Expr) {
		if expr, ok := settled[*slot]; ok {
			placed[*slot] = true
			*slot = expr
		}
	}
	ast.Inspect(body, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.ReturnStmt:
			if len(n.Results) == 1 {
				place(&n.Results[0])
			}
		case *ast.ExprStmt:
			place(&n.X)
		}
		return true
	})
	for _, d := range s.deferred {
		if !placed[d.value] {
			return t.semanticErrorAt(d.exprCtx, "internal error: the lowered result value was not found in the lambda body")
		}
	}
	s.deferred = nil
	return nil
}
