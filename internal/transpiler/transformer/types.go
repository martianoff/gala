package transformer

import (
	"go/ast"
	"go/token"
	"martianoff/gala/galaerr"
	"martianoff/gala/internal/parser/grammar"
	"martianoff/gala/internal/transpiler"
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
					if pkg == transpiler.StdPackage {
						ident = t.stdIdent(typeName)
					} else {
						// Check if this is a dot import - if so, don't qualify
						isDotImport := false
						for _, dotPkg := range t.dotImports {
							if dotPkg == pkg {
								isDotImport = true
								break
							}
						}
						if !isDotImport {
							if alias, ok := t.reverseImportAliases[pkg]; ok {
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
	knownStdTypes := map[string]bool{
		"Tuple":     true,
		"Tuple2":    true,
		"Tuple3":    true,
		"Tuple4":    true,
		"Tuple5":    true,
		"Tuple6":    true,
		"Tuple7":    true,
		"Tuple8":    true,
		"Tuple9":    true,
		"Tuple10":   true,
		"Option":    true,
		"Either":    true,
		"Some":      true,
		"None":      true,
		"Left":      true,
		"Right":     true,
		"Immutable": true,
	}
	return knownStdTypes[name]
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
			if v.Package == transpiler.StdPackage {
				return t.stdIdent(v.Name)
			}
			// Check if this is a dot import - if so, use just the type name
			for _, dotPkg := range t.dotImports {
				if dotPkg == v.Package {
					return ast.NewIdent(v.Name)
				}
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

	// Try to find the type in metadata
	if meta, ok := t.typeMetas[typeName]; ok {
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

	// Also check with package prefix if we have a current package
	if t.packageName != "" && !strings.Contains(typeName, ".") {
		fullName := t.packageName + "." + typeName
		if meta, ok := t.typeMetas[fullName]; ok {
			if constraint, ok := meta.TypeParamConstraints[paramName]; ok {
				return constraint
			}
			if paramIndex < len(meta.TypeParams) {
				tpName := meta.TypeParams[paramIndex]
				if constraint, ok := meta.TypeParamConstraints[tpName]; ok {
					return constraint
				}
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
			if _, isPkg := t.imports[x.Name]; isPkg {
				pkgName := x.Name
				if actual, ok := t.importAliases[pkgName]; ok {
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

func (t *galaASTTransformer) getExprTypeNameManual(expr ast.Expr) transpiler.Type {
	if expr == nil {
		return transpiler.NilType{}
	}
	switch e := expr.(type) {
	case *ast.BasicLit:
		switch e.Kind {
		case token.INT:
			return transpiler.BasicType{Name: "int"}
		case token.FLOAT:
			return transpiler.BasicType{Name: "float64"}
		case token.IMAG:
			return transpiler.BasicType{Name: "complex128"}
		case token.CHAR:
			return transpiler.BasicType{Name: "rune"}
		case token.STRING:
			return transpiler.BasicType{Name: "string"}
		}
	case *ast.Ident:
		if e.Name == "true" || e.Name == "false" {
			return transpiler.BasicType{Name: "bool"}
		}
		return t.getType(e.Name)
	case *ast.IndexExpr:
		xType := t.getExprTypeNameManual(e.X)
		if arr, ok := xType.(transpiler.ArrayType); ok {
			return arr.Elem
		}
		// Handle generic type expression like Option[int]
		return t.exprToType(e)
	case *ast.IndexListExpr:
		// Handle generic type expression like Tuple[int, string]
		return t.exprToType(e)
	case *ast.ParenExpr:
		return t.getExprTypeNameManual(e.X)
	case *ast.StarExpr:
		// Handle pointer dereference *x
		xType := t.getExprTypeNameManual(e.X)
		if ptr, ok := xType.(transpiler.PointerType); ok {
			return ptr.Elem
		}
		return transpiler.NilType{}
	case *ast.UnaryExpr:
		switch e.Op {
		case token.NOT:
			return transpiler.BasicType{Name: "bool"}
		case token.AND:
			return transpiler.PointerType{Elem: t.getExprTypeNameManual(e.X)}
		case token.MUL:
			xType := t.getExprTypeNameManual(e.X)
			if ptr, ok := xType.(transpiler.PointerType); ok {
				return ptr.Elem
			}
			return transpiler.NilType{}
		default:
			return t.getExprTypeNameManual(e.X)
		}
	case *ast.BinaryExpr:
		switch e.Op {
		case token.EQL, token.NEQ, token.LSS, token.LEQ, token.GTR, token.GEQ, token.LAND, token.LOR:
			return transpiler.BasicType{Name: "bool"}
		default:
			return t.getExprTypeNameManual(e.X)
		}
	case *ast.SelectorExpr:
		xType := t.getExprTypeNameManual(e.X)
		xTypeName := xType.String()
		// Extract base type name (strip generic parameters like List[T] -> List)
		baseTypeName := xTypeName
		if idx := strings.Index(xTypeName, "["); idx != -1 {
			baseTypeName = xTypeName[:idx]
		}
		// Strip pointer prefix for struct field lookup
		baseTypeName = strings.TrimPrefix(baseTypeName, "*")
		// Resolve to fully qualified name for map lookup
		resolvedTypeName := t.resolveStructTypeName(baseTypeName)
		if !xType.IsNil() && t.structFieldTypes[resolvedTypeName] != nil {
			if fType, ok := t.structFieldTypes[resolvedTypeName][e.Sel.Name]; ok && !fType.IsNil() {
				return fType
			}
		}
		// It might be a package-qualified name
		if x, ok := e.X.(*ast.Ident); ok {
			if _, isPkg := t.imports[x.Name]; isPkg {
				pkgName := x.Name
				if actual, ok := t.importAliases[pkgName]; ok {
					pkgName = actual
				}
				return transpiler.NamedType{Package: pkgName, Name: e.Sel.Name}
			}
		}
	case *ast.CallExpr:
		// Handle IIFE (used by if/match expressions)
		if fl, ok := e.Fun.(*ast.FuncLit); ok {
			if fl.Type != nil && fl.Type.Results != nil && len(fl.Type.Results.List) > 0 {
				return t.exprToType(fl.Type.Results.List[0].Type)
			}
		}

		// Handle b.Get() or std.Some()
		// Capture type arguments from generic calls like Tuple[int, string](...)
		fun := e.Fun
		var typeArgs []transpiler.Type
		if idx, ok := fun.(*ast.IndexExpr); ok {
			fun = idx.X
			typeArgs = []transpiler.Type{t.exprToType(idx.Index)}
		} else if idxList, ok := fun.(*ast.IndexListExpr); ok {
			fun = idxList.X
			for _, idx := range idxList.Indices {
				typeArgs = append(typeArgs, t.exprToType(idx))
			}
		}

		if sel, ok := fun.(*ast.SelectorExpr); ok {
			// Handle Apply method on composite literal: Some[int]{}.Apply(value) -> Option[int]
			if sel.Sel.Name == "Apply" {
				if compLit, ok := sel.X.(*ast.CompositeLit); ok {
					typeName := t.getBaseTypeName(compLit.Type)
					if typeName != "" {
						// Try to get type metadata
						typeMeta, found := t.typeMetas[typeName]
						if !found && !strings.HasPrefix(typeName, "std.") {
							typeMeta, found = t.typeMetas["std."+typeName]
							if found {
								typeName = "std." + typeName
							}
						}
						if found {
							if methodMeta, hasApply := typeMeta.Methods["Apply"]; hasApply {
								// Get type args from the composite literal type
								var litTypeArgs []transpiler.Type
								if idx, ok := compLit.Type.(*ast.IndexExpr); ok {
									litTypeArgs = []transpiler.Type{t.exprToType(idx.Index)}
								} else if idxList, ok := compLit.Type.(*ast.IndexListExpr); ok {
									for _, idxExpr := range idxList.Indices {
										litTypeArgs = append(litTypeArgs, t.exprToType(idxExpr))
									}
								}
								// Substitute type parameters in return type
								if len(litTypeArgs) > 0 && len(typeMeta.TypeParams) > 0 {
									return t.substituteConcreteTypes(methodMeta.ReturnType, typeMeta.TypeParams, litTypeArgs)
								}
								return methodMeta.ReturnType
							}
						}
					}
				}
			}
			if sel.Sel.Name == transpiler.MethodGet {
				// Special case: x.Get() where x is a known val - return the val's type directly
				if id, ok := sel.X.(*ast.Ident); ok {
					if t.isVal(id.Name) {
						return t.getType(id.Name)
					}
				}
				xType := t.getExprTypeNameManual(sel.X)
				xBaseName := xType.BaseName()
				// For Immutable[T].Get() and Option[T].Get(), return the inner type T
				if xBaseName == transpiler.TypeImmutable || xBaseName == "std."+transpiler.TypeImmutable ||
					xBaseName == transpiler.TypeOption || xBaseName == "std."+transpiler.TypeOption {
					if gen, ok := xType.(transpiler.GenericType); ok && len(gen.Params) > 0 {
						return gen.Params[0]
					}
				}
				// For other types, use generic method lookup via typeMetas
				// This handles Array[T].Get() -> T, List[T].Get() -> T, etc.
				if genType, ok := xType.(transpiler.GenericType); ok {
					baseTypeName := genType.Base.String()
					if typeMeta, ok := t.typeMetas[baseTypeName]; ok {
						if methodMeta, ok := typeMeta.Methods[sel.Sel.Name]; ok {
							return t.substituteConcreteTypes(methodMeta.ReturnType, typeMeta.TypeParams, genType.Params)
						}
					}
				}
				return xType
			}

			if sel.Sel.Name == transpiler.FuncNewImmutable || sel.Sel.Name == transpiler.TypeImmutable {
				if len(e.Args) > 0 {
					innerType := t.getExprTypeNameManual(e.Args[0])
					if t.isImmutableType(innerType) {
						panic(galaerr.NewSemanticError("recursive Immutable wrapping is not allowed"))
					}
					return transpiler.GenericType{
						Base:   transpiler.NamedType{Package: transpiler.StdPackage, Name: transpiler.TypeImmutable},
						Params: []transpiler.Type{innerType},
					}
				}
			}

			// IMPORTANT: Check for explicit type args BEFORE looking up metadata return types
			// This ensures Left_Apply[int, string] uses [int, string] instead of [A, B] from metadata
			if len(typeArgs) > 0 {
				if sel.Sel.Name == transpiler.FuncLeft || sel.Sel.Name == transpiler.FuncRight ||
					strings.HasPrefix(sel.Sel.Name, transpiler.FuncLeft+"_") || strings.HasPrefix(sel.Sel.Name, transpiler.FuncRight+"_") ||
					strings.HasPrefix(sel.Sel.Name, transpiler.TypeEither+"_") {
					return transpiler.GenericType{
						Base:   transpiler.NamedType{Package: transpiler.StdPackage, Name: transpiler.TypeEither},
						Params: typeArgs,
					}
				}
				if sel.Sel.Name == transpiler.FuncSome || sel.Sel.Name == transpiler.FuncNone ||
					strings.HasPrefix(sel.Sel.Name, transpiler.FuncSome+"_") || strings.HasPrefix(sel.Sel.Name, transpiler.FuncNone+"_") ||
					strings.HasPrefix(sel.Sel.Name, transpiler.TypeOption+"_") {
					return transpiler.GenericType{
						Base:   transpiler.NamedType{Package: transpiler.StdPackage, Name: transpiler.TypeOption},
						Params: typeArgs,
					}
				}
				if t.isTupleTypeName(sel.Sel.Name) || t.hasTupleTypePrefix(sel.Sel.Name) {
					tupleType := t.getTupleTypeFromName(sel.Sel.Name)
					return transpiler.GenericType{
						Base:   transpiler.NamedType{Package: transpiler.StdPackage, Name: tupleType},
						Params: typeArgs,
					}
				}
			}

			if id, ok := sel.X.(*ast.Ident); ok {
				if _, isPkg := t.imports[id.Name]; isPkg {
					pkgName := id.Name
					if actual, ok := t.importAliases[pkgName]; ok {
						pkgName = actual
					}
					fullName := pkgName + "." + sel.Sel.Name
					if fMeta, ok := t.functions[fullName]; ok {
						return fMeta.ReturnType
					}
					// Handle Receiver_Method (e.g., std.Some_Apply)
					if idx := strings.Index(sel.Sel.Name, "_"); idx != -1 {
						receiverType := pkgName + "." + sel.Sel.Name[:idx]
						methodName := sel.Sel.Name[idx+1:]
						// Special handling for Some_Apply to infer type parameter from argument
						if sel.Sel.Name == transpiler.FuncSome+"_Apply" && len(e.Args) >= 2 {
							argType := t.getExprTypeNameManual(e.Args[1])
							if !argType.IsNil() && argType.String() != "any" {
								return transpiler.GenericType{
									Base:   transpiler.NamedType{Package: transpiler.StdPackage, Name: transpiler.TypeOption},
									Params: []transpiler.Type{argType},
								}
							}
						}
						if meta, ok := t.typeMetas[receiverType]; ok {
							if mMeta, ok := meta.Methods[methodName]; ok {
								return mMeta.ReturnType
							}
						}
					}
					if _, ok := t.structFields[fullName]; ok {
						return transpiler.NamedType{Package: pkgName, Name: sel.Sel.Name}
					}
				}
			}

			xType := t.getExprTypeNameManual(sel.X)
			xTypeName := xType.String()
			if !xType.IsNil() {
				if typeMeta, ok := t.typeMetas[xTypeName]; ok {
					if methodMeta, ok := typeMeta.Methods[sel.Sel.Name]; ok {
						return methodMeta.ReturnType
					}
				}
				// Unwrap pointer types to get to the underlying type for method lookup
				// e.g., for *Array[int].Find(), unwrap to Array[int]
				underlyingType := xType
				if ptr, ok := xType.(transpiler.PointerType); ok {
					underlyingType = ptr.Elem
				}
				// Fallback: try base type name for generic types
				// e.g., for Pair[int, string].Swap(), try looking up Pair
				if genType, ok := underlyingType.(transpiler.GenericType); ok {
					baseTypeName := genType.Base.String()
					if typeMeta, ok := t.typeMetas[baseTypeName]; ok {
						if methodMeta, ok := typeMeta.Methods[sel.Sel.Name]; ok {
							// Substitute type parameters in return type
							// First, substitute struct-level type params (e.g., T -> int for Array[int])
							result := t.substituteConcreteTypes(methodMeta.ReturnType, typeMeta.TypeParams, genType.Params)
							// Then, substitute method-level type params (e.g., U -> string for Zip[string])
							if len(methodMeta.TypeParams) > 0 && len(typeArgs) > 0 {
								result = t.substituteConcreteTypes(result, methodMeta.TypeParams, typeArgs)
							}
							return result
						}
					}
				}
			}

			if sel.Sel.Name == transpiler.FuncLeft || sel.Sel.Name == transpiler.FuncRight {
				baseType := transpiler.NamedType{Package: transpiler.StdPackage, Name: transpiler.TypeEither}
				if len(typeArgs) > 0 {
					return transpiler.GenericType{Base: baseType, Params: typeArgs}
				}
				return baseType
			}
			if t.isTupleTypeName(sel.Sel.Name) {
				tupleType := t.getTupleTypeFromName(sel.Sel.Name)
				baseType := transpiler.NamedType{Package: transpiler.StdPackage, Name: tupleType}
				if len(typeArgs) > 0 {
					return transpiler.GenericType{Base: baseType, Params: typeArgs}
				}
				return baseType
			}
			if strings.HasPrefix(sel.Sel.Name, transpiler.TypeEither+"_") || strings.HasPrefix(sel.Sel.Name, transpiler.FuncLeft+"_") || strings.HasPrefix(sel.Sel.Name, transpiler.FuncRight+"_") {
				baseType := transpiler.NamedType{Package: transpiler.StdPackage, Name: transpiler.TypeEither}
				if len(typeArgs) > 0 {
					return transpiler.GenericType{Base: baseType, Params: typeArgs}
				}
				// For Left_Apply/Right_Apply, infer type parameters from the first argument (the type hint)
				// Left_Apply(std.Left[int, string]{}, value) -> Either[int, string]
				if (sel.Sel.Name == transpiler.FuncLeft+"_Apply" || sel.Sel.Name == transpiler.FuncRight+"_Apply") && len(e.Args) >= 1 {
					firstArgType := t.getExprTypeNameManual(e.Args[0])
					if genType, ok := firstArgType.(transpiler.GenericType); ok && len(genType.Params) > 0 {
						return transpiler.GenericType{Base: baseType, Params: genType.Params}
					}
				}
				return baseType
			}
			if strings.HasPrefix(sel.Sel.Name, transpiler.TypeOption+"_") || strings.HasPrefix(sel.Sel.Name, transpiler.FuncSome+"_") || strings.HasPrefix(sel.Sel.Name, transpiler.FuncNone+"_") {
				// For Some_Apply, infer the type parameter from the second argument (the value)
				// Some_Apply(std.Some{}, value) -> Option[typeof(value)]
				if sel.Sel.Name == transpiler.FuncSome+"_Apply" && len(e.Args) >= 2 {
					argType := t.getExprTypeNameManual(e.Args[1])
					if !argType.IsNil() && argType.String() != "any" {
						return transpiler.GenericType{
							Base:   transpiler.NamedType{Package: transpiler.StdPackage, Name: transpiler.TypeOption},
							Params: []transpiler.Type{argType},
						}
					}
				}
				return transpiler.NamedType{Package: transpiler.StdPackage, Name: transpiler.TypeOption}
			}
			if t.hasTupleTypePrefix(sel.Sel.Name) {
				tupleType := t.getTupleTypeFromName(sel.Sel.Name)
				baseType := transpiler.NamedType{Package: transpiler.StdPackage, Name: tupleType}
				if len(typeArgs) > 0 {
					return transpiler.GenericType{Base: baseType, Params: typeArgs}
				}
				return baseType
			}
			if _, ok := t.structFields[sel.Sel.Name]; ok {
				return transpiler.BasicType{Name: sel.Sel.Name}
			}
		}
		if id, ok := fun.(*ast.Ident); ok {
			if id.Name == transpiler.FuncNewImmutable || id.Name == transpiler.TypeImmutable {
				if len(e.Args) > 0 {
					innerType := t.getExprTypeNameManual(e.Args[0])
					if t.isImmutableType(innerType) {
						panic(galaerr.NewSemanticError("recursive Immutable wrapping is not allowed"))
					}
					return transpiler.GenericType{
						Base:   transpiler.NamedType{Package: transpiler.StdPackage, Name: transpiler.TypeImmutable},
						Params: []transpiler.Type{innerType},
					}
				}
			}
			if id.Name == transpiler.FuncLeft || id.Name == transpiler.FuncRight {
				baseType := transpiler.NamedType{Package: transpiler.StdPackage, Name: transpiler.TypeEither}
				if len(typeArgs) > 0 {
					return transpiler.GenericType{Base: baseType, Params: typeArgs}
				}
				return baseType
			}
			if t.isTupleTypeName(id.Name) {
				tupleType := t.getTupleTypeFromName(id.Name)
				baseType := transpiler.NamedType{Package: transpiler.StdPackage, Name: tupleType}
				if len(typeArgs) > 0 {
					return transpiler.GenericType{Base: baseType, Params: typeArgs}
				}
				return baseType
			}
			if strings.HasPrefix(id.Name, transpiler.TypeEither+"_") || strings.HasPrefix(id.Name, transpiler.FuncLeft+"_") || strings.HasPrefix(id.Name, transpiler.FuncRight+"_") {
				baseType := transpiler.NamedType{Package: transpiler.StdPackage, Name: transpiler.TypeEither}
				if len(typeArgs) > 0 {
					return transpiler.GenericType{Base: baseType, Params: typeArgs}
				}
				return baseType
			}
			if strings.HasPrefix(id.Name, transpiler.TypeOption+"_") || strings.HasPrefix(id.Name, transpiler.FuncSome+"_") || strings.HasPrefix(id.Name, transpiler.FuncNone+"_") {
				baseType := transpiler.NamedType{Package: transpiler.StdPackage, Name: transpiler.TypeOption}
				if len(typeArgs) > 0 {
					return transpiler.GenericType{Base: baseType, Params: typeArgs}
				}
				return baseType
			}
			if t.hasTupleTypePrefix(id.Name) {
				tupleType := t.getTupleTypeFromName(id.Name)
				baseType := transpiler.NamedType{Package: transpiler.StdPackage, Name: tupleType}
				if len(typeArgs) > 0 {
					return transpiler.GenericType{Base: baseType, Params: typeArgs}
				}
				return baseType
			}
			if id.Name == "len" {
				return transpiler.BasicType{Name: "int"}
			}
			// Handle type conversions like uint32(x), int64(y), string(z)
			// When a primitive type name is used as a function call, it's a type conversion
			if isPrimitiveType(id.Name) {
				return transpiler.BasicType{Name: id.Name}
			}
			if _, ok := t.structFields[id.Name]; ok {
				return transpiler.BasicType{Name: id.Name}
			}
			if fMeta := t.getFunction(id.Name); fMeta != nil {
				// Substitute type arguments if the function is generic
				if len(typeArgs) > 0 && len(fMeta.TypeParams) > 0 {
					return t.substituteConcreteTypes(fMeta.ReturnType, fMeta.TypeParams, typeArgs)
				}
				return fMeta.ReturnType
			}

			// Handle generic methods transformed to standalone functions: Receiver_Method
			// e.g., Array_Zip[string](nums.Get(), strs.Get())
			// The first argument is the receiver (nums.Get() -> Array[int])
			// typeArgs are the method's explicit type arguments ([string])
			if idx := strings.Index(id.Name, "_"); idx != -1 {
				receiverType := id.Name[:idx]
				methodName := id.Name[idx+1:]
				resolvedRecvType := t.getType(receiverType)
				resolvedRecvTypeName := resolvedRecvType.String()
				if resolvedRecvType.IsNil() {
					resolvedRecvTypeName = receiverType
				}
				if meta, ok := t.typeMetas[resolvedRecvTypeName]; ok {
					if mMeta, ok := meta.Methods[methodName]; ok {
						result := mMeta.ReturnType
						// Substitute receiver's type params from first argument
						// e.g., Array_Zip[string](nums.Get(), ...) where nums.Get() is Array[int]
						// needs to substitute T -> int from the first arg's generic type
						if len(e.Args) > 0 {
							firstArgType := t.getExprTypeNameManual(e.Args[0])
							if genType, ok := firstArgType.(transpiler.GenericType); ok && len(meta.TypeParams) > 0 {
								result = t.substituteConcreteTypes(result, meta.TypeParams, genType.Params)
							}
						}
						// Substitute method's type params from explicit type args
						// e.g., Array_Zip[string] needs to substitute U -> string
						if len(typeArgs) > 0 && len(mMeta.TypeParams) > 0 {
							result = t.substituteConcreteTypes(result, mMeta.TypeParams, typeArgs)
						}
						return result
					}
				}
			}
		}
	case *ast.CompositeLit:
		// Use exprToType to preserve generic type parameters
		typ := t.exprToType(e.Type)
		if !typ.IsNil() {
			return typ
		}
		typeName := t.getBaseTypeName(e.Type)
		return t.resolveType(typeName)
	}
	return transpiler.NilType{}
}

func (t *galaASTTransformer) resolveType(name string) transpiler.Type {
	if name == "" {
		return transpiler.NilType{}
	}
	return transpiler.ParseType(name)
}

// substituteConcreteTypes substitutes type parameters in a type with concrete types.
// For example, if returnType is Pair[B, A], typeParams is ["A", "B"], and concreteTypes is [int, string],
// the result will be Pair[string, int].
func (t *galaASTTransformer) substituteConcreteTypes(returnType transpiler.Type, typeParams []string, concreteTypes []transpiler.Type) transpiler.Type {
	if returnType == nil || returnType.IsNil() {
		return returnType
	}

	// Build a mapping from type parameter names to concrete types
	paramMap := make(map[string]transpiler.Type)
	for i, param := range typeParams {
		if i < len(concreteTypes) {
			paramMap[param] = concreteTypes[i]
		}
	}

	return t.substituteInType(returnType, paramMap)
}

// substituteInType recursively substitutes type parameters in a type
func (t *galaASTTransformer) substituteInType(typ transpiler.Type, paramMap map[string]transpiler.Type) transpiler.Type {
	if typ == nil || typ.IsNil() {
		return typ
	}

	switch v := typ.(type) {
	case transpiler.BasicType:
		if concrete, ok := paramMap[v.Name]; ok {
			return concrete
		}
		return v
	case transpiler.NamedType:
		if concrete, ok := paramMap[v.Name]; ok {
			return concrete
		}
		return v
	case transpiler.GenericType:
		newParams := make([]transpiler.Type, len(v.Params))
		for i, param := range v.Params {
			newParams[i] = t.substituteInType(param, paramMap)
		}
		newBase := t.substituteInType(v.Base, paramMap)
		if namedBase, ok := newBase.(transpiler.NamedType); ok {
			return transpiler.GenericType{
				Base:   namedBase,
				Params: newParams,
			}
		}
		return transpiler.GenericType{
			Base:   v.Base,
			Params: newParams,
		}
	case transpiler.ArrayType:
		return transpiler.ArrayType{Elem: t.substituteInType(v.Elem, paramMap)}
	case transpiler.PointerType:
		return transpiler.PointerType{Elem: t.substituteInType(v.Elem, paramMap)}
	case transpiler.MapType:
		return transpiler.MapType{
			Key:  t.substituteInType(v.Key, paramMap),
			Elem: t.substituteInType(v.Elem, paramMap),
		}
	case transpiler.FuncType:
		newParams := make([]transpiler.Type, len(v.Params))
		for i, p := range v.Params {
			newParams[i] = t.substituteInType(p, paramMap)
		}
		newResults := make([]transpiler.Type, len(v.Results))
		for i, r := range v.Results {
			newResults[i] = t.substituteInType(r, paramMap)
		}
		return transpiler.FuncType{Params: newParams, Results: newResults}
	default:
		return typ
	}
}

// isTupleTypeName checks if a name is exactly a TupleN type name
func (t *galaASTTransformer) isTupleTypeName(name string) bool {
	switch name {
	case transpiler.TypeTuple, transpiler.TypeTuple3, transpiler.TypeTuple4,
		transpiler.TypeTuple5, transpiler.TypeTuple6, transpiler.TypeTuple7,
		transpiler.TypeTuple8, transpiler.TypeTuple9, transpiler.TypeTuple10:
		return true
	}
	return false
}

// hasTupleTypePrefix checks if a name has a TupleN_ prefix
func (t *galaASTTransformer) hasTupleTypePrefix(name string) bool {
	tupleTypes := []string{
		transpiler.TypeTuple10, transpiler.TypeTuple9, transpiler.TypeTuple8,
		transpiler.TypeTuple7, transpiler.TypeTuple6, transpiler.TypeTuple5,
		transpiler.TypeTuple4, transpiler.TypeTuple3, transpiler.TypeTuple,
	}
	for _, tt := range tupleTypes {
		if strings.HasPrefix(name, tt+"_") {
			return true
		}
	}
	return false
}

// getTupleTypeFromName extracts the TupleN type name from a name that starts with a tuple type
func (t *galaASTTransformer) getTupleTypeFromName(name string) string {
	// Check in order of longest to shortest to handle Tuple10 before Tuple
	tupleTypes := []string{
		transpiler.TypeTuple10, transpiler.TypeTuple9, transpiler.TypeTuple8,
		transpiler.TypeTuple7, transpiler.TypeTuple6, transpiler.TypeTuple5,
		transpiler.TypeTuple4, transpiler.TypeTuple3, transpiler.TypeTuple,
	}
	for _, tt := range tupleTypes {
		if name == tt || strings.HasPrefix(name, tt+"_") {
			return tt
		}
	}
	return transpiler.TypeTuple
}

// getReceiverTypeArgs extracts type arguments from a receiver type and converts them to ast.Expr.
// For example, for *Array[int] or Array[int], it returns [int] as []ast.Expr.
func (t *galaASTTransformer) getReceiverTypeArgs(recvType transpiler.Type) []ast.Expr {
	if recvType == nil || recvType.IsNil() {
		return nil
	}
	// Unwrap pointer type
	if ptr, ok := recvType.(transpiler.PointerType); ok {
		return t.getReceiverTypeArgs(ptr.Elem)
	}
	// Extract type params from generic type
	if gen, ok := recvType.(transpiler.GenericType); ok {
		var args []ast.Expr
		for _, param := range gen.Params {
			args = append(args, t.typeToExpr(param))
		}
		return args
	}
	return nil
}

// getReceiverTypeArgStrings extracts type arguments from a receiver type as strings.
// For example, for *Container[int], it returns ["int"].
func (t *galaASTTransformer) getReceiverTypeArgStrings(recvType transpiler.Type) []string {
	if recvType == nil || recvType.IsNil() {
		return nil
	}
	// Unwrap pointer type
	if ptr, ok := recvType.(transpiler.PointerType); ok {
		return t.getReceiverTypeArgStrings(ptr.Elem)
	}
	// Extract type params from generic type
	if gen, ok := recvType.(transpiler.GenericType); ok {
		var args []string
		for _, param := range gen.Params {
			args = append(args, param.String())
		}
		return args
	}
	return nil
}

// exprToTypeString converts an ast.Expr to a type string.
func (t *galaASTTransformer) exprToTypeString(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.Ident:
		return e.Name
	case *ast.SelectorExpr:
		if x, ok := e.X.(*ast.Ident); ok {
			return x.Name + "." + e.Sel.Name
		}
	case *ast.StarExpr:
		return "*" + t.exprToTypeString(e.X)
	case *ast.IndexExpr:
		return t.exprToTypeString(e.X) + "[" + t.exprToTypeString(e.Index) + "]"
	case *ast.IndexListExpr:
		var params []string
		for _, idx := range e.Indices {
			params = append(params, t.exprToTypeString(idx))
		}
		return t.exprToTypeString(e.X) + "[" + strings.Join(params, ", ") + "]"
	}
	return ""
}

// substituteTranspilerTypeParams substitutes type parameters in a type with their concrete values.
func (t *galaASTTransformer) substituteTranspilerTypeParams(typ transpiler.Type, subst map[string]string) transpiler.Type {
	if typ == nil || typ.IsNil() || len(subst) == 0 {
		return typ
	}
	switch ty := typ.(type) {
	case transpiler.BasicType:
		if replacement, ok := subst[ty.Name]; ok {
			return transpiler.ParseType(replacement)
		}
		return ty
	case transpiler.NamedType:
		// Check if the full name or just the Name needs substitution
		if replacement, ok := subst[ty.Name]; ok {
			return transpiler.ParseType(replacement)
		}
		return ty
	case transpiler.PointerType:
		return transpiler.PointerType{Elem: t.substituteTranspilerTypeParams(ty.Elem, subst)}
	case transpiler.ArrayType:
		return transpiler.ArrayType{Elem: t.substituteTranspilerTypeParams(ty.Elem, subst)}
	case transpiler.GenericType:
		newParams := make([]transpiler.Type, len(ty.Params))
		for i, p := range ty.Params {
			newParams[i] = t.substituteTranspilerTypeParams(p, subst)
		}
		return transpiler.GenericType{Base: t.substituteTranspilerTypeParams(ty.Base, subst), Params: newParams}
	case transpiler.FuncType:
		newParams := make([]transpiler.Type, len(ty.Params))
		for i, p := range ty.Params {
			newParams[i] = t.substituteTranspilerTypeParams(p, subst)
		}
		newResults := make([]transpiler.Type, len(ty.Results))
		for i, r := range ty.Results {
			newResults[i] = t.substituteTranspilerTypeParams(r, subst)
		}
		return transpiler.FuncType{Params: newParams, Results: newResults}
	case transpiler.MapType:
		return transpiler.MapType{
			Key:  t.substituteTranspilerTypeParams(ty.Key, subst),
			Elem: t.substituteTranspilerTypeParams(ty.Elem, subst),
		}
	}
	return typ
}
