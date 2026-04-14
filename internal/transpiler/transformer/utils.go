package transformer

import (
	"fmt"
	"go/ast"
	"martianoff/gala/internal/transpiler/registry"
	"strings"
)

// stripStdPrefix removes the "std." prefix from a type/package name if present.
func stripStdPrefix(name string) string {
	return strings.TrimPrefix(name, registry.StdPackageName+".")
}

// hasStdPrefix checks whether a type/package name starts with "std.".
func hasStdPrefix(name string) bool {
	return strings.HasPrefix(name, registry.StdPackageName+".")
}

// withStdPrefix prepends "std." to a name.
func withStdPrefix(name string) string {
	return registry.StdPackageName + "." + name
}

// isWildcard checks if a pattern text is the wildcard pattern "_".
func isWildcard(text string) bool {
	return text == "_"
}

// isBindingPattern checks if a pattern text represents a variable binding
// (a simple lowercase identifier that will bind the matched value).
// This is a catch-all pattern, like `case body => ...` in Scala/GALA.
// Constructor calls like `Some(x)`, literals like `""` or `42`,
// and keywords like `true`/`false`/`nil` are NOT bindings.
func isBindingPattern(text string) bool {
	if len(text) == 0 || text == "_" {
		return false
	}
	// Must start with a lowercase letter or underscore (uppercase = constructor)
	if !((text[0] >= 'a' && text[0] <= 'z') || text[0] == '_') {
		return false
	}
	// Must be a simple identifier (no parens, dots, quotes, operators)
	for _, ch := range text {
		if !((ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') || ch == '_') {
			return false
		}
	}
	// Exclude keywords and built-in constants
	switch text {
	case "true", "false", "nil", "iota":
		return false
	}
	return true
}

// isDefaultPattern checks if a pattern is a catch-all: either `_` or a variable binding.
func isDefaultPattern(text string) bool {
	return isWildcard(text) || isBindingPattern(text)
}

// isLiteralTrue checks if an expression is the literal `true` identifier.
func isLiteralTrue(expr ast.Expr) bool {
	if id, ok := expr.(*ast.Ident); ok {
		return id.Name == "true"
	}
	return false
}

// extractBindingDefault checks if a match clause is a catch-all binding pattern
// (generates condition `true`). If so, returns the flattened body statements
// (bindings + the if-true body) suitable for use as an else branch.
// Returns nil if the clause is not a catch-all.
func extractBindingDefault(clause ast.Stmt) []ast.Stmt {
	// Case 1: direct `if true { ... }`
	if ifStmt, ok := clause.(*ast.IfStmt); ok && isLiteralTrue(ifStmt.Cond) {
		return ifStmt.Body.List
	}
	// Case 2: BlockStmt containing [bindings..., if true { ... }]
	if block, ok := clause.(*ast.BlockStmt); ok && len(block.List) > 0 {
		last := block.List[len(block.List)-1]
		if ifStmt, ok := last.(*ast.IfStmt); ok && isLiteralTrue(ifStmt.Cond) {
			// Combine bindings with the if-true body
			result := make([]ast.Stmt, 0, len(block.List)-1+len(ifStmt.Body.List))
			result = append(result, block.List[:len(block.List)-1]...) // bindings
			result = append(result, ifStmt.Body.List...)               // body
			return result
		}
	}
	return nil
}

func (t *galaASTTransformer) nextTempVar() string {
	t.tempVarCount++
	return fmt.Sprintf("_tmp_%d", t.tempVarCount)
}

func (t *galaASTTransformer) nextTupleID() int {
	t.tempVarCount++
	return t.tempVarCount
}

func (t *galaASTTransformer) isNoneCall(expr ast.Expr) bool {
	call, ok := expr.(*ast.CallExpr)
	if !ok {
		return false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	x, ok := sel.X.(*ast.Ident)
	if !ok {
		return false
	}
	return registry.Global.IsPreludePackage(x.Name) && sel.Sel.Name == "None"
}

func (t *galaASTTransformer) stdIdent(name string) ast.Expr {
	// If we're in the std package, no prefix needed
	if t.packageName == registry.StdPackageName {
		return ast.NewIdent(name)
	}
	// If std is dot-imported, no prefix needed
	if t.importManager.IsDotImported(registry.StdPackageName) {
		return ast.NewIdent(name)
	}
	// Otherwise, need the std. prefix and import
	t.needsStdImport = true
	return &ast.SelectorExpr{
		X:   ast.NewIdent(registry.StdPackageName),
		Sel: ast.NewIdent(name),
	}
}

func (t *galaASTTransformer) ident(name string) ast.Expr {
	if idx := strings.Index(name, "."); idx != -1 {
		pkg := name[:idx]
		base := name[idx+1:]
		if pkg == t.packageName {
			return ast.NewIdent(base)
		}
		// Check if it's dot-imported
		if t.importManager.IsDotImported(pkg) {
			t.markDotImportUsed(pkg)
			return ast.NewIdent(base)
		}
		// Check if we have an alias for this actual package name
		if alias, ok := t.importManager.GetAlias(pkg); ok {
			pkg = alias
		}
		return &ast.SelectorExpr{
			X:   ast.NewIdent(pkg),
			Sel: ast.NewIdent(base),
		}
	}
	return ast.NewIdent(name)
}

// qualifyTypeExpr recursively transforms a type expression to ensure std types
// are properly qualified with the std. prefix. This is needed for type arguments
// in generic function calls like Unfold[int, Tuple[int, int]].
func (t *galaASTTransformer) qualifyTypeExpr(expr ast.Expr) ast.Expr {
	switch e := expr.(type) {
	case *ast.Ident:
		// Check if this is a std type
		if registry.IsStdType(e.Name) {
			return t.stdIdent(e.Name)
		}
		return e
	case *ast.IndexExpr:
		// Generic type with single param: T[A]
		return &ast.IndexExpr{
			X:     t.qualifyTypeExpr(e.X),
			Index: t.qualifyTypeExpr(e.Index),
		}
	case *ast.IndexListExpr:
		// Generic type with multiple params: T[A, B]
		indices := make([]ast.Expr, len(e.Indices))
		for i, idx := range e.Indices {
			indices[i] = t.qualifyTypeExpr(idx)
		}
		return &ast.IndexListExpr{
			X:       t.qualifyTypeExpr(e.X),
			Indices: indices,
		}
	case *ast.StarExpr:
		// Pointer type: *T
		return &ast.StarExpr{X: t.qualifyTypeExpr(e.X)}
	case *ast.ArrayType:
		// Array/slice type: []T
		return &ast.ArrayType{
			Len: e.Len,
			Elt: t.qualifyTypeExpr(e.Elt),
		}
	case *ast.MapType:
		// Map type: map[K]V
		return &ast.MapType{
			Key:   t.qualifyTypeExpr(e.Key),
			Value: t.qualifyTypeExpr(e.Value),
		}
	case *ast.SelectorExpr:
		// Already qualified: pkg.Type
		return e
	case *ast.FuncType:
		// Function type: func(A) B
		return t.qualifyFuncType(e)
	default:
		return e
	}
}

// qualifyTypeExprs transforms a slice of type expressions
func (t *galaASTTransformer) qualifyTypeExprs(exprs []ast.Expr) []ast.Expr {
	result := make([]ast.Expr, len(exprs))
	for i, expr := range exprs {
		result[i] = t.qualifyTypeExpr(expr)
	}
	return result
}

// qualifyTypeArgsInExpr qualifies type arguments in an expression if it's an IndexExpr
// or IndexListExpr (e.g., Unfold[int, Tuple[int, int]] -> Unfold[int, std.Tuple[int, int]])
func (t *galaASTTransformer) qualifyTypeArgsInExpr(expr ast.Expr) ast.Expr {
	switch e := expr.(type) {
	case *ast.IndexExpr:
		return &ast.IndexExpr{
			X:     t.qualifyTypeArgsInExpr(e.X),
			Index: t.qualifyTypeExpr(e.Index),
		}
	case *ast.IndexListExpr:
		return &ast.IndexListExpr{
			X:       t.qualifyTypeArgsInExpr(e.X),
			Indices: t.qualifyTypeExprs(e.Indices),
		}
	case *ast.SelectorExpr:
		// For selector expressions like stream.Of, don't qualify the base
		return e
	default:
		return expr
	}
}

// qualifyFuncType transforms function type to ensure std types are qualified
func (t *galaASTTransformer) qualifyFuncType(ft *ast.FuncType) *ast.FuncType {
	var params, results *ast.FieldList
	if ft.Params != nil {
		fields := make([]*ast.Field, len(ft.Params.List))
		for i, f := range ft.Params.List {
			fields[i] = &ast.Field{
				Names: f.Names,
				Type:  t.qualifyTypeExpr(f.Type),
			}
		}
		params = &ast.FieldList{List: fields}
	}
	if ft.Results != nil {
		fields := make([]*ast.Field, len(ft.Results.List))
		for i, f := range ft.Results.List {
			fields[i] = &ast.Field{
				Names: f.Names,
				Type:  t.qualifyTypeExpr(f.Type),
			}
		}
		results = &ast.FieldList{List: fields}
	}
	return &ast.FuncType{
		Params:  params,
		Results: results,
	}
}

// stripTypeNameDecorations returns the bare type name from a stringified
// transpiler.Type, with generic parameters and leading pointer markers removed.
// For example, "*List[int]" -> "List", "Tuple[A,B]" -> "Tuple", "Person" -> "Person".
//
// This is the single authoritative helper for the "strip generics + unwrap pointer"
// pattern previously duplicated inline across postfix.go, type_inference_calls.go,
// and statements.go. Callers that need a lookup key into structFields, typeMetas,
// or companion registries should route through this function.
func stripTypeNameDecorations(typeName string) string {
	base := typeName
	if idx := strings.Index(typeName, "["); idx != -1 {
		base = typeName[:idx]
	}
	return strings.TrimPrefix(base, "*")
}

// warnInference records a type inference warning (only when GALA_WARN_TYPES=1).
func (t *galaASTTransformer) warnInference(format string, args ...interface{}) {
	if t.warnTypeInference {
		t.inferenceWarnings = append(t.inferenceWarnings, fmt.Sprintf(format, args...))
	}
}

// suggestExtractorName returns the nearest known extractor (companion object or
// type with Unapply) to the given name, or "" if nothing is close enough.
// Uses a simple case-insensitive prefix/substring match first, then falls back
// to Levenshtein distance <= 2.
func (t *galaASTTransformer) suggestExtractorName(name string) string {
	if name == "" {
		return ""
	}
	candidates := make([]string, 0, len(t.companionObjects)+len(t.typeMetas))
	for k := range t.companionObjects {
		if idx := strings.LastIndex(k, "."); idx >= 0 {
			candidates = append(candidates, k[idx+1:])
		} else {
			candidates = append(candidates, k)
		}
	}
	for k, m := range t.typeMetas {
		if m == nil {
			continue
		}
		if _, ok := m.Methods["Unapply"]; !ok {
			continue
		}
		if idx := strings.LastIndex(k, "."); idx >= 0 {
			candidates = append(candidates, k[idx+1:])
		} else {
			candidates = append(candidates, k)
		}
	}
	bestName := ""
	bestDist := 3
	lowerIn := strings.ToLower(name)
	for _, c := range candidates {
		if c == "" || c == name {
			continue
		}
		if strings.EqualFold(c, name) {
			return c
		}
		if strings.HasPrefix(strings.ToLower(c), lowerIn) || strings.HasPrefix(lowerIn, strings.ToLower(c)) {
			return c
		}
		d := levenshteinDistance(lowerIn, strings.ToLower(c))
		if d < bestDist {
			bestDist = d
			bestName = c
		}
	}
	return bestName
}

// levenshteinDistance returns the edit distance between two strings.
func levenshteinDistance(a, b string) int {
	if len(a) == 0 {
		return len(b)
	}
	if len(b) == 0 {
		return len(a)
	}
	prev := make([]int, len(b)+1)
	curr := make([]int, len(b)+1)
	for j := 0; j <= len(b); j++ {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		curr[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			del := prev[j] + 1
			ins := curr[j-1] + 1
			sub := prev[j-1] + cost
			m := del
			if ins < m {
				m = ins
			}
			if sub < m {
				m = sub
			}
			curr[j] = m
		}
		prev, curr = curr, prev
	}
	return prev[len(b)]
}

// parenthesizeCompositeLits wraps any ast.CompositeLit found in an expression
// tree with ast.ParenExpr. This is needed because Go's parser treats '{' in
// if/for/switch conditions as the start of the block body, making composite
// literals like OPTIONS{}.Apply() ambiguous. Wrapping in parens resolves this.
func parenthesizeCompositeLits(expr ast.Expr) ast.Expr {
	if expr == nil {
		return nil
	}
	switch e := expr.(type) {
	case *ast.CompositeLit:
		return &ast.ParenExpr{X: e}
	case *ast.CallExpr:
		e.Fun = parenthesizeCompositeLits(e.Fun)
		for i, arg := range e.Args {
			e.Args[i] = parenthesizeCompositeLits(arg)
		}
		return e
	case *ast.SelectorExpr:
		e.X = parenthesizeCompositeLits(e.X)
		return e
	case *ast.BinaryExpr:
		e.X = parenthesizeCompositeLits(e.X)
		e.Y = parenthesizeCompositeLits(e.Y)
		return e
	case *ast.UnaryExpr:
		e.X = parenthesizeCompositeLits(e.X)
		return e
	case *ast.ParenExpr:
		e.X = parenthesizeCompositeLits(e.X)
		return e
	case *ast.IndexExpr:
		e.X = parenthesizeCompositeLits(e.X)
		e.Index = parenthesizeCompositeLits(e.Index)
		return e
	case *ast.StarExpr:
		e.X = parenthesizeCompositeLits(e.X)
		return e
	case *ast.TypeAssertExpr:
		e.X = parenthesizeCompositeLits(e.X)
		return e
	case *ast.KeyValueExpr:
		e.Value = parenthesizeCompositeLits(e.Value)
		return e
	default:
		return expr
	}
}

func findLeafIf(stmt ast.Stmt) *ast.IfStmt {
	switch s := stmt.(type) {
	case *ast.IfStmt:
		return s
	case *ast.BlockStmt:
		if len(s.List) > 0 {
			return findLeafIf(s.List[len(s.List)-1])
		}
	}
	return nil
}
