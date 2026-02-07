package transformer

import (
	"fmt"
	"go/ast"
	"go/token"
	"strings"

	"martianoff/gala/galaerr"
	"martianoff/gala/internal/parser/grammar"
	"martianoff/gala/internal/transpiler"
)

func (t *galaASTTransformer) transformMatchExpression(ctx grammar.IExpressionContext) (ast.Expr, error) {
	expr, paramName, matchedType, err := t.parseMatchSubject(ctx)
	if err != nil {
		return nil, err
	}

	t.pushScope()
	defer t.popScope()
	t.addVar(paramName, matchedType)

	clauses, defaultBody, resultType, err := t.transformMatchClauses(ctx, paramName, matchedType)
	if err != nil {
		return nil, err
	}

	t.needsStdImport = true
	body := t.buildMatchBody(clauses, defaultBody, resultType)

	return t.generateMatchIIFE(expr, paramName, matchedType, body, resultType)
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
		return nil, "", nil, galaerr.NewSemanticError("cannot infer type of matched expression. Please add explicit type annotation to the variable being matched")
	}

	return expr, paramName, matchedType, nil
}

// extractVariantName extracts the variant/constructor name from a case pattern text.
// E.g. "Circle(r)" → "Circle", "Point()" → "Point"
func extractVariantName(patternText string) string {
	idx := strings.Index(patternText, "(")
	if idx <= 0 {
		return ""
	}
	name := patternText[:idx]
	if len(name) == 0 || name[0] < 'A' || name[0] > 'Z' {
		return ""
	}
	return name
}

