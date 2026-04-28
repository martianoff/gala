package transformer

import (
	"fmt"
	"go/ast"
	"go/token"
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

	result, err := t.transformPrimaryExpr(primaryExpr.(*grammar.PrimaryExprContext))
	if err != nil {
		return nil, err
	}

	// Apply postfix suffixes
	suffixes := ctx.AllPostfixSuffix()
	for _, suffix := range suffixes {
		result, err = t.applyPostfixSuffix(result, suffix.(*grammar.PostfixSuffixContext))
		if err != nil {
			return nil, err
		}
	}

	return result, nil
}

func (t *galaASTTransformer) applyPostfixSuffix(base ast.Expr, suffix *grammar.PostfixSuffixContext) (ast.Expr, error) {
	if suffix.Identifier() != nil {
		return t.resolveFieldAccess(base, suffix.Identifier().GetText())
	}

	childCount := suffix.GetChildCount()
	if childCount >= 2 {
		firstChild := suffix.GetChild(0).(antlr.ParseTree).GetText()
		if firstChild == "(" {
			return t.applyCallSuffix(base, suffix)
		}
		if firstChild == "[" {
			return t.resolveIndexAccess(base, suffix)
		}
	}

	return nil, galaerr.NewSemanticErrorAt(suffix.GetStart().GetLine(), suffix.GetStart().GetColumn(), "unknown postfix suffix type")
}

