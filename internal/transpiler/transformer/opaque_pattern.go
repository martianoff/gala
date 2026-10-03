package transformer

import (
	"fmt"
	"go/ast"

	"martianoff/gala/galaerr"
	"martianoff/gala/internal/parser/grammar"
	"martianoff/gala/internal/transpiler"
)

// The opaque-type pattern
//
// `case UserID(n)` unwraps an opaque value to its underlying value and
// matches the single sub-pattern against it: `n` binds an int64, `UserID(0)`
// compares it with a literal, `UserID(_)` ignores it. On a subject of the
// opaque type the pattern always matches; on an `any` (or interface) subject
// it first asserts the opaque type, which a value of the underlying type does
// not satisfy. Nothing is generated on the type: the pattern lowers to a Go
// conversion, so the type's method set stays as declared.

// generateOpaquePattern lowers `case Name(p)` for the opaque type meta.
func (t *galaASTTransformer) generateOpaquePattern(
	meta *transpiler.TypeMetadata,
	rawName string,
	argList *grammar.ArgumentListContext,
	explicitTypeArgs *grammar.ExpressionListContext,
	objExpr ast.Expr,
	matchedType transpiler.Type,
	patExprCtx grammar.IExpressionContext,
) (ast.Expr, []ast.Stmt, error) {
	tok := patExprCtx.GetStart()
	name := t.opaqueName(meta)
	var args []grammar.IArgumentContext
	if argList != nil {
		args = argList.AllArgument()
	}
	if len(args) != 1 {
		return nil, nil, galaerr.NewCodedSemanticError(galaerr.CodeOpaquePatternArity,
			tok.GetLine(), tok.GetColumn(),
			fmt.Sprintf("the opaque-type pattern %s(...) takes exactly one sub-pattern, got %d", name, len(args)),
			fmt.Sprintf("%s unwraps to its one underlying value: write %s(v) to bind it, %s(_) to ignore it", name, name, name),
		).WithSpan(tok.GetColumn() + len([]rune(rawName)))
	}

	var stmts []ast.Stmt
	var conds []ast.Expr
	base := objExpr
	if matchedType == nil || matchedType.IsAny() || t.isInterfaceType(matchedType) {
		// An interface subject holds the opaque type only when it was built
		// as one: assert it. A generic (phantom-typed) one must be spelled
		// with its type arguments, `case Id[User](n)`.
		assertType, _, err := t.structPatternAssertType(rawName, explicitTypeArgs, patExprCtx)
		if err != nil {
			return nil, nil, err
		}
		var stmt ast.Stmt
		var ok ast.Expr
		base, stmt, ok = t.assertPatternSubject(objExpr, assertType)
		stmts = append(stmts, stmt)
		conds = append(conds, ok)
	} else {
		// A pattern spelling type arguments, `case Id[Order](n)`, must name
		// the subject's own instantiation.
		patType := matchedType
		if explicitTypeArgs != nil {
			_, instantiated, err := t.structPatternAssertType(rawName, explicitTypeArgs, patExprCtx)
			if err != nil {
				return nil, nil, err
			}
			if instantiated != nil {
				patType = instantiated
			}
		}
		if subject := t.opaqueMeta(matchedType); subject == nil || !t.sameOpaqueType(matchedType, subject, patType, meta) {
			return nil, nil, galaerr.NewSemanticErrorAt(tok.GetLine(), tok.GetColumn(),
				fmt.Sprintf("the pattern %s(...) cannot match a value of type %s", name, matchedType.String()))
		}
	}

	pat := args[0].(*grammar.ArgumentContext).Pattern()
	if pat == nil || isWildcard(pat.GetText()) {
		return combineStructMatchConds(conds, stmts)
	}
	underlying, ok := t.underlyingOf(meta)
	if !ok {
		return nil, nil, galaerr.NewSemanticErrorAt(tok.GetLine(), tok.GetColumn(),
			fmt.Sprintf("the underlying type of opaque type %s is unknown", name))
	}
	switch p := pat.(type) {
	case *grammar.ExpressionPatternContext:
		elemExpr := &ast.CallExpr{Fun: t.typeToExpr(underlying), Args: []ast.Expr{base}}
		nestedCond, nestedStmts, err := t.transformExpressionPatternWithType(p.Expression(), elemExpr, underlying)
		if err != nil {
			return nil, nil, err
		}
		stmts = append(stmts, nestedStmts...)
		if !isLiteralTrue(nestedCond) {
			conds = append(conds, nestedCond)
		}
	case *grammar.TypedPatternContext:
		ptok := p.GetStart()
		return nil, nil, galaerr.NewSemanticErrorAt(ptok.GetLine(), ptok.GetColumn(),
			fmt.Sprintf("the value inside %s(...) is always a %s; bind it with a name, not a typed pattern", name, underlying.String()))
	default:
		ptok := pat.GetStart()
		return nil, nil, galaerr.NewSemanticErrorAt(ptok.GetLine(), ptok.GetColumn(),
			fmt.Sprintf("unsupported sub-pattern inside %s(...)", name))
	}
	return combineStructMatchConds(conds, stmts)
}

// assertPatternSubject asserts an interface-typed match subject to
// assertType: it declares `cast, ok := subject.(assertType)` and returns the
// cast value, the declaration, and the condition that the assertion held.
func (t *galaASTTransformer) assertPatternSubject(objExpr, assertType ast.Expr) (ast.Expr, ast.Stmt, ast.Expr) {
	castName := t.nextTempVar()
	okName := t.nextTempVar()
	stmt := t.patternDefine([]string{castName, okName}, []ast.Expr{assertType, ast.NewIdent("bool")},
		&ast.TypeAssertExpr{X: objExpr, Type: assertType})
	return ast.NewIdent(castName), stmt, ast.NewIdent(okName)
}

// isInterfaceType reports whether a value of type typ is an interface that
// may hold an opaque value among other things: `error`, a GALA interface
// (methods without a receiver and no fields), or a Go interface type.
func (t *galaASTTransformer) isInterfaceType(typ transpiler.Type) bool {
	if transpiler.IsUnusable(typ) {
		return false
	}
	if b, ok := typ.(transpiler.BasicType); ok && b.Name == "error" {
		return true
	}
	if meta := t.getTypeMeta(typ.BaseName()); meta != nil {
		if meta.IsOpaque || meta.IsSealed || meta.IsShorthand || len(meta.Fields) > 0 || len(meta.Methods) == 0 {
			return false
		}
		for _, m := range meta.Methods {
			if m.ReceiverName != "" {
				return false
			}
		}
		return true
	}
	if t.goTypeInfo != nil {
		if td := t.goTypeInfo.GetTypeData(t.goTypeLookupName(typ)); td != nil {
			return td.Kind == "interface"
		}
	}
	return false
}
