package transformer

import (
	"go/ast"
	"go/token"
	"martianoff/gala/galaerr"
	"martianoff/gala/internal/parser/grammar"
	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/registry"
	"strings"
)

func (t *galaASTTransformer) transformType(ctx grammar.ITypeContext) (ast.Expr, error) {
	if ctx == nil {
		return nil, nil
	}
	// Handle qualified identifier types (e.g., std.Option[T] or just Option[T])
	if ctx.QualifiedIdentifier() != nil {
		qid := ctx.QualifiedIdentifier().(*grammar.QualifiedIdentifierContext)
		identifiers := qid.AllIdentifier()

		var ident ast.Expr
		if len(identifiers) == 1 {
			// Simple type name
			typeName := identifiers[0].GetText()
			if typeName == "_" {
				return ast.NewIdent("any"), nil
			}
			ident = ast.NewIdent(typeName)
			// Use resolution to determine if this type belongs to an imported package
			resolvedType := t.getType(typeName)
			if !resolvedType.IsNil() {
				if pkg := resolvedType.GetPackage(); pkg != "" && pkg != t.packageName {
					// Type belongs to an imported package, use package-qualified identifier
					if pkg == registry.StdPackageName {
						ident = t.stdIdent(typeName)
					} else {
						// Check if this is a dot import - if so, don't qualify
						if !t.importManager.IsDotImported(pkg) {
							if alias, ok := t.importManager.GetAlias(pkg); ok {
								ident = &ast.SelectorExpr{X: ast.NewIdent(alias), Sel: ast.NewIdent(typeName)}
							}
						}
					}
				}
			}
		} else {
			// Qualified type name (e.g., std.Option)
			// Build selector expression from left to right
			ident = ast.NewIdent(identifiers[0].GetText())
			for i := 1; i < len(identifiers); i++ {
				ident = &ast.SelectorExpr{X: ident, Sel: ast.NewIdent(identifiers[i].GetText())}
			}
		}

		if ctx.TypeArguments() != nil {
			// Generic type: T[A, B] -> *ast.IndexExpr or *ast.IndexListExpr
			args := ctx.TypeArguments().(*grammar.TypeArgumentsContext).TypeList().(*grammar.TypeListContext).AllType_()
			var argExprs []ast.Expr
			for _, arg := range args {
				ae, err := t.transformType(arg)
				if err != nil {
					return nil, err
				}
				argExprs = append(argExprs, ae)
			}

			if len(argExprs) == 1 {
				return &ast.IndexExpr{X: ident, Index: argExprs[0]}, nil
			} else {
				return &ast.IndexListExpr{X: ident, Indices: argExprs}, nil
			}
		}
		return ident, nil
	}

	txt := ctx.GetText()
	if strings.HasPrefix(txt, "*") && len(ctx.AllType_()) > 0 {
		typ, err := t.transformType(ctx.Type_(0))
		if err != nil {
			return nil, err
		}
		return &ast.StarExpr{X: typ}, nil
	}
	if strings.HasPrefix(txt, "[]") && len(ctx.AllType_()) > 0 {
		typ, err := t.transformType(ctx.Type_(0))
		if err != nil {
			return nil, err
		}
		return &ast.ArrayType{Elt: typ}, nil
	}

	// Handle map types: map[K]V
	if strings.HasPrefix(txt, "map[") && len(ctx.AllType_()) >= 2 {
		keyType, err := t.transformType(ctx.Type_(0))
		if err != nil {
			return nil, err
		}
		valueType, err := t.transformType(ctx.Type_(1))
		if err != nil {
			return nil, err
		}
		return &ast.MapType{Key: keyType, Value: valueType}, nil
	}

	// Handle function types: func(params) results
	if ctx.Signature() != nil {
		sig := ctx.Signature().(*grammar.SignatureContext)
		funcType, err := t.transformFuncTypeSignature(sig)
		if err != nil {
			return nil, err
		}
		return funcType, nil
	}

	return ast.NewIdent(txt), nil
}

