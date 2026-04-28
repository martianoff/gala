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

func (t *galaASTTransformer) transformMatchExpression(ctx grammar.IExpressionContext) (ast.Expr, error) {
	expr, paramName, matchedType, err := t.parseMatchSubject(ctx)
	if err != nil {
		return nil, err
	}

	t.pushScope()
	defer t.popScope()
	t.addVar(paramName, matchedType)

	// Track match subject type so branch bodies can infer type params
	// for sealed variant constructors (e.g., None() infers None[int] from Option[int])
	prevMatchSubjectType := t.currentMatchSubjectType
	t.currentMatchSubjectType = matchedType
	defer func() { t.currentMatchSubjectType = prevMatchSubjectType }()

	// Consume the statement-position marker set by transformBlock so arm
	// bodies see matchInStatementPos = false (a NESTED match used as the
	// arm's value must remain value-producing).
	stmtPosition := t.matchInStatementPos
	t.matchInStatementPos = false
	defer func() { t.matchInStatementPos = stmtPosition }()

	clauses, defaultBody, resultType, err := t.transformMatchClauses(ctx, paramName, matchedType)
	if err != nil {
		return nil, err
	}

	// Statement-position matches discard their value; force the IIFE to be
	// void so void-returning arm calls do not appear as `return d.Skip()`
	// (which Go rejects as "no value used as value").
	if stmtPosition {
		resultType = transpiler.VoidType{}
	}

	t.needsStdImport = true
	body := t.buildMatchBody(clauses, defaultBody, resultType)

	matchLine, matchCol := 0, 0
	if ctx != nil && ctx.GetStart() != nil {
		matchLine = ctx.GetStart().GetLine()
		matchCol = ctx.GetStart().GetColumn()
	}
	return t.generateMatchIIFE(expr, paramName, matchedType, body, resultType, matchLine, matchCol)
}

// parseMatchSubject extracts and type-checks the expression being matched.
func (t *galaASTTransformer) parseMatchSubject(ctx grammar.IExpressionContext) (ast.Expr, string, transpiler.Type, error) {
	exprCtx := ctx.GetChild(0).(grammar.IExpressionContext)
	expr, err := t.transformExpression(exprCtx)
	if err != nil {
		return nil, "", nil, err
	}

	paramName := "obj"
	if primary := t.getPrimaryFromExpression(exprCtx); primary != nil {
		if primary.Identifier() != nil {
			paramName = primary.Identifier().GetText()
		}
	}

	// Infer matched expression type (manual first, then HM fallback)
	matchedType := t.getExprTypeNameManual(expr)
	if matchedType == nil || matchedType.IsNil() {
		matchedType, _ = t.inferExprType(expr)
	}
	if matchedType == nil || matchedType.IsNil() {
		if parserCtx, ok := ctx.(antlr.ParserRuleContext); ok {
			return nil, "", nil, t.semanticErrorAt(parserCtx, "cannot infer type of matched expression. Please add explicit type annotation to the variable being matched")
		}
		line, col := ctx.GetStart().GetLine(), ctx.GetStart().GetColumn()
		return nil, "", nil, galaerr.NewSemanticErrorAt(line, col, "cannot infer type of matched expression. Please add explicit type annotation to the variable being matched")
	}

	return expr, paramName, matchedType, nil
}

