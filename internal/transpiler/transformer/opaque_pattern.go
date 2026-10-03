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
	var args []grammar.IArgumentContext
	if argList != nil {
		args = argList.AllArgument()
	}
	if len(args) != 1 {
		tok := patExprCtx.GetStart()
		name := t.opaqueName(meta)
		return nil, nil, galaerr.NewCodedSemanticError(galaerr.CodeOpaquePatternArity,
			tok.GetLine(), tok.GetColumn(),
			fmt.Sprintf("the opaque-type pattern %s(...) takes exactly one sub-pattern, got %d", name, len(args)),
			fmt.Sprintf("%s unwraps to its one underlying value: write %s(v) to bind it, %s(_) to ignore it", name, name, name),
		).WithSpan(tok.GetColumn() + len([]rune(rawName)))
	}

	var stmts []ast.Stmt
	var conds []ast.Expr
	base := objExpr
	if matchedType == nil || matchedType.IsAny() || t.isInterfaceSubject(matchedType) {
		// An interface subject holds the opaque type only when it was built
		// as one: assert it.
		assertType, err := t.opaquePatternType(meta, rawName, explicitTypeArgs, patExprCtx)
		if err != nil {
			return nil, nil, err
		}
		castName := t.nextTempVar()
		okName := t.nextTempVar()
		stmts = append(stmts, t.patternDefine([]string{castName, okName}, []ast.Expr{assertType, ast.NewIdent("bool")},
			&ast.TypeAssertExpr{X: objExpr, Type: assertType}))
		conds = append(conds, ast.NewIdent(okName))
		base = ast.NewIdent(castName)
	} else if subject := t.opaqueMeta(matchedType); !transpiler.IsUnusable(matchedType) && (subject == nil || subject.Package != meta.Package || subject.Name != meta.Name) {
		tok := patExprCtx.GetStart()
		return nil, nil, galaerr.NewSemanticErrorAt(tok.GetLine(), tok.GetColumn(),
			fmt.Sprintf("the pattern %s(...) cannot match a value of type %s", t.opaqueName(meta), matchedType.String()))
	}

	underlying, ok := t.underlyingOf(meta)
	if !ok {
		return combineStructMatchConds(conds, stmts)
	}
	elemExpr := &ast.CallExpr{Fun: t.typeToExpr(underlying), Args: []ast.Expr{base}}

	arg := args[0].(*grammar.ArgumentContext)
	if arg.Pattern() == nil || isWildcard(arg.Pattern().GetText()) {
		return combineStructMatchConds(conds, stmts)
	}
	switch p := arg.Pattern().(type) {
	case *grammar.ExpressionPatternContext:
		nestedCond, nestedStmts, err := t.transformExpressionPatternWithType(p.Expression(), elemExpr, underlying)
		if err != nil {
			return nil, nil, err
		}
		stmts = append(stmts, nestedStmts...)
		if ident, isIdent := nestedCond.(*ast.Ident); !isIdent || ident.Name != "true" {
			conds = append(conds, nestedCond)
		}
	case *grammar.TypedPatternContext:
		tok := p.GetStart()
		return nil, nil, galaerr.NewSemanticErrorAt(tok.GetLine(), tok.GetColumn(),
			fmt.Sprintf("the value inside %s(...) is always a %s; bind it with a name, not a typed pattern", t.opaqueName(meta), underlying.String()))
	}
	return combineStructMatchConds(conds, stmts)
}

// isInterfaceSubject reports whether a match subject of type typ is an
// interface, which may hold the opaque type among other things: `error`, or
// a GALA interface (metadata with methods and no fields).
func (t *galaASTTransformer) isInterfaceSubject(typ transpiler.Type) bool {
	if b, ok := typ.(transpiler.BasicType); ok && b.Name == "error" {
		return true
	}
	if transpiler.IsUnusable(typ) {
		return false
	}
	meta := t.getTypeMeta(typ.BaseName())
	return meta != nil && !meta.IsOpaque && !meta.IsSealed && !meta.IsShorthand &&
		len(meta.FieldNames) == 0 && len(meta.Methods) > 0
}

// opaquePatternType is the type an interface subject is asserted to for the
// opaque-type pattern: the type itself, or for a generic (phantom-typed) one
// the instantiation the pattern spells, `case Id[User](n)`.
func (t *galaASTTransformer) opaquePatternType(meta *transpiler.TypeMetadata, rawName string, explicitTypeArgs *grammar.ExpressionListContext, patExprCtx grammar.IExpressionContext) (ast.Expr, error) {
	typeExpr := t.ident(rawName)
	if len(meta.TypeParams) == 0 {
		return typeExpr, nil
	}
	if explicitTypeArgs == nil || len(explicitTypeArgs.AllExpression()) != len(meta.TypeParams) {
		tok := patExprCtx.GetStart()
		return nil, galaerr.NewSemanticErrorAt(tok.GetLine(), tok.GetColumn(),
			fmt.Sprintf("%s is generic: on a value of an interface type the pattern must name its type arguments, as in %s[%s](v)",
				t.opaqueName(meta), t.opaqueName(meta), meta.TypeParams[0]))
	}
	var indices []ast.Expr
	for _, e := range explicitTypeArgs.AllExpression() {
		idx, err := t.transformExpression(e)
		if err != nil {
			return nil, err
		}
		indices = append(indices, idx)
	}
	return withTypeArgs(typeExpr, indices), nil
}
