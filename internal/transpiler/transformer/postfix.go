package transformer

import (
	"fmt"
	"go/ast"
	"slices"
	"strings"

	"github.com/antlr4-go/antlr/v4"
	"martianoff/gala/galaerr"
	"martianoff/gala/internal/parser/grammar"
	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/registry"
)

// This file contains postfix operation and field access transformation logic extracted from expressions.go
// Functions: transformPostfixExpr, applyPostfixSuffix, transformPrimaryExpr, transformPostfixMatchExpression,
//            buildMatchExpressionFromClauses, transformTupleLiteral

func (t *galaASTTransformer) transformPostfixExpr(ctx *grammar.PostfixExprContext) (ast.Expr, error) {
	// Check for match expression. B13: defensively guard the child cast — while
	// ANTLR children are normally non-nil ParseTrees, a malformed parse tree
	// would otherwise panic here rather than returning a clean error.
	if ctx.GetChildCount() > 1 {
		for i := 0; i < ctx.GetChildCount(); i++ {
			child := ctx.GetChild(i)
			if child == nil {
				continue
			}
			pt, ok := child.(antlr.ParseTree)
			if !ok {
				continue
			}
			if pt.GetText() == "match" {
				return t.transformPostfixMatchExpression(ctx)
			}
		}
	}

	// Get the primary expression
	primaryExpr := ctx.PrimaryExpr()
	if primaryExpr == nil {
		return nil, galaerr.NewSemanticErrorAt(ctx.GetStart().GetLine(), ctx.GetStart().GetColumn(), "postfixExpr must have primaryExpr")
	}

	// The type of the slot this expression fills is for its last suffix, the
	// call that is the value (see consumesSlotType), or for a bare tuple
	// literal: never for the receiver the call is applied to.
	suffixes := ctx.AllPostfixSuffix()
	release := func() {}
	if len(suffixes) > 0 {
		release = t.expectedArgTypes.withhold(ctx)
	}
	result, err := t.transformPrimaryExpr(primaryExpr.(*grammar.PrimaryExprContext))
	if err != nil {
		return nil, err
	}

	// Apply postfix suffixes
	for i, suffix := range suffixes {
		if i == len(suffixes)-1 {
			release()
		}
		result, err = t.applyPostfixSuffix(result, suffix.(*grammar.PostfixSuffixContext))
		if err != nil {
			return nil, err
		}
	}

	return result, nil
}

func (t *galaASTTransformer) applyPostfixSuffix(base ast.Expr, suffix *grammar.PostfixSuffixContext) (ast.Expr, error) {
	if suffix.Identifier() != nil {
		name := suffix.Identifier().GetText()
		if err := t.checkGoResultMember(base, name, suffix); err != nil {
			return nil, err
		}
		id := suffix.Identifier().GetStart()
		return t.resolveFieldAccess(base, name, id.GetLine(), id.GetColumn())
	}

	childCount := suffix.GetChildCount()
	if childCount >= 2 {
		firstChild := suffix.GetChild(0).(antlr.ParseTree).GetText()
		if firstChild == "(" {
			return t.applyGoCallSuffix(base, suffix)
		}
		if firstChild == "[" {
			return t.resolveIndexAccess(base, suffix)
		}
	}

	return nil, galaerr.NewSemanticErrorAt(suffix.GetStart().GetLine(), suffix.GetStart().GetColumn(), "unknown postfix suffix type")
}

// applyGoCallSuffix applies a call suffix, then presents a call to a Go
// function returning several results as one GALA value (see go_results.go).
// GALA's own Println and Print are statements, not Go calls, and keep their
// plain form.
func (t *galaASTTransformer) applyGoCallSuffix(base ast.Expr, suffix *grammar.PostfixSuffixContext) (ast.Expr, error) {
	isPrint := t.isBuiltinPrint(base)
	if id, ok := base.(*ast.Ident); ok && t.typeParamValue(id.Name) {
		if err := t.checkTypeParamConversion(id.Name, suffix); err != nil {
			return nil, err
		}
	}
	call, err := t.applyCallSuffix(base, suffix)
	if err != nil || isPrint {
		return call, err
	}
	// A conversion to a basic type (`string(data)`) of a converted Go call.
	if id, ok := base.(*ast.Ident); ok && transpiler.IsPrimitiveType(id.Name) && !t.isVal(id.Name) && !t.isVar(id.Name) {
		if ce, ok := call.(*ast.CallExpr); ok && len(ce.Args) == 1 {
			if res := t.goResultOf(ce.Args[0]); res != nil {
				return nil, t.goResultMisuse(res, fmt.Sprintf("it cannot be converted to `%s`", id.Name), suffix)
			}
		}
	}
	return t.liftGoResults(call, suffix)
}