func (t *galaASTTransformer) getExprType(expr ast.Expr) ast.Expr {
	if expr == nil {
		return ast.NewIdent("any")
	}
	switch e := expr.(type) {
	case *ast.Ident:
		if e.Name == "true" || e.Name == "false" {
			return ast.NewIdent("bool")
		}
		typ := t.getType(e.Name)
		if !typ.IsNil() {
			return t.typeToExpr(typ)
		}
	case *ast.BinaryExpr:
		switch e.Op {
		case token.LOR, token.LAND, token.EQL, token.NEQ, token.LSS, token.LEQ, token.GTR, token.GEQ:
			return ast.NewIdent("bool")
		default:
			return t.getExprType(e.X)
		}
	case *ast.UnaryExpr:
		if e.Op == token.NOT {
			return ast.NewIdent("bool")
		}
	}
	typ := t.getExprTypeName(expr)
	if !typ.IsNil() {
		return t.typeToExpr(typ)
	}
	return ast.NewIdent("any")
}

// isPrimitiveType is an alias for transpiler.IsPrimitiveType for local use.
func isPrimitiveType(name string) bool {
	return transpiler.IsPrimitiveType(name)
}

// isKnownStdType checks if a type name is a known standard library type
// that should always be prefixed with std.
func (t *galaASTTransformer) isKnownStdType(name string) bool {
	return registry.IsStdType(name)
}

func (t *galaASTTransformer) typeToExpr(typ transpiler.Type) ast.Expr {
	if typ.IsNil() {
		return ast.NewIdent("any")
	}
	switch v := typ.(type) {
	case transpiler.BasicType:
		// Check if this is a known std type without package prefix
		if t.isKnownStdType(v.Name) {
			return t.stdIdent(v.Name)
		}
		return ast.NewIdent(v.Name)
	case transpiler.NamedType:
		if v.Package != "" {
			// CRITICAL: Primitive types must never be package-qualified
			// This can happen when type resolution incorrectly adds a package prefix
			// to a primitive type name like "uint32" or "string"
			if isPrimitiveType(v.Name) {
				return ast.NewIdent(v.Name)
			}
			if v.Package == registry.StdPackageName {
				return t.stdIdent(v.Name)
			}
			// Check if this is a dot import - if so, use just the type name
			if t.importManager.IsDotImported(v.Package) {
				return ast.NewIdent(v.Name)
			}
			// Check if this is the current package - if so, don't qualify with package name
			if v.Package == t.packageName {
				return ast.NewIdent(v.Name)
			}
			return &ast.SelectorExpr{
				X:   ast.NewIdent(v.Package),
				Sel: ast.NewIdent(v.Name),
			}
		}
		// Check if this is a known std type without package prefix
		if t.isKnownStdType(v.Name) {
			return t.stdIdent(v.Name)
		}
		return ast.NewIdent(v.Name)
	case transpiler.GenericType:
		base := t.typeToExpr(v.Base)
		params := make([]ast.Expr, len(v.Params))
		for i, p := range v.Params {
			params[i] = t.typeToExpr(p)
		}
		if len(params) == 1 {
			return &ast.IndexExpr{X: base, Index: params[0]}
		}
		return &ast.IndexListExpr{X: base, Indices: params}
	case transpiler.ArrayType:
		return &ast.ArrayType{Elt: t.typeToExpr(v.Elem)}
	case transpiler.PointerType:
		return &ast.StarExpr{X: t.typeToExpr(v.Elem)}
	case transpiler.MapType:
		return &ast.MapType{Key: t.typeToExpr(v.Key), Value: t.typeToExpr(v.Elem)}
	case transpiler.FuncType:
		var params *ast.FieldList
		if len(v.Params) > 0 {
			params = &ast.FieldList{}
			for _, p := range v.Params {
				params.List = append(params.List, &ast.Field{Type: t.typeToExpr(p)})
			}
		}
		var results *ast.FieldList
		if len(v.Results) > 0 {
			results = &ast.FieldList{}
			for _, r := range v.Results {
				results.List = append(results.List, &ast.Field{Type: t.typeToExpr(r)})
			}
		}
		return &ast.FuncType{Params: params, Results: results}
	}
	return ast.NewIdent(typ.String())
}

