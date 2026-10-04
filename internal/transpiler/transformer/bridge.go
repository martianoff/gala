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
	if strings.Contains(name, ".") {
		return name
	}
	resolvedType := t.lookupTypeName(name)
	if !resolvedType.IsNil() {
		return resolvedType.String()
	}
	return name
}

// typeNameMemo memoizes unqualified-name normalization for one conversion
// pass. A transpiler.Type tree names the same handful of types over and over,
// so without this the same import lookup runs once per occurrence.
type typeNameMemo struct {
	resolved map[string]string
}

func (t *galaASTTransformer) normalizeTypeNameMemoized(name string, memo *typeNameMemo) string {
	// A bound type parameter names itself. It never reaches the memo, whose
	// answers hold across declarations: the same name outside the
	// declaration that binds it may well be a type.
	if t.activeTypeParams[name] {
		return name
	}
	if memo != nil {
		if resolved, ok := memo.resolved[name]; ok {
			return resolved
		}
	}
	resolved := t.normalizeTypeName(name)
	if memo != nil {
		if memo.resolved == nil {
			memo.resolved = make(map[string]string, 32)
		}
		memo.resolved[name] = resolved
	}
	return resolved
}

func (t *galaASTTransformer) toInferType(typ transpiler.Type) infer.Type {
	return t.toInferTypeMemoized(typ, nil)
}

func (t *galaASTTransformer) toInferTypeMemoized(typ transpiler.Type, normalizedNames *typeNameMemo) infer.Type {
	if transpiler.IsUnusable(typ) {
		return &infer.TypeConst{Name: "any"}
	}

	switch v := typ.(type) {
	case transpiler.BasicType:
		return &infer.TypeConst{Name: t.normalizeTypeNameMemoized(v.Name, normalizedNames)}
	case transpiler.NamedType:
		return &infer.TypeConst{Name: t.normalizeTypeNameMemoized(v.String(), normalizedNames)}
	case transpiler.GenericType:
		params := make([]infer.Type, len(v.Params))
		for i, p := range v.Params {
			params[i] = t.toInferTypeMemoized(p, normalizedNames)
		}
		return &infer.TypeApp{Name: t.normalizeTypeNameMemoized(v.Base.String(), normalizedNames), Args: params}
	case transpiler.ArrayType:
		return &infer.TypeApp{Name: "[]", Args: []infer.Type{t.toInferTypeMemoized(v.Elem, normalizedNames)}}
	case transpiler.PointerType:
		return &infer.TypeApp{Name: "*", Args: []infer.Type{t.toInferTypeMemoized(v.Elem, normalizedNames)}}
	case transpiler.MapType:
		return &infer.TypeApp{Name: "map", Args: []infer.Type{
			t.toInferTypeMemoized(v.Key, normalizedNames),
			t.toInferTypeMemoized(v.Elem, normalizedNames),
		}}
	case transpiler.FuncType:
		var res infer.Type
		if len(v.Results) > 0 {
			res = t.toInferTypeMemoized(v.Results[0], normalizedNames)
		} else {
			res = &infer.TypeConst{Name: "unit"}
		}
		// A zero-parameter function takes `unit`, matching how toInferExpr
		// encodes a zero-argument call (applied to a `unit` literal). Encoding
		// it as its bare result would make `func() int` indistinguishable from
		// `int`: an if-expression choosing between `() => 3` and `() => 4`
		// would be typed `int`.
		if len(v.Params) == 0 {
			return &infer.TypeApp{Name: "->", Args: []infer.Type{&infer.TypeConst{Name: "unit"}, res}}
		}
		for i := len(v.Params) - 1; i >= 0; i-- {
			res = &infer.TypeApp{Name: "->", Args: []infer.Type{
				t.toInferTypeMemoized(v.Params[i], normalizedNames),
				res,
			}}
		}
		return res
	case transpiler.VoidType:
		return &infer.TypeConst{Name: "unit"}
	}

	return &infer.TypeConst{Name: typ.String()}
}

// isInferUnit reports whether typ is the `unit` type constant.
func isInferUnit(typ infer.Type) bool {
	c, ok := typ.(*infer.TypeConst)
	return ok && c.Name == "unit"
}

