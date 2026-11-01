package transformer

import (
	"go/ast"
	"go/token"
	"martianoff/gala/internal/parser/grammar"
	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/registry"
	"strconv"
	"strings"
)

// isStdTupleIdent reports whether `ident` refers to the `std.Tuple` (2-arity)
// type, written either as the bare `Tuple` ident (when the file dot-imports
// std) or as the qualified `std.Tuple` selector. Used to decide whether to
// rewrite a Tuple[T1,…,Tn] type expression to its arity-specific form
// std.Tuple3 / Tuple4 / … / Tuple10 when the user wrote `Tuple` with 3+ args.
func isStdTupleIdent(ident ast.Expr) bool {
	switch e := ident.(type) {
	case *ast.Ident:
		return e.Name == "Tuple"
	case *ast.SelectorExpr:
		x, ok := e.X.(*ast.Ident)
		return ok && x.Name == registry.StdPackageName && e.Sel != nil && e.Sel.Name == "Tuple"
	}
	return false
}

// tupleTypeNames returns the canonical std tuple type names in arity order
// (`Tuple` then `Tuple3` … `Tuple10`). Used by callers that need to iterate
// every tuple metadata entry. (B2)
func tupleTypeNames() []string {
	return []string{
		transpiler.TypeTuple, transpiler.TypeTuple3, transpiler.TypeTuple4,
		transpiler.TypeTuple5, transpiler.TypeTuple6, transpiler.TypeTuple7,
		transpiler.TypeTuple8, transpiler.TypeTuple9, transpiler.TypeTuple10,
	}
}

// isTupleTypeName reports whether `name` is one of the canonical std tuple
// type names. Mirror of transpiler.TupleArityName for recognition contexts (pattern
// matching, type unification). (B2)
func isTupleTypeName(name string) bool {
	for _, n := range tupleTypeNames() {
		if name == n {
			return true
		}
	}
	return false
}

// rewriteStdTupleIdent rewrites a bare `Tuple` (or `std.Tuple`) ident to
// the arity-specific `std.TupleN` form when the call/type expression supplies
// 3+ type/value arguments. Returns the original ident unchanged when it is
// not std.Tuple or when n < 3 (the 2-arity case keeps the canonical name).
// All four Tuple-arity rewrite sites in the transformer route through here.
func (t *galaASTTransformer) rewriteStdTupleIdent(ident ast.Expr, n int) ast.Expr {
	if n < 3 || n > 10 || !isStdTupleIdent(ident) {
		return ident
	}
	name, _ := transpiler.TupleArityName(n)
	return t.stdIdent(name)
}

// transformType lowers the type ctx, on ANTLR's tree (see lowerType).
func (t *galaASTTransformer) transformType(ctx grammar.ITypeContext) (ast.Expr, error) {
	return t.lowerType(antlrType(ctx))
}