func (t *galaASTTransformer) wrapWithAssertion(expr ast.Expr, targetType ast.Expr) ast.Expr {
	if targetType == nil {
		return expr
	}

	// Don't wrap if target type is 'any'
	if id, ok := targetType.(*ast.Ident); ok && id.Name == "any" {
		return expr
	}

	// If it's a CallExpr to a FuncLit (like match generates), or a Get_ call, we should assert
	if call, ok := expr.(*ast.CallExpr); ok {
		isFuncLit := false
		var funcLit *ast.FuncLit
		if fl, ok := call.Fun.(*ast.FuncLit); ok {
			isFuncLit = true
			funcLit = fl
		}

		isGetter := false
		if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
			if strings.HasPrefix(sel.Sel.Name, "Get_") {
				isGetter = true
			}
		}

		if isFuncLit {
			// Check if the FuncLit already returns the target type
			// If so, no assertion is needed
			if funcLit.Type.Results != nil && len(funcLit.Type.Results.List) > 0 {
				resultType := funcLit.Type.Results.List[0].Type
				if t.typeExprsEqual(resultType, targetType) {
					return expr
				}
				// If result type is concrete (not 'any'), the IIFE returns a concrete type - no assertion needed
				// Concrete types include: non-any identifiers, generic types (IndexExpr), qualified types (SelectorExpr)
				if !t.isAnyType(resultType) {
					return expr
				}
			}
			return &ast.TypeAssertExpr{
				X:    expr,
				Type: targetType,
			}
		}

		if isGetter {
			return &ast.TypeAssertExpr{
				X:    expr,
				Type: targetType,
			}
		}
	}
	return expr
}

// typeExprsEqual checks if two type expressions represent the same type
func (t *galaASTTransformer) typeExprsEqual(a, b ast.Expr) bool {
	aIdent, aOk := a.(*ast.Ident)
	bIdent, bOk := b.(*ast.Ident)
	if aOk && bOk {
		return aIdent.Name == bIdent.Name
	}
	return false
}

// isAnyType checks if a type expression represents the 'any' type
func (t *galaASTTransformer) isAnyType(typeExpr ast.Expr) bool {
	if id, ok := typeExpr.(*ast.Ident); ok {
		return id.Name == "any"
	}
	// Generic types (IndexExpr), qualified types (SelectorExpr), etc. are not 'any'
	return false
}

func (t *galaASTTransformer) extractTypeParams(typ ast.Expr) []*ast.Field {
	var params []*ast.Field
	switch e := typ.(type) {
	case *ast.StarExpr:
		// Handle pointer types like *Array[T] - recurse into the base type
		return t.extractTypeParams(e.X)
	case *ast.IndexExpr:
		if id, ok := e.Index.(*ast.Ident); ok {
			constraint := t.getTypeParamConstraint(e.X, id.Name, 0)
			params = append(params, &ast.Field{
				Names: []*ast.Ident{id},
				Type:  ast.NewIdent(constraint),
			})
		}
	case *ast.IndexListExpr:
		for i, index := range e.Indices {
			if id, ok := index.(*ast.Ident); ok {
				constraint := t.getTypeParamConstraint(e.X, id.Name, i)
				params = append(params, &ast.Field{
					Names: []*ast.Ident{id},
					Type:  ast.NewIdent(constraint),
				})
			}
		}
	}
	return params
}

// getTypeParamConstraint looks up the constraint for a type parameter from the type's metadata.
// baseType is the type expression (e.g., the "Container" in "Container[T]")
// paramName is the name of the type parameter (e.g., "T")
// paramIndex is the position of the type parameter (used when paramName doesn't match)
func (t *galaASTTransformer) getTypeParamConstraint(baseType ast.Expr, paramName string, paramIndex int) string {
	typeName := ""
	switch bt := baseType.(type) {
	case *ast.Ident:
		typeName = bt.Name
	case *ast.SelectorExpr:
		if id, ok := bt.X.(*ast.Ident); ok {
			typeName = id.Name + "." + bt.Sel.Name
		}
	}

	if typeName == "" {
		return "any"
	}

	// Use unified resolution to find the type in metadata
	if meta := t.getTypeMeta(typeName); meta != nil {
		// First try to match by parameter name
		if constraint, ok := meta.TypeParamConstraints[paramName]; ok {
			return constraint
		}
		// If no match by name, try by index
		if paramIndex < len(meta.TypeParams) {
			tpName := meta.TypeParams[paramIndex]
			if constraint, ok := meta.TypeParamConstraints[tpName]; ok {
				return constraint
			}
		}
	}

	return "any"
}