// resolveFieldAccess handles member access with automatic Immutable/ConstPtr unwrapping.
// line and col locate selName, for a diagnostic about it.
func (t *galaASTTransformer) resolveFieldAccess(base ast.Expr, selName string, line, col int) (ast.Expr, error) {
	// `pkg.Name` naming an imported package-level binding is read at once, as
	// transformPrimary reads a same-package one: a val types as Immutable[T]
	// (inferSelectorExprType), so unwrapImmutable adds the .Get().
	if xIdent, ok := base.(*ast.Ident); ok && t.importedPackageVal(xIdent.Name, selName) != nil {
		return t.unwrapImmutable(&ast.SelectorExpr{X: base, Sel: ast.NewIdent(selName)}), nil
	}

	xType := t.getExprTypeName(base)
	isImmutable := t.isImmutableType(xType)

	// Don't unwrap if we're accessing Immutable's own fields/methods
	selectsThroughImmutable := !isImmutable || (selName != "Get" && selName != "value")
	if selectsThroughImmutable {
		base = t.unwrapImmutable(base)
		// After unwrapping Immutable[T], update xType to T so that
		// isImmutableField can look up the correct struct metadata.
		if isImmutable {
			if gen, ok := xType.(transpiler.GenericType); ok && len(gen.Params) > 0 {
				xType = gen.Params[0]
			}
		}
	}

	// Also unwrap ConstPtr to access fields (but not ConstPtr's own methods)
	isConstPtr := t.isConstPtrType(xType)
	if isConstPtr && selName != "Deref" && selName != "IsNil" && selName != "ptr" {
		base = t.unwrapConstPtr(base)
		xType = t.getExprTypeName(base)
	}

	// A pointer-receiver method needs an addressable receiver; a val's Get()
	// is a copy, so it gets an addressable copy instead.
	if selectsThroughImmutable {
		var err error
		if base, err = t.addressableReceiver(base, xType, selName, line, col); err != nil {
			return nil, err
		}
	}

	selExpr := &ast.SelectorExpr{X: base, Sel: ast.NewIdent(selName)}

	if t.isImmutableField(xType, selExpr, selName) {
		return &ast.CallExpr{
			Fun: &ast.SelectorExpr{X: selExpr, Sel: ast.NewIdent("Get")},
		}, nil
	}

	return selExpr, nil
}

// isImmutableField checks if a field access should be auto-unwrapped via .Get().
func (t *galaASTTransformer) isImmutableField(xType transpiler.Type, selExpr *ast.SelectorExpr, selName string) bool {
	// A Go type has no Immutable fields. Its package NAME can match a GALA
	// package (io/fs's `fs.FileInfo` beside GALA's `fs.FileInfo`), so the
	// name-keyed lookups below must not see it.
	base := xType
	if p, ok := base.(transpiler.PointerType); ok {
		base = p.Elem
	}
	if nt, ok := base.(transpiler.NamedType); ok && t.isGoTyped(nt) {
		return false
	}
	xTypeName := xType.String()
	baseTypeName := stripTypeNameDecorations(xTypeName)

	// Check structFields (current package types)
	resolvedTypeName := t.resolveStructTypeName(baseTypeName)
	if fields, ok := t.structFields[resolvedTypeName]; ok {
		immut := t.structImmutFields[resolvedTypeName]
		for i, f := range fields {
			if f == selName {
				// Some registration paths populate structFields but not
				// structImmutFields (e.g. a struct synthesized from a Go
				// `type X struct {…}` declaration in a hand-written .go
				// file or a cross-package metadata path that only fills
				// names). Treat missing entries as not-immutable rather
				// than panicking with index-out-of-range.
				if i < len(immut) {
					return immut[i]
				}
				return false
			}
		}
	}

	// Check typeMetas (cross-package types)
	if typeMeta := t.getTypeMeta(baseTypeName); typeMeta != nil {
		for i, f := range typeMeta.FieldNames {
			if f == selName {
				return i < len(typeMeta.ImmutFlags) && typeMeta.ImmutFlags[i]
			}
		}
		// A method is not a field, so it is never an Immutable one. Without
		// this, `opt.Map` on a std type reaches the field-type query below,
		// which has no answer for a method and reports it as unresolved.
		if _, isMethod := typeMeta.Methods[selName]; isMethod {
			return false
		}
	}

	// Check structFieldTypes (Immutable wrapper in field type)
	if fieldTypes, ok := t.structFieldTypes[resolvedTypeName]; ok {
		if fieldType, ok := fieldTypes[selName]; ok && t.isImmutableType(fieldType) {
			return true
		}
	}

	// Std library types: check generated field type
	if t.isKnownStdType(baseTypeName) || (hasStdPrefix(baseTypeName) && registry.IsStdType(stripStdPrefix(baseTypeName))) {
		fieldType := t.getExprTypeName(selExpr)
		if t.isImmutableType(fieldType) {
			return true
		}
	}

	// Fallback: when receiver type is unknown AND the base expression is a
	// {val}.Get() call, try to resolve the val's stored type from scope and
	// check if the field is immutable on that specific type.
	// We do NOT scan all known types — that's too broad and causes false
	// positives (e.g., "Err" matching std.Try.Err on a context.Context val).
	if xTypeName == "" || xType.IsNil() {
		// bindingRef accepts a call only as a val's `.Get()` (`v` or `pkg.V`).
		// The recursion runs with a known type, so it cannot come back here.
		if _, isCall := selExpr.X.(*ast.CallExpr); isCall {
			if b, ok := t.bindingRef(selExpr.X); ok {
				if inner := unwrapGalaType(b.typ); !inner.IsNil() {
					return t.isImmutableField(inner, selExpr, selName)
				}
			}
		}

		// Fallback: when the receiver type is unknown but the LHS is a bare
		// function call (e.g., `TermSize().V2`), resolve the call's declared
		// return type via function metadata. The Go importer can fail to
		// resolve transitive imports for a dot-imported Go package consumed
		// only as compiled artifacts (no source on the module path) — when
		// that happens, `getExprTypeName` returns NilType and the early
		// checks above all miss, even though the function's return type is
		// recorded in metadata. Looking up by function name still gives us a
		// struct name we can match against typeMetas/structFields.
		if ce, ok := selExpr.X.(*ast.CallExpr); ok {
			if retType := t.callExprReturnType(ce); !retType.IsNil() {
				retName := retType.String()
				if idx := strings.Index(retName, "["); idx != -1 {
					retName = retName[:idx]
				}
				retName = strings.TrimPrefix(retName, "*")
				resolvedRet := t.resolveStructTypeName(retName)
				if fields, ok := t.structFields[resolvedRet]; ok {
					for i, f := range fields {
						if f == selName {
							return t.structImmutFields[resolvedRet][i]
						}
					}
				}
				if typeMeta := t.getTypeMeta(retName); typeMeta != nil {
					for i, f := range typeMeta.FieldNames {
						if f == selName {
							return i < len(typeMeta.ImmutFlags) && typeMeta.ImmutFlags[i]
						}
					}
				}
			}
			// Last-resort fallback: when even function metadata is unavailable
			// (e.g., a dot-imported Go module fetched but not analyzed) and the
			// field name matches the std.Tuple component shape (V1..V10), check
			// any std.Tuple* typeMeta. All std.Tuple variants register their
			// V_N fields as Immutable, so a positive hit is unambiguous and
			// avoids the failure mode where `funcReturningTuple().V2` reaches
			// arithmetic without an injected .Get().
			if isTupleFieldName(selName) && t.isImmutableTupleField(selName) {
				return true
			}
		}
	}

	return false
}