// extractVariantName extracts the variant/constructor name from a case pattern text.
// E.g. "Circle(r)" → "Circle", "Point()" → "Point", "Debug" → "Debug"
// Bare uppercase identifiers (e.g., `case Debug =>`) name zero-field sealed variants;
// they are recognised as variant patterns rather than simple bindings.
func extractVariantName(patternText string) string {
	idx := strings.Index(patternText, "(")
	var name string
	if idx < 0 {
		// No call suffix — bare identifier form like `Debug`.
		name = patternText
	} else if idx == 0 {
		return ""
	} else {
		name = patternText[:idx]
	}
	if len(name) == 0 || name[0] < 'A' || name[0] > 'Z' {
		return ""
	}
	// Reject identifiers containing non-identifier characters (e.g., dots, operators).
	for _, ch := range name {
		if !((ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') || ch == '_') {
			return ""
		}
	}
	return name
}

// isExhaustiveMatch checks if a set of case patterns exhaustively covers all possible
// values of the matched type. Supports booleans (true/false) and sealed types.
// Returns (isExhaustive type, isExhaustive, missingCases).
// First return is false when the matched type is not an exhaustive type at all.
func (t *galaASTTransformer) isExhaustiveMatch(matchedType transpiler.Type, patternTexts []string) (bool, bool, []string) {
	// Check boolean exhaustiveness first
	if bt, ok := matchedType.(transpiler.BasicType); ok && bt.Name == "bool" {
		hasTrue, hasFalse := false, false
		for _, pat := range patternTexts {
			if pat == "true" {
				hasTrue = true
			}
			if pat == "false" {
				hasFalse = true
			}
		}
		var missing []string
		if !hasTrue {
			missing = append(missing, "true")
		}
		if !hasFalse {
			missing = append(missing, "false")
		}
		return true, len(missing) == 0, missing
	}
	// Fall through to sealed type check
	return t.isSealedExhaustive(matchedType, patternTexts)
}

// isSealedExhaustive checks if a set of case patterns exhaustively covers all variants
// of a sealed type. Returns (isSealed, isExhaustive, missingVariants).
// isSealed is false when the matched type is not a sealed type at all.
func (t *galaASTTransformer) isSealedExhaustive(matchedType transpiler.Type, patternTexts []string) (bool, bool, []string) {
	baseName := matchedType.BaseName()
	meta := t.getTypeMeta(baseName)
	if meta == nil || !meta.IsSealed || len(meta.SealedVariants) == 0 {
		return false, false, nil
	}

	covered := make(map[string]bool)
	for _, pat := range patternTexts {
		if name := extractVariantName(pat); name != "" {
			covered[name] = true
		}
	}

	var missing []string
	for _, v := range meta.SealedVariants {
		if !covered[v.Name] {
			missing = append(missing, v.Name)
		}
	}

	return true, len(missing) == 0, missing
}

// isNoReturnCallExpr reports whether expr is a call to a Go builtin/function
// that does not return a value (e.g., `panic(...)`). Such a call cannot be
// wrapped in a `return <expr>` statement — Go rejects `return panic(...)` as
// "no value used as value". When such a call appears at the tail of a match
// arm, the transformer must emit it as a bare statement and rely on Go's
// terminating-statement analysis (panic does not fall through) so the
// surrounding IIFE still type-checks.
func isNoReturnCallExpr(expr ast.Expr) bool {
	call, ok := expr.(*ast.CallExpr)
	if !ok {
		return false
	}
	if ident, ok := call.Fun.(*ast.Ident); ok {
		return ident.Name == "panic"
	}
	return false
}

// lowerMatchArmTailExpr classifies a tail expression in a match arm and
// returns the statement that should appear in its place along with the
// type that should feed common-result-type inference. (B8 — single source
// of truth for the value-vs-no-return decision that PR #209 / #214 / #260
// have each had to re-derive in slightly different ways.)
//
//   - For a no-return call (e.g. `panic("…")`), return a bare ExprStmt and
//     NilType. Wrapping in `return panic(…)` would produce invalid Go;
//     NilType keeps the arm out of common-type inference.
//   - For any other expression, return a `return <expr>` statement and
//     report the expression's inferred type.
func (t *galaASTTransformer) lowerMatchArmTailExpr(expr ast.Expr) (ast.Stmt, transpiler.Type) {
	if isNoReturnCallExpr(expr) {
		return &ast.ExprStmt{X: expr}, transpiler.NilType{}
	}
	return &ast.ReturnStmt{Results: []ast.Expr{expr}}, t.inferResultType(expr)
}

// containsBareReturn reports whether any statement in stmts (recursively) is a
// `return` with no results. It is used to detect "bare return" inside a branch
// of a match-as-value expression, which would generate invalid Go because the
// generated IIFE has a non-void return type.
//
// Only structural containers are traversed (blocks, if/else, the top-level
// case body). Constructs that establish their own return scope (func literals,
// nested IIFEs) are NOT traversed, since a bare return inside a nested lambda
// exits that lambda, not the enclosing match IIFE.
func containsBareReturn(stmts []ast.Stmt) bool {
	for _, s := range stmts {
		if stmtContainsBareReturn(s) {
			return true
		}
	}
	return false
}

func stmtContainsBareReturn(stmt ast.Stmt) bool {
	switch s := stmt.(type) {
	case *ast.ReturnStmt:
		return len(s.Results) == 0
	case *ast.BlockStmt:
		return containsBareReturn(s.List)
	case *ast.IfStmt:
		if s.Body != nil && containsBareReturn(s.Body.List) {
			return true
		}
		if s.Else != nil && stmtContainsBareReturn(s.Else) {
			return true
		}
		return false
	case *ast.LabeledStmt:
		return stmtContainsBareReturn(s.Stmt)
	}
	return false
}

// validateNoBareReturnsInValueMatch rejects a match expression whose result is
// used as a value (non-void) but whose case bodies contain a bare `return`.
// Such a construct would emit Go like:
//
//	func(obj T) string { ...; return /* no value */ }(subject)
//
// which fails to compile with "not enough return values". The user intent is
// ambiguous between "exit the outer function" and "exit the match IIFE", so we
// reject it and point at explicit rewrites in docs/errors/GALA-E0015.md.
//
// Called after case bodies have been transformed and the common result type
// has been inferred; no-ops when resultType is void/nil.
func (t *galaASTTransformer) validateNoBareReturnsInValueMatch(
	clauses []ast.Stmt,
	defaultBody []ast.Stmt,
	resultType transpiler.Type,
	startLine, startCol int,
) error {
	if resultType == nil || resultType.IsNil() {
		return nil
	}
	if resultType != nil && resultType.IsVoid() {
		return nil
	}
	offender := false
	for _, c := range clauses {
		if stmtContainsBareReturn(c) {
			offender = true
			break
		}
	}
	if !offender && containsBareReturn(defaultBody) {
		offender = true
	}
	if !offender {
		return nil
	}
	return galaerr.NewCodedSemanticError(
		galaerr.CodeBareReturnInValueMatch,
		startLine, startCol,
		"bare `return` inside a match branch whose result is used as a value",
		"the match is wrapped in a function that must return "+resultType.String()+
			"; restructure to early-exit before the match, or use combinators like .Recover / .GetOrElse. See docs/errors/GALA-E0015.md",
	)
}

// validateSealedVariantArity checks that each sealed-variant extractor pattern
// binds the same number of fields as the variant declares. Wildcards and
// binding names each count as one field. Patterns that do not target a known
// sealed variant are skipped. Returns nil if all patterns are well-formed.
func (t *galaASTTransformer) validateSealedVariantArity(matchedType transpiler.Type, patternTexts []string, ctx grammar.IExpressionContext) error {
	if matchedType == nil || matchedType.IsNil() {
		return nil
	}
	meta := t.getTypeMeta(matchedType.BaseName())
	if meta == nil || !meta.IsSealed || len(meta.SealedVariants) == 0 {
		return nil
	}
	variantByName := make(map[string]*transpiler.SealedVariant, len(meta.SealedVariants))
	for i := range meta.SealedVariants {
		v := &meta.SealedVariants[i]
		variantByName[v.Name] = v
	}
	for _, pat := range patternTexts {
		name := extractVariantName(pat)
		if name == "" {
			continue
		}
		variant, ok := variantByName[name]
		if !ok {
			continue
		}
		got, wellFormed := countTopLevelArgs(pat)
		if !wellFormed {
			continue
		}
		want := len(variant.FieldNames)
		if got != want {
			return galaerr.NewCodedSemanticError(
				galaerr.CodeVariantArityMismatch,
				ctx.GetStart().GetLine(),
				ctx.GetStart().GetColumn(),
				fmt.Sprintf("sealed variant %q pattern binds %d field(s) but declares %d",
					name, got, want),
				"use `_` for unused fields",
			)
		}
	}
	return nil
}

// countTopLevelArgs counts the number of comma-separated arguments inside the
// outermost parentheses of a pattern text (e.g., "Rect(w, h)" → 2, "Point()" → 0,
// "Wrap(Pair(a, b), c)" → 2). Returns (count, wellFormed). wellFormed is false
// if the pattern does not contain a balanced top-level `(...)`.
func countTopLevelArgs(patternText string) (int, bool) {
	open := strings.Index(patternText, "(")
	if open < 0 {
		return 0, false
	}
	depth := 0
	args := 0
	sawContent := false
	for i := open; i < len(patternText); i++ {
		c := patternText[i]
		switch c {
		case '(', '[', '{':
			if c == '(' && depth == 0 {
				depth++
				continue
			}
			depth++
		case ')', ']', '}':
			depth--
			if c == ')' && depth == 0 {
				if sawContent {
					args++
				}
				return args, true
			}
		case ',':
			if depth == 1 {
				args++
				sawContent = false
				continue
			}
		default:
			if depth == 1 && c != ' ' && c != '\t' {
				sawContent = true
			}
		}
	}
	return 0, false
}

// inferMatchedTypeFromCases attempts to infer the sealed parent type from case pattern names.
// When the match subject type cannot be inferred from the expression itself, we look at the
// case patterns (e.g., Some/None → Option, Success/Failure → Try, Left/Right → Either)
// and resolve the parent sealed type from companion object metadata.
func (t *galaASTTransformer) inferMatchedTypeFromCases(caseClauses []grammar.ICaseClauseContext) transpiler.Type {
	for _, cc := range caseClauses {
		ccCtx := cc.(*grammar.CaseClauseContext)
		patCtx := ccCtx.Pattern()
		if patCtx == nil {
			continue
		}
		patternText := patCtx.GetText()
		if isWildcard(patternText) || isBindingPattern(patternText) {
			continue
		}
		variantName := extractVariantName(patternText)
		if variantName == "" {
			continue
		}

		// Look up the variant in companion objects to find the parent sealed type
		companion := t.lookupCompanion(variantName)
		if companion == nil {
			continue
		}

		// Resolve the parent sealed type from companion metadata.
		// companion.TargetType may already include the package prefix (e.g., "std.Try"
		// from NamedType.BaseName()), so we must NOT blindly prepend companion.Package.
		meta := t.getTypeMeta(companion.TargetType)
		if meta == nil && companion.Package != "" {
			// Only add package prefix if TargetType doesn't already contain one
			if !strings.Contains(companion.TargetType, ".") {
				meta = t.getTypeMeta(companion.Package + "." + companion.TargetType)
			}
		}
		if meta == nil {
			// Last resort: strip any package prefix and try bare name
			base := companion.TargetType
			if idx := strings.LastIndex(base, "."); idx >= 0 {
				base = base[idx+1:]
			}
			meta = t.getTypeMeta(base)
		}
		if meta == nil || !meta.IsSealed {
			continue
		}

		// Build the type. For generic sealed types (Option[T], Try[T], Either[A,B]),
		// we use 'any' as the type parameter since we can't infer the concrete type
		// from the case patterns alone.
		var baseType transpiler.Type
		if companion.Package != "" {
			// Strip package prefix from TargetType if it already contains one
			// (e.g., "std.Try" -> "Try") to avoid double prefix "std.std.Try"
			typeName := companion.TargetType
			if strings.HasPrefix(typeName, companion.Package+".") {
				typeName = typeName[len(companion.Package)+1:]
			}
			baseType = transpiler.NamedType{Package: companion.Package, Name: typeName}
		} else {
			baseType = transpiler.BasicType{Name: companion.TargetType}
		}

		if len(meta.TypeParams) > 0 {
			params := make([]transpiler.Type, len(meta.TypeParams))
			for i := range meta.TypeParams {
				params[i] = transpiler.BasicType{Name: "any"}
			}
			return transpiler.GenericType{Base: baseType, Params: params}
		}
		return baseType
	}
	return nil
}

// lookupCompanion searches for a companion object by name, checking both
// fully-qualified and unqualified names across all registered companions.
func (t *galaASTTransformer) lookupCompanion(name string) *transpiler.CompanionObjectMetadata {
	// Try exact match first
	if c, ok := t.companionObjects[name]; ok {
		return c
	}
	// Try with std prefix
	stdName := registry.StdPackageName + "." + name
	if c, ok := t.companionObjects[stdName]; ok {
		return c
	}
	// Search all companion objects for a matching base name
	for key, c := range t.companionObjects {
		// Check if key ends with ".Name" (e.g., "std.Some" matches "Some")
		if idx := strings.LastIndex(key, "."); idx >= 0 {
			if key[idx+1:] == name {
				return c
			}
		}
	}
	return nil
}

// transformMatchClauses processes all case clauses and infers the common result type.
func (t *galaASTTransformer) transformMatchClauses(ctx grammar.IExpressionContext, paramName string, matchedType transpiler.Type) ([]ast.Stmt, []ast.Stmt, transpiler.Type, error) {
	var clauses []ast.Stmt
	var defaultBody []ast.Stmt
	foundDefault := false
	var resultTypes []transpiler.Type
	var casePatterns []string

	// Pre-scan: check if there's an explicit wildcard `_` case.
	hasExplicitWildcard := false
	for i := 3; i < ctx.GetChildCount()-1; i++ {
		ccCtx, ok := ctx.GetChild(i).(*grammar.CaseClauseContext)
		if ok && isWildcard(ccCtx.Pattern().GetText()) {
			hasExplicitWildcard = true
			break
		}
	}

	for i := 3; i < ctx.GetChildCount()-1; i++ {
		ccCtx, ok := ctx.GetChild(i).(*grammar.CaseClauseContext)
		if !ok {
			continue
		}

		patCtx := ccCtx.Pattern()
		patternText := patCtx.GetText()
		treatAsDefault := isWildcard(patternText) ||
			(!hasExplicitWildcard && isBindingPattern(patternText))

		if treatAsDefault {
			if foundDefault {
				return nil, nil, nil, galaerr.NewCodedSemanticError(
					galaerr.CodeMultipleDefaults,
					ccCtx.GetStart().GetLine(), ccCtx.GetStart().GetColumn(),
					"multiple default cases in match expression",
					"keep one default case; combine logic with guards or nested matches if you need sub-cases")
			}
			foundDefault = true

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
				// arm's value (when not void), so it is value-consumed.
				t.blockLastStmtIsValue = true
				b, err := t.transformBlock(ccCtx.GetBodyBlock().(*grammar.BlockContext))
				if err != nil {
					return nil, nil, nil, err
				}
				defaultBody = append(bindingStmts, b.List...)
				if len(b.List) > 0 {
					lastStmt := b.List[len(b.List)-1]
					if ret, ok := lastStmt.(*ast.ReturnStmt); ok && len(ret.Results) > 0 {
						resultTypes = append(resultTypes, t.inferResultType(ret.Results[0]))
						casePatterns = append(casePatterns, "case _")
					} else if exprStmt, ok := lastStmt.(*ast.ExprStmt); ok {
						// B8: classify and lower via the unified helper.
						stmt, typ := t.lowerMatchArmTailExpr(exprStmt.X)
						defaultBody[len(defaultBody)-1] = stmt
						resultTypes = append(resultTypes, typ)
						casePatterns = append(casePatterns, "case _")
					}
				}
			} else if ccCtx.GetBodyStmt() != nil {
				bodyStmts, bodyType, err := t.transformCaseBodyStmt(ccCtx.GetBodyStmt())
				if err != nil {
					return nil, nil, nil, err
				}
				defaultBody = append(bindingStmts, bodyStmts...)
				resultTypes = append(resultTypes, bodyType)
				casePatterns = append(casePatterns, "case _")
			}
			continue
		}

		clause, resultType, err := t.transformCaseClauseWithType(ccCtx, paramName, matchedType)
		if err != nil {
			return nil, nil, nil, err
		}
		clauses = append(clauses, clause)
		resultTypes = append(resultTypes, resultType)
		casePatterns = append(casePatterns, fmt.Sprintf("case %s", patternText))
	}

	// Always collect variant patterns for exhaustiveness check
	var variantPatterns []string
	for i := 3; i < ctx.GetChildCount()-1; i++ {
		ccCtx, ok := ctx.GetChild(i).(*grammar.CaseClauseContext)
		if !ok {
			continue
		}
		pat := ccCtx.Pattern().GetText()
		if !isDefaultPattern(pat) {
			variantPatterns = append(variantPatterns, pat)
		}
	}

	// B6: Validate arity of sealed-variant extractor patterns. A pattern like
	// `Rect(w, h)` against a variant `Rect(w, h, label)` silently truncates and
	// causes runtime confusion; reject it here.
	if arityErr := t.validateSealedVariantArity(matchedType, variantPatterns, ctx); arityErr != nil {
		return nil, nil, nil, arityErr
	}

	isSealed, isExhaustive, missing := t.isExhaustiveMatch(matchedType, variantPatterns)

	// A binding pattern (e.g., `case n =>`) is a catch-all even though it's
	// processed as a regular clause.
	hasDefault := foundDefault
	if !hasDefault {
		for i := 3; i < ctx.GetChildCount()-1; i++ {
			ccCtx, ok := ctx.GetChild(i).(*grammar.CaseClauseContext)
			if !ok {
				continue
			}
			if isBindingPattern(ccCtx.Pattern().GetText()) {
				hasDefault = true
				break
			}
		}
	}

	if !hasDefault {
		if isSealed && !isExhaustive {
			return nil, nil, nil, galaerr.NewCodedSemanticError(
				galaerr.CodeNonExhaustiveMatch,
				ctx.GetStart().GetLine(), ctx.GetStart().GetColumn(),
				fmt.Sprintf("non-exhaustive match: missing cases: %s", strings.Join(missing, ", ")),
				"add the missing variant cases, or add a `case _ => ...` default to cover them")
		} else if isSealed && isExhaustive {
			// Exhaustive sealed match — generate synthetic panic("unreachable") default
			defaultBody = []ast.Stmt{
				&ast.ExprStmt{X: &ast.CallExpr{
					Fun:  ast.NewIdent("panic"),
					Args: []ast.Expr{&ast.BasicLit{Kind: token.STRING, Value: `"unreachable"`}},
				}},
			}
		} else if !isSealed {
			return nil, nil, nil, galaerr.NewCodedSemanticError(
				galaerr.CodeMissingDefault,
				ctx.GetStart().GetLine(), ctx.GetStart().GetColumn(),
				"match expression must have a default case",
				"add `case _ => ...`")
		}
	}
	// When foundDefault && isSealed && isExhaustive: unreachable default is harmless, allow it

	var matchCtx antlr.ParserRuleContext
	if pc, ok := ctx.(antlr.ParserRuleContext); ok {
		matchCtx = pc
	}
	resultType, err := t.inferCommonResultType(resultTypes, casePatterns, matchCtx)
	if err != nil {
		return nil, nil, nil, err
	}

	// Reject bare `return` inside a value-producing match (the IIFE would need
	// to return a concrete type, but a bare return produces none). See GALA-E0015.
	startLine, startCol := ctx.GetStart().GetLine(), ctx.GetStart().GetColumn()
	if err := t.validateNoBareReturnsInValueMatch(clauses, defaultBody, resultType, startLine, startCol); err != nil {
		return nil, nil, nil, err
	}

	return clauses, defaultBody, resultType, nil
}

