package transformer

import (
	"fmt"
	"go/ast"
	"sort"
	"strings"

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

// sourcePos is the position of a token a diagnostic points at.
type sourcePos interface {
	GetLine() int
	GetColumn() int
}

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
	failAt := func(at sourcePos, format string, a ...any) (ast.Expr, []ast.Stmt, error) {
		return nil, nil, galaerr.NewSemanticErrorAt(at.GetLine(), at.GetColumn(), fmt.Sprintf(format, a...))
	}
	var args []grammar.IArgumentContext
	if argList != nil {
		args = argList.AllArgument()
	}
	if len(args) != 1 {
		// The caret spans the callee as written (an import alias included).
		written := transpiler.SourceText(patExprCtx)
		if i := strings.IndexAny(written, "(["); i > 0 {
			written = written[:i]
		}
		return nil, nil, galaerr.NewCodedSemanticError(galaerr.CodeOpaquePatternArity,
			tok.GetLine(), tok.GetColumn(),
			fmt.Sprintf("the opaque-type pattern %s(...) takes exactly one sub-pattern, got %d", name, len(args)),
			fmt.Sprintf("%s unwraps to its one underlying value: write %s(v) to bind it, %s(_) to ignore it", name, name, name),
		).WithSpan(tok.GetColumn() + len([]rune(written)))
	}
	// A generic opaque type's type-argument count is checked where the
	// instantiation is built (structPatternAssertType).
	if explicitTypeArgs != nil && len(meta.TypeParams) == 0 {
		return failAt(tok, "%s takes 0 type argument(s), got %d", name, len(explicitTypeArgs.AllExpression()))
	}

	known := matchedType != nil && !transpiler.IsUnusable(matchedType)
	var ifaceMethods []string
	iface := false
	if known {
		if matchedType.IsAny() {
			iface = true
		} else {
			ifaceMethods, iface = t.interfaceMethodNames(matchedType)
		}
	}
	if iface {
		// An interface subject holds the opaque type only when it was built
		// as one, and only if the opaque type implements the interface.
		if missing := t.missingInterfaceMethods(meta, ifaceMethods); len(missing) > 0 {
			return failAt(tok, "%s does not implement %s (missing %s), so a %s value never holds one",
				name, matchedType.String(), strings.Join(missing, ", "), matchedType.String())
		}
	}

	// The type the pattern names: on an interface subject a generic
	// (phantom-typed) one must spell its type arguments, `case Id[User](n)`.
	var assertType ast.Expr
	var instantiated transpiler.Type
	if iface || explicitTypeArgs != nil {
		var err error
		if assertType, instantiated, err = t.structPatternAssertType(rawName, explicitTypeArgs, patExprCtx); err != nil {
			return nil, nil, err
		}
	}

	var stmts []ast.Stmt
	var conds []ast.Expr
	base := objExpr
	assertSubject := func(subject ast.Expr) {
		var stmt ast.Stmt
		var ok ast.Expr
		base, stmt, ok = t.assertPatternSubject(subject, assertType)
		stmts = append(stmts, stmt)
		conds = append(conds, ok)
	}
	switch {
	case !known:
		// The subject's type is unknown: the conversion below is checked by Go.
	case iface:
		assertSubject(objExpr)
	default:
		// A pattern spelling type arguments, `case Id[Order](n)`, must name
		// the subject's own instantiation.
		patType := matchedType
		if instantiated != nil {
			patType = instantiated
		}
		subject := t.opaqueMeta(matchedType)
		if subject == nil || !t.sameOpaqueType(matchedType, subject, patType, meta) {
			return failAt(tok, "the pattern %s(...) cannot match a value of type %s", name, matchedType.String())
		}
		// Inside a generic declaration the subject's type argument may still
		// be open (`Id[T]`); the instantiation the pattern names is then
		// checked at run time.
		if explicitTypeArgs != nil && t.hasOpenTypeArg(matchedType) {
			assertSubject(&ast.CallExpr{Fun: ast.NewIdent("any"), Args: []ast.Expr{objExpr}})
		}
	}

	pat := args[0].(*grammar.ArgumentContext).Pattern()
	if pat == nil || isWildcard(pat.GetText()) {
		return combineStructMatchConds(conds, stmts)
	}
	underlying, ok := t.underlyingOf(meta)
	if !ok {
		return failAt(tok, "the underlying type of opaque type %s is unknown", name)
	}
	switch p := pat.(type) {
	case *grammar.ExpressionPatternContext:
		text := p.Expression().GetText()
		if inner := t.opaqueStableIdentifier(text); inner != nil && inner.Package == meta.Package && inner.Name == meta.Name {
			return failAt(tok, "%s is already a %s: match it with `case %s`, not inside %s(...)", text, name, text, name)
		}
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
		return failAt(p.GetStart(), "the value inside %s(...) is always a %s; bind it with a name, not a typed pattern", name, underlying.String())
	default:
		return failAt(pat.GetStart(), "unsupported sub-pattern inside %s(...)", name)
	}
	return combineStructMatchConds(conds, stmts)
}