// isTupleFieldName reports whether name is V1..V10 — the field naming
// convention used by std.Tuple/Tuple3/.../Tuple10.
func isTupleFieldName(name string) bool {
	if len(name) < 2 || name[0] != 'V' {
		return false
	}
	rest := name[1:]
	for _, c := range rest {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// isImmutableTupleField reports whether `selName` appears as an Immutable-
// flagged field on any registered std.Tuple* metadata. Used only as a
// last-resort fallback when no other lookup path can determine the field
// type — see callers for the surrounding gate (CallExpr LHS, unknown xType).
func (t *galaASTTransformer) isImmutableTupleField(selName string) bool {
	for _, name := range tupleTypeNames() {
		typeMeta := t.getTypeMeta(name)
		if typeMeta == nil {
			continue
		}
		for i, f := range typeMeta.FieldNames {
			if f == selName && i < len(typeMeta.ImmutFlags) && typeMeta.ImmutFlags[i] {
				return true
			}
		}
	}
	return false
}

// callExprReturnType resolves the declared return type of a bare-ident
// function call by consulting metadata sources directly, without re-running
// the type-inference path that already returned NilType. Used as a fallback
// for auto-unwrap on `funcCall().Field` when type inference for the call
// itself failed (e.g., dot-imported Go-only packages whose transitive imports
// the Go SDK can't load from the analyzer's view).
//
// Sources, in order:
//   - GALA function metadata (functions declared in any analyzed .gala source)
//   - Go function metadata under the current package
//   - Go function metadata under any dot-imported package
func (t *galaASTTransformer) callExprReturnType(ce *ast.CallExpr) transpiler.Type {
	fun := ce.Fun
	// Strip explicit type arguments: TermSize[int]() -> TermSize
	if idx, ok := fun.(*ast.IndexExpr); ok {
		fun = idx.X
	} else if idxList, ok := fun.(*ast.IndexListExpr); ok {
		fun = idxList.X
	}
	id, ok := fun.(*ast.Ident)
	if !ok {
		return transpiler.NilType{}
	}
	// GALA function metadata first.
	if fMeta := t.getFunction(id.Name); fMeta != nil && fMeta.ReturnType != nil && !fMeta.ReturnType.IsNil() {
		return fMeta.ReturnType
	}
	// Go function metadata: current package, then dot-imported packages.
	// Resolved through the call-site-aware form so a generic callee's declared
	// type parameters are instantiated rather than surfacing verbatim.
	if t.packageName != "" {
		if r := t.getGoFuncReturnTypeForCall(t.packageName+"."+id.Name, ce, nil); !r.IsNil() {
			return r
		}
	}
	if t.importManager != nil {
		for _, pkg := range t.importManager.GetDotImports() {
			if pkg == "" || pkg == t.packageName {
				continue
			}
			if r := t.getGoFuncReturnTypeForCall(pkg+"."+id.Name, ce, nil); !r.IsNil() {
				return r
			}
		}
	}
	return transpiler.NilType{}
}

// resolveIndexAccess handles index/subscript expressions with Immutable unwrapping.
func (t *galaASTTransformer) resolveIndexAccess(base ast.Expr, suffix *grammar.PostfixSuffixContext) (ast.Expr, error) {
	exprList := suffix.ExpressionList()
	if exprList == nil {
		return nil, galaerr.NewSemanticErrorAt(suffix.GetStart().GetLine(), suffix.GetStart().GetColumn(), "index expression requires expression list")
	}
	if res := t.goResultOf(base); res != nil {
		return nil, t.goResultMisuse(res, "it cannot be indexed", suffix)
	}
	if id, ok := base.(*ast.Ident); ok && t.typeParamValue(id.Name) {
		return nil, t.typeParamMisuseError(suffix, id.Name, "takes no type arguments")
	}
	if err := t.checkVariantTypeArgs(base, exprList.AllExpression()); err != nil {
		return nil, err
	}
	base = t.unwrapImmutable(base)
	indices, err := t.transformExpressionList(exprList.(*grammar.ExpressionListContext))
	if err != nil {
		return nil, err
	}
	if len(indices) == 1 {
		return &ast.IndexExpr{X: base, Index: indices[0]}, nil
	}
	return &ast.IndexListExpr{X: base, Indices: indices}, nil
}

// applyCallSuffix moved to calls.go

// transformCallWithArgsCtx moved to calls.go

// handleNamedArgsCall moved to calls.go

func (t *galaASTTransformer) transformPrimaryExpr(ctx *grammar.PrimaryExprContext) (ast.Expr, error) {
	if p := ctx.Primary(); p != nil {
		return t.transformPrimary(p.(*grammar.PrimaryContext))
	}

	if l := ctx.LambdaExpression(); l != nil {
		return t.transformLambda(l.(*grammar.LambdaExpressionContext))
	}

	if i := ctx.IfExpression(); i != nil {
		return t.transformIfExpression(i.(*grammar.IfExpressionContext))
	}

	if pf := ctx.PartialFunctionLiteral(); pf != nil {
		return t.transformPartialFunctionLiteral(pf.(*grammar.PartialFunctionLiteralContext), nil)
	}

	return nil, galaerr.NewSemanticErrorAt(ctx.GetStart().GetLine(), ctx.GetStart().GetColumn(), "primaryExpr must have primary, lambda, if expression, or partial function")
}

// transformPostfixMatchExpression handles match expressions with the new grammar.
func (t *galaASTTransformer) transformPostfixMatchExpression(ctx *grammar.PostfixExprContext) (ast.Expr, error) {
	return t.transformPostfixMatchExpressionAgainst(ctx, slot{})
}

// transformPostfixMatchExpressionAgainst lowers a match whose value fills slot s
// (zero when none); see buildMatchExpressionFromClauses.
func (t *galaASTTransformer) transformPostfixMatchExpressionAgainst(ctx *grammar.PostfixExprContext, s slot) (ast.Expr, error) {
	// Get the primary expression being matched
	primaryExpr := ctx.PrimaryExpr()
	if primaryExpr == nil {
		return nil, galaerr.NewSemanticErrorAt(ctx.GetStart().GetLine(), ctx.GetStart().GetColumn(), "match expression must have subject")
	}

	subject, err := t.transformPrimaryExpr(primaryExpr.(*grammar.PrimaryExprContext))
	if err != nil {
		return nil, err
	}

	// Apply any suffixes before the match
	suffixes := ctx.AllPostfixSuffix()
	for _, suffix := range suffixes {
		subject, err = t.applyPostfixSuffix(subject, suffix.(*grammar.PostfixSuffixContext))
		if err != nil {
			return nil, err
		}
	}

	// Now handle the match expression
	caseClauses := ctx.AllCaseClause()
	return t.buildMatchExpressionFromClauses(subject, "obj", caseClauses, ctx, s)
}

// buildMatchExpressionFromClauses builds a match expression from the subject and case clauses.
// ctx is used for error position reporting when case clauses are empty.
func (t *galaASTTransformer) buildMatchExpressionFromClauses(subject ast.Expr, paramName string, caseClauses []grammar.ICaseClauseContext, ctx antlr.ParserRuleContext, s slot) (ast.Expr, error) {
	// Get the type of the matched expression
	matchedType := t.getExprTypeNameManual(subject)
	if transpiler.IsUnusable(matchedType) {
		matchedType, _ = t.inferExprType(subject)
	}
	if transpiler.IsUnusable(matchedType) {
		// Fallback: try to infer the sealed parent type from the case patterns.
		// If cases are Some/None, the subject must be Option; Success/Failure → Try; etc.
		matchedType = t.inferMatchedTypeFromCases(caseClauses)
	}
	if transpiler.IsUnusable(matchedType) {
		if len(caseClauses) > 0 {
			cc := caseClauses[0].(*grammar.CaseClauseContext)
			return nil, galaerr.NewSemanticErrorAt(cc.GetStart().GetLine(), cc.GetStart().GetColumn(), "cannot infer type of matched expression")
		}
		return nil, galaerr.NewSemanticErrorAt(ctx.GetStart().GetLine(), ctx.GetStart().GetColumn(), "cannot infer type of matched expression")
	}
	// A subject typed by an alias (`type Checked Try[Email]`) is matched as
	// the type the alias names: its variants, extractors and exhaustiveness.
	// A binding of the whole subject keeps the type as written, so methods
	// declared on the alias itself (`func (c Coord) Sum()`) stay reachable.
	subjectType := matchedType
	matchedType = t.followAliasChain(matchedType)

	// Note: We intentionally do NOT replace types with unresolved type parameters (like Box[T])
	// with 'any'. Keeping the original parametric type allows correct extractor type inference
	// and valid Go code generation when inside a generic function where type parameters are in scope.

	t.pushScope()
	defer t.popScope()
	t.addVar(paramName, subjectType)

	// Consume the statement-position marker set by transformBlock. Arm bodies
	// must see matchInStatementPos = false so a NESTED match used as the arm's
	// value is not also forced to void.
	stmtPosition := t.matchInStatementPos
	t.matchInStatementPos = false
	defer func() { t.matchInStatementPos = stmtPosition }()
	// In statement position every arm's value is discarded too, so an arm
	// block's trailing match is itself a statement.
	s.discarded = stmtPosition
	// A match whose value a local declaration stores, and whose arms hold a
	// `return`, `break` or `continue`, is lowered as statements storing its
	// value (see hoisted_value.go); its arms store theirs the same way.
	if s.hoist != "" && (stmtPosition || !escapesConstruct(t, caseClauses...)) {
		s.hoist = ""
	}
	hoist := s.hoist

	// The slot type the match fills (see lowerAgainst) is each arm's expected
	// value type.
	// A match in value position lowers to an IIFE, so a `return` in an arm
	// leaves the IIFE: it must not fill or defer into an enclosing lambda's
	// fillable slot. (A statement-position match whose arms return is inlined,
	// and its returns do exit the lambda, so it keeps the lambda's slot; so is
	// a match lowered as statements.)
	switch {
	case hoist != "":
	case !stmtPosition:
		defer t.enterIIFEReturnSlot(s.typ)()
	case !transpiler.IsUnusable(s.typ):
		defer t.enterReturnSlot(returnSlot{typ: s.typ})()
	}

	// Validate sealed-variant pattern arity before transforming arms. An
	// under-/over-bound extractor pattern (e.g. `Rect(w, h)` for a 3-field
	// Rect) must surface as a coded GALA-E0004 here; otherwise the mis-bound
	// pattern flows into arm type inference and fails far away as a confusing
	// match-branch type mismatch.
	{
		patterns := make([]grammar.IPatternContext, 0, len(caseClauses))
		for _, cc := range caseClauses {
			patterns = append(patterns, cc.(*grammar.CaseClauseContext).Pattern())
		}
		if arityErr := t.validateSealedVariantArity(matchedType, patterns); arityErr != nil {
			return nil, arityErr
		}
	}

	// Pre-scan: check if there's an explicit, UNGUARDED wildcard `_` case.
	// If there is, binding patterns are regular clauses. If not, the last
	// binding pattern acts as the default (catch-all) case. A guarded wildcard
	// (`case _ if g`) is conditional, not a catch-all, so it does not count.
	hasExplicitWildcard := false
	for _, cc := range caseClauses {
		ccCtx := cc.(*grammar.CaseClauseContext)
		if isWildcard(ccCtx.Pattern().GetText()) && ccCtx.GetGuard() == nil {
			hasExplicitWildcard = true
			break
		}
	}

	// Which arm is the default depends on the patterns alone: an explicit
	// wildcard `_` always, or a binding pattern when there is no explicit
	// wildcard elsewhere (the binding acts as catch-all). A guarded clause is
	// conditional and never a default — control can fall through to a later
	// case when the guard is false.
	// isBinding[i] reports whether clause i's pattern binds the whole subject
	// (see isBindingPatternOf); it is resolved once and read below.
	isBinding := make([]bool, len(caseClauses))
	isDefault := make([]bool, len(caseClauses))
	foundDefault := false
	for i, cc := range caseClauses {
		ccCtx := cc.(*grammar.CaseClauseContext)
		patternText := ccCtx.Pattern().GetText()
		isBinding[i] = t.isBindingPatternOf(patternText, matchedType)
		if ccCtx.GetGuard() != nil || !(isWildcard(patternText) || (!hasExplicitWildcard && isBinding[i])) {
			continue
		}
		if foundDefault {
			return nil, galaerr.NewCodedSemanticError(
				galaerr.CodeMultipleDefaults,
				ccCtx.GetStart().GetLine(), ccCtx.GetStart().GetColumn(),
				"multiple default cases in match expression",
				"keep one default case; combine logic with guards or nested matches if you need sub-cases")
		}
		foundDefault = true
		isDefault[i] = true
	}

	arms := make([]matchArm, len(caseClauses))
	lowerArm := func(i int, armSlot slot) (transpiler.Type, error) {
		ccCtx := caseClauses[i].(*grammar.CaseClauseContext)
		var arm matchArm
		var err error
		if isDefault[i] {
			arm, err = t.lowerDefaultMatchArm(ccCtx, paramName, subjectType, isBinding[i], armSlot)
		} else {
			// A guarded binding of the whole subject (`case p if ...`) keeps
			// the written type too; every other pattern reads the variants.
			armType := matchedType
			if isBinding[i] {
				armType = subjectType
			}
			arm.clause, arm.resultType, err = t.transformCaseClauseWithType(ccCtx, paramName, armType, armSlot)
			arm.hasResult = arm.resultType != nil
		}
		if err != nil {
			return nil, err
		}
		arms[i] = arm
		return arm.resultType, nil
	}
	if err := t.lowerBranches(len(caseClauses), s, !stmtPosition && transpiler.IsUnusable(s.typ), lowerArm); err != nil {
		return nil, err
	}

	var clauses []ast.Stmt
	var defaultBody []ast.Stmt
	// irrefutableTupleArm: an unguarded tuple arm whose lowered condition is
	// constant true, so it matches every value (see armMatchesEverything).
	irrefutableTupleArm := false
	var resultTypes []transpiler.Type
	var casePatterns []string
	for i, arm := range arms {
		ccCtx := caseClauses[i].(*grammar.CaseClauseContext)
		if isDefault[i] {
			defaultBody = arm.defaultBody
			if arm.hasResult {
				resultTypes = append(resultTypes, arm.resultType)
				casePatterns = append(casePatterns, "case _")
			}
			continue
		}
		if !irrefutableTupleArm && ccCtx.GetGuard() == nil && armMatchesEverything(arm.clause) &&
			t.isTuplePatternOfSubjectArity(ccCtx.Pattern(), matchedType) {
			irrefutableTupleArm = true
		}
		if arm.clause != nil {
			clauses = append(clauses, arm.clause)
		}
		if arm.hasResult {
			resultTypes = append(resultTypes, arm.resultType)
			casePatterns = append(casePatterns, fmt.Sprintf("case %s", ccCtx.Pattern().GetText()))
		}
	}

	// Loop control in an arm decides the lowering: a statement match is
	// inlined so the loop sees it, and a match whose value is used rejects it
	// before its arms are unified (an arm ending in `break` has no value).
	loopControl := t.escapingLoopControl(clauses, defaultBody)
	if loopControl != nil && !stmtPosition && hoist == "" {
		return nil, t.loopControlInValueError("a match", loopControl)
	}

	// Infer common result type from all branches. In statement position the
	// value is discarded, so arms need not unify (see inferCommonResultType).
	resultType, err := t.inferCommonResultType(resultTypes, casePatterns, ctx, stmtPosition)
	if err != nil {
		return nil, err
	}
	resultType = t.branchingResultType(resultType, s)
	// A match lowered as statements stores a value of its type, so it needs
	// one.
	if hoist != "" && (transpiler.IsUnusable(resultType) || resultType.IsVoid()) {
		allLeave := !slices.ContainsFunc(arms, func(a matchArm) bool { return !t.armLeaves(a.clause, a.defaultBody) })
		if resultType, err = t.hoistedType("match", resultType, s, allLeave,
			ctx.GetStart().GetLine(), ctx.GetStart().GetColumn(), clauses, defaultBody); err != nil {
			return nil, err
		}
	}

	// Statement-position matches discard their value; force the IIFE to be
	// void so that arms with mixed value/void payloads — e.g. one arm calling
	// a Go method returning bool, another calling a void Go method — do not
	// emit `return <voidCall>` (rejected by Go as "no value used as value").
	// So is a dispatch-style match no arm and no slot types (see
	// inferCommonResultType).
	if stmtPosition || transpiler.IsUnusable(resultType) {
		resultType = transpiler.VoidType{}
	}

	// Reject bare `return` inside a value-producing match (the IIFE would need
	// to return a concrete type, but a bare return produces none). See GALA-E0015.
	// A match lowered as statements has no function literal to return from:
	// its bare `return` leaves the enclosing function, which must then return
	// nothing.
	if err := t.validateNoBareReturnsInValueMatch(clauses, defaultBody, resultType, hoist != "",
		ctx.GetStart().GetLine(), ctx.GetStart().GetColumn()); err != nil {
		return nil, err
	}

	// Note: We keep result types with unresolved type parameters because they are valid Go
	// when inside a generic function where the type parameters are in scope.

	if len(clauses) == 0 && len(defaultBody) == 0 {
		if len(caseClauses) > 0 {
			cc := caseClauses[0].(*grammar.CaseClauseContext)
			return nil, galaerr.NewSemanticErrorAt(cc.GetStart().GetLine(), cc.GetStart().GetColumn(), "match expression must have at least one case")
		}
		return nil, galaerr.NewSemanticErrorAt(ctx.GetStart().GetLine(), ctx.GetStart().GetColumn(), "match expression must have at least one case")
	}

	// Exhaustiveness of a match over a sealed type or bool (see coverageOf)
	{
		ccs := make([]*grammar.CaseClauseContext, len(caseClauses))
		for i, cc := range caseClauses {
			ccs[i] = cc.(*grammar.CaseClauseContext)
		}
		isSealed, isExhaustive, missing, guardedMissing := t.coverageOf(matchedType, ccs)

		// An unguarded binding (`case n =>`) already set foundDefault above.
		hasDefault := foundDefault

		// An unguarded irrefutable tuple arm — `case (_, _, err) =>` against a
		// Tuple3 — matches every value, so the match is complete without a
		// default. It lowers to an ordinary clause, so the if-chain still
		// needs a terminating else: give it the same unreachable panic an
		// exhaustive sealed match gets.
		if !hasDefault && irrefutableTupleArm {
			hasDefault = true
			defaultBody = unreachableDefaultBody()
		}

		if !hasDefault {
			// Both diagnoses below are about the match as a whole, so they
			// anchor on the first case clause (the match keyword itself sits
			// after the subject and reads worse in the framed snippet).
			line, col := ctx.GetStart().GetLine(), ctx.GetStart().GetColumn()
			if len(caseClauses) > 0 {
				cc := caseClauses[0].(*grammar.CaseClauseContext)
				line, col = cc.GetStart().GetLine(), cc.GetStart().GetColumn()
			}
			if isSealed && !isExhaustive {
				hint := "add the missing variant cases, or add a `case _ => ...` default to cover them"
				if guardedMissing {
					hint += "; a case with an `if` guard does not count, since its guard may be false"
				}
				return nil, galaerr.NewCodedSemanticError(
					galaerr.CodeNonExhaustiveMatch,
					line, col,
					fmt.Sprintf("non-exhaustive match: missing cases: %s", strings.Join(missing, ", ")),
					hint)
			} else if isSealed && isExhaustive {
				// Exhaustive sealed match — generate synthetic panic("unreachable") default
				defaultBody = unreachableDefaultBody()
			} else if !isSealed {
				// The remediation lives in the hint only — repeating
				// `case _ => ...` in the message duplicated what the
				// renderer already prints as the caret annotation and the
				// hint footer.
				return nil, galaerr.NewCodedSemanticError(
					galaerr.CodeMissingDefault,
					line, col,
					"match expression must have a default case",
					"add `case _ => ...`")
			}
		}
		// When foundDefault && isSealed && isExhaustive: unreachable default is harmless, allow it
	}

	// Statement-position match with a user-written `return X` inside an arm
	// body cannot use the IIFE lowering. Wrapping the body in
	// `func(obj T) { ... }(subject)` would trap that `return X` inside the
	// IIFE's lambda — the surrounding gala function never returns, and any
	// enclosing for-loop spins forever (the bug fired as a runtime hang, not
	// a compile error). Inline the body instead so user returns become
	// genuine Go returns from the enclosing function. Synthesized arm-tail
	// returns (added to feed the IIFE's value channel) are stripped, since
	// the value would have been discarded anyway.
	//
	// A `break` / `continue` in an arm is inlined for the same reason: in the
	// IIFE it would name no loop, and the enclosing loop must see it.
	if stmtPosition && (loopControl != nil || t.containsUserReturnInClauses(clauses, defaultBody)) {
		// A `use` in an arm is released at the end of the match, as in a
		// match lowered to a function literal (see scopeReleases).
		body := t.buildMatchBodyForInline(clauses, defaultBody)
		stmts, err := t.scopeReleases([]ast.Stmt{t.buildInlinedMatchBlock(subject, paramName, matchedType, body)},
			ctx.GetStart().GetLine(), ctx.GetStart().GetColumn())
		if err != nil {
			return nil, err
		}
		block, ok := stmts[0].(*ast.BlockStmt)
		if !ok {
			block = &ast.BlockStmt{List: stmts}
		}
		t.pendingMatchStmtBlock = block
		// Return a placeholder; transformBlock recognises pendingMatchStmtBlock
		// and replaces the wrapping ExprStmt with the inlined block.
		return ast.NewIdent("_"), nil
	}

	// A match whose value a local declaration stores, and whose arms hold a
	// `return`, `break` or `continue`, is lowered as statements: each arm
	// stores its value in the declaration's variable, and its control flow
	// acts on the enclosing function or loop (see hoisted_value.go).
	if hoist != "" {
		body := t.storeArmValues(chainMatchClauses(clauses, defaultBody), hoist, resultType)
		block := t.buildInlinedMatchBlock(subject, paramName, matchedType, body)
		return t.hoistedResult(hoist, []ast.Stmt{block}, resultType, ctx.GetStart().GetLine(), ctx.GetStart().GetColumn())
	}

	// Build the match body: chain clauses into if-else, attach default, handle void stripping
	stmts := t.buildMatchBody(clauses, defaultBody, resultType)

	// Check if result type is void (for side-effect only match statements)
	isVoid := resultType != nil && resultType.IsVoid()

	// Build IIFE with or without return type depending on void
	var resultsField *ast.FieldList
	if !isVoid {
		resultsField = &ast.FieldList{List: []*ast.Field{{Type: t.typeToExpr(resultType)}}}
	}

	funcLit := &ast.FuncLit{
		Type: &ast.FuncType{
			Params:  &ast.FieldList{List: []*ast.Field{{Names: []*ast.Ident{ast.NewIdent(paramName)}, Type: t.typeToExpr(matchedType)}}},
			Results: resultsField,
		},
		Body: &ast.BlockStmt{List: stmts},
	}

	call := &ast.CallExpr{Fun: funcLit, Args: []ast.Expr{subject}}
	if !stmtPosition {
		t.recordBranchingCall(call, "match", ctx.GetStart().GetLine(), ctx.GetStart().GetColumn())
	}
	return call, nil
}

func (t *galaASTTransformer) transformTupleLiteral(exprs []ast.Expr, line ...int) (ast.Expr, error) {
	return t.transformTupleLiteralWithExpected(exprs, nil, line...)
}

// transformTupleLiteralWithExpected lowers a tuple literal `(a, b, ...)` to
// `std.TupleN[T1, T2, ...]{V1: NewImmutable(a), V2: NewImmutable(b), ...}`.
//
// slotElems, when non-nil, holds the element types of the slot the literal
// itself fills (a declared return, argument or field type; see
// tupleElementExpectedTypes). An element whose own type is unknown takes its
// slot element's type. So does an element Go would convert on assignment: an
// untyped numeric constant (`(1, 2)` into Tuple[int64, float32]), or a value
// going into an interface slot (`(Square(1.0), 2)` into Tuple[Shape, int]).
// Without a slot an element of unknown type degrades to `any`.
func (t *galaASTTransformer) transformTupleLiteralWithExpected(exprs []ast.Expr, slotElems []transpiler.Type, line ...int) (ast.Expr, error) {
	n := len(exprs)
	if n < 2 || n > 10 {
		errLine, errCol := t.lastLine, t.lastCol
		if len(line) >= 2 {
			errLine, errCol = line[0], line[1]
		}
		return nil, galaerr.NewSemanticErrorAt(errLine, errCol, fmt.Sprintf("tuple literals must have 2-10 elements, got %d", n))
	}

	// Determine tuple type name based on arity (B2 — single source of truth).
	typeName, _ := transpiler.TupleArityName(n)

	if len(slotElems) != n {
		slotElems = nil
	}

	var typeParams []ast.Expr
	// slotTypes records, per element, the slot type the tuple took for it in
	// place of the element's own type, so its NewImmutable wrapper names the
	// same type argument.
	slotTypes := make([]transpiler.Type, n)
	for i, expr := range exprs {
		var expected transpiler.Type = transpiler.NilType{}
		if slotElems != nil && slotElems[i] != nil {
			expected = slotElems[i]
		}
		exprType := t.getExprTypeName(expr)
		if exprType.IsNil() || exprType.IsAny() {
			if !expected.IsNil() && !expected.IsAny() {
				typeParams = append(typeParams, t.typeToExpr(expected))
				// A bare `nil` element has no type of its own; its wrapper
				// must name the slot type too.
				slotTypes[i] = expected
			} else {
				typeParams = append(typeParams, ast.NewIdent("any"))
			}
		} else {
			if !expected.IsNil() && expected.String() != exprType.String() {
				// An element whose wrapper must name its slot type — an untyped
				// constant going into a numeric or opaque slot, a value going
				// into an interface (`Shape`, `error`, `any`) or a Go named
				// function type slot — gives the tuple that type argument too,
				// as a Go assignment would convert it: Tuple[Square, int] is not
				// a Tuple[Shape, int].
				if typeArg := t.immutableTypeArg(expr, expected); typeArg != nil {
					typeParams = append(typeParams, typeArg)
					slotTypes[i] = expected
					continue
				}
				// Sealed widening: when the element's static type is a sealed
				// CASE (e.g. `MsgCmd[AppMsg]`) and the slot's element type is its
				// sealed PARENT (`Cmd[AppMsg]`), emit the parent in the tuple's
				// type arguments, the lowest common type for every arm filling
				// that slot.
				if parent := t.sealedCaseParent(exprType); parent != nil && parent.String() == expected.String() {
					typeParams = append(typeParams, t.typeToExpr(expected))
					continue
				}
			}
			typeParams = append(typeParams, t.typeToExpr(exprType))
		}
	}

	// Build the type expression: std.TupleN[T1, T2, ...]
	var typeExpr ast.Expr = t.stdIdent(typeName)
	if len(typeParams) == 1 {
		typeExpr = &ast.IndexExpr{X: typeExpr, Index: typeParams[0]}
	} else if len(typeParams) > 1 {
		typeExpr = &ast.IndexListExpr{X: typeExpr, Indices: typeParams}
	}

	// Build the composite literal: std.TupleN[...]{V1: NewImmutable(a), V2: NewImmutable(b), ...}
	// Tuple fields are Immutable, so we need to wrap each value
	var elts []ast.Expr
	for i, expr := range exprs {
		fieldName := fmt.Sprintf("V%d", i+1)
		// Wrap value in NewImmutable unless it's already immutable
		wrappedExpr := expr
		exprType := t.getExprTypeName(expr)
		if !t.isImmutableType(exprType) {
			wrappedExpr = t.newImmutableFor(expr, slotTypes[i])
		}
		elts = append(elts, &ast.KeyValueExpr{
			Key:   ast.NewIdent(fieldName),
			Value: wrappedExpr,
		})
	}

	t.needsStdImport = true
	return &ast.CompositeLit{
		Type: typeExpr,
		Elts: elts,
	}, nil
}

// inferTypeArgsFromApply infers type arguments for a generic type from its Apply method arguments.
// For example, when calling Some(10), this infers T=int from the argument type.
// It matches the type's type parameters with the Apply method's parameter types to determine
// which argument positions correspond to which type parameters.
// inferTypeArgsFromApply moved to calls.go

// transformPartialFunctionLiteral transforms a partial function literal { case ... => ... }
// into a function that returns Option[T], where matched cases return Some(result)
// and unmatched cases return None[T]()
// Partial function related functions moved to lambdas.go