// buildMatchBody chains case clauses into an if-else chain with default body,
// and applies void stripping or return fixup based on result type.
func (t *galaASTTransformer) buildMatchBody(clauses []ast.Stmt, defaultBody []ast.Stmt, resultType transpiler.Type) []ast.Stmt {
	var rootIf ast.Stmt
	var currentIf *ast.IfStmt

	for _, clause := range clauses {
		if rootIf == nil {
			rootIf = clause
			currentIf = findLeafIf(clause)
		} else {
			if currentIf != nil {
				currentIf.Else = clause
				currentIf = findLeafIf(clause)
			}
		}
	}

	var body []ast.Stmt
	if rootIf != nil {
		if len(defaultBody) > 0 && currentIf != nil {
			currentIf.Else = &ast.BlockStmt{List: defaultBody}
		}
		body = []ast.Stmt{rootIf}
	} else {
		body = defaultBody
	}

	isVoid := resultType != nil && resultType.IsVoid()
	if isVoid {
		body = t.stripReturnStatements(body)
	} else if resultType != nil && !resultType.IsNil() && !resultType.IsAny() {
		t.fixupReturnStatements(body, resultType)
	}

	return body
}

// generateMatchIIFE wraps the match body in an immediately-invoked function expression.
func (t *galaASTTransformer) generateMatchIIFE(expr ast.Expr, paramName string, matchedType transpiler.Type, body []ast.Stmt, resultType transpiler.Type, matchLine, matchCol int) (ast.Expr, error) {
	paramType := t.typeToExpr(matchedType)
	if paramType == nil {
		return nil, galaerr.NewSemanticErrorAt(matchLine, matchCol, "cannot infer type of matched expression. Please add explicit type annotation")
	}

	var resultsField *ast.FieldList
	if resultType == nil || !resultType.IsVoid() {
		resultTypeExpr := t.typeToExpr(resultType)
		if resultTypeExpr == nil {
			return nil, galaerr.NewSemanticErrorAt(matchLine, matchCol, "cannot infer result type of match expression. Please ensure all branches return the same type")
		}
		resultsField = &ast.FieldList{
			List: []*ast.Field{{Type: resultTypeExpr}},
		}
	}

	return &ast.CallExpr{
		Fun: &ast.FuncLit{
			Type: &ast.FuncType{
				Params: &ast.FieldList{
					List: []*ast.Field{
						{
							Names: []*ast.Ident{ast.NewIdent(paramName)},
							Type:  paramType,
						},
					},
				},
				Results: resultsField,
			},
			Body: &ast.BlockStmt{List: body},
		},
		Args: []ast.Expr{expr},
	}, nil
}