// isSealedExhaustive checks if a set of case patterns exhaustively covers all variants
// of a sealed type. Returns (isExhaustive, missingVariants).
func (t *galaASTTransformer) isSealedExhaustive(matchedType transpiler.Type, patternTexts []string) (bool, []string) {
	baseName := matchedType.BaseName()
	meta := t.getTypeMeta(baseName)
	if meta == nil || !meta.IsSealed || len(meta.SealedVariants) == 0 {
		return false, nil
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

	return len(missing) == 0, missing
}

// transformMatchClauses processes all case clauses and infers the common result type.
func (t *galaASTTransformer) transformMatchClauses(ctx grammar.IExpressionContext, paramName string, matchedType transpiler.Type) ([]ast.Stmt, []ast.Stmt, transpiler.Type, error) {
	var clauses []ast.Stmt
	var defaultBody []ast.Stmt
	foundDefault := false
	var resultTypes []transpiler.Type
	var casePatterns []string

	for i := 3; i < ctx.GetChildCount()-1; i++ {
		ccCtx, ok := ctx.GetChild(i).(*grammar.CaseClauseContext)
		if !ok {
			continue
		}

		patCtx := ccCtx.Pattern()
		patternText := patCtx.GetText()
		if patternText == "_" {
			if foundDefault {
				return nil, nil, nil, galaerr.NewSemanticError("multiple default cases in match expression")
			}
			foundDefault = true

			if ccCtx.GetBodyBlock() != nil {
				b, err := t.transformBlock(ccCtx.GetBodyBlock().(*grammar.BlockContext))
				if err != nil {
					return nil, nil, nil, err
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
					return nil, nil, nil, err
				}
				defaultBody = []ast.Stmt{&ast.ReturnStmt{Results: []ast.Expr{bodyExpr}}}
				resultTypes = append(resultTypes, t.inferResultType(bodyExpr))
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

	if !foundDefault {
		// Collect variant names from non-default patterns for exhaustiveness check
		var variantPatterns []string
		for i := 3; i < ctx.GetChildCount()-1; i++ {
			ccCtx, ok := ctx.GetChild(i).(*grammar.CaseClauseContext)
			if !ok {
				continue
			}
			pat := ccCtx.Pattern().GetText()
			if pat != "_" {
				variantPatterns = append(variantPatterns, pat)
			}
		}

		isExhaustive, missing := t.isSealedExhaustive(matchedType, variantPatterns)
		if !isExhaustive {
			if len(missing) > 0 {
				return nil, nil, nil, galaerr.NewSemanticError(
					fmt.Sprintf("non-exhaustive match on sealed type: missing variants: %s", strings.Join(missing, ", ")))
			}
			return nil, nil, nil, galaerr.NewSemanticError("match expression must have a default case (case _ => ...)")
		}
		// Exhaustive sealed match — generate synthetic panic("unreachable") default
		defaultBody = []ast.Stmt{
			&ast.ExprStmt{X: &ast.CallExpr{
				Fun:  ast.NewIdent("panic"),
				Args: []ast.Expr{&ast.BasicLit{Kind: token.STRING, Value: `"unreachable"`}},
			}},
		}
	}

	resultType, err := t.inferCommonResultType(resultTypes, casePatterns)
	if err != nil {
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

	_, isVoid := resultType.(transpiler.VoidType)
	if isVoid {
		body = t.stripReturnStatements(body)
	} else if resultType != nil && !resultType.IsNil() && !resultType.IsAny() {
		t.fixupReturnStatements(body, resultType)
	}

	return body
}

// generateMatchIIFE wraps the match body in an immediately-invoked function expression.
func (t *galaASTTransformer) generateMatchIIFE(expr ast.Expr, paramName string, matchedType transpiler.Type, body []ast.Stmt, resultType transpiler.Type) (ast.Expr, error) {
	paramType := t.typeToExpr(matchedType)
	if paramType == nil {
		return nil, galaerr.NewSemanticError("cannot infer type of matched expression. Please add explicit type annotation")
	}

	var resultsField *ast.FieldList
	if _, isVoid := resultType.(transpiler.VoidType); !isVoid {
		resultTypeExpr := t.typeToExpr(resultType)
		if resultTypeExpr == nil {
			return nil, galaerr.NewSemanticError("cannot infer result type of match expression. Please ensure all branches return the same type")
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

// inferCommonResultType checks that all result types are compatible and returns the common type
func (t *galaASTTransformer) inferCommonResultType(types []transpiler.Type, patterns []string) (transpiler.Type, error) {
	if len(types) == 0 {
		return nil, galaerr.NewSemanticError("match expression has no case branches")
	}

	// Check if all branches are void (side-effect only, like fmt.Printf calls)
	allVoid := true
	for _, typ := range types {
		if _, isVoid := typ.(transpiler.VoidType); !isVoid {
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
			if _, isVoid := typ.(transpiler.VoidType); isVoid {
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
				if _, isVoid := typ.(transpiler.VoidType); !isVoid {
					allNilOrVoid = false
					if t.isTypeParameter(typ.String()) {
						hasTypeParam = true
					}
				}
			}
		}

		if allNilOrVoid && !hasTypeParam {
			// Complete inference failure — no branch could be typed
			return nil, galaerr.NewSemanticError("cannot infer result type of match expression: no branch returns a concrete type. Please add explicit type annotation")
		}
		// Type parameters or mixed type-param/nil: use 'any' as the Go type erasure
		return transpiler.BasicType{Name: "any"}, nil
	}

	// Check all types are compatible with the reference type
	for i, typ := range types {
		if typ == nil {
			return nil, galaerr.NewSemanticError(fmt.Sprintf("cannot infer result type for '%s'. Please add explicit type annotation", patterns[i]))
		}
		// VoidType is compatible with any type (for mixed match where some branches are void)
		if _, isVoid := typ.(transpiler.VoidType); isVoid {
			continue
		}
		// Note: NilType (from nil literal) is allowed and checked in typesCompatible
		if !t.typesCompatible(refType, typ) {
			return nil, galaerr.NewSemanticError(fmt.Sprintf("type mismatch in match expression: '%s' returns '%s' but '%s' returns '%s'. All branches must return the same type",
				refPattern, refType.String(), patterns[i], typ.String()))
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
		// Same base type?
		if gen1.Base.String() == gen2.Base.String() && len(gen1.Params) == len(gen2.Params) {
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

// isTypeParameter checks if a type name represents a type parameter (like T, U, std.T)
func (t *galaASTTransformer) isTypeParameter(typeName string) bool {
	// Remove std. prefix if present
	name := typeName
	if len(name) > 4 && name[:4] == "std." {
		name = name[4:]
	}

	// Type parameters are typically single uppercase letters
	if len(name) == 1 && name[0] >= 'A' && name[0] <= 'Z' {
		return true
	}

	return false
}

// typeHasUnresolvedParams checks if a type contains unresolved type parameters (like T, U, A, B)
func (t *galaASTTransformer) typeHasUnresolvedParams(typ transpiler.Type) bool {
	if typ == nil || typ.IsNil() {
		return false
	}

	switch ty := typ.(type) {
	case transpiler.BasicType:
		return t.isTypeParameter(ty.Name)
	case transpiler.NamedType:
		return t.isTypeParameter(ty.Name)
	case transpiler.GenericType:
		// Check if base type is a type parameter
		if t.typeHasUnresolvedParams(ty.Base) {
			return true
		}
		// Check all type parameters
		for _, param := range ty.Params {
			if t.typeHasUnresolvedParams(param) {
				return true
			}
		}
		return false
	case transpiler.FuncType:
		// Check parameter and return types
		for _, param := range ty.Params {
			if t.typeHasUnresolvedParams(param) {
				return true
			}
		}
		for _, result := range ty.Results {
			if t.typeHasUnresolvedParams(result) {
				return true
			}
		}
		return false
	default:
		return false
	}
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

// transformCaseClauseWithType transforms a case clause and returns its result type
func (t *galaASTTransformer) transformCaseClauseWithType(ctx *grammar.CaseClauseContext, paramName string, matchedType transpiler.Type) (ast.Stmt, transpiler.Type, error) {
	t.pushScope()
	defer t.popScope()

	patCtx := ctx.Pattern()
	cond, bindings, err := t.transformPatternWithType(patCtx, ast.NewIdent(paramName), matchedType)
	if err != nil {
		return nil, nil, err
	}

	if ctx.GetGuard() != nil {
		guard, err := t.transformExpression(ctx.GetGuard())
		if err != nil {
			return nil, nil, err
		}
		cond = &ast.BinaryExpr{
			X:  cond,
			Op: token.LAND,
			Y:  guard,
		}
	}

	var body []ast.Stmt
	var resultType transpiler.Type

	if ctx.GetBodyBlock() != nil {
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
					body[len(body)-1] = &ast.ReturnStmt{Results: []ast.Expr{exprStmt.X}}
					resultType = t.inferResultType(exprStmt.X)
				} else if ret, ok := lastStmt.(*ast.ReturnStmt); ok && len(ret.Results) > 0 {
					resultType = t.inferResultType(ret.Results[0])
				}
			}
		}
	} else if ctx.GetBody() != nil {
		expr, err := t.transformExpression(ctx.GetBody())
		if err != nil {
			return nil, nil, err
		}
		body = []ast.Stmt{&ast.ReturnStmt{Results: []ast.Expr{expr}}}
		resultType = t.inferResultType(expr)
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

// Pattern transformation functions moved to patterns.go
