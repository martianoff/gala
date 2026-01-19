package transformer

import (
	"fmt"
	"github.com/antlr4-go/antlr/v4"
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
	if primary := exprCtx.Primary(); primary != nil {
		if p, ok := primary.(*grammar.PrimaryContext); ok && p.Identifier() != nil {
			paramName = p.Identifier().GetText()
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

	// Fix up return statements that return 'any' values when result type is concrete
	// This handles cases where pattern-bound variables have unknown types
	if resultType != nil && !resultType.IsNil() && resultType.String() != "any" {
		t.fixupReturnStatements(body, resultType)
	}

	// Use concrete matched type for IIFE parameter
	paramType := t.typeToExpr(matchedType)
	if paramType == nil {
		return nil, galaerr.NewSemanticError("cannot infer type of matched expression. Please add explicit type annotation")
	}

	// Use inferred result type
	resultTypeExpr := t.typeToExpr(resultType)
	if resultTypeExpr == nil {
		return nil, galaerr.NewSemanticError("cannot infer result type of match expression. Please ensure all branches return the same type")
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
				Results: &ast.FieldList{
					List: []*ast.Field{{Type: resultTypeExpr}},
				},
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

// inferCommonResultType checks that all result types are compatible and returns the common type
func (t *galaASTTransformer) inferCommonResultType(types []transpiler.Type, patterns []string) (transpiler.Type, error) {
	if len(types) == 0 {
		return nil, galaerr.NewSemanticError("match expression has no case branches")
	}

	// Find the first non-nil, non-type-parameter type as reference
	var refType transpiler.Type
	var refPattern string
	for i, typ := range types {
		if typ != nil && !typ.IsNil() {
			// Skip type parameters (like A, B, T, U) - they're not concrete types
			typeName := typ.String()
			if t.isTypeParameter(typeName) {
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

func (t *galaASTTransformer) transformPattern(patCtx grammar.IPatternContext, objExpr ast.Expr) (ast.Expr, []ast.Stmt, error) {
	return t.transformPatternWithType(patCtx, objExpr, nil)
}

func (t *galaASTTransformer) transformPatternWithType(patCtx grammar.IPatternContext, objExpr ast.Expr, matchedType transpiler.Type) (ast.Expr, []ast.Stmt, error) {
	if patCtx.GetText() == "_" {
		return ast.NewIdent("true"), nil, nil
	}

	switch ctx := patCtx.(type) {
	case *grammar.ExpressionPatternContext:
		return t.transformExpressionPatternWithType(ctx.Expression(), objExpr, matchedType)
	case *grammar.TypedPatternContext:
		return t.transformTypedPattern(ctx, objExpr)
	default:
		return nil, nil, fmt.Errorf("unknown pattern type: %T", patCtx)
	}
}

func (t *galaASTTransformer) transformExpressionPattern(patExprCtx grammar.IExpressionContext, objExpr ast.Expr) (ast.Expr, []ast.Stmt, error) {
	return t.transformExpressionPatternWithType(patExprCtx, objExpr, nil)
}

func (t *galaASTTransformer) transformExpressionPatternWithType(patExprCtx grammar.IExpressionContext, objExpr ast.Expr, matchedType transpiler.Type) (ast.Expr, []ast.Stmt, error) {
	if patExprCtx.GetText() == "_" {
		return ast.NewIdent("true"), nil, nil
	}

	// Tuple pattern with parentheses syntax: (a, b, c) => Tuple3(a, b, c)
	if primary := patExprCtx.Primary(); primary != nil {
		if p, ok := primary.(*grammar.PrimaryContext); ok {
			if exprList := p.ExpressionList(); exprList != nil {
				if el, ok := exprList.(*grammar.ExpressionListContext); ok {
					exprs := el.AllExpression()
					if len(exprs) >= 2 {
						// This is a tuple pattern (a, b, c) - transform to TupleN pattern
						return t.transformTuplePattern(exprs, objExpr, matchedType)
					}
				}
			}
		}
	}

	// Simple Binding - bind variable with the matched type
	if primary := patExprCtx.Primary(); primary != nil {
		if p, ok := primary.(*grammar.PrimaryContext); ok && p.Identifier() != nil {
			name := p.Identifier().GetText()
			t.currentScope.vals[name] = false // Treat as var to avoid .Get() wrapping
			// Set the type of the bound variable to the matched type
			if matchedType != nil && !matchedType.IsNil() {
				t.currentScope.valTypes[name] = matchedType
			} else {
				// Type is unknown, explicitly set to any so type inference works correctly
				t.currentScope.valTypes[name] = transpiler.BasicType{Name: "any"}
			}
			assign := &ast.AssignStmt{
				Lhs: []ast.Expr{ast.NewIdent(name)},
				Tok: token.DEFINE,
				Rhs: []ast.Expr{objExpr},
			}
			return ast.NewIdent("true"), []ast.Stmt{assign}, nil
		}
	}

	// Extractor
	if patExprCtx.GetChildCount() >= 3 && patExprCtx.GetChild(1).(antlr.ParseTree).GetText() == "(" {
		extractorCtx := patExprCtx.GetChild(0).(grammar.IExpressionContext)
		patternExpr, err := t.transformExpression(extractorCtx)
		if err != nil {
			return nil, nil, err
		}

		var unapplyFun ast.Expr = t.stdIdent("UnapplyFull")

		// If it's a type name, determine how to match it
		rawName := t.getBaseTypeName(patternExpr)
		var typeObj transpiler.Type = transpiler.NilType{}
		if rawName != "" {
			typeObj = t.getType(rawName)
			if typeObj.IsNil() {
				typeObj = transpiler.ParseType(rawName)
			}
		}
		typeName := typeObj.String()

		if _, ok := t.structFields[typeName]; ok {
			// Check if this is a direct struct match (pattern type equals container type)
			// This handles cases like Tuple(a, b) matching against Tuple[A, B]
			if t.isDirectStructMatch(rawName, matchedType) {
				// Select the appropriate UnapplyTupleN function based on the tuple type
				unapplyFun = t.getUnapplyTupleFunc(rawName, matchedType)
			} else {
				patternExpr = &ast.CompositeLit{Type: t.ident(rawName)}
			}
		} else if t.getCompanionObjectMetadata(rawName) != nil {
			// For companion objects like Some, Left, Right, create a composite literal
			// so we generate std.Some{} instead of std.Some
			patternExpr = &ast.CompositeLit{Type: patternExpr}
		}

		var argList *grammar.ArgumentListContext
		if ctx, ok := patExprCtx.GetChild(2).(*grammar.ArgumentListContext); ok {
			argList = ctx
		}

		resName := t.nextTempVar()
		okName := t.nextTempVar()

		// Only use resName if there are nested patterns that need it
		lhsRes := ast.NewIdent("_")
		if argList != nil && len(argList.AllArgument()) > 0 {
			hasNonUnderscore := false
			for _, argCtx := range argList.AllArgument() {
				if argCtx.(*grammar.ArgumentContext).Pattern().GetText() != "_" {
					hasNonUnderscore = true
					break
				}
			}
			if hasNonUnderscore {
				lhsRes = ast.NewIdent(resName)
			}
		}

		args := []ast.Expr{objExpr}
		// If it's a specialized Unapply, it only takes one arg (the object)
		// For UnapplyFull, it takes (obj, pattern)
		isUnapplyFull := false
		if sel, ok := unapplyFun.(*ast.SelectorExpr); ok {
			if sel.Sel.Name == "UnapplyFull" {
				isUnapplyFull = true
			}
		}

		if isUnapplyFull && patternExpr != nil {
			args = append(args, patternExpr)
		}

		init := &ast.AssignStmt{
			Lhs: []ast.Expr{lhsRes, ast.NewIdent(okName)},
			Tok: token.DEFINE,
			Rhs: []ast.Expr{
				&ast.CallExpr{
					Fun:  unapplyFun,
					Args: args,
				},
			},
		}

		var allBindings []ast.Stmt
		allBindings = append(allBindings, init)

		var conds []ast.Expr
		conds = append(conds, ast.NewIdent(okName))

		// Infer the type of the matched object to enable type-safe extraction
		var objType transpiler.Type
		// First try to get type directly from scope if objExpr is an identifier
		if ident, ok := objExpr.(*ast.Ident); ok {
			objType = t.getType(ident.Name)
		}
		// Fall back to type inference if direct lookup didn't work
		if objType == nil || objType.IsNil() {
			objType, _ = t.inferExprType(objExpr)
		}
		// Use matchedType if objType lacks generic parameters but matchedType has them
		// This handles cases where the scope lookup returns a non-generic type
		if matchedType != nil && !matchedType.IsNil() {
			if objType == nil || objType.IsNil() {
				objType = matchedType
			} else if _, ok := objType.(transpiler.GenericType); !ok {
				// objType is not a GenericType, prefer matchedType if it's a GenericType
				if _, ok := matchedType.(transpiler.GenericType); ok {
					objType = matchedType
				}
			}
		}

		// Handle arguments (nested patterns)
		if argList != nil {
			for i, argCtx := range argList.AllArgument() {
				arg := argCtx.(*grammar.ArgumentContext)

				if arg.Pattern().GetText() == "_" {
					continue
				}

				getSafeExpr := &ast.CallExpr{
					Fun: t.stdIdent("GetSafe"),
					Args: []ast.Expr{
						ast.NewIdent(resName),
						&ast.BasicLit{Kind: token.INT, Value: fmt.Sprintf("%d", i)},
					},
				}

				// Get the extracted type for this specific index
				var extractedTypeAtIdx transpiler.Type
				if objType != nil && !objType.IsNil() {
					extractedTypeAtIdx = t.getExtractedTypeAtIndex(rawName, objType, i)
				}

				// If we know the extracted type, use std.As[T] for safe type checking
				// This avoids panics when the pattern doesn't match
				var valExpr ast.Expr = getSafeExpr
				if extractedTypeAtIdx != nil && !extractedTypeAtIdx.IsNil() {
					// Use std.As[T](getSafeExpr) which returns (value, ok)
					asOkName := t.nextTempVar()
					asCall := &ast.CallExpr{
						Fun: &ast.IndexExpr{
							X:     t.stdIdent("As"),
							Index: t.typeToExpr(extractedTypeAtIdx),
						},
						Args: []ast.Expr{getSafeExpr},
					}

					// Check if the pattern is a simple identifier (not underscore, not a complex pattern)
					patternText := arg.Pattern().GetText()
					if t.isSimpleIdentifier(patternText) {
						varName := patternText
						t.currentScope.vals[varName] = false
						t.currentScope.valTypes[varName] = extractedTypeAtIdx

						asAssign := &ast.AssignStmt{
							Lhs: []ast.Expr{ast.NewIdent(varName), ast.NewIdent(asOkName)},
							Tok: token.DEFINE,
							Rhs: []ast.Expr{asCall},
						}
						allBindings = append(allBindings, asAssign)
						conds = append(conds, ast.NewIdent(asOkName))
						continue // Skip the regular pattern transformation
					}

					// For other patterns, keep using GetSafe without type assertion
				}

				subCond, subBindings, err := t.transformPatternWithType(arg.Pattern(), valExpr, extractedTypeAtIdx)
				if err != nil {
					return nil, nil, err
				}
				if subCond != nil {
					conds = append(conds, subCond)
				}
				allBindings = append(allBindings, subBindings...)
			}
		}

		var finalCond ast.Expr = conds[0]
		for i := 1; i < len(conds); i++ {
			finalCond = &ast.BinaryExpr{
				X:  finalCond,
				Op: token.LAND,
				Y:  conds[i],
			}
		}

		return finalCond, allBindings, nil
	}

	// Literal or other
	patExpr, err := t.transformExpression(patExprCtx)
	if err != nil {
		return nil, nil, err
	}
	cond := &ast.CallExpr{
		Fun: t.stdIdent("UnapplyCheck"),
		Args: []ast.Expr{
			objExpr,
			patExpr,
		},
	}
	return cond, nil, nil
}

func (t *galaASTTransformer) transformTypedPattern(ctx *grammar.TypedPatternContext, objExpr ast.Expr) (ast.Expr, []ast.Stmt, error) {
	name := ctx.Identifier().GetText()
	typeExpr, err := t.transformType(ctx.Type_())
	if err != nil {
		return nil, nil, err
	}

	typeName := t.resolveType(t.getBaseTypeName(typeExpr))
	if qName := t.getType(typeName.String()); !qName.IsNil() {
		typeName = qName
	}
	t.addVar(name, typeName)

	okName := t.nextTempVar()

	// v, ok := std.As[T](obj)
	asCall := &ast.CallExpr{
		Fun: &ast.IndexExpr{
			X:     t.stdIdent("As"),
			Index: typeExpr,
		},
		Args: []ast.Expr{objExpr},
	}

	assign := &ast.AssignStmt{
		Lhs: []ast.Expr{ast.NewIdent(name), ast.NewIdent(okName)},
		Tok: token.DEFINE,
		Rhs: []ast.Expr{asCall},
	}

	return ast.NewIdent(okName), []ast.Stmt{assign}, nil
}

func (t *galaASTTransformer) transformCaseClause(ctx *grammar.CaseClauseContext, paramName string) (ast.Stmt, error) {
	t.pushScope()
	defer t.popScope()

	patCtx := ctx.Pattern()
	cond, bindings, err := t.transformPattern(patCtx, ast.NewIdent(paramName))
	if err != nil {
		return nil, err
	}

	if ctx.GetGuard() != nil {
		guard, err := t.transformExpression(ctx.GetGuard())
		if err != nil {
			return nil, err
		}
		cond = &ast.BinaryExpr{
			X:  cond,
			Op: token.LAND,
			Y:  guard,
		}
	}

	var body []ast.Stmt
	if ctx.GetBodyBlock() != nil {
		b, err := t.transformBlock(ctx.GetBodyBlock().(*grammar.BlockContext))
		if err != nil {
			return nil, err
		}
		body = b.List
	} else if ctx.GetBody() != nil {
		expr, err := t.transformExpression(ctx.GetBody())
		if err != nil {
			return nil, err
		}
		body = []ast.Stmt{&ast.ReturnStmt{Results: []ast.Expr{expr}}}
	}

	bodyBlock := &ast.BlockStmt{List: body}

	ifStmt := &ast.IfStmt{
		Cond: cond,
		Body: bodyBlock,
	}

	if len(bindings) > 0 {
		return &ast.BlockStmt{
			List: append(bindings, ifStmt),
		}, nil
	}

	return ifStmt, nil
}

// getExtractedType determines the type of the value extracted by an extractor pattern.
// For example, when matching Some(v) against Option[int], the extracted type is int.
func (t *galaASTTransformer) getExtractedType(extractorName string, objType transpiler.Type) transpiler.Type {
	return t.getExtractedTypeAtIndex(extractorName, objType, 0)
}

// getExtractedTypeAtIndex determines the type of the value extracted at a specific index.
// It uses companion object metadata discovered by the analyzer instead of hardcoding extractor names.
// For Tuple[A, B], index 0 returns A, index 1 returns B.
func (t *galaASTTransformer) getExtractedTypeAtIndex(extractorName string, objType transpiler.Type, index int) transpiler.Type {
	genType, ok := objType.(transpiler.GenericType)
	if !ok || len(genType.Params) == 0 {
		return nil
	}

	baseName := genType.Base.BaseName()

	var extractedType transpiler.Type

	// Normalize extractor name by removing package prefix for lookup
	normalizedName := extractorName
	if len(normalizedName) > 4 && normalizedName[:4] == "std." {
		normalizedName = normalizedName[4:]
	}

	// Check if this is a direct struct match (extractor type equals container type)
	// This handles cases like Tuple(a, b) matching against Tuple[A, B]
	if normalizedName == baseName || extractorName == baseName {
		// Direct struct match - extract type param at the specified index
		if index < len(genType.Params) {
			extractedType = genType.Params[index]
		}
	} else {
		// Look up companion object metadata
		companionMeta := t.getCompanionObjectMetadata(extractorName)
		if companionMeta != nil {
			// Verify the companion works with this container type
			if companionMeta.TargetType == baseName ||
				companionMeta.TargetType == "std."+baseName ||
				"std."+companionMeta.TargetType == baseName {
				// Find which container type param index to extract
				if index < len(companionMeta.ExtractIndices) {
					paramIndex := companionMeta.ExtractIndices[index]
					if paramIndex < len(genType.Params) {
						extractedType = genType.Params[paramIndex]
					}
				} else if len(companionMeta.ExtractIndices) == 1 && index == 0 {
					// Common case: companion extracts one value, use its index
					paramIndex := companionMeta.ExtractIndices[0]
					if paramIndex < len(genType.Params) {
						extractedType = genType.Params[paramIndex]
					}
				}
			}
		}
	}

	// Check if the extracted type is a type parameter (like T, U, A, B)
	// If so, return nil to avoid generating invalid type assertions
	if extractedType != nil {
		if basic, ok := extractedType.(transpiler.BasicType); ok {
			name := basic.Name
			// Type parameters are typically single uppercase letters or short names
			if len(name) == 1 && name[0] >= 'A' && name[0] <= 'Z' {
				return nil
			}
		}
	}

	return extractedType
}

// isDirectStructMatch checks if the pattern type directly matches the container type
// AND the matched type is a generic type with type parameters.
// For example, Tuple pattern matching against Tuple[A, B] is a direct match.
// This is different from:
// - Companion objects like Some matching Option[T]
// - Non-generic struct matching (like Person matching Person) which should use UnapplyFull
func (t *galaASTTransformer) isDirectStructMatch(patternTypeName string, matchedType transpiler.Type) bool {
	if matchedType == nil || matchedType.IsNil() {
		return false
	}

	// Only consider generic types for direct struct matching
	// Non-generic structs should use UnapplyFull with their own Unapply method
	genType, ok := matchedType.(transpiler.GenericType)
	if !ok || len(genType.Params) == 0 {
		return false
	}

	containerBaseName := genType.Base.BaseName()

	// Normalize names by removing package prefixes
	normalizedPattern := patternTypeName
	if len(normalizedPattern) > 4 && normalizedPattern[:4] == "std." {
		normalizedPattern = normalizedPattern[4:]
	}

	normalizedContainer := containerBaseName
	if len(normalizedContainer) > 4 && normalizedContainer[:4] == "std." {
		normalizedContainer = normalizedContainer[4:]
	}

	// Check for exact match
	if normalizedPattern == normalizedContainer {
		return true
	}

	// Check for tuple pattern matching with parentheses syntax
	// When using (a, b, c) pattern, the patternTypeName might not be set,
	// but we want to match against TupleN types
	if t.isTupleType(normalizedContainer) {
		return true
	}

	return false
}

// isTupleType checks if a type name is a Tuple type (Tuple, Tuple3, ..., Tuple10)
func (t *galaASTTransformer) isTupleType(typeName string) bool {
	switch typeName {
	case transpiler.TypeTuple, transpiler.TypeTuple3, transpiler.TypeTuple4,
		transpiler.TypeTuple5, transpiler.TypeTuple6, transpiler.TypeTuple7,
		transpiler.TypeTuple8, transpiler.TypeTuple9, transpiler.TypeTuple10:
		return true
	}
	return false
}

// getUnapplyTupleFunc returns the appropriate UnapplyTupleN function based on the tuple type.
func (t *galaASTTransformer) getUnapplyTupleFunc(patternTypeName string, matchedType transpiler.Type) ast.Expr {
	// Get the tuple arity from the matched type
	genType, ok := matchedType.(transpiler.GenericType)
	if !ok {
		return t.stdIdent("UnapplyTuple")
	}

	containerBaseName := genType.Base.BaseName()
	// Normalize by removing package prefix
	if len(containerBaseName) > 4 && containerBaseName[:4] == "std." {
		containerBaseName = containerBaseName[4:]
	}

	// Select the appropriate unapply function based on the tuple type
	switch containerBaseName {
	case transpiler.TypeTuple:
		return t.stdIdent("UnapplyTuple")
	case transpiler.TypeTuple3:
		return t.stdIdent("UnapplyTuple3")
	case transpiler.TypeTuple4:
		return t.stdIdent("UnapplyTuple4")
	case transpiler.TypeTuple5:
		return t.stdIdent("UnapplyTuple5")
	case transpiler.TypeTuple6:
		return t.stdIdent("UnapplyTuple6")
	case transpiler.TypeTuple7:
		return t.stdIdent("UnapplyTuple7")
	case transpiler.TypeTuple8:
		return t.stdIdent("UnapplyTuple8")
	case transpiler.TypeTuple9:
		return t.stdIdent("UnapplyTuple9")
	case transpiler.TypeTuple10:
		return t.stdIdent("UnapplyTuple10")
	default:
		return t.stdIdent("UnapplyTuple")
	}
}

// transformTuplePattern transforms a tuple pattern like (a, b, c) into the appropriate
// UnapplyTupleN call and variable bindings.
func (t *galaASTTransformer) transformTuplePattern(patternExprs []grammar.IExpressionContext, objExpr ast.Expr, matchedType transpiler.Type) (ast.Expr, []ast.Stmt, error) {
	n := len(patternExprs)
	if n < 2 || n > 10 {
		return nil, nil, galaerr.NewSemanticError(fmt.Sprintf("tuple patterns must have 2-10 elements, got %d", n))
	}

	// Determine the unapply function based on arity
	var unapplyFunc string
	switch n {
	case 2:
		unapplyFunc = "UnapplyTuple"
	case 3:
		unapplyFunc = "UnapplyTuple3"
	case 4:
		unapplyFunc = "UnapplyTuple4"
	case 5:
		unapplyFunc = "UnapplyTuple5"
	case 6:
		unapplyFunc = "UnapplyTuple6"
	case 7:
		unapplyFunc = "UnapplyTuple7"
	case 8:
		unapplyFunc = "UnapplyTuple8"
	case 9:
		unapplyFunc = "UnapplyTuple9"
	case 10:
		unapplyFunc = "UnapplyTuple10"
	}

	// Generate temporary variables for result and ok
	resName := t.nextTempVar()
	okName := t.nextTempVar()

	// Check if we need the result (i.e., there are non-underscore patterns)
	hasNonUnderscore := false
	for _, patExpr := range patternExprs {
		if patExpr.GetText() != "_" {
			hasNonUnderscore = true
			break
		}
	}

	lhsRes := ast.NewIdent("_")
	if hasNonUnderscore {
		lhsRes = ast.NewIdent(resName)
	}

	// Generate: res, ok := std.UnapplyTupleN(obj)
	unapplyCall := &ast.CallExpr{
		Fun:  t.stdIdent(unapplyFunc),
		Args: []ast.Expr{objExpr},
	}

	unapplyStmt := &ast.AssignStmt{
		Lhs: []ast.Expr{lhsRes, ast.NewIdent(okName)},
		Tok: token.DEFINE,
		Rhs: []ast.Expr{unapplyCall},
	}

	stmts := []ast.Stmt{unapplyStmt}

	// Extract element types from matched type if available
	var elementTypes []transpiler.Type
	if genType, ok := matchedType.(transpiler.GenericType); ok {
		elementTypes = genType.Params
	}

	// Generate bindings for each pattern element
	for i, patExpr := range patternExprs {
		patText := patExpr.GetText()
		if patText == "_" {
			continue
		}

		// Determine the type for this element
		var elemType transpiler.Type = transpiler.BasicType{Name: "any"}
		if i < len(elementTypes) {
			elemType = elementTypes[i]
		}

		// Get the element from the result slice: std.GetSafe(res, i)
		elemExpr := &ast.CallExpr{
			Fun: t.stdIdent("GetSafe"),
			Args: []ast.Expr{
				ast.NewIdent(resName),
				&ast.BasicLit{Kind: token.INT, Value: fmt.Sprintf("%d", i)},
			},
		}

		// Check if this is a simple binding (identifier) or nested pattern
		if primary := patExpr.Primary(); primary != nil {
			if p, ok := primary.(*grammar.PrimaryContext); ok && p.Identifier() != nil {
				// Simple binding: x := std.GetSafe(res, i).(type)
				name := p.Identifier().GetText()
				t.currentScope.vals[name] = false
				t.currentScope.valTypes[name] = elemType

				var rhs ast.Expr = elemExpr
				if elemType.String() != "any" {
					rhs = &ast.TypeAssertExpr{
						X:    elemExpr,
						Type: t.typeToExpr(elemType),
					}
				}

				assign := &ast.AssignStmt{
					Lhs: []ast.Expr{ast.NewIdent(name)},
					Tok: token.DEFINE,
					Rhs: []ast.Expr{rhs},
				}
				stmts = append(stmts, assign)
				continue
			}
		}

		// Handle nested patterns recursively
		nestedCond, nestedStmts, err := t.transformExpressionPatternWithType(patExpr, elemExpr, elemType)
		if err != nil {
			return nil, nil, err
		}
		stmts = append(stmts, nestedStmts...)

		// If nested pattern has a condition other than "true", we need to AND it with ok
		if ident, ok := nestedCond.(*ast.Ident); !ok || ident.Name != "true" {
			// okName = okName && nestedCond
			stmts = append(stmts, &ast.AssignStmt{
				Lhs: []ast.Expr{ast.NewIdent(okName)},
				Tok: token.ASSIGN,
				Rhs: []ast.Expr{&ast.BinaryExpr{
					X:  ast.NewIdent(okName),
					Op: token.LAND,
					Y:  nestedCond,
				}},
			})
		}
	}

	t.needsStdImport = true
	return ast.NewIdent(okName), stmts, nil
}

// getCompanionObjectMetadata looks up companion object metadata by name.
// It tries various name formats: short name, std-prefixed name, and fully qualified name.
func (t *galaASTTransformer) getCompanionObjectMetadata(name string) *transpiler.CompanionObjectMetadata {
	if t.companionObjects == nil {
		return nil
	}

	// Try exact name first
	if meta, ok := t.companionObjects[name]; ok {
		return meta
	}

	// Try with std prefix
	if meta, ok := t.companionObjects["std."+name]; ok {
		return meta
	}

	// Try without std prefix
	if len(name) > 4 && name[:4] == "std." {
		if meta, ok := t.companionObjects[name[4:]]; ok {
			return meta
		}
	}

	return nil
}

// wrapWithTypeAssertion wraps an expression with a type assertion.
// For example, wraps `std.GetSafe(res, 0)` to `std.GetSafe(res, 0).(int)`
func (t *galaASTTransformer) wrapWithTypeAssertion(expr ast.Expr, typ transpiler.Type) ast.Expr {
	typeExpr := t.typeToExpr(typ)
	if typeExpr == nil {
		return expr
	}

	return &ast.TypeAssertExpr{
		X:    expr,
		Type: typeExpr,
	}
}

// fixupReturnStatements traverses statements and adds type assertions to return statements
// that return 'any' values when the expected result type is concrete.
func (t *galaASTTransformer) fixupReturnStatements(stmts []ast.Stmt, resultType transpiler.Type) {
	for _, stmt := range stmts {
		t.fixupReturnStatement(stmt, resultType)
	}
}

func (t *galaASTTransformer) fixupReturnStatement(stmt ast.Stmt, resultType transpiler.Type) {
	switch s := stmt.(type) {
	case *ast.ReturnStmt:
		if len(s.Results) > 0 {
			for i, result := range s.Results {
				// Check if the result expression is a simple identifier with type 'any'
				if ident, ok := result.(*ast.Ident); ok {
					varType := t.getType(ident.Name)
					if varType != nil && varType.String() == "any" {
						// Wrap with type assertion to the expected result type
						s.Results[i] = &ast.TypeAssertExpr{
							X:    result,
							Type: t.typeToExpr(resultType),
						}
					}
				}
			}
		}
	case *ast.IfStmt:
		// Recursively process if body and else clause
		if s.Body != nil {
			t.fixupReturnStatements(s.Body.List, resultType)
		}
		if s.Else != nil {
			if block, ok := s.Else.(*ast.BlockStmt); ok {
				t.fixupReturnStatements(block.List, resultType)
			} else {
				t.fixupReturnStatement(s.Else, resultType)
			}
		}
	case *ast.BlockStmt:
		t.fixupReturnStatements(s.List, resultType)
	}
}