// inferResultType infers the type of an expression used as a case clause result
func (t *galaASTTransformer) inferResultType(expr ast.Expr) transpiler.Type {
	// Check for void IIFE (from nested void match expressions)
	// A void IIFE is a CallExpr where Fun is a FuncLit with no return type
	if call, ok := expr.(*ast.CallExpr); ok {
		if funcLit, ok := call.Fun.(*ast.FuncLit); ok {
			if funcLit.Type.Results == nil {
				return transpiler.VoidType{}
			}
		}
	}

	// Check if this is a call to a known multi-return function (like fmt.Printf, fmt.Println)
	// These should be treated as void for match statement purposes
	if call, ok := expr.(*ast.CallExpr); ok {
		if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
			if pkgIdent, ok := sel.X.(*ast.Ident); ok {
				// Check specifically for known multi-return functions
				pkgName := pkgIdent.Name
				funcName := sel.Sel.Name
				if t.isKnownMultiReturnFunction(pkgName, funcName) {
					return transpiler.VoidType{}
				}
			}
		}
	}

	// Try manual type extraction first
	typ := t.getExprTypeNameManual(expr)
	if typ != nil && !typ.IsNil() {
		return typ
	}
	// Fall back to HM inference
	typ, _ = t.inferExprType(expr)
	if typ != nil && !typ.IsNil() {
		return typ
	}
	return transpiler.NilType{}
}