// opaqueStableIdentifier returns the opaque type of the value a pattern
// expression names when it is a stable identifier (see
// isStableIdentifierPattern), or nil.
func (t *galaASTTransformer) opaqueStableIdentifier(text string) *transpiler.TypeMetadata {
	if !t.isSimpleIdentifier(text) || !t.isStableIdentifierPattern(text) {
		return nil
	}
	typ := t.getType(text)
	if transpiler.IsUnusable(typ) && t.richAST != nil {
		if pv := t.richAST.PackageVals[text]; pv != nil {
			typ = pv.Type
		}
	}
	return t.opaqueMeta(unwrapGalaType(typ))
}

// hasOpenTypeArg reports whether typ is an instantiation with a type argument
// that is still open (a type parameter in scope).
func (t *galaASTTransformer) hasOpenTypeArg(typ transpiler.Type) bool {
	g, ok := typ.(transpiler.GenericType)
	if !ok {
		return false
	}
	for _, p := range g.Params {
		if _, closed := t.typeArgKey(p); !closed {
			return true
		}
	}
	return false
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

// interfaceMethodNames returns the methods an interface type requires, and
// false when typ is not an interface that may hold an opaque value among
// other things: `error`, a GALA interface, or a Go interface type.
func (t *galaASTTransformer) interfaceMethodNames(typ transpiler.Type) ([]string, bool) {
	if transpiler.IsUnusable(typ) {
		return nil, false
	}
	if b, ok := typ.(transpiler.BasicType); ok && b.Name == "error" {
		return []string{"Error"}, true
	}
	if meta := t.getTypeMeta(typ.BaseName()); meta != nil {
		if meta.IsOpaque || meta.IsSealed || meta.IsShorthand || len(meta.Methods) == 0 || !isGalaInterfaceMeta(meta) {
			return nil, false
		}
		names := make([]string, 0, len(meta.Methods))
		for n := range meta.Methods {
			names = append(names, n)
		}
		return names, true
	}
	if t.goTypeInfo != nil {
		if td := t.goTypeInfo.GetTypeData(t.goTypeLookupName(typ)); td != nil && td.Kind == "interface" {
			names := make([]string, 0, len(td.Methods))
			for n := range td.Methods {
				names = append(names, n)
			}
			return names, true
		}
	}
	return nil, false
}

// missingInterfaceMethods returns the methods of required (an interface's
// method set) that the opaque type meta does not have — declared in GALA, in
// a .go file of its package, or generated (Hash, Compare) — sorted.
func (t *galaASTTransformer) missingInterfaceMethods(meta *transpiler.TypeMetadata, required []string) []string {
	if len(required) == 0 {
		return nil
	}
	goMethods := t.goMethodsOnGalaType(meta)
	var missing []string
	for _, m := range required {
		_, inGala := meta.Methods[m]
		_, inGo := goMethods[m]
		if !inGala && !inGo && !t.isSynthesizedOpaqueMethod(meta, m) {
			missing = append(missing, m)
		}
	}
	sort.Strings(missing)
	return missing
}
