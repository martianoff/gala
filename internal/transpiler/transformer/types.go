package transformer

import (
	"go/ast"
	"go/token"
	"martianoff/gala/internal/parser/grammar"
	"martianoff/gala/internal/transpiler"
	"strings"
)

func (t *galaASTTransformer) transformType(ctx grammar.ITypeContext) (ast.Expr, error) {
	if ctx == nil {
		return nil, nil
	}
	// Simplified type handling
	if ctx.Identifier() != nil {
		typeName := ctx.Identifier().GetText()
		if typeName == "_" {
			return ast.NewIdent("any"), nil
		}
		var ident ast.Expr = ast.NewIdent(typeName)
		if typeName == transpiler.TypeOption || typeName == transpiler.TypeTuple || typeName == transpiler.TypeEither || typeName == transpiler.TypeImmutable {
			ident = t.stdIdent(typeName)
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

func (t *galaASTTransformer) typeToExpr(typ transpiler.Type) ast.Expr {
	if typ.IsNil() {
		return ast.NewIdent("any")
	}
	switch v := typ.(type) {
	case transpiler.BasicType:
		return ast.NewIdent(v.Name)
	case transpiler.NamedType:
		if v.Package != "" {
			if v.Package == transpiler.StdPackage {
				return t.stdIdent(v.Name)
			}
			return &ast.SelectorExpr{
				X:   ast.NewIdent(v.Package),
				Sel: ast.NewIdent(v.Name),
			}
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
		if _, ok := call.Fun.(*ast.FuncLit); ok {
			isFuncLit = true
		}

		isGetter := false
		if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
			if strings.HasPrefix(sel.Sel.Name, "Get_") {
				isGetter = true
			}
		}

		if isFuncLit || isGetter {
			return &ast.TypeAssertExpr{
				X:    expr,
				Type: targetType,
			}
		}
	}
	return expr
}

func (t *galaASTTransformer) extractTypeParams(typ ast.Expr) []*ast.Field {
	var params []*ast.Field
	switch e := typ.(type) {
	case *ast.IndexExpr:
		if id, ok := e.Index.(*ast.Ident); ok {
			params = append(params, &ast.Field{
				Names: []*ast.Ident{id},
				Type:  ast.NewIdent("any"),
			})
		}
	case *ast.IndexListExpr:
		for _, index := range e.Indices {
			if id, ok := index.(*ast.Ident); ok {
				params = append(params, &ast.Field{
					Names: []*ast.Ident{id},
					Type:  ast.NewIdent("any"),
				})
			}
		}
	}
	return params
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

func (t *galaASTTransformer) getExprTypeName(expr ast.Expr) transpiler.Type {
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
		xType := t.getExprTypeName(e.X)
		if arr, ok := xType.(transpiler.ArrayType); ok {
			return arr.Elem
		}
		return transpiler.NilType{}
	case *ast.ParenExpr:
		return t.getExprTypeName(e.X)
	case *ast.UnaryExpr:
		switch e.Op {
		case token.NOT:
			return transpiler.BasicType{Name: "bool"}
		case token.AND:
			return transpiler.PointerType{Elem: t.getExprTypeName(e.X)}
		case token.MUL:
			xType := t.getExprTypeName(e.X)
			if ptr, ok := xType.(transpiler.PointerType); ok {
				return ptr.Elem
			}
			return transpiler.NilType{}
		default:
			return t.getExprTypeName(e.X)
		}
	case *ast.BinaryExpr:
		switch e.Op {
		case token.EQL, token.NEQ, token.LSS, token.LEQ, token.GTR, token.GEQ, token.LAND, token.LOR:
			return transpiler.BasicType{Name: "bool"}
		default:
			return t.getExprTypeName(e.X)
		}
	case *ast.SelectorExpr:
		xType := t.getExprTypeName(e.X)
		xTypeName := xType.String()
		if !xType.IsNil() && t.structFieldTypes[xTypeName] != nil {
			if fType, ok := t.structFieldTypes[xTypeName][e.Sel.Name]; ok && !fType.IsNil() {
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
		// Handle b.Get() or std.Some()
		fun := e.Fun
		if idx, ok := fun.(*ast.IndexExpr); ok {
			fun = idx.X
		} else if idxList, ok := fun.(*ast.IndexListExpr); ok {
			fun = idxList.X
		}

		if sel, ok := fun.(*ast.SelectorExpr); ok {
			if sel.Sel.Name == transpiler.MethodGet {
				if id, ok := sel.X.(*ast.Ident); ok {
					if t.isVal(id.Name) {
						return t.getType(id.Name)
					}
				}
				xType := t.getExprTypeName(sel.X)
				xBaseName := xType.BaseName()
				if xBaseName == transpiler.TypeImmutable || xBaseName == "std."+transpiler.TypeImmutable ||
					xBaseName == transpiler.TypeOption || xBaseName == "std."+transpiler.TypeOption {
					if gen, ok := xType.(transpiler.GenericType); ok && len(gen.Params) > 0 {
						return gen.Params[0]
					}
				}
				return xType
			}

			if sel.Sel.Name == transpiler.FuncNewImmutable || sel.Sel.Name == transpiler.TypeImmutable {
				if len(e.Args) > 0 {
					return t.getExprTypeName(e.Args[0])
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
					// Handle Receiver_Method
					if idx := strings.Index(sel.Sel.Name, "_"); idx != -1 {
						receiverType := pkgName + "." + sel.Sel.Name[:idx]
						methodName := sel.Sel.Name[idx+1:]
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

			xType := t.getExprTypeName(sel.X)
			xTypeName := xType.String()
			if !xType.IsNil() {
				if typeMeta, ok := t.typeMetas[xTypeName]; ok {
					if methodMeta, ok := typeMeta.Methods[sel.Sel.Name]; ok {
						return methodMeta.ReturnType
					}
				}
			}

			if sel.Sel.Name == transpiler.FuncLeft || sel.Sel.Name == transpiler.FuncRight {
				return transpiler.NamedType{Package: transpiler.StdPackage, Name: transpiler.TypeEither}
			}
			if sel.Sel.Name == transpiler.TypeTuple {
				return transpiler.NamedType{Package: transpiler.StdPackage, Name: transpiler.TypeTuple}
			}
			if strings.HasPrefix(sel.Sel.Name, transpiler.TypeEither+"_") || strings.HasPrefix(sel.Sel.Name, transpiler.FuncLeft+"_") || strings.HasPrefix(sel.Sel.Name, transpiler.FuncRight+"_") {
				return transpiler.NamedType{Package: transpiler.StdPackage, Name: transpiler.TypeEither}
			}
			if strings.HasPrefix(sel.Sel.Name, transpiler.TypeOption+"_") || strings.HasPrefix(sel.Sel.Name, transpiler.FuncSome+"_") || strings.HasPrefix(sel.Sel.Name, transpiler.FuncNone+"_") {
				return transpiler.NamedType{Package: transpiler.StdPackage, Name: transpiler.TypeOption}
			}
			if strings.HasPrefix(sel.Sel.Name, transpiler.TypeTuple+"_") {
				return transpiler.NamedType{Package: transpiler.StdPackage, Name: transpiler.TypeTuple}
			}
			if _, ok := t.structFields[sel.Sel.Name]; ok {
				return transpiler.BasicType{Name: sel.Sel.Name}
			}
		}
		if id, ok := fun.(*ast.Ident); ok {
			if id.Name == transpiler.FuncLeft || id.Name == transpiler.FuncRight {
				return transpiler.NamedType{Package: transpiler.StdPackage, Name: transpiler.TypeEither}
			}
			if id.Name == transpiler.TypeTuple {
				return transpiler.NamedType{Package: transpiler.StdPackage, Name: transpiler.TypeTuple}
			}
			if strings.HasPrefix(id.Name, transpiler.TypeEither+"_") || strings.HasPrefix(id.Name, transpiler.FuncLeft+"_") || strings.HasPrefix(id.Name, transpiler.FuncRight+"_") {
				return transpiler.NamedType{Package: transpiler.StdPackage, Name: transpiler.TypeEither}
			}
			if strings.HasPrefix(id.Name, transpiler.TypeOption+"_") || strings.HasPrefix(id.Name, transpiler.FuncSome+"_") || strings.HasPrefix(id.Name, transpiler.FuncNone+"_") {
				return transpiler.NamedType{Package: transpiler.StdPackage, Name: transpiler.TypeOption}
			}
			if strings.HasPrefix(id.Name, transpiler.TypeTuple+"_") {
				return transpiler.NamedType{Package: transpiler.StdPackage, Name: transpiler.TypeTuple}
			}
			if id.Name == "len" {
				return transpiler.BasicType{Name: "int"}
			}
			if _, ok := t.structFields[id.Name]; ok {
				return transpiler.BasicType{Name: id.Name}
			}
			if fMeta := t.getFunction(id.Name); fMeta != nil {
				return fMeta.ReturnType
			}

			// Handle generic methods transformed to standalone functions: Receiver_Method
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
						return mMeta.ReturnType
					}
				}
			}
		}
	case *ast.CompositeLit:
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
