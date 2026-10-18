package transformer

import (
	"fmt"
	"go/ast"

	"github.com/antlr4-go/antlr/v4"

	"martianoff/gala/galaerr"
	"martianoff/gala/internal/parser/grammar"
	"martianoff/gala/internal/transpiler"
)

// sealedViewMethod is the method that gives a type a sealed view: a type V
// with `func (v V) MatchView() S`, S a sealed type, can be matched with S's
// variants (see sealedViewForCases). The name is the only thing that marks
// the method; nothing else about V or S is special.
const sealedViewMethod = "MatchView"

// sealedViewOf returns the sealed type S that typ views itself as through
// `MatchView() S`, with typ's type arguments substituted, or nil when typ has
// no such method: no parameters, no type parameters of its own, and a sealed
// result. A pointer type is viewed through the type it points to, as Go's
// method set does; an alias through the method declared on the alias itself,
// or else on the type it names.
func (t *galaASTTransformer) sealedViewOf(typ transpiler.Type) transpiler.Type {
	if transpiler.IsUnusable(typ) {
		return nil
	}
	if ptr, ok := typ.(transpiler.PointerType); ok {
		if transpiler.IsUnusable(ptr.Elem) {
			return nil
		}
		typ = ptr.Elem
	}
	if view := t.declaredSealedView(typ); view != nil {
		return view
	}
	if target := t.followAliasChain(typ); target.BaseName() != typ.BaseName() {
		return t.declaredSealedView(target)
	}
	return nil
}

// declaredSealedView is sealedViewOf for a MatchView declared on typ itself.
func (t *galaASTTransformer) declaredSealedView(typ transpiler.Type) transpiler.Type {
	meta, resolved := t.getTypeMetaResolved(typ.BaseName())
	if meta == nil {
		return nil
	}
	method, ok := meta.Methods[sealedViewMethod]
	if !ok || len(method.ParamTypes) > 0 || len(method.TypeParams) > 0 || method.Variadic {
		return nil
	}
	view := t.resolveMethodCallTypeWithParams(resolved, sealedViewMethod, nil, nil, -1, t.getReceiverTypeArgTypes(typ))
	if transpiler.IsUnusable(view) {
		return nil
	}
	viewMeta := t.getTypeMeta(t.followAliasChain(view).BaseName())
	if viewMeta == nil || !viewMeta.IsSealed {
		return nil
	}
	return view
}

// sealedViewForCases decides whether a match over a subject of subjectType
// reads its cases against the subject's sealed view (see sealedViewOf). It
// does when the subject has a view S and some case names a variant of S that
// is not a variant of the subject's own type — `case Some(n)` or `case None`
// on a type whose MatchView returns Option. It returns S then, and nil when
// the match is an ordinary one over subjectType.
//
// A match over the view holds only patterns that apply to S: S's variants,
// `_`, stable identifiers, typed patterns of S, and alternatives of those.
// A pattern over the subject itself — its own extractor, a literal, a typed
// pattern of another type — or a binding of the whole subject, which would
// bind the view rather than the subject, is GALA-E0079.
func (t *galaASTTransformer) sealedViewForCases(subjectType transpiler.Type, caseClauses []grammar.ICaseClauseContext) (transpiler.Type, error) {
	view := t.sealedViewOf(subjectType)
	if view == nil {
		return nil, nil
	}
	usesView := false
	for _, cc := range caseClauses {
		if exprPat, ok := cc.(*grammar.CaseClauseContext).Pattern().(*grammar.ExpressionPatternContext); ok &&
			t.namesViewVariant(exprPat.Expression(), subjectType, view) {
			usesView = true
			break
		}
	}
	if !usesView {
		return nil, nil
	}
	for _, cc := range caseClauses {
		if err := t.checkSealedViewPattern(cc.(*grammar.CaseClauseContext).Pattern(), subjectType, view); err != nil {
			return nil, err
		}
	}
	return view, nil
}

// subPatternView is sealedViewForCases for a sub-pattern — a field of an
// extractor, an element of a tuple — whose value has type typ: the
// sub-pattern reads the view when one of its alternatives names a variant of
// it (`case (Some(a), None)` over two MaybeInts). Each sub-pattern decides for
// itself, so a binding beside it (`case (Some(a), b)`) binds the value of
// typ. Every alternative of a sub-pattern that reads the view applies to the
// view (see checkViewAlternatives).
func (t *galaASTTransformer) subPatternView(pat grammar.IExpressionContext, typ transpiler.Type) (transpiler.Type, error) {
	view := t.sealedViewOf(typ)
	if view == nil || !t.namesViewVariant(pat, typ, view) {
		return nil, nil
	}
	if err := t.checkViewAlternatives(pat, typ, view); err != nil {
		return nil, err
	}
	return view, nil
}

// namesViewVariant reports whether one of pat's alternatives names a variant
// of view that is not a variant of own, the type of the value matched.
func (t *galaASTTransformer) namesViewVariant(pat grammar.IExpressionContext, own, view transpiler.Type) bool {
	own = t.followAliasChain(own)
	for _, alt := range t.flatAlternatives(pat) {
		name := t.patternHeadName(alt)
		if v, _ := t.sealedVariantOfMatchedType(name, view); v == nil {
			continue
		}
		if ownVariant, _ := t.sealedVariantOfMatchedType(name, own); ownVariant == nil {
			return true
		}
	}
	return false
}

