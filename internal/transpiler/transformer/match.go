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
	case *grammar.RestPatternContext:
		// Rest pattern like "rest..." or "_..." - these should only appear in argument lists
		// If we get here, it's an error (rest patterns must be part of a sequence pattern)
		return nil, nil, galaerr.NewSemanticError("rest pattern (...) can only be used as the last argument in a sequence pattern like Array(first, second, rest...)")
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
	if p := t.getPrimaryFromExpression(patExprCtx); p != nil {
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

	// Extractor - check for call patterns like Left(n), Some(x), IntStack(first, second, _...) etc.
	// This check must come BEFORE the simple binding check because a pattern like `Foo(x)`
	// has a primary with identifier `Foo`, but it's not a simple binding.
	if primaryExprCtx, argList := t.getCallPatternFromExpression(patExprCtx); primaryExprCtx != nil {
		patternExpr, err := t.transformPrimaryExpr(primaryExprCtx)
		if err != nil {
			return nil, nil, err
		}

		// If it's a type name, determine how to match it
		rawName := t.getBaseTypeName(patternExpr)
		resolvedTypeName := t.resolveTypeMetaName(rawName)

		// Check if we can use direct Unapply call (no reflection)
		// This applies to any extractor with an Unapply method - both generic and non-generic
		// For generic extractors like Cons[T], Some[T], we infer type params from the matched type
		// For non-generic extractors like Even, we just call Even{}.Unapply(x) directly
		// Requires Unapply to return bool or Option[T] - otherwise fall back to reflection
		if resolvedTypeName != "" {
			if meta, ok := t.typeMetas[resolvedTypeName]; ok {
				if unapplyMeta, hasUnapply := meta.Methods["Unapply"]; hasUnapply {
					var inferredTypes []transpiler.Type
					if len(meta.TypeParams) > 0 {
						inferredTypes = t.inferExtractorTypeParams(meta, matchedType)
						if len(inferredTypes) != len(meta.TypeParams) {
							return nil, nil, galaerr.NewSemanticError(
								fmt.Sprintf("cannot infer type parameters for extractor '%s'. Ensure the Unapply method's parameter type matches the matched type", rawName))
						}
					}
					// Check if return type is supported (bool or Option[T])
					returnType := t.substituteConcreteTypes(unapplyMeta.ReturnType, meta.TypeParams, inferredTypes)
					if !t.isDirectUnapplyReturnType(returnType) {
						return nil, nil, galaerr.NewSemanticError(
							fmt.Sprintf("extractor '%s' must have Unapply returning bool or Option[T], got '%s'. Use Option[T] for extractors or bool for guard patterns. Unapply(any) any is not allowed",
								rawName, returnType.String()))
					}
					// Use direct Unapply call - no reflection needed!
					return t.generateDirectUnapplyPattern(rawName, meta, inferredTypes, unapplyMeta, objExpr, argList, matchedType)
				}
			}
		}

		// Check if this is a sequence pattern with rest arguments (e.g., Array(first, second, rest...))
		// This handles Seq types like Array and List with variable-length extraction
		if t.hasRestPattern(argList) {
			if t.isSeqType(matchedType) {
				return t.generateSeqPatternMatch(objExpr, argList, matchedType)
			}
			return nil, nil, galaerr.NewSemanticError(
				fmt.Sprintf("rest pattern (...) requires a sequence type (Array, List, or type implementing Seq). Got '%s'", matchedType.String()))
		}

		// Check if this is a direct struct match for tuples (pattern type equals container type)
		// This handles cases like (a, b) matching against Tuple[A, B]
		if t.isDirectStructMatch(rawName, matchedType) {
			return t.generateDirectTupleStructMatch(objExpr, argList, matchedType)
		}

		// Check if this is a non-generic struct pattern match (e.g., Person(name, age))
		// Use direct field access for known structs
		resolvedStructName := t.resolveStructTypeName(rawName)
		if fields, ok := t.structFields[resolvedStructName]; ok && len(fields) > 0 {
			return t.generateDirectStructFieldMatch(objExpr, argList, fields, resolvedStructName)
		}

		// Extractor not found or doesn't have Unapply method
		return nil, nil, galaerr.NewSemanticError(
			fmt.Sprintf("extractor '%s' must define an Unapply method. For generic extractors use: func (e Extractor[T]) Unapply(v ContainerType[T]) Option[T]. For guard patterns use: func (e Extractor) Unapply(v ConcreteType) bool",
				rawName))
	}

	// Simple Binding - bind variable with the matched type
	// This check comes after the extractor check because extractors like `Foo(x)` have a primary
	// with an identifier, but they're not simple bindings.
	if p := t.getPrimaryFromExpression(patExprCtx); p != nil && p.Identifier() != nil {
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

	// Literal or other - use direct equality comparison
	patExpr, err := t.transformExpression(patExprCtx)
	if err != nil {
		return nil, nil, err
	}
	cond := &ast.BinaryExpr{
		X:  objExpr,
		Op: token.EQL,
		Y:  patExpr,
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
	return t.getExtractedTypeAtIndexWithArgs(extractorName, objType, index, -1)
}

// getExtractedTypeAtIndexWithArgs determines the type of the value extracted at a specific index.
// numArgs is the total number of arguments in the pattern, used to decide whether to expand tuples.
func (t *galaASTTransformer) getExtractedTypeAtIndexWithArgs(extractorName string, objType transpiler.Type, index int, numArgs int) transpiler.Type {
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
		// First, check if this is a generic extractor with type parameters
		// For example, Cons[T] with Unapply(l List[T]) Option[Tuple[T, List[T]]]
		extractedType = t.getGenericExtractorResultTypeWithArgs(extractorName, objType, index, numArgs)

		// If not found, look up companion object metadata
		if extractedType == nil {
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

// generateDirectTupleStructMatch generates direct field access code for tuple patterns.
// Instead of using reflection-based UnapplyTupleN, this generates direct access like:
//
//	a := obj.V1.Get()
//	b := obj.V2.Get()
//
// The condition is always true since the type already matches.
func (t *galaASTTransformer) generateDirectTupleStructMatch(objExpr ast.Expr, argList *grammar.ArgumentListContext, matchedType transpiler.Type) (ast.Expr, []ast.Stmt, error) {
	if argList == nil {
		return ast.NewIdent("true"), nil, nil
	}

	args := argList.AllArgument()
	if len(args) == 0 {
		return ast.NewIdent("true"), nil, nil
	}

	var stmts []ast.Stmt
	var conds []ast.Expr

	// Extract element types from matched type if available
	var elementTypes []transpiler.Type
	if genType, ok := matchedType.(transpiler.GenericType); ok {
		elementTypes = genType.Params
	}

	// Generate bindings for each pattern argument using direct field access
	for i, argCtx := range args {
		arg := argCtx.(*grammar.ArgumentContext)
		patternText := arg.Pattern().GetText()

		if patternText == "_" {
			continue
		}

		// Determine the type for this element
		var elemType transpiler.Type = transpiler.BasicType{Name: "any"}
		if i < len(elementTypes) {
			elemType = elementTypes[i]
		}

		// Generate direct field access: objExpr.V{i+1}.Get()
		fieldName := fmt.Sprintf("V%d", i+1)
		elemExpr := &ast.CallExpr{
			Fun: &ast.SelectorExpr{
				X: &ast.SelectorExpr{
					X:   objExpr,
					Sel: ast.NewIdent(fieldName),
				},
				Sel: ast.NewIdent("Get"),
			},
		}

		// Check if this is a simple binding or a nested pattern
		patCtx := arg.Pattern()
		if exprPat, ok := patCtx.(*grammar.ExpressionPatternContext); ok {
			if p := t.getPrimaryFromExpression(exprPat.Expression()); p != nil && p.Identifier() != nil {
				// Simple binding: name := obj.V{i+1}.Get()
				// Note: .Get() already returns the concrete type, so no type assertion needed
				name := p.Identifier().GetText()
				t.currentScope.vals[name] = false
				t.currentScope.valTypes[name] = elemType

				assign := &ast.AssignStmt{
					Lhs: []ast.Expr{ast.NewIdent(name)},
					Tok: token.DEFINE,
					Rhs: []ast.Expr{elemExpr},
				}
				stmts = append(stmts, assign)
				continue
			}

			// Nested pattern - transform recursively
			nestedCond, nestedStmts, err := t.transformExpressionPatternWithType(exprPat.Expression(), elemExpr, elemType)
			if err != nil {
				return nil, nil, err
			}
			stmts = append(stmts, nestedStmts...)
			if ident, ok := nestedCond.(*ast.Ident); !ok || ident.Name != "true" {
				conds = append(conds, nestedCond)
			}
		}
	}

	t.needsStdImport = true

	// Combine all conditions
	if len(conds) == 0 {
		return ast.NewIdent("true"), stmts, nil
	}

	finalCond := conds[0]
	for i := 1; i < len(conds); i++ {
		finalCond = &ast.BinaryExpr{
			X:  finalCond,
			Op: token.LAND,
			Y:  conds[i],
		}
	}
	return finalCond, stmts, nil
}

// generateDirectStructFieldMatch generates direct field access code for struct patterns.
// For example, Person(name, age) matching against Person{Name: "Alice", Age: 25}
// generates: name := obj.Name; age := obj.Age
// The condition is always true since we're just extracting fields.
func (t *galaASTTransformer) generateDirectStructFieldMatch(objExpr ast.Expr, argList *grammar.ArgumentListContext, fields []string, structName string) (ast.Expr, []ast.Stmt, error) {
	if argList == nil {
		return ast.NewIdent("true"), nil, nil
	}

	args := argList.AllArgument()
	if len(args) == 0 {
		return ast.NewIdent("true"), nil, nil
	}

	if len(args) > len(fields) {
		return nil, nil, galaerr.NewSemanticError(fmt.Sprintf("struct '%s' has %d fields but pattern has %d arguments", structName, len(fields), len(args)))
	}

	var stmts []ast.Stmt
	var conds []ast.Expr

	// Get field types if available
	fieldTypes := t.structFieldTypes[structName]

	// Generate bindings for each pattern argument using direct field access
	for i, argCtx := range args {
		arg := argCtx.(*grammar.ArgumentContext)
		patternText := arg.Pattern().GetText()

		if patternText == "_" {
			continue
		}

		// Get the field name and type
		fieldName := fields[i]
		var fieldType transpiler.Type = transpiler.BasicType{Name: "any"}
		if fieldTypes != nil {
			if ft, ok := fieldTypes[fieldName]; ok {
				fieldType = ft
			}
		}

		// Generate direct field access: objExpr.FieldName.Get()
		// Struct fields are stored as Immutable[T], so we need to call .Get()
		elemExpr := &ast.CallExpr{
			Fun: &ast.SelectorExpr{
				X: &ast.SelectorExpr{
					X:   objExpr,
					Sel: ast.NewIdent(fieldName),
				},
				Sel: ast.NewIdent("Get"),
			},
		}

		// Check if this is a simple binding or a nested pattern
		patCtx := arg.Pattern()
		if exprPat, ok := patCtx.(*grammar.ExpressionPatternContext); ok {
			if p := t.getPrimaryFromExpression(exprPat.Expression()); p != nil && p.Identifier() != nil {
				// Simple binding: name := obj.FieldName.Get()
				name := p.Identifier().GetText()
				t.currentScope.vals[name] = false
				t.currentScope.valTypes[name] = fieldType

				assign := &ast.AssignStmt{
					Lhs: []ast.Expr{ast.NewIdent(name)},
					Tok: token.DEFINE,
					Rhs: []ast.Expr{elemExpr},
				}
				stmts = append(stmts, assign)
				continue
			}

			// Nested pattern - transform recursively
			nestedCond, nestedStmts, err := t.transformExpressionPatternWithType(exprPat.Expression(), elemExpr, fieldType)
			if err != nil {
				return nil, nil, err
			}
			stmts = append(stmts, nestedStmts...)
			if ident, ok := nestedCond.(*ast.Ident); !ok || ident.Name != "true" {
				conds = append(conds, nestedCond)
			}
		} else if typedPat, ok := patCtx.(*grammar.TypedPatternContext); ok {
			// Typed pattern: case Person(name: string, age: int)
			varName := typedPat.Identifier().GetText()

			// Parse the expected type
			typeExpr, err := t.transformType(typedPat.Type_())
			if err != nil {
				return nil, nil, err
			}

			expectedType := t.resolveType(t.getBaseTypeName(typeExpr))
			t.currentScope.vals[varName] = false
			t.currentScope.valTypes[varName] = expectedType

			// Generate: varName, okN := std.As[ExpectedType](field.Get())
			okName := t.nextTempVar()
			asCall := &ast.CallExpr{
				Fun: &ast.IndexExpr{
					X:     t.stdIdent("As"),
					Index: typeExpr,
				},
				Args: []ast.Expr{elemExpr},
			}

			asAssign := &ast.AssignStmt{
				Lhs: []ast.Expr{ast.NewIdent(varName), ast.NewIdent(okName)},
				Tok: token.DEFINE,
				Rhs: []ast.Expr{asCall},
			}
			stmts = append(stmts, asAssign)
			conds = append(conds, ast.NewIdent(okName))
		}
	}

	t.needsStdImport = true

	// Combine all conditions
	if len(conds) == 0 {
		return ast.NewIdent("true"), stmts, nil
	}

	finalCond := conds[0]
	for i := 1; i < len(conds); i++ {
		finalCond = &ast.BinaryExpr{
			X:  finalCond,
			Op: token.LAND,
			Y:  conds[i],
		}
	}
	return finalCond, stmts, nil
}

// hasRestPattern checks if any argument in the argument list is a rest pattern (ends with ...).
func (t *galaASTTransformer) hasRestPattern(argList *grammar.ArgumentListContext) bool {
	if argList == nil {
		return false
	}
	for _, argCtx := range argList.AllArgument() {
		arg := argCtx.(*grammar.ArgumentContext)
		if _, ok := arg.Pattern().(*grammar.RestPatternContext); ok {
			return true
		}
	}
	return false
}

// isSeqType checks if a type implements the Seq interface (has Size, Get, SeqDrop methods).
func (t *galaASTTransformer) isSeqType(typ transpiler.Type) bool {
	if typ == nil || typ.IsNil() {
		return false
	}

	// Get the base type name
	var baseName string
	if genType, ok := typ.(transpiler.GenericType); ok {
		baseName = genType.Base.BaseName()
	} else if basicType, ok := typ.(transpiler.BasicType); ok {
		baseName = basicType.Name
	} else {
		return false
	}

	// Check if it's a known Seq type
	switch baseName {
	case "Array", "collection_immutable.Array", "List", "collection_immutable.List":
		return true
	}

	// Check if the type has the required methods
	resolvedName := t.resolveStructTypeName(baseName)
	if meta, ok := t.typeMetas[resolvedName]; ok {
		_, hasSize := meta.Methods["Size"]
		_, hasGet := meta.Methods["Get"]
		_, hasSeqDrop := meta.Methods["SeqDrop"]
		return hasSize && hasGet && hasSeqDrop
	}

	return false
}

// getSeqElementType extracts the element type from a Seq type like Array[int] or List[string].
func (t *galaASTTransformer) getSeqElementType(typ transpiler.Type) transpiler.Type {
	if genType, ok := typ.(transpiler.GenericType); ok {
		if len(genType.Params) > 0 {
			return genType.Params[0]
		}
	}
	return transpiler.BasicType{Name: "any"}
}

// generateSeqPatternMatch generates code for sequence pattern matching with rest patterns.
// For example, Array(first, second, rest...) matching against Array[int] generates:
//
//	_tmp_ok := obj.Size() >= 2
//	var first int
//	var second int
//	var rest Array[int]
//	if _tmp_ok {
//	    first = obj.Get(0)
//	    second = obj.Get(1)
//	    rest = obj.SeqDrop(2).(Array[int])
//	}
//	if _tmp_ok { ... body }
func (t *galaASTTransformer) generateSeqPatternMatch(objExpr ast.Expr, argList *grammar.ArgumentListContext, matchedType transpiler.Type) (ast.Expr, []ast.Stmt, error) {
	if argList == nil {
		return ast.NewIdent("true"), nil, nil
	}

	args := argList.AllArgument()
	if len(args) == 0 {
		return ast.NewIdent("true"), nil, nil
	}

	var stmts []ast.Stmt
	var conds []ast.Expr

	// Find the rest pattern and count non-rest arguments
	var restPatternIndex int = -1
	var restPatternName string
	nonRestCount := 0

	for i, argCtx := range args {
		arg := argCtx.(*grammar.ArgumentContext)
		if restPat, ok := arg.Pattern().(*grammar.RestPatternContext); ok {
			restPatternIndex = i
			// Get the identifier before ...
			exprText := restPat.Expression().GetText()
			if exprText != "_" {
				restPatternName = exprText
			}
		} else {
			nonRestCount++
		}
	}

	// Rest pattern must be the last argument
	if restPatternIndex >= 0 && restPatternIndex != len(args)-1 {
		return nil, nil, galaerr.NewSemanticError("rest pattern (...) must be the last argument in a sequence pattern")
	}

	// Generate size check: _tmp_ok := obj.Size() >= minRequired
	sizeCheckName := t.nextTempVar()
	sizeCheck := &ast.AssignStmt{
		Lhs: []ast.Expr{ast.NewIdent(sizeCheckName)},
		Tok: token.DEFINE,
		Rhs: []ast.Expr{
			&ast.BinaryExpr{
				X: &ast.CallExpr{
					Fun: &ast.SelectorExpr{
						X:   objExpr,
						Sel: ast.NewIdent("Size"),
					},
				},
				Op: token.GEQ,
				Y:  &ast.BasicLit{Kind: token.INT, Value: fmt.Sprintf("%d", nonRestCount)},
			},
		},
	}
	stmts = append(stmts, sizeCheck)
	conds = append(conds, ast.NewIdent(sizeCheckName))

	// Get the element type
	elemType := t.getSeqElementType(matchedType)
	elemTypeExpr := t.typeToExpr(elemType)
	if elemTypeExpr == nil {
		elemTypeExpr = ast.NewIdent("any")
	}

	// Collect variable declarations and guarded assignments
	var varDecls []ast.Stmt
	var guardedAssigns []ast.Stmt
	var bindingNames []string

	// Process non-rest arguments
	argIndex := 0
	for _, argCtx := range args {
		arg := argCtx.(*grammar.ArgumentContext)
		patCtx := arg.Pattern()

		// Skip rest pattern for now
		if _, ok := patCtx.(*grammar.RestPatternContext); ok {
			continue
		}

		patternText := patCtx.GetText()
		if patternText == "_" {
			argIndex++
			continue
		}

		// Handle different pattern types
		if exprPat, ok := patCtx.(*grammar.ExpressionPatternContext); ok {
			if p := t.getPrimaryFromExpression(exprPat.Expression()); p != nil && p.Identifier() != nil {
				// Simple binding: declare var, then assign inside guard
				name := p.Identifier().GetText()
				bindingNames = append(bindingNames, name)
				t.currentScope.vals[name] = false
				t.currentScope.valTypes[name] = elemType

				// var name ElemType
				varDecl := &ast.DeclStmt{
					Decl: &ast.GenDecl{
						Tok: token.VAR,
						Specs: []ast.Spec{
							&ast.ValueSpec{
								Names: []*ast.Ident{ast.NewIdent(name)},
								Type:  elemTypeExpr,
							},
						},
					},
				}
				varDecls = append(varDecls, varDecl)

				// name = obj.Get(i) (inside guard)
				guardedAssigns = append(guardedAssigns, &ast.AssignStmt{
					Lhs: []ast.Expr{ast.NewIdent(name)},
					Tok: token.ASSIGN,
					Rhs: []ast.Expr{
						&ast.CallExpr{
							Fun: &ast.SelectorExpr{
								X:   objExpr,
								Sel: ast.NewIdent("Get"),
							},
							Args: []ast.Expr{&ast.BasicLit{Kind: token.INT, Value: fmt.Sprintf("%d", argIndex)}},
						},
					},
				})

				argIndex++
				continue
			}

			// Nested pattern - need to handle more carefully
			// For now, just generate the Get call and transform
			tempName := t.nextTempVar()
			varDecl := &ast.DeclStmt{
				Decl: &ast.GenDecl{
					Tok: token.VAR,
					Specs: []ast.Spec{
						&ast.ValueSpec{
							Names: []*ast.Ident{ast.NewIdent(tempName)},
							Type:  elemTypeExpr,
						},
					},
				},
			}
			varDecls = append(varDecls, varDecl)

			guardedAssigns = append(guardedAssigns, &ast.AssignStmt{
				Lhs: []ast.Expr{ast.NewIdent(tempName)},
				Tok: token.ASSIGN,
				Rhs: []ast.Expr{
					&ast.CallExpr{
						Fun: &ast.SelectorExpr{
							X:   objExpr,
							Sel: ast.NewIdent("Get"),
						},
						Args: []ast.Expr{&ast.BasicLit{Kind: token.INT, Value: fmt.Sprintf("%d", argIndex)}},
					},
				},
			})

			// Transform the nested pattern using the temp variable
			nestedCond, nestedStmts, err := t.transformExpressionPatternWithType(exprPat.Expression(), ast.NewIdent(tempName), elemType)
			if err != nil {
				return nil, nil, err
			}
			guardedAssigns = append(guardedAssigns, nestedStmts...)
			if ident, ok := nestedCond.(*ast.Ident); !ok || ident.Name != "true" {
				conds = append(conds, nestedCond)
			}
		} else if typedPat, ok := patCtx.(*grammar.TypedPatternContext); ok {
			// Typed pattern: case Array(x: int, y: string, ...)
			varName := typedPat.Identifier().GetText()

			typeExpr, err := t.transformType(typedPat.Type_())
			if err != nil {
				return nil, nil, err
			}

			expectedType := t.resolveType(t.getBaseTypeName(typeExpr))
			t.currentScope.vals[varName] = false
			t.currentScope.valTypes[varName] = expectedType

			// var varName ExpectedType
			varDecl := &ast.DeclStmt{
				Decl: &ast.GenDecl{
					Tok: token.VAR,
					Specs: []ast.Spec{
						&ast.ValueSpec{
							Names: []*ast.Ident{ast.NewIdent(varName)},
							Type:  typeExpr,
						},
					},
				},
			}
			varDecls = append(varDecls, varDecl)

			// Generate: varName, okN := std.As[ExpectedType](obj.Get(i)) inside guard
			okName := t.nextTempVar()
			asCall := &ast.CallExpr{
				Fun: &ast.IndexExpr{
					X:     t.stdIdent("As"),
					Index: typeExpr,
				},
				Args: []ast.Expr{
					&ast.CallExpr{
						Fun: &ast.SelectorExpr{
							X:   objExpr,
							Sel: ast.NewIdent("Get"),
						},
						Args: []ast.Expr{&ast.BasicLit{Kind: token.INT, Value: fmt.Sprintf("%d", argIndex)}},
					},
				},
			}

			guardedAssigns = append(guardedAssigns, &ast.AssignStmt{
				Lhs: []ast.Expr{ast.NewIdent(varName), ast.NewIdent(okName)},
				Tok: token.DEFINE,
				Rhs: []ast.Expr{asCall},
			})
			conds = append(conds, ast.NewIdent(okName))
		}

		argIndex++
	}

	// Handle rest pattern if present and named
	if restPatternName != "" {
		t.currentScope.vals[restPatternName] = false
		t.currentScope.valTypes[restPatternName] = matchedType

		// var restPatternName MatchedType
		varDecl := &ast.DeclStmt{
			Decl: &ast.GenDecl{
				Tok: token.VAR,
				Specs: []ast.Spec{
					&ast.ValueSpec{
						Names: []*ast.Ident{ast.NewIdent(restPatternName)},
						Type:  t.typeToExpr(matchedType),
					},
				},
			},
		}
		varDecls = append(varDecls, varDecl)

		// rest = obj.SeqDrop(n).(MatchedType) inside guard
		guardedAssigns = append(guardedAssigns, &ast.AssignStmt{
			Lhs: []ast.Expr{ast.NewIdent(restPatternName)},
			Tok: token.ASSIGN,
			Rhs: []ast.Expr{
				&ast.TypeAssertExpr{
					X: &ast.CallExpr{
						Fun: &ast.SelectorExpr{
							X:   objExpr,
							Sel: ast.NewIdent("SeqDrop"),
						},
						Args: []ast.Expr{&ast.BasicLit{Kind: token.INT, Value: fmt.Sprintf("%d", nonRestCount)}},
					},
					Type: t.typeToExpr(matchedType),
				},
			},
		})
	}

	// Add variable declarations
	stmts = append(stmts, varDecls...)

	// Generate guarded assignment block: if sizeCheck { assignments... }
	if len(guardedAssigns) > 0 {
		guardedBlock := &ast.IfStmt{
			Cond: ast.NewIdent(sizeCheckName),
			Body: &ast.BlockStmt{
				List: guardedAssigns,
			},
		}
		stmts = append(stmts, guardedBlock)
	}

	t.needsStdImport = true

	// Combine all conditions
	if len(conds) == 0 {
		return ast.NewIdent("true"), stmts, nil
	}

	finalCond := conds[0]
	for i := 1; i < len(conds); i++ {
		finalCond = &ast.BinaryExpr{
			X:  finalCond,
			Op: token.LAND,
			Y:  conds[i],
		}
	}
	return finalCond, stmts, nil
}

// transformTuplePattern transforms a tuple pattern like (a, b, c) into direct field access.
// Instead of using reflection-based UnapplyTupleN, this generates direct access to V1, V2, etc.
func (t *galaASTTransformer) transformTuplePattern(patternExprs []grammar.IExpressionContext, objExpr ast.Expr, matchedType transpiler.Type) (ast.Expr, []ast.Stmt, error) {
	n := len(patternExprs)
	if n < 2 || n > 10 {
		return nil, nil, galaerr.NewSemanticError(fmt.Sprintf("tuple patterns must have 2-10 elements, got %d", n))
	}

	var stmts []ast.Stmt

	// Extract element types from matched type if available
	var elementTypes []transpiler.Type
	if genType, ok := matchedType.(transpiler.GenericType); ok {
		elementTypes = genType.Params
	}

	// Generate bindings for each pattern element using direct field access
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

		// Generate direct field access: objExpr.V{i+1}.Get()
		// Tuple fields are V1, V2, V3, etc.
		fieldName := fmt.Sprintf("V%d", i+1)
		elemExpr := &ast.CallExpr{
			Fun: &ast.SelectorExpr{
				X: &ast.SelectorExpr{
					X:   objExpr,
					Sel: ast.NewIdent(fieldName),
				},
				Sel: ast.NewIdent("Get"),
			},
		}

		// Check if this is a simple binding (identifier) or nested pattern
		if p := t.getPrimaryFromExpression(patExpr); p != nil && p.Identifier() != nil {
			// Simple binding: x := obj.V{i+1}.Get()
			// Note: .Get() already returns the concrete type, so no type assertion needed
			name := p.Identifier().GetText()
			t.currentScope.vals[name] = false
			t.currentScope.valTypes[name] = elemType

			assign := &ast.AssignStmt{
				Lhs: []ast.Expr{ast.NewIdent(name)},
				Tok: token.DEFINE,
				Rhs: []ast.Expr{elemExpr},
			}
			stmts = append(stmts, assign)
			continue
		}

		// Handle nested patterns recursively
		nestedCond, nestedStmts, err := t.transformExpressionPatternWithType(patExpr, elemExpr, elemType)
		if err != nil {
			return nil, nil, err
		}
		stmts = append(stmts, nestedStmts...)

		// Collect nested conditions - we'll AND them together at the end
		if ident, ok := nestedCond.(*ast.Ident); !ok || ident.Name != "true" {
			// Return the nested condition - caller will handle combining conditions
			t.needsStdImport = true
			return nestedCond, stmts, nil
		}
	}

	// All patterns are simple bindings or wildcards, condition is always true
	t.needsStdImport = true
	return ast.NewIdent("true"), stmts, nil
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

// inferExtractorTypeParams attempts to infer type parameters for a generic extractor
// by examining its Unapply method's first parameter type and matching it against
// the type of the expression being matched.
// For example, if Cons[T] has Unapply(l List[T]) and we're matching against List[int],
// this function will return [int] to instantiate Cons[int].
func (t *galaASTTransformer) inferExtractorTypeParams(extractorMeta *transpiler.TypeMetadata, matchedType transpiler.Type) []transpiler.Type {
	if extractorMeta == nil || len(extractorMeta.TypeParams) == 0 {
		return nil
	}

	// Get the Unapply method
	unapplyMeta, ok := extractorMeta.Methods["Unapply"]
	if !ok || len(unapplyMeta.ParamTypes) == 0 {
		return nil
	}

	// Get the first parameter type (the type we're matching against)
	unapplyParamType := unapplyMeta.ParamTypes[0]
	if unapplyParamType == nil || unapplyParamType.IsNil() {
		return nil
	}

	// Try to unify the parameter type with the matched type to infer type parameters
	// For example: unify List[T] with List[int] -> {T: int}
	substitution := make(map[string]transpiler.Type)
	if !t.unifyTypes(unapplyParamType, matchedType, extractorMeta.TypeParams, substitution) {
		return nil
	}

	// Build the result in the order of the extractor's type parameters
	result := make([]transpiler.Type, len(extractorMeta.TypeParams))
	for i, paramName := range extractorMeta.TypeParams {
		if inferredType, ok := substitution[paramName]; ok {
			result[i] = inferredType
		} else {
			// If we couldn't infer a type parameter, return nil to fall back to 'any'
			return nil
		}
	}

	return result
}

// unifyTypes attempts to unify two types and extract type parameter substitutions.
// pattern is a type that may contain type parameters (e.g., List[T])
// concrete is a concrete type (e.g., List[int])
// typeParams is the list of type parameter names to match
// substitution is populated with the inferred mappings (e.g., T -> int)
func (t *galaASTTransformer) unifyTypes(pattern, concrete transpiler.Type, typeParams []string, substitution map[string]transpiler.Type) bool {
	if pattern == nil || concrete == nil || pattern.IsNil() || concrete.IsNil() {
		return false
	}

	// Check if pattern is a type parameter
	patternStr := pattern.String()
	for _, tp := range typeParams {
		if patternStr == tp {
			// This is a type parameter - record the substitution
			if existing, ok := substitution[tp]; ok {
				// Already have a substitution - check consistency
				return existing.String() == concrete.String()
			}
			substitution[tp] = concrete
			return true
		}
	}

	// Check if both are generic types
	patternGen, patternIsGen := pattern.(transpiler.GenericType)
	concreteGen, concreteIsGen := concrete.(transpiler.GenericType)

	if patternIsGen && concreteIsGen {
		// Both are generic - check base types match and unify parameters
		// Normalize base type names to handle package prefixes (e.g., "Option" vs "std.Option")
		patternBase := patternGen.Base.BaseName()
		concreteBase := concreteGen.Base.BaseName()
		if patternBase != concreteBase {
			return false
		}
		if len(patternGen.Params) != len(concreteGen.Params) {
			return false
		}
		for i := range patternGen.Params {
			if !t.unifyTypes(patternGen.Params[i], concreteGen.Params[i], typeParams, substitution) {
				return false
			}
		}
		return true
	}

	// For non-generic types, check for match using BaseName to handle package prefixes
	return pattern.BaseName() == concrete.BaseName()
}

// getGenericExtractorResultTypeWithArgs determines the extracted type for a generic extractor.
// For example, Cons[T] with Unapply(l List[T]) Option[Tuple[T, List[T]]] - when matching
// against List[int], this returns Tuple[int, List[int]] for index 0.
// numArgs is the total number of arguments in the pattern (-1 means use default behavior).
// If numArgs > 1 and the result is a Tuple with matching arity, individual elements are returned.
func (t *galaASTTransformer) getGenericExtractorResultTypeWithArgs(extractorName string, objType transpiler.Type, index int, numArgs int) transpiler.Type {
	// Resolve the extractor type name using the same logic as resolveStructTypeName
	resolvedName := t.resolveTypeMetaName(extractorName)
	if resolvedName == "" {
		return nil
	}

	// Look up the extractor's type metadata
	extractorMeta, ok := t.typeMetas[resolvedName]
	if !ok || extractorMeta == nil || len(extractorMeta.TypeParams) == 0 {
		return nil
	}

	// Get the Unapply method
	unapplyMeta, ok := extractorMeta.Methods["Unapply"]
	if !ok || len(unapplyMeta.ParamTypes) == 0 {
		return nil
	}

	// Infer type parameters from the matched type
	inferredTypes := t.inferExtractorTypeParams(extractorMeta, objType)
	if len(inferredTypes) != len(extractorMeta.TypeParams) {
		return nil
	}

	// Substitute type parameters in the return type
	returnType := t.substituteConcreteTypes(unapplyMeta.ReturnType, extractorMeta.TypeParams, inferredTypes)
	if returnType == nil || returnType.IsNil() {
		return nil
	}

	// Unwrap Option[X] to get X
	innerType := t.unwrapOptionType(returnType)
	if innerType == nil || innerType.IsNil() {
		return nil
	}

	// Check if the result is a Tuple
	if genType, ok := innerType.(transpiler.GenericType); ok {
		baseName := genType.Base.BaseName()
		if t.isTupleTypeName(baseName) || baseName == "Tuple" || baseName == "std.Tuple" {
			// If numArgs matches the Tuple arity, expand the Tuple
			// This handles implicit expansion: Cons(head, tail) -> [int, List[int]]
			if numArgs > 0 && numArgs == len(genType.Params) {
				if index < len(genType.Params) {
					return genType.Params[index]
				}
				return nil
			}
			// If numArgs is 1, return the full Tuple type for explicit Tuple matching
			// This handles: Cons(Tuple(head, tail)) -> Tuple[int, List[int]]
			if numArgs == 1 && index == 0 {
				return innerType
			}
			// Default behavior (numArgs == -1): index 0 returns full Tuple, others expand
			if numArgs < 0 {
				if index == 0 {
					return innerType
				}
				if index < len(genType.Params) {
					return genType.Params[index]
				}
			}
		}
	}

	// For non-tuple results, return the inner type for index 0
	if index == 0 {
		return innerType
	}

	return nil
}

// unwrapOptionType unwraps Option[X] to return X
func (t *galaASTTransformer) unwrapOptionType(typ transpiler.Type) transpiler.Type {
	genType, ok := typ.(transpiler.GenericType)
	if !ok {
		return nil
	}
	baseName := genType.Base.BaseName()
	if baseName == "Option" || baseName == "std.Option" {
		if len(genType.Params) > 0 {
			return genType.Params[0]
		}
	}
	return nil
}

// isDirectUnapplyReturnType returns true if the return type of an Unapply method
// can be handled directly without reflection. Supported types are:
// - bool (guard pattern)
// - Option[T] (extractor pattern)
func (t *galaASTTransformer) isDirectUnapplyReturnType(typ transpiler.Type) bool {
	if typ == nil || typ.IsNil() {
		return false
	}
	// Check for bool
	if basic, ok := typ.(transpiler.BasicType); ok && basic.Name == "bool" {
		return true
	}
	// Check for Option[T]
	if genType, ok := typ.(transpiler.GenericType); ok {
		baseName := genType.Base.BaseName()
		if baseName == "Option" || baseName == "std.Option" {
			return true
		}
	}
	return false
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

// generateDirectUnapplyPattern generates reflection-free code for generic extractors.
// Instead of using std.UnapplyFull (which uses reflection), this generates direct method calls:
//
//	_tmp_opt := Cons[int]{}.Unapply(list)
//	_tmp_ok := _tmp_opt.IsDefined()
//	var _tmp_tuple std.Tuple[int, List[int]]
//	if _tmp_ok {
//	    _tmp_tuple = _tmp_opt.Get()
//	}
//	head := _tmp_tuple.V1
//	tail := _tmp_tuple.V2
//	if _tmp_ok { ... body }
//
// This eliminates reflection from: UnapplyFull, UnapplyTuple, GetSafe, and As.
// The .Get() is guarded by IsDefined() to prevent panics.
func (t *galaASTTransformer) generateDirectUnapplyPattern(
	extractorName string,
	extractorMeta *transpiler.TypeMetadata,
	inferredTypes []transpiler.Type,
	unapplyMeta *transpiler.MethodMetadata,
	objExpr ast.Expr,
	argList *grammar.ArgumentListContext,
	matchedType transpiler.Type,
) (ast.Expr, []ast.Stmt, error) {

	var allBindings []ast.Stmt
	var conds []ast.Expr

	// Build the extractor type expression with inferred type parameters
	// e.g., Cons[int]{}
	extractorTypeExpr := t.ident(extractorName)
	if len(inferredTypes) == 1 {
		extractorTypeExpr = &ast.IndexExpr{X: extractorTypeExpr, Index: t.typeToExpr(inferredTypes[0])}
	} else if len(inferredTypes) > 1 {
		indices := make([]ast.Expr, len(inferredTypes))
		for i, tp := range inferredTypes {
			indices[i] = t.typeToExpr(tp)
		}
		extractorTypeExpr = &ast.IndexListExpr{X: extractorTypeExpr, Indices: indices}
	}

	// Get the return type of Unapply and substitute type parameters
	// e.g., Option[Tuple[T, List[T]]] -> Option[Tuple[int, List[int]]]
	returnType := t.substituteConcreteTypes(unapplyMeta.ReturnType, extractorMeta.TypeParams, inferredTypes)

	// Check if Unapply returns bool (guard pattern) or Option[T] (extractor pattern)
	isBoolReturn := false
	if basic, ok := returnType.(transpiler.BasicType); ok && basic.Name == "bool" {
		isBoolReturn = true
	}

	// Generate: _tmp_result := Extractor[T]{}.Unapply(obj)
	resultName := t.nextTempVar()
	unapplyCall := &ast.AssignStmt{
		Lhs: []ast.Expr{ast.NewIdent(resultName)},
		Tok: token.DEFINE,
		Rhs: []ast.Expr{
			&ast.CallExpr{
				Fun: &ast.SelectorExpr{
					X:   &ast.CompositeLit{Type: extractorTypeExpr},
					Sel: ast.NewIdent("Unapply"),
				},
				Args: []ast.Expr{objExpr},
			},
		},
	}
	allBindings = append(allBindings, unapplyCall)

	var okName string
	var innerType transpiler.Type

	if isBoolReturn {
		// For bool-returning extractors, the result IS the condition
		// No inner value to extract
		okName = resultName
		conds = append(conds, ast.NewIdent(okName))
		innerType = transpiler.NilType{}
	} else {
		// For Option-returning extractors, check IsDefined and extract inner value
		// Generate: _tmp_ok := _tmp_result.IsDefined()
		okName = t.nextTempVar()
		isDefinedAssign := &ast.AssignStmt{
			Lhs: []ast.Expr{ast.NewIdent(okName)},
			Tok: token.DEFINE,
			Rhs: []ast.Expr{
				&ast.CallExpr{
					Fun: &ast.SelectorExpr{
						X:   ast.NewIdent(resultName),
						Sel: ast.NewIdent("IsDefined"),
					},
				},
			},
		}
		allBindings = append(allBindings, isDefinedAssign)
		conds = append(conds, ast.NewIdent(okName))

		// Unwrap Option to get the inner type
		innerType = t.unwrapOptionType(returnType)
	}

	// Handle the pattern arguments
	if argList != nil && len(argList.AllArgument()) > 0 && !isBoolReturn {
		numArgs := len(argList.AllArgument())

		// Check if the inner type is a Tuple that needs expansion
		isTupleResult := false
		var tupleParamTypes []transpiler.Type
		if genType, ok := innerType.(transpiler.GenericType); ok {
			baseName := genType.Base.BaseName()
			if t.isTupleTypeName(baseName) || baseName == "Tuple" || baseName == "std.Tuple" {
				isTupleResult = true
				tupleParamTypes = genType.Params
			}
		}

		// Generate a guarded .Get() call:
		// var _tmp_inner InnerType
		// if _tmp_ok { _tmp_inner = _tmp_result.Get() }
		innerName := t.nextTempVar()

		// Declare the variable with its type
		innerTypeExpr := t.typeToExpr(innerType)
		if innerTypeExpr == nil {
			innerTypeExpr = ast.NewIdent("any")
		}

		varDecl := &ast.DeclStmt{
			Decl: &ast.GenDecl{
				Tok: token.VAR,
				Specs: []ast.Spec{
					&ast.ValueSpec{
						Names: []*ast.Ident{ast.NewIdent(innerName)},
						Type:  innerTypeExpr,
					},
				},
			},
		}
		allBindings = append(allBindings, varDecl)

		// Generate: if _tmp_ok { _tmp_inner = _tmp_result.Get() }
		guardedGet := &ast.IfStmt{
			Cond: ast.NewIdent(okName),
			Body: &ast.BlockStmt{
				List: []ast.Stmt{
					&ast.AssignStmt{
						Lhs: []ast.Expr{ast.NewIdent(innerName)},
						Tok: token.ASSIGN,
						Rhs: []ast.Expr{
							&ast.CallExpr{
								Fun: &ast.SelectorExpr{
									X:   ast.NewIdent(resultName),
									Sel: ast.NewIdent("Get"),
								},
							},
						},
					},
				},
			},
		}
		allBindings = append(allBindings, guardedGet)

		// For each argument pattern, generate direct field access
		for i, argCtx := range argList.AllArgument() {
			arg := argCtx.(*grammar.ArgumentContext)
			patternText := arg.Pattern().GetText()

			if patternText == "_" {
				continue
			}

			// Determine the type and access expression for this element
			var elemType transpiler.Type
			var elemExpr ast.Expr

			if isTupleResult && numArgs > 1 && numArgs == len(tupleParamTypes) {
				// Implicit tuple expansion: Cons(head, tail) -> access tuple.V1, tuple.V2
				if i < len(tupleParamTypes) {
					elemType = tupleParamTypes[i]
				}
				// Access tuple field directly: _tmp_inner.V1.Get(), _tmp_inner.V2.Get(), etc.
				// Note: Tuple fields are stored as Immutable[T], so we need to call .Get() to unwrap
				fieldName := fmt.Sprintf("V%d", i+1)
				elemExpr = &ast.CallExpr{
					Fun: &ast.SelectorExpr{
						X: &ast.SelectorExpr{
							X:   ast.NewIdent(innerName),
							Sel: ast.NewIdent(fieldName),
						},
						Sel: ast.NewIdent("Get"),
					},
				}
			} else if isTupleResult && numArgs == 1 {
				// Explicit Tuple pattern: Cons(Tuple(head, tail)) -> return full tuple
				elemType = innerType
				elemExpr = ast.NewIdent(innerName)
			} else {
				// Single value extraction (not a tuple)
				elemType = innerType
				elemExpr = ast.NewIdent(innerName)
			}

			// Check if this is a simple identifier binding
			if t.isSimpleIdentifier(patternText) {
				varName := patternText
				t.currentScope.vals[varName] = false
				if elemType != nil && !elemType.IsNil() {
					t.currentScope.valTypes[varName] = elemType
				} else {
					t.currentScope.valTypes[varName] = transpiler.BasicType{Name: "any"}
				}

				// Generate: varName := elemExpr
				varAssign := &ast.AssignStmt{
					Lhs: []ast.Expr{ast.NewIdent(varName)},
					Tok: token.DEFINE,
					Rhs: []ast.Expr{elemExpr},
				}
				allBindings = append(allBindings, varAssign)
			} else {
				// Handle nested patterns recursively
				subCond, subBindings, err := t.transformPatternWithType(arg.Pattern(), elemExpr, elemType)
				if err != nil {
					return nil, nil, err
				}
				allBindings = append(allBindings, subBindings...)
				// Add sub-condition to the list of conditions to check
				if subCond != nil {
					// Check if subCond is just "true" - if so, skip it
					if ident, ok := subCond.(*ast.Ident); !ok || ident.Name != "true" {
						conds = append(conds, subCond)
					}
				}
			}
		}
	}

	// Build final condition by ANDing all conditions
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