// isKnownMultiReturnFunction checks if a function is known to return multiple values.
// These functions are used for side effects and their return values shouldn't be used in match expressions.
func (t *galaASTTransformer) isKnownMultiReturnFunction(pkgName, funcName string) bool {
	// Resolve package alias
	resolvedPkg := pkgName
	if actual, ok := t.importManager.ResolveAlias(pkgName); ok {
		resolvedPkg = actual
	}

	// List of known functions that return multiple values (usually (int, error) or similar)
	switch resolvedPkg {
	case "fmt":
		switch funcName {
		case "Print", "Printf", "Println",
			"Fprint", "Fprintf", "Fprintln",
			"Scan", "Scanf", "Scanln",
			"Fscan", "Fscanf", "Fscanln",
			"Sscan", "Sscanf", "Sscanln":
			return true
		}
	case "log":
		switch funcName {
		case "Print", "Printf", "Println",
			"Fatal", "Fatalf", "Fatalln",
			"Panic", "Panicf", "Panicln":
			return true
		}
	case "io":
		switch funcName {
		case "Copy", "CopyN", "CopyBuffer",
			"ReadFull", "ReadAtLeast",
			"WriteString":
			return true
		}
	}

	return false
}

// inferCommonResultType checks that all result types are compatible and returns the common type.
// ctx is optional and used for position info in error messages.
func (t *galaASTTransformer) inferCommonResultType(types []transpiler.Type, patterns []string, ctx antlr.ParserRuleContext) (transpiler.Type, error) {
	if len(types) == 0 {
		line, col := t.lastLine, t.lastCol
		if ctx != nil && ctx.GetStart() != nil {
			line, col = ctx.GetStart().GetLine(), ctx.GetStart().GetColumn()
		}
		return nil, galaerr.NewSemanticErrorAt(line, col, "match expression has no case branches")
	}

	// Check if all branches are void (side-effect only, like fmt.Printf calls)
	allVoid := true
	for _, typ := range types {
		if !typ.IsVoid() {
			allVoid = false
			break
		}
	}
	if allVoid {
		return transpiler.VoidType{}, nil
	}

	// Find the first non-nil, non-type-parameter, non-void type as reference
	var refType transpiler.Type
	var refPattern string
	for i, typ := range types {
		if typ != nil && !typ.IsNil() {
			// Skip type parameters (like A, B, T, U) - they're not concrete types
			typeName := typ.String()
			if t.isTypeParameter(typeName) {
				continue
			}
			// Skip void types when looking for reference
			if typ.IsVoid() {
				continue
			}
			refType = typ
			refPattern = patterns[i]
			break
		}
	}

	if refType == nil {
		// Check if all non-void types are NilType (complete inference failure) vs type parameters
		hasTypeParam := false
		allNilOrVoid := true
		for _, typ := range types {
			if typ != nil && !typ.IsNil() {
				if !typ.IsVoid() {
					allNilOrVoid = false
					if t.isTypeParameter(typ.String()) {
						hasTypeParam = true
					}
				}
			}
		}

		if allNilOrVoid && !hasTypeParam {
			// Complete inference failure — no branch could be typed.
			// Fall back to the enclosing function/lambda's declared return type.
			// This handles cases where branches call methods from pure Go packages
			// whose return types aren't in the GALA type metadata.
			// Safety: if the fallback type is wrong, the Go compiler will catch it.
			if t.currentFuncReturnType != nil && !t.currentFuncReturnType.IsNil() {
				t.traceType(nil, t.currentFuncReturnType, "match-result-fallback-to-enclosing-return")
				return t.currentFuncReturnType, nil
			}
			// No enclosing concrete return type either — this is a dispatch-style
			// match used purely for side effects (all arms call void functions,
			// recurse, or are empty `{}` blocks, with none producing a typed value).
			// Treat the match as void: the IIFE has no return type and the result
			// is discarded. If the caller tried to use the match as a value, the
			// Go compiler will surface that error downstream.
			t.traceType(nil, transpiler.VoidType{}, "match-result-fallback-to-void-dispatch")
			return transpiler.VoidType{}, nil
		}
		// Type parameters or mixed type-param/nil: prefer the enclosing
		// function's return type when it matches one of the branch types.
		// This handles `func f[T any](...) T { return e match { case ... => fnReturningT(...) } }`,
		// where every branch yields the same type parameter T as the
		// declared return — emitting `func(...) any` for the IIFE breaks
		// the Go compile because `any` does not satisfy `T`.
		if t.currentFuncReturnType != nil && !t.currentFuncReturnType.IsNil() {
			enclosingName := t.currentFuncReturnType.String()
			for _, typ := range types {
				if typ != nil && !typ.IsNil() && typ.String() == enclosingName {
					t.traceType(nil, t.currentFuncReturnType, "match-result-fallback-to-enclosing-typeparam-return")
					return t.currentFuncReturnType, nil
				}
			}
		}
		// B3: when no enclosing return is available (or it doesn't match), but
		// every typed arm names the *same* type parameter AND that parameter
		// is in scope of the currently-transforming function, return it
		// directly. Without the in-scope guard the IIFE would be emitted
		// with a return type Go cannot resolve (the type param is bound at
		// the matched value's type, not at this function's signature) — so
		// we fall through to `any` for the out-of-scope case.
		var sharedTypeParam transpiler.Type
		for _, typ := range types {
			if typ == nil || typ.IsNil() {
				continue
			}
			if typ.IsVoid() {
				continue
			}
			name := typ.String()
			if !t.isTypeParameter(name) {
				sharedTypeParam = nil
				break
			}
			if sharedTypeParam == nil {
				sharedTypeParam = typ
			} else if sharedTypeParam.String() != name {
				sharedTypeParam = nil
				break
			}
		}
		// Direct map lookup, bypassing the single-uppercase-letter fallback in
		// isActiveTypeParam — that fallback fires when activeTypeParams is empty
		// (e.g. inside a non-generic function whose body matches on a generic
		// type), and would otherwise let us emit a type-param Go signature
		// for a function that doesn't declare it.
		if sharedTypeParam != nil && t.activeTypeParams[sharedTypeParam.String()] {
			t.traceType(nil, sharedTypeParam, "match-result-fallback-to-shared-type-param")
			return sharedTypeParam, nil
		}
		t.warnInference("match expression defaulting to 'any' return type (all branches are type parameters)")
		return transpiler.BasicType{Name: "any"}, nil
	}

	// Check all types are compatible with the reference type
	for i, typ := range types {
		if typ == nil {
			line, col := t.lastLine, t.lastCol
			if ctx != nil && ctx.GetStart() != nil {
				line, col = ctx.GetStart().GetLine(), ctx.GetStart().GetColumn()
			}
			return nil, galaerr.NewSemanticErrorAt(line, col, fmt.Sprintf("cannot infer result type for '%s'. Please add explicit type annotation", patterns[i]))
		}
		// VoidType is compatible with any type (for mixed match where some branches are void)
		if typ.IsVoid() {
			continue
		}
		// NilType means inference failed for this branch — treat as compatible
		// when at least one other branch has a concrete type. The Go compiler
		// will catch any real type mismatch downstream.
		if typ.IsNil() {
			continue
		}
		// Note: NilType (from nil literal) is allowed and checked in typesCompatible
		if !t.typesCompatible(refType, typ) {
			msg := fmt.Sprintf("type mismatch in match expression: '%s' returns '%s' but '%s' returns '%s'. All branches must return the same type",
				refPattern, refType.String(), patterns[i], typ.String())
			if ctx != nil {
				return nil, t.semanticErrorAt(ctx, msg)
			}
			return nil, galaerr.NewSemanticErrorAt(t.lastLine, t.lastCol, msg)
		}
	}

	return refType, nil
}