// lowerType lowers the type s, of either tree (types_source.go); no type
// lowers to nil.
func (t *galaASTTransformer) lowerType(s typeSrc) (ast.Expr, error) {
	if !s.ok() {
		return nil, nil
	}
	t.noteNativeType(s.at())
	// The non-null pointer `Ptr[T]` is `*T` in generated Go (see ptr.go).
	if elem := s.ptrElem(); elem.ok() {
		typ, err := t.lowerType(elem)
		if err != nil {
			return nil, err
		}
		return t.ptrStar(typ), nil
	}
	// Handle qualified identifier types (e.g., std.Option[T] or just Option[T])
	if s.named() {
		identifiers := s.nameParts()

		var ident ast.Expr
		if len(identifiers) == 1 {
			// Simple type name
			typeName := identifiers[0]
			// B12: `_` is a wildcard type, desugared to Go's `any`. It is intended
			// for pattern-matching contexts (e.g., `case Wrap[_] =>`) and generic
			// type-parameter holes where the exact type does not matter. Users who
			// write `_` outside a pattern context get `any`, which may be surprising;
			// see docs/GALA.MD "Wildcard type" for the full contract.
			if typeName == "_" {
				return ast.NewIdent("any"), nil
			}
			ident = ast.NewIdent(typeName)
			if t.activeTypeParams[typeName] && s.hasTypeArgs() {
				return nil, t.typeParamMisuseErrorAt(s.at(), typeName, "takes no type arguments")
			}
			// Use resolution to determine if this type belongs to an imported package
			resolvedType := t.lookupTypeName(typeName)
			if !resolvedType.IsNil() {
				if pkg := resolvedType.GetPackage(); pkg != "" && pkg != t.packageName {
					// Type belongs to an imported package, use package-qualified identifier
					if pkg == registry.StdPackageName {
						ident = t.stdIdent(typeName)
					} else {
						// Check if this is a dot import in the CURRENT file — don't qualify
						if t.importManager.IsDotImported(pkg) {
							t.markDotImportUsed(pkg)
						} else {
							if alias, ok := t.packageQualifier(pkg); ok {
								ident = &ast.SelectorExpr{X: ast.NewIdent(alias), Sel: ast.NewIdent(typeName)}
							}
						}
					}
				}
			} else if t.isKnownStdType(typeName) {
				// Fallback: if type resolution failed but this is a known std type,
				// qualify it with std. prefix (e.g., Tuple, Option, Either, etc.)
				ident = t.stdIdent(typeName)
			}
		} else {
			// Qualified type name (e.g., std.Option)
			// Build selector expression from left to right
			ident = ast.NewIdent(identifiers[0])
			for i := 1; i < len(identifiers); i++ {
				ident = &ast.SelectorExpr{X: ident, Sel: ast.NewIdent(identifiers[i])}
			}
		}

		if args, ok := s.typeArgs(); ok {
			if len(args) == 0 {
				return nil, t.semanticErrorAtNode(s.at(), "type "+s.nameText()+" has an empty type argument list")
			}
			// Transparent concurrency-boundary marker: `Sendable[F]` is exactly
			// `F` in generated Go — it carries no runtime representation. Emit the
			// inner type directly so the marker never leaks into the output. The
			// analyzer still records the un-unwrapped `Sendable[F]` in parameter
			// metadata, which is what drives the capture-safety check at call
			// sites; codegen (here) is the point that makes it disappear.
			if len(identifiers) == 1 && identifiers[0] == transpiler.TypeSendable && len(args) == 1 {
				return t.lowerType(args[0])
			}

			// Generic type: T[A, B] -> *ast.IndexExpr or *ast.IndexListExpr
			var argExprs []ast.Expr
			for _, arg := range args {
				ae, err := t.lowerType(arg)
				if err != nil {
					return nil, err
				}
				argExprs = append(argExprs, ae)
			}
			if err := t.checkPtrTypeArgsOfType(s, argExprs, args); err != nil {
				return nil, err
			}

			// std.Tuple covers 2 type args; 3+ type args map to std.Tuple3..Tuple10.
			// Route through the unified helper so every Tuple-arity rewrite site
			// agrees on the mapping (B2).
			ident = t.rewriteStdTupleIdent(ident, len(argExprs))

			if len(argExprs) == 1 {
				return &ast.IndexExpr{X: ident, Index: argExprs[0]}, nil
			} else {
				return &ast.IndexListExpr{X: ident, Indices: argExprs}, nil
			}
		}
		return ident, nil
	}

	switch shape, elems := s.shape(); shape {
	case shapePointer:
		typ, err := t.lowerType(elems[0])
		if err != nil {
			return nil, err
		}
		return &ast.StarExpr{X: typ}, nil
	case shapeSlice:
		typ, err := t.lowerType(elems[0])
		if err != nil {
			return nil, err
		}
		return &ast.ArrayType{Elt: typ}, nil
	case shapeMap:
		keyType, err := t.lowerType(elems[0])
		if err != nil {
			return nil, err
		}
		valueType, err := t.lowerType(elems[1])
		if err != nil {
			return nil, err
		}
		return &ast.MapType{Key: keyType, Value: valueType}, nil
	case shapeFunc:
		return t.lowerFuncTypeSignature(s)
	}
	return ast.NewIdent(s.text()), nil
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
		case token.LOR, token.LAND:
			if typ := t.logicalOperandType(e.X, e.Y); typ != nil {
				return t.typeToExpr(typ)
			}
			return ast.NewIdent("bool")
		case token.EQL, token.NEQ, token.LSS, token.LEQ, token.GTR, token.GEQ:
			return ast.NewIdent("bool")
		default:
			// Delegated rather than restated: arithmeticResultType owns the
			// whole rule — governing operand, the shift exception, and the
			// default of an all-constant expression — and a second spelling
			// here answered `1 * 1.5` with int where that one says float64.
			//
			// A result still carrying type parameters (`b.Get() * 2` on a
			// Box[T]) is not returned: the expression takes its left
			// operand's type expression.
			if typ := t.arithmeticResultType(e); !transpiler.IsUnusableOrAny(typ) && !t.hasTypeParams(typ) {
				return t.typeToExpr(typ)
			}
			return t.getExprType(e.X)
		}
	case *ast.UnaryExpr:
		if e.Op == token.NOT {
			if typ := t.logicalOperandType(e.X); typ != nil {
				return t.typeToExpr(typ)
			}
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

// isKnownStdType reports whether the bare name means a std type here, so it is
// emitted as std.Name: std exports it, and neither a type parameter of the
// enclosing generic declaration nor a type the package declares shadows it.
func (t *galaASTTransformer) isKnownStdType(name string) bool {
	return registry.IsStdType(name) && !t.activeTypeParams[name] && !t.packageDeclaresType(name)
}

// goChanDir is each channel direction as go/ast spells it.
var goChanDir = [...]ast.ChanDir{
	transpiler.ChanBoth: ast.SEND | ast.RECV,
	transpiler.ChanRecv: ast.RECV,
	transpiler.ChanSend: ast.SEND,
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
			// A foreign Go package whose *name* collides with the current GALA
			// package (e.g. `io/fs` — package name "fs" — referenced from GALA's
			// own `fs` stdlib package) carries an ImportPath, which
			// resolveTypeQualifier checks before treating the type as local.
			return t.resolveTypeQualifier(v)
		}
		// Check if this is a known std type without package prefix
		if t.isKnownStdType(v.Name) {
			return t.stdIdent(v.Name)
		}
		return ast.NewIdent(v.Name)
	case transpiler.GenericType:
		// Transparent boundary marker: `Sendable[F]` emits as `F` (see
		// transpiler.UnwrapSendable). Keeps the marker out of generated Go when a
		// type is reconstructed from metadata (e.g. a thunk's expected result).
		if inner, ok := transpiler.UnwrapSendable(v); ok {
			return t.typeToExpr(inner)
		}
		base := t.typeToExpr(v.Base)
		params := make([]ast.Expr, len(v.Params))
		for i, p := range v.Params {
			params[i] = t.typeToExpr(p)
		}
		// std.Tuple covers 2 type args; 3+ type args map to std.Tuple3..Tuple10.
		// Mirrors the rewrite in transformType (source-level) so types
		// reconstructed from transpiler.GenericType values (e.g., from a match
		// subject's inferred type) emit the correct arity-specific name (B2).
		base = t.rewriteStdTupleIdent(base, len(params))
		if len(params) == 1 {
			return &ast.IndexExpr{X: base, Index: params[0]}
		}
		return &ast.IndexListExpr{X: base, Indices: params}
	case transpiler.ArrayType:
		return &ast.ArrayType{Elt: t.typeToExpr(v.Elem)}
	case transpiler.PointerType:
		if v.NonNull {
			return t.ptrStar(t.typeToExpr(v.Elem))
		}
		return &ast.StarExpr{X: t.typeToExpr(v.Elem)}
	case transpiler.MapType:
		return &ast.MapType{Key: t.typeToExpr(v.Key), Value: t.typeToExpr(v.Elem)}
	case transpiler.ChanType:
		elem := t.typeToExpr(v.Elem)
		if e, ok := v.Elem.(transpiler.ChanType); ok && v.Dir == transpiler.ChanBoth && e.Dir == transpiler.ChanRecv {
			elem = &ast.ParenExpr{X: elem} // chan (<-chan T), see transpiler.ChanType
		}
		return &ast.ChanType{Dir: goChanDir[v.Dir], Value: elem}
	case transpiler.StructType:
		if len(v.Fields) == 0 {
			// go/printer breaks the braces of an empty field list that has
			// no source positions: `struct {\n}`.
			return ast.NewIdent("struct{}")
		}
		fields := &ast.FieldList{}
		for _, f := range v.Fields {
			field := &ast.Field{Type: t.typeToExpr(f.Type)}
			if !f.Embedded {
				field.Names = []*ast.Ident{ast.NewIdent(f.Name)}
			}
			if f.Tag != "" {
				field.Tag = &ast.BasicLit{Kind: token.STRING, Value: strconv.Quote(f.Tag)}
			}
			fields.List = append(fields.List, field)
		}
		return &ast.StructType{Fields: fields}
	case transpiler.FuncType:
		// Params is never nil: go/ast's FuncType.End reads it when there are
		// no results, so a nil list panics the printer on `func()`.
		params := &ast.FieldList{}
		for _, p := range v.Params {
			params.List = append(params.List, &ast.Field{Type: t.typeToExpr(p)})
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
	switch aTyped := a.(type) {
	case *ast.Ident:
		if bTyped, ok := b.(*ast.Ident); ok {
			return aTyped.Name == bTyped.Name
		}
	case *ast.SelectorExpr:
		if bTyped, ok := b.(*ast.SelectorExpr); ok {
			return t.typeExprsEqual(aTyped.X, bTyped.X) && aTyped.Sel.Name == bTyped.Sel.Name
		}
	case *ast.IndexExpr:
		if bTyped, ok := b.(*ast.IndexExpr); ok {
			return t.typeExprsEqual(aTyped.X, bTyped.X) && t.typeExprsEqual(aTyped.Index, bTyped.Index)
		}
	case *ast.StarExpr:
		if bTyped, ok := b.(*ast.StarExpr); ok {
			return t.typeExprsEqual(aTyped.X, bTyped.X)
		}
	case *ast.ArrayType:
		if bTyped, ok := b.(*ast.ArrayType); ok {
			if aTyped.Len == nil && bTyped.Len == nil {
				return t.typeExprsEqual(aTyped.Elt, bTyped.Elt)
			}
		}
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

func (t *galaASTTransformer) astTypeToTranspilerType(expr ast.Expr) transpiler.Type {
	if expr == nil {
		return transpiler.NilType{}
	}
	switch e := expr.(type) {
	case *ast.Ident:
		if e.Name == "struct{}" { // the empty struct as typeToExpr emits it
			return transpiler.StructType{}
		}
		// Resolve in the type namespace first (handles dot imports, std types, etc.)
		if resolved := t.lookupTypeName(e.Name); !resolved.IsNil() {
			return resolved
		}
		// Fall back to simple resolution (for type parameters like T, U, etc.)
		return t.resolveType(e.Name)
	case *ast.SelectorExpr:
		x, ok := e.X.(*ast.Ident)
		if !ok {
			return transpiler.NilType{}
		}
		named := transpiler.NamedType{Package: x.Name, Name: e.Sel.Name}
		// The qualifier is whatever name this file binds the import to; a
		// type names its package by the package's own name, which is how
		// struct and type metadata are keyed. Under `import l "pkg/lib"`
		// the literal `l.Point{...}` is a `lib.Point`.
		if entry, ok := t.importManager.GetByAlias(x.Name); ok && !entry.IsDot && entry.PkgName != "" {
			named.Package = entry.PkgName
		}
		// Preserve the import path so this AST->type round trip stays lossless.
		// Without it a foreign package whose name collides with the current GALA
		// package (io/fs — package "fs" — inside GALA's own `fs`) becomes
		// indistinguishable from a local type, which drops its qualifier at
		// generation (`fs.DirEntry` -> bare `DirEntry`) and blocks method lookup.
		if path, ok := t.importManager.PathForQualifier(x.Name); ok {
			named.ImportPath = path
		}
		return named
	case *ast.IndexExpr:
		base := t.astTypeToTranspilerType(e.X)
		param := t.astTypeToTranspilerType(e.Index)
		return transpiler.GenericType{Base: base, Params: []transpiler.Type{param}}
	case *ast.IndexListExpr:
		base := t.astTypeToTranspilerType(e.X)
		params := make([]transpiler.Type, len(e.Indices))
		for i, idx := range e.Indices {
			params[i] = t.astTypeToTranspilerType(idx)
		}
		return transpiler.GenericType{Base: base, Params: params}
	case *ast.StarExpr:
		return transpiler.PointerType{Elem: t.astTypeToTranspilerType(e.X), NonNull: t.isPtrTypeExpr(e)}
	case *ast.ArrayType:
		return transpiler.ArrayType{Elem: t.astTypeToTranspilerType(e.Elt)}
	case *ast.MapType:
		// A Go map is legal in every type position a Go slice is, so it has to
		// round-trip here for the same reasons. Without this case a declared
		// `map[K]V` collapsed to NilType, and anything typed from it — a struct
		// field read, a val annotated with a map type — lost its type silently
		// and only failed further downstream.
		return transpiler.MapType{
			Key:  t.astTypeToTranspilerType(e.Key),
			Elem: t.astTypeToTranspilerType(e.Value),
		}
	case *ast.ChanType:
		dir := transpiler.ChanBoth
		switch e.Dir {
		case ast.RECV:
			dir = transpiler.ChanRecv
		case ast.SEND:
			dir = transpiler.ChanSend
		}
		return transpiler.ChanType{Elem: t.astTypeToTranspilerType(e.Value), Dir: dir}
	case *ast.StructType:
		var st transpiler.StructType
		for _, f := range e.Fields.List {
			field := transpiler.StructField{Type: t.astTypeToTranspilerType(f.Type)}
			if f.Tag != nil {
				field.Tag, _ = strconv.Unquote(f.Tag.Value)
			}
			if len(f.Names) == 0 {
				field.Name, field.Embedded = transpiler.EmbeddedFieldName(field.Type), true
				st.Fields = append(st.Fields, field)
			}
			for _, name := range f.Names {
				field.Name = name.Name
				st.Fields = append(st.Fields, field)
			}
		}
		return st
	case *ast.FuncType:
		// Handle function types like func(S) Option[Tuple[T, S]]
		var params []transpiler.Type
		var results []transpiler.Type
		if e.Params != nil {
			for _, field := range e.Params.List {
				paramType := t.astTypeToTranspilerType(field.Type)
				// If there are multiple names, repeat the type for each
				if len(field.Names) > 0 {
					for range field.Names {
						params = append(params, paramType)
					}
				} else {
					params = append(params, paramType)
				}
			}
		}
		if e.Results != nil {
			for _, field := range e.Results.List {
				resultType := t.astTypeToTranspilerType(field.Type)
				if len(field.Names) > 0 {
					for range field.Names {
						results = append(results, resultType)
					}
				} else {
					results = append(results, resultType)
				}
			}
		}
		return transpiler.FuncType{Params: params, Results: results}
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

// isTypeBaseExpr reports whether a call-target expression names a type (as
// opposed to a value of that type). It mirrors the isType detection used by the
// Apply-constructor paths: a bare identifier resolves to a known type only when
// it is not shadowed by a local val/var, and a package-qualified selector is a
// type when its head is a known package. Generic instantiations (`Foo[T]`) are
// unwrapped to their base before the check.
func (t *galaASTTransformer) isTypeBaseExpr(expr ast.Expr) bool {
	switch e := expr.(type) {
	case *ast.IndexExpr:
		return t.isTypeBaseExpr(e.X)
	case *ast.IndexListExpr:
		return t.isTypeBaseExpr(e.X)
	case *ast.Ident:
		return !t.isVal(e.Name) && !t.isVar(e.Name) && !t.lookupTypeName(e.Name).IsNil()
	case *ast.SelectorExpr:
		if id, ok := e.X.(*ast.Ident); ok {
			return t.importManager.IsPackage(id.Name) || id.Name == registry.StdPackageName
		}
	}
	return false
}

func (t *galaASTTransformer) isImmutableType(typ transpiler.Type) bool {
	if typ == nil || typ.IsNil() {
		return false
	}
	isImm := transpiler.IsImmutableType(typ)
	if isImm {
		if gen, ok := typ.(transpiler.GenericType); ok {
			for _, p := range gen.Params {
				if t.isImmutableType(p) {
					t.raiseSemanticError("recursive Immutable wrapping is not allowed")
				}
			}
		}
	}

	return isImm
}

// getExprTypeName is the type of a lowered expression (see
// getExprTypeNameManual), recorded for GALA_TRACE_TYPES=1.
func (t *galaASTTransformer) getExprTypeName(expr ast.Expr) transpiler.Type {
	if expr == nil {
		return transpiler.NilType{}
	}

	res := t.getExprTypeNameManual(expr)
	t.traceType(expr, res, "manual")
	return res
}

func (t *galaASTTransformer) hasTypeParams(typ transpiler.Type) bool {
	return typeNameMatches(typ, t.isActiveTypeParam)
}

// mentionsUnboundTypeParam reports whether typ names a type parameter the code
// being transformed does not bind (see isUnboundTypeParam), a method's own
// type parameter that inference left as its sentinel name included. A type of
// the package's own .go files is a type, whatever its name.
func (t *galaASTTransformer) mentionsUnboundTypeParam(typ transpiler.Type) bool {
	return typeNameMatches(typ, func(name string) bool {
		_, bare := splitPackageQualifier(name)
		if strings.HasPrefix(bare, freshMethodTypeParamPrefix) {
			return true
		}
		return bare == name && !t.isOwnGoType(name) && t.isUnboundTypeParam(name)
	})
}

// typeNameMatches reports whether typ, or any type inside it, is a name
// (qualified as written: `testing.B` is Go's type, not a parameter B) for
// which match holds.
func typeNameMatches(typ transpiler.Type, match func(string) bool) bool {
	if typ == nil || typ.IsNil() {
		return false
	}
	switch v := typ.(type) {
	case transpiler.BasicType:
		return match(v.Name)
	case transpiler.NamedType:
		if v.Package != "" {
			return match(v.Package + "." + v.Name)
		}
		return match(v.Name)
	case transpiler.GenericType:
		for _, p := range v.Params {
			if typeNameMatches(p, match) {
				return true
			}
		}
		return typeNameMatches(v.Base, match)
	case transpiler.ArrayType:
		return typeNameMatches(v.Elem, match)
	case transpiler.PointerType:
		return typeNameMatches(v.Elem, match)
	case transpiler.MapType:
		return typeNameMatches(v.Key, match) || typeNameMatches(v.Elem, match)
	case transpiler.ChanType:
		return typeNameMatches(v.Elem, match)
	case transpiler.StructType:
		for _, f := range v.FieldTypes() {
			if typeNameMatches(f, match) {
				return true
			}
		}
	case transpiler.FuncType:
		for _, p := range v.Params {
			if typeNameMatches(p, match) {
				return true
			}
		}
		for _, r := range v.Results {
			if typeNameMatches(r, match) {
				return true
			}
		}
		return false
	}
	return false
}

// inferRangeTypes returns the key and value types for a range expression.
// For slices/arrays: key is int, value is element type
// For maps: key is map key type, value is map value type
// For strings: key is int, value is rune
func (t *galaASTTransformer) inferRangeTypes(rangeExpr ast.Expr) (keyType transpiler.Type, valueType transpiler.Type) {
	exprType := t.getExprTypeName(rangeExpr)
	if exprType.IsNil() {
		return transpiler.NilType{}, transpiler.NilType{}
	}

	switch v := exprType.(type) {
	case transpiler.ArrayType:
		return transpiler.BasicType{Name: "int"}, v.Elem
	case transpiler.MapType:
		return v.Key, v.Elem
	case transpiler.ChanType:
		// `for v := range ch` binds the one variable to each received value.
		return v.Elem, transpiler.NilType{}
	case transpiler.BasicType:
		if v.Name == "string" {
			return transpiler.BasicType{Name: "int"}, transpiler.BasicType{Name: "rune"}
		}
	}

	// Default: int key, unknown value
	return transpiler.BasicType{Name: "int"}, transpiler.NilType{}
}

// Type inference functions moved to type_inference.go
