package transformer

import (
	"fmt"
	"go/ast"
	"go/token"
	"maps"
	"slices"
	"sort"
	"strings"

	"github.com/antlr4-go/antlr/v4"

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
	failAt := func(at antlr.Token, format string, a ...any) (ast.Expr, []ast.Stmt, error) {
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
	arg := args[0].(*grammar.ArgumentContext)
	if arg.Identifier() != nil || arg.Pattern() == nil {
		// `UserID(id = n)` names a field an opaque type does not have, and a
		// lambda is no pattern.
		return failAt(arg.GetStart(), "the sub-pattern of %s(...) is a single pattern for its underlying value, as in %s(v)", name, name)
	}
	if explicitTypeArgs != nil {
		if n := len(explicitTypeArgs.AllExpression()); n != len(meta.TypeParams) {
			return failAt(tok, "%s takes %d type argument(s), got %d", name, len(meta.TypeParams), n)
		}
	}

	// Classify the subject: an interface value is asserted to the opaque
	// type; a value of a type parameter is asserted through `any`, so the
	// pattern never matches a value of another type with the same
	// underlying type; a value of the opaque type itself is converted. A
	// subject of unknown type is an error rather than a guess.
	if matchedType == nil || transpiler.IsUnusable(matchedType) {
		return failAt(tok, "cannot infer the type of the value matched against %s(...); give it a declared type", name)
	}
	subjectType := t.followAliasChain(matchedType)
	var ifaceMethods []string
	iface, dynamic := false, false
	switch {
	case subjectType.IsAny():
		iface = true
	case t.isTypeParamSubject(subjectType):
		dynamic = true
	default:
		ifaceMethods, iface = t.interfaceMethodNames(subjectType)
	}
	if iface {
		// An interface holds the opaque type only if the type implements it.
		if missing := t.missingInterfaceMethods(meta, ifaceMethods); len(missing) > 0 {
			return failAt(tok, "%s does not implement %s (missing %s), so a %s value never holds one",
				name, matchedType.String(), strings.Join(missing, ", "), matchedType.String())
		}
	}

	// The type the pattern names: asserted to, a generic (phantom-typed) one
	// must spell its type arguments, `case Id[User](n)`.
	var assertType ast.Expr
	var instantiated transpiler.Type
	if iface || dynamic || explicitTypeArgs != nil {
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
	viaAny := func() ast.Expr { return &ast.CallExpr{Fun: ast.NewIdent("any"), Args: []ast.Expr{objExpr}} }
	switch {
	case iface:
		assertSubject(objExpr)
	case dynamic:
		assertSubject(viaAny())
	default:
		// A pattern spelling type arguments, `case Id[Order](n)`, must name
		// the subject's own instantiation.
		patType := subjectType
		if instantiated != nil {
			patType = instantiated
		}
		subject := t.opaqueMeta(subjectType)
		if subject == nil || !t.sameOpaqueType(subjectType, subject, patType, meta) {
			return failAt(tok, "the pattern %s(...) cannot match a value of type %s", name, matchedType.String())
		}
		// When the subject's type arguments are still open (`Id[T]` inside a
		// generic declaration) or unknown, the instantiation the pattern
		// names is checked at run time.
		if explicitTypeArgs != nil && t.hasOpenTypeArg(subjectType, meta) {
			assertSubject(viaAny())
		}
	}

	pat := arg.Pattern()
	if isWildcard(pat.GetText()) {
		if cast, asserted := base.(*ast.Ident); asserted && base != objExpr {
			// Only the assertion is checked; its value goes unused.
			stmts = blankAssignTempVar(stmts, cast.Name)
		}
		return combineStructMatchConds(conds, stmts)
	}
	underlying, ok := t.underlyingOf(meta)
	if !ok {
		return failAt(tok, "the underlying type of opaque type %s is unknown", name)
	}
	switch p := pat.(type) {
	case *grammar.ExpressionPatternContext:
		text := p.Expression().GetText()
		if inner := t.opaqueTypeOfPatternValue(p.Expression()); inner != nil {
			if inner.Package == meta.Package && inner.Name == meta.Name {
				return failAt(p.GetStart(), "%s is already a %s: match it with `case %s`, not inside %s(...)", text, name, text, name)
			}
			return failAt(p.GetStart(), "%s has type %s, but the value inside %s(...) has type %s",
				text, t.opaqueName(inner), name, underlying.String())
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

// isTypeParamSubject reports whether a match subject's type is a type
// parameter in scope, whose values may be of any type.
func (t *galaASTTransformer) isTypeParamSubject(typ transpiler.Type) bool {
	b, ok := typ.(transpiler.BasicType)
	return ok && t.isActiveTypeParam(b.Name)
}

// opaqueTypeOfPatternValue returns the opaque type of the value a sub-pattern
// compares with when it names one — a stable identifier (`Debug`, see
// isStableIdentifierPattern) or a qualified value (`billing.Guest`) — or nil.
// A lowercase name binds instead, and other expressions are left to the
// pattern they are.
func (t *galaASTTransformer) opaqueTypeOfPatternValue(expr grammar.IExpressionContext) *transpiler.TypeMetadata {
	text := expr.GetText()
	qualifier, bare := splitPackageQualifier(text)
	if !token.IsIdentifier(bare) || (qualifier != "" && !token.IsIdentifier(qualifier)) {
		return nil
	}
	if qualifier != "" {
		goExpr, err := t.transformExpression(expr)
		if err != nil {
			return nil
		}
		return t.opaqueMeta(unwrapGalaType(t.probeExprType(goExpr)))
	}
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

// hasOpenTypeArg reports whether the type arguments of typ, a subject of the
// generic opaque type meta, are not all known: one is still open (a type
// parameter in scope), or typ names the type without them.
func (t *galaASTTransformer) hasOpenTypeArg(typ transpiler.Type, meta *transpiler.TypeMetadata) bool {
	g, ok := typ.(transpiler.GenericType)
	if !ok {
		return len(meta.TypeParams) > 0
	}
	for _, p := range g.Params {
		if _, closed := t.typeArgKey(p); !closed {
			return true
		}
	}
	return false
}

// assertPatternSubject asserts an interface-typed match subject to
// assertType: it declares `cast, ok := std.As[assertType](subject)` and
// returns the cast value, the declaration, and the condition that the
// assertion held. std.As is the Go assertion plus a look through std's own
// transparent wrappers (Immutable, a recovered panic's error), as for a type
// pattern `case x: T`.
func (t *galaASTTransformer) assertPatternSubject(objExpr, assertType ast.Expr) (ast.Expr, ast.Stmt, ast.Expr) {
	castName := t.nextTempVar()
	okName := t.nextTempVar()
	stmt := t.patternDefine([]string{castName, okName}, []ast.Expr{assertType, ast.NewIdent("bool")},
		t.stdAsCall(assertType, objExpr))
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
	// A Go type (of a Go package, or of this package's .go files) is what its
	// Go declaration says, whatever metadata was synthesized for it.
	if t.goTypeInfo != nil {
		if td := t.goTypeInfo.GetTypeData(t.goTypeLookupName(typ)); td != nil {
			if td.Kind != "interface" {
				return nil, false
			}
			return slices.Collect(maps.Keys(td.Methods)), true
		}
	}
	if meta := t.getTypeMeta(typ.BaseName()); meta != nil {
		if meta.IsOpaque || meta.IsSealed || meta.IsShorthand || len(meta.Methods) == 0 || !isGalaInterfaceMeta(meta) {
			return nil, false
		}
		return slices.Collect(maps.Keys(meta.Methods)), true
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
