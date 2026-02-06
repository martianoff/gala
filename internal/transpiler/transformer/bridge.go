package transformer

import (
	"fmt"
	"go/ast"
	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/infer"
	"strings"
)

// normalizeTypeName resolves an unqualified type name to its fully qualified form
// using the import resolution mechanism. Types already qualified are returned as-is.
func (t *galaASTTransformer) normalizeTypeName(name string) string {
	// If already qualified (contains a dot), return as-is
	if strings.Contains(name, ".") {
		return name
	}
	// Use the unified type resolution mechanism
	resolvedType := t.getType(name)
	if !resolvedType.IsNil() {
		return resolvedType.String()
	}
	return name
}

// toInferType converts a transpiler.Type to an infer.Type
func (t *galaASTTransformer) toInferType(typ transpiler.Type) infer.Type {
	if typ == nil || typ.IsNil() {
		return &infer.TypeConst{Name: "any"}
	}

	switch v := typ.(type) {
	case transpiler.BasicType:
		return &infer.TypeConst{Name: t.normalizeTypeName(v.Name)}
	case transpiler.NamedType:
		return &infer.TypeConst{Name: t.normalizeTypeName(v.String())}
	case transpiler.GenericType:
		params := make([]infer.Type, len(v.Params))
		for i, p := range v.Params {
			params[i] = t.toInferType(p)
		}
		return &infer.TypeApp{Name: t.normalizeTypeName(v.Base.String()), Args: params}
	case transpiler.ArrayType:
		return &infer.TypeApp{Name: "[]", Args: []infer.Type{t.toInferType(v.Elem)}}
	case transpiler.PointerType:
		return &infer.TypeApp{Name: "*", Args: []infer.Type{t.toInferType(v.Elem)}}
	case transpiler.MapType:
		return &infer.TypeApp{Name: "map", Args: []infer.Type{t.toInferType(v.Key), t.toInferType(v.Elem)}}
	case transpiler.FuncType:
		// Curried function type: (a, b) -> r  becomes  a -> (b -> r)
		var res infer.Type
		if len(v.Results) > 0 {
			res = t.toInferType(v.Results[0])
		} else {
			// Void function returns unit
			res = &infer.TypeConst{Name: "unit"}
		}
		for i := len(v.Params) - 1; i >= 0; i-- {
			res = &infer.TypeApp{Name: "->", Args: []infer.Type{t.toInferType(v.Params[i]), res}}
		}
		return res
	case transpiler.VoidType:
		return &infer.TypeConst{Name: "unit"}
	}

	return &infer.TypeConst{Name: typ.String()}
}

// fromInferType converts an infer.Type back to a transpiler.Type
// Returns NilType for unresolved type variables (caller should check and handle)
func (t *galaASTTransformer) fromInferType(typ infer.Type) transpiler.Type {
	if typ == nil {
		return transpiler.NilType{}
	}

	switch v := typ.(type) {
	case *infer.TypeConst:
		return transpiler.ParseType(v.Name)
	case *infer.TypeVariable:
		// Unresolved type variable - return NilType to signal inference failure
		return transpiler.NilType{}
	case *infer.TypeApp:
		if v.Name == "->" {
			// This is more complex because it's curried
			params := []transpiler.Type{t.fromInferType(v.Args[0])}
			curr := v.Args[1]
			for {
				if next, ok := curr.(*infer.TypeApp); ok && next.Name == "->" {
					params = append(params, t.fromInferType(next.Args[0]))
					curr = next.Args[1]
				} else {
					break
				}
			}
			return transpiler.FuncType{
				Params:  params,
				Results: []transpiler.Type{t.fromInferType(curr)},
			}
		}
		if v.Name == "[]" {
			return transpiler.ArrayType{Elem: t.fromInferType(v.Args[0])}
		}
		if v.Name == "*" {
			return transpiler.PointerType{Elem: t.fromInferType(v.Args[0])}
		}
		if v.Name == "map" {
			return transpiler.MapType{Key: t.fromInferType(v.Args[0]), Elem: t.fromInferType(v.Args[1])}
		}

		base := transpiler.ParseType(v.Name)
		params := make([]transpiler.Type, len(v.Args))
		for i, arg := range v.Args {
			params[i] = t.fromInferType(arg)
		}
		return transpiler.GenericType{Base: base, Params: params}
	}

	return transpiler.ParseType(typ.String())
}