// fromInferType converts an infer.Type back to a transpiler.Type
// Returns NilType for unresolved type variables (caller should check and handle)
func (t *galaASTTransformer) fromInferType(typ infer.Type) transpiler.Type {
	if typ == nil {
		return transpiler.NilType{}
	}

	switch v := typ.(type) {
	case *infer.TypeConst:
		if v.Name == "unit" {
			return transpiler.VoidType{}
		}
		return transpiler.ParseType(v.Name)
	case *infer.TypeVariable:
		// Unresolved type variable - return NilType to signal inference failure
		return transpiler.NilType{}
	case *infer.TypeApp:
		if v.Name == "->" {
			// `unit -> R` is a zero-parameter function (see toInferType).
			if isInferUnit(v.Args[0]) {
				return transpiler.FuncType{Results: []transpiler.Type{t.fromInferType(v.Args[1])}}
			}
			// This is more complex because it's curried. A nested `unit -> R`
			// is the result (a returned thunk), not a further parameter.
			params := []transpiler.Type{t.fromInferType(v.Args[0])}
			curr := v.Args[1]
			for {
				if next, ok := curr.(*infer.TypeApp); ok && next.Name == "->" && !isInferUnit(next.Args[0]) {
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

// toInferCallee converts a call's callee, keeping that one query out of the
// unresolved-type inventory when the call itself is already accounted for.
//
// The bridge reaches a call after querying the call's own type, and converts
// it only when that type was not usable as-is: missing, `any`, or written in
// terms of the enclosing function's type parameters. A missing type is
// recorded as the call; a type in terms of type parameters (`T`, `Option[T]`
// inside a generic body) is the call's real type. Either way, a callee that
// names a function or method — the `opt.Map` in `opt.Map(f)` — has nothing
// more to report: it is not a value and has no type of its own. Recording it
// counted every such call a second time, or counted a call that had typed.
//
// `any` is left out, so the callee of a call widened to `any` still counts.
// So does a callee that computes a function (a call, a func literal, an index
// into a slice of funcs), the receiver and arguments, and every other query
// of the same node.
func (t *galaASTTransformer) toInferCallee(call *ast.CallExpr, callType transpiler.Type) infer.Expr {
	if !t.warnTypeInference || callType.IsAny() || !isCalleeName(call.Fun) {
		return t.toInferExpr(call.Fun)
	}
	prev := t.unrecordedCallee
	t.unrecordedCallee = call.Fun
	defer func() { t.unrecordedCallee = prev }()
	return t.toInferExpr(call.Fun)
}

// isCalleeName reports whether a callee names a function or method (`f`,
// `pkg.F`, `x.Method`, `F[K, V]`) rather than computing one. `F[int]` is left
// out: it cannot be told apart from indexing a slice of funcs.
func isCalleeName(fun ast.Expr) bool {
	switch f := ast.Unparen(fun).(type) {
	case *ast.Ident, *ast.SelectorExpr:
		return true
	case *ast.IndexListExpr:
		return isCalleeName(f.X)
	}
	return false
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
		fn := t.toInferCallee(e, manualType)
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

// buildTypeEnv assembles the Hindley-Milner environment for one expression.
//
// The two halves have very different lifetimes. The function half is a pure
// function of file-level metadata and is converted once per file (see
// functionTypeEnv). The scope half depends on the bindings in scope right
// now, so it is rebuilt per call — it is also cheap, because a scope chain
// holds a handful of bindings against a file that may declare hundreds of
// functions.
//
// The innermost binding of a name wins, and a local binding shadows a
// package-level function of the same name, as in Go (see shadowingScope, which
// also says which bindings a lowered default sees as shadowing): the scope half
// is written first and a function fills only the names left free. A binding
// with no recorded type still hides every outer entry of its name, so the name
// is unknown to inference rather than typed from the wrong declaration.
func (t *galaASTTransformer) buildTypeEnv() infer.TypeEnv {
	fnEnv := t.functionTypeEnv()

	memo := t.sharedTypeNameMemo()

	var useScope *scope
	if t.loweringDefault != nil {
		useScope = t.loweringDefault.useScope
	}
	// The operator entries are sized in too: every caller adds them
	// immediately after, and letting ten more keys land in a map sized to
	// the rest is a rehash per inference.
	env := make(infer.TypeEnv, len(fnEnv)+len(builtinTypeEnv)+t.scopeBindingCount())
	var hidden map[string]bool // names a typeless binding hides
	shadows := true
	for s := t.currentScope; s != nil; s = s.parent {
		shadows = shadows && s != useScope
		for name := range s.vals {
			if _, bound := env[name]; bound || hidden[name] {
				continue // an inner binding of the name came first
			}
			if _, isFunction := fnEnv[name]; isFunction && !shadows {
				continue
			}
			if typ := s.valTypes[name]; typ != nil {
				env[name] = &infer.Scheme{Type: t.toInferTypeMemoized(typ, memo)}
			} else {
				if hidden == nil {
					hidden = make(map[string]bool)
				}
				hidden[name] = true
			}
		}
	}

	for name, scheme := range fnEnv {
		if _, bound := env[name]; !bound && !hidden[name] {
			env[name] = scheme
		}
	}

	return env
}

// scopeBindingCount is the number of distinct names the current scope chain
// binds, used only to size the environment map. An exact count is not worth
// a separate pass over a second map, so this over-counts the chain.
func (t *galaASTTransformer) scopeBindingCount() int {
	n := 0
	for s := t.currentScope; s != nil; s = s.parent {
		n += len(s.valTypes)
	}
	return n
}

// functionTypeEnv returns the function-derived half of the type environment.
//
// Every expression whose type the manual resolver cannot pin down runs
// Hindley-Milner over a freshly built environment, and building it meant
// re-converting every function signature in the file. A CPU profile of a
// stdlib transpile put that conversion at 26% of all samples, with the
// signature walk, the generic-parameter substitution and the scheme
// allocations together accounting for all but the scope walk. The conversion
// is therefore cached and only redone when something it reads has changed.
//
// Name normalization answers from the type namespace only (lookupTypeName),
// never from the scope chain, so which local bindings are in scope cannot
// change the result. Two things can, and they are checked differently. The import
// manager moves a revision on every mutation of its entry set, so the cache
// records the revision it was stamped with and a moved revision rebuilds it —
// the same derived validity the resolver snapshot uses, which means a site
// that adds, renames or drops an import cannot leave this cache stale by
// forgetting to say so. The remaining state (t.functions, t.typeMetas,
// t.typeAliases) has no such token, so writes to it are announced through
// invalidateTypeEnv.
//
// Cached schemes are shared by pointer across inference runs. That is safe
// because infer treats Type and Scheme as immutable: unification returns a
// Substitution instead of binding into a variable, and instantiate hands a
// generic scheme's quantified variables a fresh variable per use, so a
// cached scheme's variables stay an uninstantiated template no matter how
// many runs read it.
func (t *galaASTTransformer) functionTypeEnv() infer.TypeEnv {
	if t.funcTypeEnv != nil &&
		t.funcTypeEnvEpoch == t.typeEnvEpoch &&
		t.funcTypeEnvImportRev == t.importManager.Revision() {
		return t.funcTypeEnv
	}

	// Under tracing the memo is nil (see sharedTypeNameMemo), and the cache
	// would turn a per-inference conversion into a per-file one, so build
	// fresh and keep nothing, which is what buildTypeEnv does for the scope
	// half.
	memo := t.sharedTypeNameMemo()

	// Each signature is read in its own declaration's scope: its type
	// parameters, and none of the declaration being transformed, are bound
	// while it is converted, so the cached environment does not depend on
	// where it was built.
	enclosing := t.activeTypeParams
	defer func() { t.activeTypeParams = enclosing }()
	t.activeTypeParams = make(map[string]bool)
	env := make(infer.TypeEnv, len(t.functions))
	for name, meta := range t.functions {
		clear(t.activeTypeParams)
		for _, tp := range meta.TypeParams {
			t.activeTypeParams[tp] = true
		}
		funcType := t.toInferTypeMemoized(transpiler.FuncType{
			Params:  meta.ParamTypes,
			Results: []transpiler.Type{meta.ReturnType},
		}, memo)

		if len(meta.TypeParams) > 0 {
			tvMap := make(map[string]*infer.TypeVariable, len(meta.TypeParams))
			vars := make([]*infer.TypeVariable, 0, len(meta.TypeParams))
			for _, tp := range meta.TypeParams {
				tv := t.inferer.NewTypeVar()
				tvMap[tp] = tv
				vars = append(vars, tv)
			}

			env[name] = &infer.Scheme{
				Vars: vars,
				Type: t.substituteTypeParams(funcType, tvMap),
			}
		} else {
			env[name] = &infer.Scheme{Type: funcType}
		}
	}

	// Under tracing there is nothing worth keeping (see above).
	if memo == nil {
		return env
	}

	t.funcTypeEnv = env
	t.funcTypeEnvEpoch = t.typeEnvEpoch
	t.funcTypeEnvImportRev = t.importManager.Revision()
	return env
}

// sharedTypeNameMemo returns the name-normalization memo shared by
// functionTypeEnv and buildTypeEnv. normalizeTypeName reads only the type
// namespace, never the scope chain, and a bound type parameter bypasses the
// memo (see normalizeTypeNameMemoized), so a memoized answer stays right for as
// long as the state the function-environment cache is keyed on stays put: the
// memo is reset when typeEnvEpoch or the import manager's revision moves, and
// kept across inferences otherwise.
//
// Under tracing it returns nil: every resolution must be recorded, so none
// may be served from a memo that would collapse repeats into one event.
func (t *galaASTTransformer) sharedTypeNameMemo() *typeNameMemo {
	if t.traceTypeResolution {
		return nil
	}
	rev := t.importManager.Revision()
	if t.typeNameCache.resolved == nil {
		t.typeNameCache.resolved = make(map[string]string, 32)
	} else if t.typeNameCacheEpoch != t.typeEnvEpoch || t.typeNameCacheImportRev != rev {
		clear(t.typeNameCache.resolved)
	}
	t.typeNameCacheEpoch = t.typeEnvEpoch
	t.typeNameCacheImportRev = rev
	return &t.typeNameCache
}

// invalidateTypeEnv marks the cached function environment stale. Every write
// to the state the conversion reads that has no change token of its own —
// t.functions, t.typeMetas and t.typeAliases — must call this, because several
// of them happen mid-traversal rather than at Transform entry. Writes to the
// import manager are deliberately absent: it carries a revision that
// functionTypeEnv compares directly.
func (t *galaASTTransformer) invalidateTypeEnv() {
	t.typeEnvEpoch++
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

// builtinTypeEnv holds the operator schemes addBuiltinsToEnv installs.
//
// The set is fixed, so it is built once instead of on every expression
// inference — a dozen nodes and a scheme per operator, per call. Sharing is
// safe for the same reason the cached function environment is: infer never
// writes through a Scheme or a Type, and these have no quantified variables,
// so nothing here is ever instantiated afresh.
var builtinTypeEnv = func() infer.TypeEnv {
	// Simple arithmetic operators
	intType := &infer.TypeConst{Name: "int"}
	intOp := &infer.TypeApp{Name: "->", Args: []infer.Type{intType, &infer.TypeApp{Name: "->", Args: []infer.Type{intType, intType}}}}

	boolType := &infer.TypeConst{Name: "bool"}
	compareOp := &infer.TypeApp{Name: "->", Args: []infer.Type{intType, &infer.TypeApp{Name: "->", Args: []infer.Type{intType, boolType}}}}

	env := make(infer.TypeEnv, 10)
	for _, op := range []string{"+", "-", "*", "/"} {
		env[op] = &infer.Scheme{Type: intOp}
	}
	for _, op := range []string{"==", "!=", "<", ">", "<=", ">="} {
		env[op] = &infer.Scheme{Type: compareOp}
	}
	return env
}()

func (t *galaASTTransformer) addBuiltinsToEnv(env infer.TypeEnv) {
	for name, scheme := range builtinTypeEnv {
		env[name] = scheme
	}
}
