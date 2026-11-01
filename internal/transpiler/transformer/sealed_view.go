package transformer

import (
	"fmt"
	"go/ast"
	"slices"

	"github.com/antlr4-go/antlr/v4"

	"martianoff/gala/galaerr"
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
// result. The method is found as a call `typ.MatchView()` finds it (see
// methodReceiverType): through a pointer, and along an alias chain.
func (t *galaASTTransformer) sealedViewOf(typ transpiler.Type) transpiler.Type {
	if ptr, ok := typ.(transpiler.PointerType); transpiler.IsUnusable(typ) || ok && transpiler.IsUnusable(ptr.Elem) {
		return nil
	}
	typ = t.methodReceiverType(typ, sealedViewMethod)
	if ptr, ok := typ.(transpiler.PointerType); ok {
		typ = ptr.Elem
	}
	if transpiler.IsUnusable(typ) {
		return nil
	}
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
func (t *galaASTTransformer) sealedViewForCases(subjectType transpiler.Type, caseClauses []caseSrc) (transpiler.Type, error) {
	view := t.sealedViewOf(subjectType)
	if view == nil {
		return nil, nil
	}
	usesView := slices.ContainsFunc(caseClauses, func(cc caseSrc) bool {
		pat := cc.pattern()
		return pat.kind() == patKindExpr && t.namesViewVariant(pat.expr(), subjectType, view)
	})
	if !usesView {
		return nil, nil
	}
	for _, cc := range caseClauses {
		if err := t.checkSealedViewPattern(cc.pattern(), subjectType, view); err != nil {
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
func (t *galaASTTransformer) subPatternView(pat patExpr, typ transpiler.Type) (transpiler.Type, error) {
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
func (t *galaASTTransformer) namesViewVariant(pat patExpr, own, view transpiler.Type) bool {
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
func (t *galaASTTransformer) patternHeadName(alt patExpr) string {
	if name, _, ok := alt.callShape(); ok {
		return name
	}
	if qualified := t.qualifiedZeroFieldCase(alt); qualified != "" {
		return stripPackagePrefix(qualified)
	}
	return alt.identifier()
}

// checkSealedViewPattern rejects a case pattern that does not apply to the
// sealed view of a match over a value of subjectType (see
// sealedViewForCases).
func (t *galaASTTransformer) checkSealedViewPattern(pat patSrc, subjectType, view transpiler.Type) error {
	switch pat.kind() {
	case patKindTyped:
		typeExpr, err := t.lowerType(pat.typedType())
		if err != nil {
			return err
		}
		written := t.astTypeToTranspilerType(typeExpr)
		if t.namesViewType(written, view) {
			return nil
		}
		return sealedViewPatternError(pat.at(), subjectType, view,
			fmt.Sprintf("`%s` tests for %s, which is not %s",
				pat.text(), displayType(written), displayType(view)))
	case patKindExpr:
		return t.checkViewAlternatives(pat.expr(), subjectType, view)
	default:
		return sealedViewPatternError(pat.at(), subjectType, view,
			fmt.Sprintf("`%s` is not a variant of %s", pat.text(), displayType(view)))
	}
}

// checkViewAlternatives rejects an alternative of pat, a pattern matched
// against the view of a value of typ, that does not apply to the view. Each
// one is a variant of the view, `_`, or a stable identifier holding a value
// of the view type, compared with the view by ==.
func (t *galaASTTransformer) checkViewAlternatives(pat patExpr, typ, view transpiler.Type) error {
	for _, alt := range t.flatAlternatives(pat) {
		text := alt.text()
		if isWildcard(text) {
			continue
		}
		name := t.patternHeadName(alt)
		if v, _ := t.sealedVariantOfMatchedType(name, view); v != nil {
			continue
		}
		if name == text {
			if valueType, stable := t.stablePatternValueType(alt); stable {
				// Compared with the view by ==, so it holds a view value.
				if t.namesViewType(valueType, view) {
					continue
				}
				return sealedViewPatternError(alt.at(), typ, view,
					fmt.Sprintf("`%s` is not a value of %s", name, displayType(view)))
			}
			if extractor, _ := t.bareExtractor(name, view); t.isSimpleIdentifier(name) && extractor == nil {
				return sealedViewPatternError(alt.at(), typ, view,
					fmt.Sprintf("`case %s` would bind the %s view, not the %s itself", name, displayType(view), displayType(typ)))
			}
		}
		return sealedViewPatternError(alt.at(), typ, view,
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
func sealedViewPatternError(at srcNode, subjectType, view transpiler.Type, why string) error {
	line, col := at.start()
	return galaerr.NewCodedSemanticError(
		galaerr.CodeInvalidSealedViewMatch,
		line, col,
		fmt.Sprintf("%s; this match reads %s through %s() as %s",
			why, displayType(subjectType), sealedViewMethod, displayType(view)),
		fmt.Sprintf("match only %s's variants and `_` here; match the %s itself in a separate match",
			displayType(view), displayType(subjectType)),
	)
}

// matchViewSubject returns what a match over subject, a value of subjectType,
// tests: subject.MatchView() and the view type when its cases read the
// subject's sealed view (see sealedViewForCases), and subject and subjectType
// unchanged otherwise; viewed reports which. at locates the match.
func (t *galaASTTransformer) matchViewSubject(subject ast.Expr, subjectType transpiler.Type, caseClauses []caseSrc, at antlr.ParserRuleContext) (ast.Expr, transpiler.Type, bool, error) {
	return t.matchViewSubjectAt(subject, subjectType, caseClauses, srcNode{ctx: at})
}

// matchViewSubjectAt is matchViewSubject of a match at a node of either tree.
func (t *galaASTTransformer) matchViewSubjectAt(subject ast.Expr, subjectType transpiler.Type, caseClauses []caseSrc, at srcNode) (ast.Expr, transpiler.Type, bool, error) {
	view, err := t.sealedViewForCases(subjectType, caseClauses)
	if err != nil || view == nil {
		return subject, subjectType, false, err
	}
	viewExpr, err := t.viewSubjectAt(subject, subjectType, at)
	if err != nil {
		return nil, nil, false, err
	}
	return viewExpr, view, true, nil
}

// viewSubject returns the expression a match over a sealed view tests: the
// subject's MatchView() call, taken through an addressable receiver when the
// method has a pointer receiver. at locates the match.
func (t *galaASTTransformer) viewSubject(subject ast.Expr, subjectType transpiler.Type, at antlr.ParserRuleContext) (ast.Expr, error) {
	return t.viewSubjectAt(subject, subjectType, srcNode{ctx: at})
}

// viewSubjectAt is viewSubject of a match at a node of either tree.
func (t *galaASTTransformer) viewSubjectAt(subject ast.Expr, subjectType transpiler.Type, at srcNode) (ast.Expr, error) {
	if err := t.checkPtrViewReceiverAt(subjectType, at); err != nil {
		return nil, err
	}
	line, col := at.start()
	recv, err := t.addressableReceiver(subject, subjectType, sealedViewMethod, line, col)
	if err != nil {
		return nil, err
	}
	return &ast.CallExpr{Fun: &ast.SelectorExpr{X: recv, Sel: ast.NewIdent(sealedViewMethod)}}, nil
}