// typesCompatible checks if two types are compatible (same type, both any, or type parameter with any)
func (t *galaASTTransformer) typesCompatible(t1, t2 transpiler.Type) bool {
	if t1 == nil || t2 == nil {
		return false
	}

	// NilType (from nil literal) is compatible with any type
	if t1.IsNil() || t2.IsNil() {
		return true
	}

	// Types are compatible if they have the same string representation
	if t1.String() == t2.String() {
		return true
	}

	// Check qualified vs unqualified equivalence: "Response" should match "server.Response"
	// when server is dot-imported, is the current package, or is a known imported package.
	// Type alias resolution can produce qualified names from any of these sources.
	s1, s2 := t1.String(), t2.String()
	if strings.Contains(s2, ".") && !strings.Contains(s1, ".") {
		// s2 is qualified (pkg.Type), s1 is bare (Type)
		dotIdx := strings.Index(s2, ".")
		pkg := s2[:dotIdx]
		bareName := s2[dotIdx+1:]
		if bareName == s1 {
			if t.importManager.IsDotImported(pkg) || pkg == t.packageName || t.importManager.IsPackage(pkg) {
				return true
			}
		}
	}
	if strings.Contains(s1, ".") && !strings.Contains(s2, ".") {
		// s1 is qualified (pkg.Type), s2 is bare (Type)
		dotIdx := strings.Index(s1, ".")
		pkg := s1[:dotIdx]
		bareName := s1[dotIdx+1:]
		if bareName == s2 {
			if t.importManager.IsDotImported(pkg) || pkg == t.packageName || t.importManager.IsPackage(pkg) {
				return true
			}
		}
	}

	// any is compatible with everything
	if t1.IsAny() || t2.IsAny() {
		return true
	}

	// Type parameters (like T, U, std.T, std.U) are compatible with any
	if t.isTypeParameter(t1.String()) || t.isTypeParameter(t2.String()) {
		return true
	}

	// Check generic types with same base but different parameters
	// e.g., Option[T] is compatible with Option[any] if T is a type parameter
	gen1, ok1 := t1.(transpiler.GenericType)
	gen2, ok2 := t2.(transpiler.GenericType)
	if ok1 && ok2 {
		// Same base type? Use typesCompatible for base to handle qualified vs unqualified names.
		basesMatch := gen1.Base.String() == gen2.Base.String() || t.typesCompatible(gen1.Base, gen2.Base)
		if basesMatch && len(gen1.Params) == len(gen2.Params) {
			allParamsCompatible := true
			for i := range gen1.Params {
				if !t.typesCompatible(gen1.Params[i], gen2.Params[i]) {
					allParamsCompatible = false
					break
				}
			}
			if allParamsCompatible {
				return true
			}
		}
	}

	return false
}

