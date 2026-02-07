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
	// Check for match expression
	if ctx.GetChildCount() > 1 {
		for i := 0; i < ctx.GetChildCount(); i++ {
			if ctx.GetChild(i).(antlr.ParseTree).GetText() == "match" {
				return t.transformPostfixMatchExpression(ctx)
			}
		}
	}

	// Get the primary expression
	primaryExpr := ctx.PrimaryExpr()
	if primaryExpr == nil {
		return nil, galaerr.NewSemanticError("postfixExpr must have primaryExpr")
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

	return nil, galaerr.NewSemanticError("unknown postfix suffix type")
}

// resolveFieldAccess handles member access with automatic Immutable/ConstPtr unwrapping.
func (t *galaASTTransformer) resolveFieldAccess(base ast.Expr, selName string) (ast.Expr, error) {
	xType := t.getExprTypeName(base)
	isImmutable := t.isImmutableType(xType)

	// Don't unwrap if we're accessing Immutable's own fields/methods
	if !isImmutable || (selName != "Get" && selName != "value") {
		base = t.unwrapImmutable(base)
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
	baseTypeName := xTypeName
	if idx := strings.Index(xTypeName, "["); idx != -1 {
		baseTypeName = xTypeName[:idx]
	}
	baseTypeName = strings.TrimPrefix(baseTypeName, "*")

	// Check structFields (current package types)
	resolvedTypeName := t.resolveStructTypeName(baseTypeName)
	if fields, ok := t.structFields[resolvedTypeName]; ok {
		for i, f := range fields {
			if f == selName {
				return t.structImmutFields[resolvedTypeName][i]
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

	return false
}

// resolveIndexAccess handles index/subscript expressions with Immutable unwrapping.
func (t *galaASTTransformer) resolveIndexAccess(base ast.Expr, suffix *grammar.PostfixSuffixContext) (ast.Expr, error) {
	exprList := suffix.ExpressionList()
	if exprList == nil {
		return nil, galaerr.NewSemanticError("index expression requires expression list")
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

	return nil, galaerr.NewSemanticError("primaryExpr must have primary, lambda, if expression, or partial function")
}

// transformPostfixMatchExpression handles match expressions with the new grammar
func (t *galaASTTransformer) transformPostfixMatchExpression(ctx *grammar.PostfixExprContext) (ast.Expr, error) {
	// Get the primary expression being matched
	primaryExpr := ctx.PrimaryExpr()
	if primaryExpr == nil {
		return nil, galaerr.NewSemanticError("match expression must have subject")
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
	return t.buildMatchExpressionFromClauses(subject, "obj", caseClauses)
}

// buildMatchExpressionFromClauses builds a match expression from the subject and case clauses
func (t *galaASTTransformer) buildMatchExpressionFromClauses(subject ast.Expr, paramName string, caseClauses []grammar.ICaseClauseContext) (ast.Expr, error) {
	// Get the type of the matched expression
	matchedType := t.getExprTypeNameManual(subject)
	if matchedType == nil || matchedType.IsNil() {
		matchedType, _ = t.inferExprType(subject)
	}
	if matchedType == nil || matchedType.IsNil() {
		return nil, galaerr.NewSemanticError("cannot infer type of matched expression")
	}

	// Note: We intentionally do NOT replace types with unresolved type parameters (like Box[T])
	// with 'any'. Keeping the original parametric type allows correct extractor type inference
	// and valid Go code generation when inside a generic function where type parameters are in scope.

	t.pushScope()
	defer t.popScope()
	t.addVar(paramName, matchedType)

	var clauses []ast.Stmt
	var defaultBody []ast.Stmt
	foundDefault := false
	var resultTypes []transpiler.Type
	var casePatterns []string

	for _, cc := range caseClauses {
		ccCtx := cc.(*grammar.CaseClauseContext)

		patCtx := ccCtx.Pattern()
		patternText := patCtx.GetText()
		if isWildcard(patternText) {
			if foundDefault {
				return nil, galaerr.NewSemanticError("multiple default cases in match expression")
			}
			foundDefault = true

			if ccCtx.GetBodyBlock() != nil {
				b, err := t.transformBlock(ccCtx.GetBodyBlock().(*grammar.BlockContext))
				if err != nil {
					return nil, err
				}
				defaultBody = b.List
				if len(b.List) > 0 {
					if ret, ok := b.List[len(b.List)-1].(*ast.ReturnStmt); ok && len(ret.Results) > 0 {
						resultTypes = append(resultTypes, t.inferResultType(ret.Results[0]))
						casePatterns = append(casePatterns, "case _")
					}
				}
			} else if ccCtx.GetBody() != nil {
				bodyExpr, err := t.transformExpression(ccCtx.GetBody())
				if err != nil {
					return nil, err
				}
				defaultBody = []ast.Stmt{&ast.ReturnStmt{Results: []ast.Expr{bodyExpr}}}
				resultTypes = append(resultTypes, t.inferResultType(bodyExpr))
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
	resultType, err := t.inferCommonResultType(resultTypes, casePatterns)
	if err != nil {
		return nil, err
	}

	// Note: We keep result types with unresolved type parameters because they are valid Go
	// when inside a generic function where the type parameters are in scope.

	if len(clauses) == 0 && len(defaultBody) == 0 {
		return nil, galaerr.NewSemanticError("match expression must have at least one case")
	}

	// Always collect variant patterns for exhaustiveness check
	{
		var variantPatterns []string
		for _, cc := range caseClauses {
			pat := cc.(*grammar.CaseClauseContext).Pattern().GetText()
			if !isWildcard(pat) {
				variantPatterns = append(variantPatterns, pat)
			}
		}

		isSealed, isExhaustive, missing := t.isSealedExhaustive(matchedType, variantPatterns)

		if !foundDefault {
			if isSealed && !isExhaustive {
				return nil, galaerr.NewSemanticError(
					fmt.Sprintf("non-exhaustive match on sealed type: missing variants: %s", strings.Join(missing, ", ")))
			} else if isSealed && isExhaustive {
				// Exhaustive sealed match — generate synthetic panic("unreachable") default
				defaultBody = []ast.Stmt{
					&ast.ExprStmt{X: &ast.CallExpr{
						Fun:  ast.NewIdent("panic"),
						Args: []ast.Expr{&ast.BasicLit{Kind: token.STRING, Value: `"unreachable"`}},
					}},
				}
			} else if !isSealed {
				return nil, galaerr.NewSemanticError("match expression must have a default case (case _ => ...)")
			}
		}
		// When foundDefault && isSealed && isExhaustive: unreachable default is harmless, allow it
		_ = isSealed
	}

	var stmts []ast.Stmt
	for _, c := range clauses {
		stmts = append(stmts, c)
	}
	stmts = append(stmts, defaultBody...)

	// Check if result type is void (for side-effect only match statements)
	_, isVoid := resultType.(transpiler.VoidType)
	if isVoid {
		// Strip return statements for void match - convert returns to expression statements
		stmts = t.stripReturnStatements(stmts)
	}

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

func (t *galaASTTransformer) transformTupleLiteral(exprs []ast.Expr) (ast.Expr, error) {
	n := len(exprs)
	if n < 2 || n > 10 {
		return nil, galaerr.NewSemanticError(fmt.Sprintf("tuple literals must have 2-10 elements, got %d", n))
	}

	// Determine tuple type name based on arity
	var typeName string
	switch n {
	case 2:
		typeName = transpiler.TypeTuple
	case 3:
		typeName = transpiler.TypeTuple3
	case 4:
		typeName = transpiler.TypeTuple4
	case 5:
		typeName = transpiler.TypeTuple5
	case 6:
		typeName = transpiler.TypeTuple6
	case 7:
		typeName = transpiler.TypeTuple7
	case 8:
		typeName = transpiler.TypeTuple8
	case 9:
		typeName = transpiler.TypeTuple9
	case 10:
		typeName = transpiler.TypeTuple10
	}

	// Infer type parameters from expression types
	var typeParams []ast.Expr
	for _, expr := range exprs {
		exprType := t.getExprTypeName(expr)
		if exprType.IsNil() || exprType.IsAny() {
			typeParams = append(typeParams, ast.NewIdent("any"))
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