// causesInstantiationCycle checks if a method return type would cause a Go generics
// instantiation cycle. This happens when:
// - The receiver is a generic type (e.g., MyList[T])
// - The return type is the same base type (e.g., MyList)
// - But with different type arguments (e.g., MyList[Pair[T, int]])
// Go's compiler detects this as a potential infinite instantiation chain.
func (t *galaASTTransformer) causesInstantiationCycle(receiverType ast.Expr, returnType ast.Expr) bool {
	if receiverType == nil || returnType == nil {
		return false
	}

	// Get base type name and type args from receiver
	recvBase, recvArgs := t.getBaseTypeAndArgs(receiverType)
	if recvBase == "" || len(recvArgs) == 0 {
		return false // Not a generic receiver
	}

	// Get base type name and type args from return type
	retBase, retArgs := t.getBaseTypeAndArgs(returnType)
	if retBase == "" {
		return false
	}

	// Check if base types match
	if recvBase != retBase {
		return false
	}

	// Check if type arguments differ
	// If they're exactly the same, no cycle (e.g., MyList[T] -> MyList[T])
	// If they differ, potential cycle (e.g., MyList[T] -> MyList[Pair[T, int]])
	if len(recvArgs) != len(retArgs) {
		return true // Different number of args = different
	}

	for i, recvArg := range recvArgs {
		if recvArg != retArgs[i] {
			return true // Different arg = potential cycle
		}
	}

	return false
}

// getBaseTypeAndArgs extracts the base type name and type arguments from a type expression
func (t *galaASTTransformer) getBaseTypeAndArgs(typ ast.Expr) (string, []string) {
	switch e := typ.(type) {
	case *ast.StarExpr:
		// Handle pointer types like *Array[T] - recurse into the base type
		return t.getBaseTypeAndArgs(e.X)
	case *ast.Ident:
		return e.Name, nil
	case *ast.SelectorExpr:
		if x, ok := e.X.(*ast.Ident); ok {
			return x.Name + "." + e.Sel.Name, nil
		}
	case *ast.IndexExpr:
		base, _ := t.getBaseTypeAndArgs(e.X)
		argStr := t.typeArgToString(e.Index)
		return base, []string{argStr}
	case *ast.IndexListExpr:
		base, _ := t.getBaseTypeAndArgs(e.X)
		var args []string
		for _, idx := range e.Indices {
			args = append(args, t.typeArgToString(idx))
		}
		return base, args
	}
	return "", nil
}

// typeArgToString converts a type argument expression to a string for comparison
func (t *galaASTTransformer) typeArgToString(arg ast.Expr) string {
	switch e := arg.(type) {
	case *ast.StarExpr:
		// Handle pointer types like *Array[T]
		return "*" + t.typeArgToString(e.X)
	case *ast.Ident:
		return e.Name
	case *ast.SelectorExpr:
		if x, ok := e.X.(*ast.Ident); ok {
			return x.Name + "." + e.Sel.Name
		}
	case *ast.IndexExpr:
		base := t.typeArgToString(e.X)
		inner := t.typeArgToString(e.Index)
		return base + "[" + inner + "]"
	case *ast.IndexListExpr:
		base := t.typeArgToString(e.X)
		var inners []string
		for _, idx := range e.Indices {
			inners = append(inners, t.typeArgToString(idx))
		}
		return base + "[" + strings.Join(inners, ", ") + "]"
	}
	return ""
}

func (t *galaASTTransformer) exprToType(expr ast.Expr) transpiler.Type {
	if expr == nil {
		return transpiler.NilType{}
	}
	switch e := expr.(type) {
	case *ast.Ident:
		// Try to resolve via getType first (handles dot imports, std types, etc.)
		if resolved := t.getType(e.Name); !resolved.IsNil() {
			return resolved
		}
		// Fall back to simple resolution (for type parameters like T, U, etc.)
		return t.resolveType(e.Name)
	case *ast.SelectorExpr:
		x, ok := e.X.(*ast.Ident)
		if !ok {
			return transpiler.NilType{}
		}
		return transpiler.NamedType{Package: x.Name, Name: e.Sel.Name}
	case *ast.IndexExpr:
		base := t.exprToType(e.X)
		param := t.exprToType(e.Index)
		return transpiler.GenericType{Base: base, Params: []transpiler.Type{param}}
	case *ast.IndexListExpr:
		base := t.exprToType(e.X)
		params := make([]transpiler.Type, len(e.Indices))
		for i, idx := range e.Indices {
			params[i] = t.exprToType(idx)
		}
		return transpiler.GenericType{Base: base, Params: params}
	case *ast.StarExpr:
		return transpiler.PointerType{Elem: t.exprToType(e.X)}
	case *ast.ArrayType:
		return transpiler.ArrayType{Elem: t.exprToType(e.Elt)}
	}
	return transpiler.NilType{}
}