// isTypeParameter checks if a type name represents a type parameter (like T, U, std.T).
// Delegates to isActiveTypeParam for consistent type parameter detection.
func (t *galaASTTransformer) isTypeParameter(typeName string) bool {
	return t.isActiveTypeParam(typeName)
}

// typeHasUnresolvedParams checks if a type contains unresolved type parameters (like T, U, A, B).
// Delegates to hasTypeParams for consistent type parameter detection.
func (t *galaASTTransformer) typeHasUnresolvedParams(typ transpiler.Type) bool {
	return t.hasTypeParams(typ)
}

// isSimpleIdentifier checks if a string is a simple identifier (not underscore, not complex)
func (t *galaASTTransformer) isSimpleIdentifier(s string) bool {
	if s == "_" || s == "" {
		return false
	}
	// Simple identifiers start with a letter and contain only letters, digits, or underscores
	for i, c := range s {
		if i == 0 {
			if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c == '_') {
				return false
			}
		} else {
			if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_') {
				return false
			}
		}
	}
	// Exclude patterns that contain parentheses, brackets, or colons (complex patterns)
	for _, c := range s {
		if c == '(' || c == ')' || c == '[' || c == ']' || c == ':' {
			return false
		}
	}
	return true
}

// extractUserPatternVarNames walks pattern bindings AST and collects user-defined variable names.
// It skips internal temp vars (_tmp_* prefix) and blank identifiers (_).
func extractUserPatternVarNames(bindings []ast.Stmt) []string {
	var names []string
	for _, stmt := range bindings {
		extractUserVarsFromStmt(stmt, &names)
	}
	return names
}