// toInferExpr converts a Go AST expression to an infer.Expr
func (t *galaASTTransformer) toInferExpr(expr ast.Expr) infer.Expr {
	if expr == nil {
		return nil
	}

	// Try manual inference first as a shortcut for non-generic types
	manualType := t.getExprTypeNameManual(expr)
	if !manualType.IsNil() && !t.hasTypeParams(manualType) && !manualType.IsAny() {
		return &infer.Lit{Value: "manual", Type: t.toInferType(manualType)}
	}

	switch e := expr.(type) {
	case *ast.BasicLit:
		typ := t.getExprTypeNameManual(e)
		return &infer.Lit{Value: e.Value, Type: t.toInferType(typ)}
	case *ast.Ident:
		if e.Name == "true" || e.Name == "false" {
			return &infer.Lit{Value: e.Name, Type: &infer.TypeConst{Name: "bool"}}
		}
		if e.Name == "nil" {
			return &infer.Lit{Value: "nil", Type: t.inferer.NewTypeVar()}
		}
		return &infer.Var{Name: e.Name}
	case *ast.ParenExpr:
		return t.toInferExpr(e.X)
	case *ast.CallExpr:
		fn := t.toInferExpr(e.Fun)
		if len(e.Args) == 0 {
			// Call with no args... HM App needs an arg.
			// In GALA/Go, we can use a "unit" type or just handle it.
			// For now, let's use a dummy.
			return &infer.App{Fn: fn, Arg: &infer.Lit{Value: "()", Type: &infer.TypeConst{Name: "unit"}}}
		}
		res := &infer.App{Fn: fn, Arg: t.toInferExpr(e.Args[0])}
		for i := 1; i < len(e.Args); i++ {
			res = &infer.App{Fn: res, Arg: t.toInferExpr(e.Args[i])}
		}
		return res
	case *ast.SelectorExpr:
		// For now, treat selector as a single variable name if it's pkg.Name
		if id, ok := e.X.(*ast.Ident); ok {
			if t.importManager.IsPackage(id.Name) {
				return &infer.Var{Name: id.Name + "." + e.Sel.Name}
			}
		}
		// Otherwise, it might be a struct field access.
		// HM doesn't support this directly yet, so we'll just use the name for now.
		return &infer.Var{Name: fmt.Sprintf("%s.%s", t.getExprTypeNameManual(e.X), e.Sel.Name)}
	case *ast.BinaryExpr:
		// Convert binary expr to function call: (op x y)
		opFunc := &infer.Var{Name: e.Op.String()}
		return &infer.App{
			Fn:  &infer.App{Fn: opFunc, Arg: t.toInferExpr(e.X)},
			Arg: t.toInferExpr(e.Y),
		}
	case *ast.UnaryExpr:
		opFunc := &infer.Var{Name: e.Op.String()}
		return &infer.App{Fn: opFunc, Arg: t.toInferExpr(e.X)}
	}

	return &infer.Var{Name: "_"} // Unknown
}

// inferExprType uses Hindley-Milner to infer the type of an expression
func (t *galaASTTransformer) inferExprType(expr ast.Expr) (transpiler.Type, error) {
	inferExpr := t.toInferExpr(expr)
	if inferExpr == nil {
		return transpiler.NilType{}, nil
	}

	env := t.buildTypeEnv()

	// Add built-in operators to env
	t.addBuiltinsToEnv(env)

	typ, err := t.inferer.Infer(env, inferExpr)
	if err != nil {
		return nil, err
	}

	return t.fromInferType(typ), nil
}