func (t *galaASTTransformer) getBaseTypeName(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.Ident:
		return e.Name
	case *ast.ArrayType:
		return "[]" + t.getBaseTypeName(e.Elt)
	case *ast.IndexExpr:
		return t.getBaseTypeName(e.X)
	case *ast.IndexListExpr:
		return t.getBaseTypeName(e.X)
	case *ast.StarExpr:
		return t.getBaseTypeName(e.X)
	case *ast.SelectorExpr:
		if x, ok := e.X.(*ast.Ident); ok {
			if t.importManager.IsPackage(x.Name) {
				pkgName := x.Name
				if actual, ok := t.importManager.ResolveAlias(pkgName); ok {
					pkgName = actual
				}
				return pkgName + "." + e.Sel.Name
			}
		}
		return e.Sel.Name
	case *ast.FuncType:
		return "func"
	}
	return ""
}

func (t *galaASTTransformer) isImmutableType(typ transpiler.Type) bool {
	if typ == nil || typ.IsNil() {
		return false
	}
	baseName := typ.BaseName()
	// Check if base name is Immutable (with or without package prefix)
	isImm := baseName == transpiler.TypeImmutable ||
		strings.HasSuffix(baseName, "."+transpiler.TypeImmutable)

	if isImm {
		if gen, ok := typ.(transpiler.GenericType); ok {
			for _, p := range gen.Params {
				if t.isImmutableType(p) {
					panic(galaerr.NewSemanticError("recursive Immutable wrapping is not allowed"))
				}
			}
		}
	}

	return isImm
}

func (t *galaASTTransformer) isConstPtrType(typ transpiler.Type) bool {
	if typ == nil || typ.IsNil() {
		return false
	}
	baseName := typ.BaseName()
	// Check if base name is ConstPtr (with or without package prefix)
	return baseName == transpiler.TypeConstPtr ||
		strings.HasSuffix(baseName, "."+transpiler.TypeConstPtr)
}

func (t *galaASTTransformer) getExprTypeName(expr ast.Expr) transpiler.Type {
	if expr == nil {
		return transpiler.NilType{}
	}

	// Try manual inference first for speed and simple cases
	res := t.getExprTypeNameManual(expr)
	if !res.IsNil() && !t.hasTypeParams(res) && res.String() != "any" {
		return res
	}

	// Fallback to Hindley-Milner for more complex cases
	hmRes, err := t.inferExprType(expr)
	if err == nil && !hmRes.IsNil() && hmRes.String() != "any" {
		return hmRes
	}

	return res
}

func (t *galaASTTransformer) hasTypeParams(typ transpiler.Type) bool {
	if typ == nil || typ.IsNil() {
		return false
	}
	switch v := typ.(type) {
	case transpiler.BasicType:
		// Check if it's a known type parameter in current scope
		// OR if it's a single uppercase letter (common convention for type params)
		if t.activeTypeParams[v.Name] {
			return true
		}
		if len(v.Name) == 1 && v.Name[0] >= 'A' && v.Name[0] <= 'Z' {
			return true
		}
		return false
	case transpiler.GenericType:
		for _, p := range v.Params {
			if t.hasTypeParams(p) {
				return true
			}
		}
		return t.hasTypeParams(v.Base)
	case transpiler.ArrayType:
		return t.hasTypeParams(v.Elem)
	case transpiler.PointerType:
		return t.hasTypeParams(v.Elem)
	case transpiler.MapType:
		return t.hasTypeParams(v.Key) || t.hasTypeParams(v.Elem)
	case transpiler.FuncType:
		for _, p := range v.Params {
			if t.hasTypeParams(p) {
				return true
			}
		}
		for _, r := range v.Results {
			if t.hasTypeParams(r) {
				return true
			}
		}
		return false
	}
	return false
}

// Type inference functions moved to type_inference.go
