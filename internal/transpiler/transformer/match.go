package transformer

import (
	"fmt"
	"go/ast"
	"go/token"
	"martianoff/gala/galaerr"
	"martianoff/gala/internal/parser/grammar"
	"martianoff/gala/internal/transpiler"
)

func (t *galaASTTransformer) transformMatchExpression(ctx grammar.IExpressionContext) (ast.Expr, error) {
	// expression 'match' '{' caseClause+ '}'
	// Use children because it's not a distinct context type
	exprCtx := ctx.GetChild(0).(grammar.IExpressionContext)
	expr, err := t.transformExpression(exprCtx)
	if err != nil {
		return nil, err
	}

	paramName := "obj"
	if primary := t.getPrimaryFromExpression(exprCtx); primary != nil {
		if primary.Identifier() != nil {
			paramName = primary.Identifier().GetText()
		}
	}

	// Get the type of the matched expression
	// Try multiple approaches to get the concrete instantiated type
	var matchedType transpiler.Type

	// First try manual type extraction which handles .Get() calls specially
	matchedType = t.getExprTypeNameManual(expr)

	// If that didn't work, try HM inference
	if matchedType == nil || matchedType.IsNil() {
		matchedType, _ = t.inferExprType(expr)
	}

	// Check if type inference failed - require explicit type annotation
	if matchedType == nil || matchedType.IsNil() {
		return nil, galaerr.NewSemanticError("cannot infer type of matched expression. Please add explicit type annotation to the variable being matched")
	}

	// If the matched type contains unresolved type parameters (like T, U),
	// fall back to 'any' to avoid generating invalid Go code
	if t.typeHasUnresolvedParams(matchedType) {
		matchedType = transpiler.BasicType{Name: "any"}
	}

	t.pushScope()
	defer t.popScope()
	t.addVar(paramName, matchedType)

	var clauses []ast.Stmt
	var defaultBody []ast.Stmt
	foundDefault := false

	// Collect result types from all case branches for type inference
	var resultTypes []transpiler.Type
	var casePatterns []string // For error messages

	// case clauses start from child 3 (0: expr, 1: match, 2: {, 3: case...)
	for i := 3; i < ctx.GetChildCount()-1; i++ {
		ccCtx, ok := ctx.GetChild(i).(*grammar.CaseClauseContext)
		if !ok {
			continue
		}

		// Check if it's a default case
		patCtx := ccCtx.Pattern()
		patternText := patCtx.GetText()
		if patternText == "_" {
			if foundDefault {
				return nil, galaerr.NewSemanticError("multiple default cases in match expression")
			}
			foundDefault = true

			// Transform the body of default case and infer its type
			if ccCtx.GetBodyBlock() != nil {
				b, err := t.transformBlock(ccCtx.GetBodyBlock().(*grammar.BlockContext))
				if err != nil {
					return nil, err
				}
				defaultBody = b.List
				// Infer type from last statement in block if it's a return
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
		clauses = append(clauses, clause)
		resultTypes = append(resultTypes, resultType)
		casePatterns = append(casePatterns, fmt.Sprintf("case %s", patternText))
	}

	if !foundDefault {
		return nil, galaerr.NewSemanticError("match expression must have a default case (case _ => ...)")
	}

	// Infer common result type from all branches
	resultType, err := t.inferCommonResultType(resultTypes, casePatterns)
	if err != nil {
		return nil, err
	}

	// If the result type contains unresolved type parameters (like T, U),
	// fall back to 'any' to avoid generating invalid Go code
	if t.typeHasUnresolvedParams(resultType) {
		resultType = transpiler.BasicType{Name: "any"}
	}

	t.needsStdImport = true
	// Transpile to IIFE: func(obj T) R { ... }(expr)
	var body []ast.Stmt
	// Add clauses as if-else chain
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

	if rootIf != nil {
		if len(defaultBody) > 0 {
			if currentIf != nil {
				currentIf.Else = &ast.BlockStmt{List: defaultBody}
			}
		}
		body = []ast.Stmt{rootIf}
	} else {
		body = defaultBody
	}

	// Check if result type is void (for side-effect only match statements)
	_, isVoid := resultType.(transpiler.VoidType)
	if isVoid {
		// Strip return statements for void match - convert returns to expression statements
		body = t.stripReturnStatements(body)
	} else {
		// Fix up return statements that return 'any' values when result type is concrete
		// This handles cases where pattern-bound variables have unknown types
		if resultType != nil && !resultType.IsNil() && resultType.String() != "any" {
			t.fixupReturnStatements(body, resultType)
		}
	}

	// Use concrete matched type for IIFE parameter
	paramType := t.typeToExpr(matchedType)
	if paramType == nil {
		return nil, galaerr.NewSemanticError("cannot infer type of matched expression. Please add explicit type annotation")
	}

	// Build IIFE with or without return type depending on void
	var resultsField *ast.FieldList
	if !isVoid {
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
			Body: &ast.BlockStmt{
				List: body,
			},
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
		// If we can't infer any concrete type, fall back to 'any' to allow the code to compile
		// This is a permissive approach - the Go compiler will catch actual type errors
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

	s1, s2 := t1.String(), t2.String()

	// Types are compatible if they have the same string representation
	if s1 == s2 {
		return true
	}

	// any is compatible with everything
	if s1 == "any" || s2 == "any" {
		return true
	}

	// Type parameters (like T, U, std.T, std.U) are compatible with any
	if t.isTypeParameter(s1) || t.isTypeParameter(s2) {
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
		// Infer type from last statement if it's a return
		if len(b.List) > 0 {
			if ret, ok := b.List[len(b.List)-1].(*ast.ReturnStmt); ok && len(ret.Results) > 0 {
				resultType = t.inferResultType(ret.Results[0])
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