func (t *galaASTTransformer) inferIfType(cond, then, elseExpr ast.Expr) (transpiler.Type, error) {
	condExpr := t.toInferExpr(cond)
	thenExpr := t.toInferExpr(then)
	elseExpr_ := t.toInferExpr(elseExpr)

	if condExpr == nil || thenExpr == nil || elseExpr_ == nil {
		return transpiler.NilType{}, nil
	}

	env := t.buildTypeEnv()
	t.addBuiltinsToEnv(env)

	typ, err := t.inferer.Infer(env, &infer.If{
		Cond: condExpr,
		Then: thenExpr,
		Else: elseExpr_,
	})
	if err != nil {
		return nil, err
	}

	return t.fromInferType(typ), nil
}

func (t *galaASTTransformer) buildTypeEnv() infer.TypeEnv {
	env := make(infer.TypeEnv)

	// Add variables from current scope
	s := t.currentScope
	for s != nil {
		for name, typ := range s.valTypes {
			if _, ok := env[name]; !ok {
				env[name] = &infer.Scheme{Type: t.toInferType(typ)}
			}
		}
		s = s.parent
	}

	// Add functions from RichAST
	for name, meta := range t.functions {
		// Convert FunctionMetadata to a function type
		funcType := t.toInferType(transpiler.FuncType{
			Params:  meta.ParamTypes,
			Results: []transpiler.Type{meta.ReturnType},
		})

		// If there are type parameters, we need to convert them to type variables in the HM system
		// and generalize the scheme.
		if len(meta.TypeParams) > 0 {
			// Map from type param name to type variable
			tvMap := make(map[string]*infer.TypeVariable)
			for _, tp := range meta.TypeParams {
				tvMap[tp] = t.inferer.NewTypeVar()
			}

			// Substitute type constants with type variables
			funcType = t.substituteTypeParams(funcType, tvMap)

			// Generalize (quantify all type variables we just created)
			var vars []*infer.TypeVariable
			for _, tv := range tvMap {
				vars = append(vars, tv)
			}
			env[name] = &infer.Scheme{
				Vars: vars,
				Type: funcType,
			}
		} else {
			env[name] = &infer.Scheme{Type: funcType}
		}
	}

	return env
}

func (t *galaASTTransformer) substituteTypeParams(typ infer.Type, tvMap map[string]*infer.TypeVariable) infer.Type {
	if typ == nil {
		return nil
	}

	switch v := typ.(type) {
	case *infer.TypeConst:
		if tv, ok := tvMap[v.Name]; ok {
			return tv
		}
		return v
	case *infer.TypeApp:
		newArgs := make([]infer.Type, len(v.Args))
		for i, arg := range v.Args {
			newArgs[i] = t.substituteTypeParams(arg, tvMap)
		}
		return &infer.TypeApp{Name: v.Name, Args: newArgs}
	case *infer.TypeVariable:
		return v
	}
	return typ
}

func (t *galaASTTransformer) addBuiltinsToEnv(env infer.TypeEnv) {
	// Simple arithmetic operators
	intType := &infer.TypeConst{Name: "int"}
	intOp := &infer.TypeApp{Name: "->", Args: []infer.Type{intType, &infer.TypeApp{Name: "->", Args: []infer.Type{intType, intType}}}}

	env["+"] = &infer.Scheme{Type: intOp}
	env["-"] = &infer.Scheme{Type: intOp}
	env["*"] = &infer.Scheme{Type: intOp}
	env["/"] = &infer.Scheme{Type: intOp}

	boolType := &infer.TypeConst{Name: "bool"}
	compareOp := &infer.TypeApp{Name: "->", Args: []infer.Type{intType, &infer.TypeApp{Name: "->", Args: []infer.Type{intType, boolType}}}}
	env["=="] = &infer.Scheme{Type: compareOp}
	env["!="] = &infer.Scheme{Type: compareOp}
	env["<"] = &infer.Scheme{Type: compareOp}
	env[">"] = &infer.Scheme{Type: compareOp}
	env["<="] = &infer.Scheme{Type: compareOp}
	env[">="] = &infer.Scheme{Type: compareOp}
}