func extractUserVarsFromStmt(stmt ast.Stmt, names *[]string) {
	switch s := stmt.(type) {
	case *ast.AssignStmt:
		if s.Tok == token.DEFINE {
			for _, lhs := range s.Lhs {
				if ident, ok := lhs.(*ast.Ident); ok {
					name := ident.Name
					if name != "_" && !strings.HasPrefix(name, "_tmp_") {
						*names = append(*names, name)
					}
				}
			}
		}
	case *ast.BlockStmt:
		for _, inner := range s.List {
			extractUserVarsFromStmt(inner, names)
		}
	case *ast.IfStmt:
		// Walk the body (guarded assignments may define vars inside if blocks)
		if s.Body != nil {
			for _, inner := range s.Body.List {
				extractUserVarsFromStmt(inner, names)
			}
		}
	}
}

// collectReferencedIdents walks Go AST nodes and collects all referenced identifier names.
func collectReferencedIdents(nodes []ast.Node) map[string]bool {
	refs := make(map[string]bool)
	for _, node := range nodes {
		if node == nil {
			continue
		}
		ast.Inspect(node, func(n ast.Node) bool {
			if ident, ok := n.(*ast.Ident); ok {
				refs[ident.Name] = true
			}
			return true
		})
	}
	return refs
}

// transformCaseClauseWithType transforms a case clause and returns its result type
func (t *galaASTTransformer) transformCaseClauseWithType(ctx *grammar.CaseClauseContext, paramName string, matchedType transpiler.Type) (ast.Stmt, transpiler.Type, error) {
	t.pushScope()
	defer t.popScope()

	patCtx := ctx.Pattern()
	cond, bindings, err := t.transformPatternWithType(patCtx, ast.NewIdent(paramName), matchedType)
	if err != nil {
		return nil, nil, err
	}

	// Transform guard expression separately so we can check variable references in it
	var guardExpr ast.Expr
	if ctx.GetGuard() != nil {
		guardExpr, err = t.transformExpression(ctx.GetGuard())
		if err != nil {
			return nil, nil, err
		}
		cond = &ast.BinaryExpr{
			X:  cond,
			Op: token.LAND,
			Y:  guardExpr,
		}
	}

	var body []ast.Stmt
	var resultType transpiler.Type

	if ctx.GetBodyBlock() != nil {
		// The case body's block last expression becomes the arm's value, so
		// it is value-consumed (not statement-position).
		t.blockLastStmtIsValue = true
		b, err := t.transformBlock(ctx.GetBodyBlock().(*grammar.BlockContext))
		if err != nil {
			return nil, nil, err
		}
		body = b.List
		// In GALA, a block used as an expression returns its last expression.
		// Convert the last expression statement to a return statement.
		if len(body) > 0 {
			lastStmt := body[len(body)-1]
			if lastStmt != nil {
				if exprStmt, ok := lastStmt.(*ast.ExprStmt); ok {
					// B8: classify and lower via the unified helper.
					stmt, typ := t.lowerMatchArmTailExpr(exprStmt.X)
					body[len(body)-1] = stmt
					resultType = typ
				} else if ret, ok := lastStmt.(*ast.ReturnStmt); ok && len(ret.Results) > 0 {
					resultType = t.inferResultType(ret.Results[0])
				}
			}
		}
		// If resultType is still nil, this is a void (side-effect) branch:
		// either empty block `{}` or last statement is an assignment/loop
		if resultType == nil {
			resultType = transpiler.VoidType{}
		}
	} else if ctx.GetBodyStmt() != nil {
		bodyStmts, bodyType, err := t.transformCaseBodyStmt(ctx.GetBodyStmt())
		if err != nil {
			return nil, nil, err
		}
		body = bodyStmts
		resultType = bodyType
	}

	// Check for unused pattern variables: user vars that appear in bindings but
	// are not referenced in the body or guard expression.
	userVars := extractUserPatternVarNames(bindings)
	if len(userVars) > 0 {
		// Collect identifiers referenced in body and guard
		var nodesToCheck []ast.Node
		for _, s := range body {
			nodesToCheck = append(nodesToCheck, s)
		}
		if guardExpr != nil {
			nodesToCheck = append(nodesToCheck, guardExpr)
		}
		refs := collectReferencedIdents(nodesToCheck)

		for _, varName := range userVars {
			if !refs[varName] {
				line := ctx.GetStart().GetLine()
				col := ctx.GetStart().GetColumn()
				return nil, nil, galaerr.NewSemanticErrorAt(line, col,
					fmt.Sprintf("unused variable '%s' in match branch — use '_' to discard this value", varName))
			}
		}
	}

	bodyBlock := &ast.BlockStmt{List: body}

	ifStmt := &ast.IfStmt{
		Cond: cond,
		Body: bodyBlock,
	}

	if len(bindings) > 0 {
		return &ast.BlockStmt{
			List: append(bindings, ifStmt),
		}, resultType, nil
	}

	return ifStmt, resultType, nil
}

// transformCaseBodyStmt transforms a simpleStatement case body.
// Returns (stmts, resultType, error) where stmts are the Go statements for the body,
// and resultType is the type (VoidType for assignments/incDec, or the expression type).
func (t *galaASTTransformer) transformCaseBodyStmt(ctx grammar.ISimpleStatementContext) ([]ast.Stmt, transpiler.Type, error) {
	// If the body is an expression, wrap it in a return (value-returning case)
	if exprCtx := ctx.Expression(); exprCtx != nil {
		expr, err := t.transformExpression(exprCtx)
		if err != nil {
			return nil, nil, err
		}
		// B8: route through the unified value-vs-no-return classifier so
		// every match-arm-tail decision agrees.
		stmt, typ := t.lowerMatchArmTailExpr(expr)
		return []ast.Stmt{stmt}, typ, nil
	}
	// Otherwise it's a side-effect statement (assignment, incDec, shortVarDecl)
	stmt, err := t.transformSimpleStatement(ctx)
	if err != nil {
		return nil, nil, err
	}
	return []ast.Stmt{stmt}, transpiler.VoidType{}, nil
}

// Pattern transformation functions moved to patterns.go
