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
		if parserCtx, ok := ctx.(antlr.ParserRuleContext); ok {
			return nil, "", nil, t.semanticErrorAt(parserCtx, "cannot infer type of matched expression. Please add explicit type annotation to the variable being matched")
		}
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
		if isWildcard(patternText) {
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

	for i := 3; i < ctx.GetChildCount()-1; i++ {
		ccCtx, ok := ctx.GetChild(i).(*grammar.CaseClauseContext)
		if !ok {
			continue
		}

		patCtx := ccCtx.Pattern()
		patternText := patCtx.GetText()
		if isWildcard(patternText) {
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

	// Always collect variant patterns for exhaustiveness check
	var variantPatterns []string
	for i := 3; i < ctx.GetChildCount()-1; i++ {
		ccCtx, ok := ctx.GetChild(i).(*grammar.CaseClauseContext)
		if !ok {
			continue
		}
		pat := ccCtx.Pattern().GetText()
		if !isWildcard(pat) {
			variantPatterns = append(variantPatterns, pat)
		}
	}

	isSealed, isExhaustive, missing := t.isExhaustiveMatch(matchedType, variantPatterns)

	if !foundDefault {
		if isSealed && !isExhaustive {
			return nil, nil, nil, galaerr.NewSemanticError(
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
			return nil, nil, nil, galaerr.NewSemanticError("match expression must have a default case (case _ => ...)")
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
			// Complete inference failure — no branch could be typed.
			// Fall back to the enclosing function/lambda's declared return type.
			// This handles cases where branches call methods from pure Go packages
			// whose return types aren't in the GALA type metadata.
			// Safety: if the fallback type is wrong, the Go compiler will catch it.
			if t.currentFuncReturnType != nil && !t.currentFuncReturnType.IsNil() {
				t.traceType(nil, t.currentFuncReturnType, "match-result-fallback-to-enclosing-return")
				return t.currentFuncReturnType, nil
			}
			return nil, galaerr.NewSemanticError("cannot infer result type of match expression: no branch returns a concrete type. Please add explicit type annotation")
		}
		// Type parameters or mixed type-param/nil: use 'any' as the Go type erasure
		t.warnInference("match expression defaulting to 'any' return type (all branches are type parameters)")
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
			return nil, galaerr.NewSemanticError(msg)
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

	// Check dot-import equivalence: "Duration" should match "time_utils.Duration"
	// when time_utils is dot-imported
	s1, s2 := t1.String(), t2.String()
	if strings.Contains(s2, ".") && !strings.Contains(s1, ".") {
		// s2 is qualified (pkg.Type), s1 is bare (Type)
		if pkg := s2[:strings.Index(s2, ".")]; t.importManager.IsDotImported(pkg) {
			if s2[strings.Index(s2, ".")+1:] == s1 {
				return true
			}
		}
	}
	if strings.Contains(s1, ".") && !strings.Contains(s2, ".") {
		// s1 is qualified (pkg.Type), s2 is bare (Type)
		if pkg := s1[:strings.Index(s1, ".")]; t.importManager.IsDotImported(pkg) {
			if s1[strings.Index(s1, ".")+1:] == s2 {
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
		// If resultType is still nil but body is non-empty, this is a void (side-effect) branch
		// (e.g., last statement is an assignment like `items = items.Append(v)`)
		if resultType == nil && len(body) > 0 {
			resultType = transpiler.VoidType{}
		}
	} else if ctx.GetBody() != nil {
		expr, err := t.transformExpression(ctx.GetBody())
		if err != nil {
			return nil, nil, err
		}
		resultType = t.inferResultType(expr)
		body = []ast.Stmt{&ast.ReturnStmt{Results: []ast.Expr{expr}}}
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
				return nil, nil, galaerr.NewSemanticError(
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

// Pattern transformation functions moved to patterns.go