// resolveFieldAccess handles member access with automatic Immutable/ConstPtr unwrapping.
func (t *galaASTTransformer) resolveFieldAccess(base ast.Expr, selName string) (ast.Expr, error) {
	xType := t.getExprTypeName(base)
	isImmutable := t.isImmutableType(xType)

	// Don't unwrap if we're accessing Immutable's own fields/methods
	if !isImmutable || (selName != "Get" && selName != "value") {
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
	}

	// Check structFieldTypes (Immutable wrapper in field type)
	if fieldTypes, ok := t.structFieldTypes[resolvedTypeName]; ok {
		if fieldType, ok := fieldTypes[selName]; ok && t.isImmutableType(fieldType) {
			return true
		}
	}

	// Std library types: check generated field type
	if registry.IsStdType(baseTypeName) || registry.IsStdType(strings.TrimPrefix(baseTypeName, registry.StdPackageName+".")) {
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
		if ce, ok := selExpr.X.(*ast.CallExpr); ok && len(ce.Args) == 0 {
			if se, ok := ce.Fun.(*ast.SelectorExpr); ok && se.Sel.Name == "Get" {
				if id, ok := se.X.(*ast.Ident); ok && t.isVal(id.Name) {
					// Resolve the val's actual type from scope
					valType := t.getValType(id.Name)
					if !valType.IsNil() {
						innerType := valType
						// Unwrap Immutable[T] → T
						if gen, ok := valType.(transpiler.GenericType); ok && len(gen.Params) > 0 {
							baseName := gen.Base.String()
							if baseName == transpiler.TypeImmutable || baseName == "std."+transpiler.TypeImmutable {
								innerType = gen.Params[0]
							}
						}
						// Check if the resolved inner type has this field as immutable
						innerName := innerType.String()
						if idx := strings.Index(innerName, "["); idx != -1 {
							innerName = innerName[:idx]
						}
						innerName = strings.TrimPrefix(innerName, "*")
						resolvedInner := t.resolveStructTypeName(innerName)
						if fields, ok := t.structFields[resolvedInner]; ok {
							for i, f := range fields {
								if f == selName {
									return t.structImmutFields[resolvedInner][i]
								}
							}
						}
						if typeMeta := t.getTypeMeta(innerName); typeMeta != nil {
							for i, f := range typeMeta.FieldNames {
								if f == selName {
									return i < len(typeMeta.ImmutFlags) && typeMeta.ImmutFlags[i]
								}
							}
						}
					}
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
	if t.packageName != "" {
		if r := t.getGoFuncReturnType(t.packageName + "." + id.Name); !r.IsNil() {
			return r
		}
	}
	if t.importManager != nil {
		for _, pkg := range t.importManager.GetDotImports() {
			if pkg == "" || pkg == t.packageName {
				continue
			}
			if r := t.getGoFuncReturnType(pkg + "." + id.Name); !r.IsNil() {
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

// transformPostfixMatchExpression handles match expressions with the new grammar
func (t *galaASTTransformer) transformPostfixMatchExpression(ctx *grammar.PostfixExprContext) (ast.Expr, error) {
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
	return t.buildMatchExpressionFromClauses(subject, "obj", caseClauses, ctx)
}

// buildMatchExpressionFromClauses builds a match expression from the subject and case clauses.
// ctx is used for error position reporting when case clauses are empty.
func (t *galaASTTransformer) buildMatchExpressionFromClauses(subject ast.Expr, paramName string, caseClauses []grammar.ICaseClauseContext, ctx antlr.ParserRuleContext) (ast.Expr, error) {
	// Get the type of the matched expression
	matchedType := t.getExprTypeNameManual(subject)
	if matchedType == nil || matchedType.IsNil() {
		matchedType, _ = t.inferExprType(subject)
	}
	if matchedType == nil || matchedType.IsNil() {
		// Fallback: try to infer the sealed parent type from the case patterns.
		// If cases are Some/None, the subject must be Option; Success/Failure → Try; etc.
		matchedType = t.inferMatchedTypeFromCases(caseClauses)
	}
	if matchedType == nil || matchedType.IsNil() {
		if len(caseClauses) > 0 {
			cc := caseClauses[0].(*grammar.CaseClauseContext)
			return nil, galaerr.NewSemanticErrorAt(cc.GetStart().GetLine(), cc.GetStart().GetColumn(), "cannot infer type of matched expression")
		}
		return nil, galaerr.NewSemanticErrorAt(ctx.GetStart().GetLine(), ctx.GetStart().GetColumn(), "cannot infer type of matched expression")
	}

	// Note: We intentionally do NOT replace types with unresolved type parameters (like Box[T])
	// with 'any'. Keeping the original parametric type allows correct extractor type inference
	// and valid Go code generation when inside a generic function where type parameters are in scope.

	t.pushScope()
	defer t.popScope()
	t.addVar(paramName, matchedType)

	// Track match subject type so branch bodies can infer type params
	// for sealed variant constructors (e.g., None() infers None[int] from Option[int])
	prevMatchSubjectType := t.currentMatchSubjectType
	t.currentMatchSubjectType = matchedType
	defer func() { t.currentMatchSubjectType = prevMatchSubjectType }()

	// Consume the statement-position marker set by transformBlock. Arm bodies
	// must see matchInStatementPos = false so a NESTED match used as the arm's
	// value is not also forced to void.
	stmtPosition := t.matchInStatementPos
	t.matchInStatementPos = false
	defer func() { t.matchInStatementPos = stmtPosition }()

	// Downward inference for match-arm sealed-variant constructors (context 5):
	// when the match expression's value flows into a slot with a known
	// concrete type (val with explicit type, function return, function arg),
	// promote that expected type to the IIFE's enclosing return type so each
	// arm's expression can resolve unannotated case constructors against it.
	// expectedArgTypes carries the slot type set by the val-decl / argument
	// transformers; consume the top hint (so subsequent unrelated calls
	// don't see it) and stash on currentFuncReturnType for the duration of
	// arm processing (B1).
	if pending := t.expectedArgTypes.peek(); pending != nil && !pending.IsNil() {
		t.expectedArgTypes.consume()
		prevReturn := t.currentFuncReturnType
		t.currentFuncReturnType = pending
		defer func() { t.currentFuncReturnType = prevReturn }()
	}

	var clauses []ast.Stmt
	var defaultBody []ast.Stmt
	foundDefault := false
	var resultTypes []transpiler.Type
	var casePatterns []string

	// Pre-scan: check if there's an explicit wildcard `_` case.
	// If there is, binding patterns are regular clauses. If not, the last
	// binding pattern acts as the default (catch-all) case.
	hasExplicitWildcard := false
	for _, cc := range caseClauses {
		if isWildcard(cc.(*grammar.CaseClauseContext).Pattern().GetText()) {
			hasExplicitWildcard = true
			break
		}
	}

	for _, cc := range caseClauses {
		ccCtx := cc.(*grammar.CaseClauseContext)

		patCtx := ccCtx.Pattern()
		patternText := patCtx.GetText()
		// Treat as default: explicit wildcard `_` always, OR binding pattern when
		// there's no explicit wildcard elsewhere (binding acts as catch-all).
		treatAsDefault := isWildcard(patternText) ||
			(!hasExplicitWildcard && isBindingPattern(patternText))

		if treatAsDefault {
			if foundDefault {
				return nil, galaerr.NewSemanticErrorAt(ccCtx.GetStart().GetLine(), ccCtx.GetStart().GetColumn(), "multiple default cases in match expression")
			}
			foundDefault = true

			// For binding patterns, register the variable and add assignment
			var bindingStmts []ast.Stmt
			if isBindingPattern(patternText) {
				t.currentScope.vals[patternText] = false
				if matchedType != nil && !matchedType.IsNil() {
					t.currentScope.valTypes[patternText] = matchedType
				}
				bindingStmts = append(bindingStmts, &ast.AssignStmt{
					Lhs: []ast.Expr{ast.NewIdent(patternText)},
					Tok: token.DEFINE,
					Rhs: []ast.Expr{ast.NewIdent(paramName)},
				})
			}

			if ccCtx.GetBodyBlock() != nil {
				// The default arm's block-body last expression becomes the
				// arm's value, so it is value-consumed.
				t.blockLastStmtIsValue = true
				b, err := t.transformBlock(ccCtx.GetBodyBlock().(*grammar.BlockContext))
				if err != nil {
					return nil, err
				}
				defaultBody = append(bindingStmts, b.List...)
				if len(b.List) > 0 {
					lastStmt := b.List[len(b.List)-1]
					if ret, ok := lastStmt.(*ast.ReturnStmt); ok && len(ret.Results) > 0 {
						resultTypes = append(resultTypes, t.inferResultType(ret.Results[0]))
						casePatterns = append(casePatterns, "case _")
					} else if exprStmt, ok := lastStmt.(*ast.ExprStmt); ok {
						// Block's last expression statement becomes the return value
						defaultBody[len(defaultBody)-1] = &ast.ReturnStmt{Results: []ast.Expr{exprStmt.X}}
						resultTypes = append(resultTypes, t.inferResultType(exprStmt.X))
						casePatterns = append(casePatterns, "case _")
					}
				}
			} else if ccCtx.GetBodyStmt() != nil {
				bodyStmts, bodyType, err := t.transformCaseBodyStmt(ccCtx.GetBodyStmt())
				if err != nil {
					return nil, err
				}
				defaultBody = append(bindingStmts, bodyStmts...)
				resultTypes = append(resultTypes, bodyType)
				casePatterns = append(casePatterns, "case _")
			}
			continue
		}

		clause, resultType, err := t.transformCaseClauseWithType(ccCtx, paramName, matchedType)
		if err != nil {
			return nil, err
		}
		if clause != nil {
			clauses = append(clauses, clause)
		}
		if resultType != nil {
			resultTypes = append(resultTypes, resultType)
			casePatterns = append(casePatterns, fmt.Sprintf("case %s", patternText))
		}
	}

	// Infer common result type from all branches
	resultType, err := t.inferCommonResultType(resultTypes, casePatterns, ctx)
	if err != nil {
		return nil, err
	}

	// Statement-position matches discard their value; force the IIFE to be
	// void so that arms with mixed value/void payloads — e.g. one arm calling
	// a Go method returning bool, another calling a void Go method — do not
	// emit `return <voidCall>` (rejected by Go as "no value used as value").
	if stmtPosition {
		resultType = transpiler.VoidType{}
	}

	// Reject bare `return` inside a value-producing match (the IIFE would need
	// to return a concrete type, but a bare return produces none). See GALA-E0015.
	{
		startLine, startCol := ctx.GetStart().GetLine(), ctx.GetStart().GetColumn()
		if err := t.validateNoBareReturnsInValueMatch(clauses, defaultBody, resultType, startLine, startCol); err != nil {
			return nil, err
		}
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

	// Always collect variant patterns for exhaustiveness check
	{
		var variantPatterns []string
		for _, cc := range caseClauses {
			pat := cc.(*grammar.CaseClauseContext).Pattern().GetText()
			if !isDefaultPattern(pat) {
				variantPatterns = append(variantPatterns, pat)
			}
		}

		isSealed, isExhaustive, missing := t.isExhaustiveMatch(matchedType, variantPatterns)

		// A binding pattern (e.g., `case n =>`) is a catch-all even though it's
		// processed as a regular clause. Check for it in the exhaustiveness check.
		hasDefault := foundDefault
		if !hasDefault {
			for _, cc := range caseClauses {
				pat := cc.(*grammar.CaseClauseContext).Pattern().GetText()
				if isBindingPattern(pat) {
					hasDefault = true
					break
				}
			}
		}

		if !hasDefault {
			if isSealed && !isExhaustive {
				if len(caseClauses) > 0 {
					cc := caseClauses[0].(*grammar.CaseClauseContext)
					return nil, galaerr.NewSemanticErrorAt(cc.GetStart().GetLine(), cc.GetStart().GetColumn(),
						fmt.Sprintf("non-exhaustive match: missing cases: %s", strings.Join(missing, ", ")))
				}
				return nil, galaerr.NewSemanticErrorAt(ctx.GetStart().GetLine(), ctx.GetStart().GetColumn(),
					fmt.Sprintf("non-exhaustive match: missing cases: %s", strings.Join(missing, ", ")))
			} else if isSealed && isExhaustive {
				// Exhaustive sealed match — generate synthetic panic("unreachable") default
				defaultBody = []ast.Stmt{
					&ast.ExprStmt{X: &ast.CallExpr{
						Fun:  ast.NewIdent("panic"),
						Args: []ast.Expr{&ast.BasicLit{Kind: token.STRING, Value: `"unreachable"`}},
					}},
				}
			} else if !isSealed {
				if len(caseClauses) > 0 {
					cc := caseClauses[0].(*grammar.CaseClauseContext)
					return nil, galaerr.NewSemanticErrorAt(cc.GetStart().GetLine(), cc.GetStart().GetColumn(), "match expression must have a default case (case _ => ...)")
				}
				return nil, galaerr.NewSemanticErrorAt(ctx.GetStart().GetLine(), ctx.GetStart().GetColumn(), "match expression must have a default case (case _ => ...)")
			}
		}
		// When foundDefault && isSealed && isExhaustive: unreachable default is harmless, allow it
		_ = isSealed
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

	return &ast.CallExpr{Fun: funcLit, Args: []ast.Expr{subject}}, nil
}

func (t *galaASTTransformer) transformTupleLiteral(exprs []ast.Expr, line ...int) (ast.Expr, error) {
	n := len(exprs)
	if n < 2 || n > 10 {
		errLine, errCol := t.lastLine, t.lastCol
		if len(line) >= 2 {
			errLine, errCol = line[0], line[1]
		}
		return nil, galaerr.NewSemanticErrorAt(errLine, errCol, fmt.Sprintf("tuple literals must have 2-10 elements, got %d", n))
	}

	// Determine tuple type name based on arity (B2 — single source of truth).
	typeName, _ := tupleArityName(n)

	// Infer type parameters from expression types.
	// When inference fails for an element, fall back to the enclosing function's
	// return type if it is a Tuple with matching arity (prevents type widening to any).
	var fallbackTypes []transpiler.Type
	if retType, ok := t.currentFuncReturnType.(transpiler.GenericType); ok &&
		t.isTupleTypeName(retType.Base.String()) && len(retType.Params) == n {
		fallbackTypes = retType.Params
	}

	var typeParams []ast.Expr
	for i, expr := range exprs {
		exprType := t.getExprTypeName(expr)
		if exprType.IsNil() || exprType.IsAny() {
			if fallbackTypes != nil && !fallbackTypes[i].IsNil() && !fallbackTypes[i].IsAny() {
				typeParams = append(typeParams, t.typeToExpr(fallbackTypes[i]))
			} else {
				typeParams = append(typeParams, ast.NewIdent("any"))
			}
		} else {
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
			wrappedExpr = &ast.CallExpr{
				Fun:  t.stdIdent(transpiler.FuncNewImmutable),
				Args: []ast.Expr{expr},
			}
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