// patternHeadName returns the constructor a pattern alternative names: the
// callee of a call-shaped pattern (`Some(n)`, `pkg.None()`) or a bare
// identifier (`None`), and "" for any other shape.
func (t *galaASTTransformer) patternHeadName(alt grammar.IExpressionContext) string {
	if name, _, ok := t.patternCallShape(alt); ok {
		return name
	}
	return patternIdentifier(alt)
}

// checkSealedViewPattern rejects a case pattern that does not apply to the
// sealed view of a match over a value of subjectType (see
// sealedViewForCases).
func (t *galaASTTransformer) checkSealedViewPattern(pat grammar.IPatternContext, subjectType, view transpiler.Type) error {
	switch p := pat.(type) {
	case *grammar.TypedPatternContext:
		typeExpr, err := t.transformType(p.Type_())
		if err != nil {
			return err
		}
		written := t.astTypeToTranspilerType(typeExpr)
		if t.namesViewType(written, view) {
			return nil
		}
		return sealedViewPatternError(p, subjectType, view,
			fmt.Sprintf("`%s` tests for %s, which is not %s",
				p.GetText(), displayType(written), displayType(view)))
	case *grammar.ExpressionPatternContext:
		return t.checkViewAlternatives(p.Expression(), subjectType, view)
	default:
		return sealedViewPatternError(pat, subjectType, view,
			fmt.Sprintf("`%s` is not a variant of %s", pat.GetText(), displayType(view)))
	}
}

// checkViewAlternatives rejects an alternative of pat, a pattern matched
// against the view of a value of typ, that does not apply to the view. Each
// one is a variant of the view, `_`, or a stable identifier, compared with the
// view by ==.
func (t *galaASTTransformer) checkViewAlternatives(pat grammar.IExpressionContext, typ, view transpiler.Type) error {
	for _, alt := range t.flatAlternatives(pat) {
		text := alt.GetText()
		if isWildcard(text) {
			continue
		}
		name := t.patternHeadName(alt)
		if v, _ := t.sealedVariantOfMatchedType(name, view); v != nil {
			continue
		}
		if name == text {
			if t.isStableIdentifierPattern(name) {
				continue
			}
			if t.isSimpleIdentifier(name) && !t.bareNameTests(name, view) {
				return sealedViewPatternError(alt, typ, view,
					fmt.Sprintf("`case %s` would bind the %s view, not the %s itself", name, displayType(view), displayType(typ)))
			}
		}
		return sealedViewPatternError(alt, typ, view,
			fmt.Sprintf("`%s` is not a variant of %s", text, displayType(view)))
	}
	return nil
}

// namesViewType reports whether written, the type of a typed pattern, is the
// sealed type view. (A variant is not a type; see GALA-E0061.)
func (t *galaASTTransformer) namesViewType(written, view transpiler.Type) bool {
	return !transpiler.IsUnusable(written) &&
		t.followAliasChain(written).String() == t.followAliasChain(view).String()
}

// sealedViewPatternError is GALA-E0079 at the offending pattern.
func sealedViewPatternError(at antlr.ParserRuleContext, subjectType, view transpiler.Type, why string) error {
	return galaerr.NewCodedSemanticError(
		galaerr.CodeInvalidSealedViewMatch,
		at.GetStart().GetLine(), at.GetStart().GetColumn(),
		fmt.Sprintf("%s; this match reads %s through %s() as %s",
			why, displayType(subjectType), sealedViewMethod, displayType(view)),
		fmt.Sprintf("match only %s's variants and `_` here; match the %s itself in a separate match",
			displayType(view), displayType(subjectType)),
	)
}

// matchViewSubject returns what a match over subject, a value of subjectType,
// tests: subject.MatchView() and the view type when its cases read the
// subject's sealed view (see sealedViewForCases), and subject and subjectType
// unchanged otherwise. at locates the match.
func (t *galaASTTransformer) matchViewSubject(subject ast.Expr, subjectType transpiler.Type, caseClauses []grammar.ICaseClauseContext, at antlr.ParserRuleContext) (ast.Expr, transpiler.Type, error) {
	view, err := t.sealedViewForCases(subjectType, caseClauses)
	if err != nil || view == nil {
		return subject, subjectType, err
	}
	viewExpr, err := t.viewSubject(subject, subjectType, at.GetStart().GetLine(), at.GetStart().GetColumn())
	if err != nil {
		return nil, nil, err
	}
	return viewExpr, view, nil
}

// viewSubject returns the expression a match over a sealed view tests: the
// subject's MatchView() call, taken through an addressable receiver when the
// method has a pointer receiver. line and col locate the match.
func (t *galaASTTransformer) viewSubject(subject ast.Expr, subjectType transpiler.Type, line, col int) (ast.Expr, error) {
	recv, err := t.addressableReceiver(subject, subjectType, sealedViewMethod, line, col)
	if err != nil {
		return nil, err
	}
	return &ast.CallExpr{Fun: &ast.SelectorExpr{X: recv, Sel: ast.NewIdent(sealedViewMethod)}}, nil
}
