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
	deferred []deferredReturn
}

// deferredReturn is a `return` in a fillable slot whose value had no settled
// type when it was reached. stmt is patched when the return is settled; scope
// and subject are the lexical scope and match subject the value is lowered in
// again; err is the first lowering's error.
type deferredReturn struct {
	stmt    *ast.ReturnStmt
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
// current slot. In a fillable slot that is still empty, the value is lowered
// on its own: a settled type fills the slot, anything else defers the return
// until the body is lowered (see settleReturnSlot).
//
// Only the value's own type may fill the slot, never a guess: the enclosing
// match subject, which a zero-arg constructor such as `None()` falls back to,
// is hidden while the value is lowered here.
func (t *galaASTTransformer) lowerReturnValue(exprCtx grammar.IExpressionContext) (*ast.ReturnStmt, error) {
	if !t.returnSlot.fillable || !transpiler.IsUnusable(t.returnSlot.typ) {
		expr, err := t.lowerAgainst(exprCtx, resultSlot(t.returnSlot.typ), false)
		if err != nil {
			return nil, err
		}
		return &ast.ReturnStmt{Results: []ast.Expr{t.unwrapImmutable(expr)}}, nil
	}
	subject := t.currentMatchSubjectType
	t.currentMatchSubjectType = nil
	expr, err := t.transformExpression(exprCtx)
	t.currentMatchSubjectType = subject
	if err == nil {
		expr = t.unwrapImmutable(expr)
		if t.tryFillReturnSlot(t.getExprTypeName(expr)) {
			return &ast.ReturnStmt{Results: []ast.Expr{expr}}, nil
		}
	} else {
		// A placeholder until the return is settled, so it still reads as a
		// value return: a statement-position match whose arm holds it must
		// be inlined, not lowered to a void IIFE.
		expr = ast.NewIdent("nil")
	}
	stmt := &ast.ReturnStmt{Results: []ast.Expr{expr}}
	t.returnSlot.deferred = append(t.returnSlot.deferred, deferredReturn{
		stmt: stmt, exprCtx: exprCtx, scope: t.currentScope, subject: subject, err: err,
	})
	return stmt, nil
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
	if transpiler.IsUnusable(s.typ) {
		deferred := make(map[*ast.ReturnStmt]bool, len(s.deferred))
		for _, d := range s.deferred {
			deferred[d.stmt] = true
		}
		ast.Inspect(body, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.FuncLit:
				return false // a nested function's returns are its own
			case *ast.ReturnStmt:
				if !deferred[n] && len(n.Results) == 1 {
					t.tryFillReturnSlot(t.getExprTypeName(n.Results[0]))
				}
			}
			return transpiler.IsUnusable(s.typ)
		})
	}
	filled := !transpiler.IsUnusable(s.typ)
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
		d.stmt.Results = []ast.Expr{expr}
	}
	s.deferred = nil
	return nil
}
