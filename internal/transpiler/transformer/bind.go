package transformer

import (
	"go/ast"
	"go/token"
	"strconv"
	"strings"

	"github.com/antlr4-go/antlr/v4"

	"martianoff/gala/internal/parser/grammar"
	"martianoff/gala/internal/transpiler"
)

// bind.go implements the `bind` monadic do-notation.
//
// A block that contains `bind name = e` statements is lowered to a chain of
// FlatMap calls, so that
//
//	bind o = fetchOrder(id)
//	bind p = charge(o)
//	Success(Receipt(o, p))
//
// becomes
//
//	M_FlatMap[R, A](fetchOrder(id), func(o A) M[R] {
//	    return M_FlatMap[R, B](charge(o), func(p B) M[R] {
//	        return Success(Receipt(o, p))
//	    })
//	})
//
// where M is the block's monad (Try/Option/Either/Future or any user type with a
// FlatMap method) and M[R] is the enclosing function's return type. The lowering
// is structural: no type is special-cased, so a user-defined monad works exactly
// like std's. See docs/BIND_NOTATION.MD.

// bindDeclFromStatement returns the BindDeclaration context if the statement is a
// `bind name = expr`, else nil.
func bindDeclFromStatement(stmtCtx grammar.IStatementContext) grammar.IBindDeclarationContext {
	sc, ok := stmtCtx.(*grammar.StatementContext)
	if !ok || sc == nil {
		return nil
	}
	dc, ok := sc.Declaration().(*grammar.DeclarationContext)
	if !ok || dc == nil {
		return nil
	}
	if bd := dc.BindDeclaration(); bd != nil {
		return bd
	}
	return nil
}

// alsoDeclFromStatement returns the AlsoDeclaration context if the statement is an
// `also name = expr`, else nil.
func alsoDeclFromStatement(stmtCtx grammar.IStatementContext) grammar.IAlsoDeclarationContext {
	sc, ok := stmtCtx.(*grammar.StatementContext)
	if !ok || sc == nil {
		return nil
	}
	dc, ok := sc.Declaration().(*grammar.DeclarationContext)
	if !ok || dc == nil {
		return nil
	}
	if ad := dc.AlsoDeclaration(); ad != nil {
		return ad
	}
	return nil
}

// trailingBindValueExpr extracts the value expression from a block's trailing
// statement (e.g. `Success(...)`), or nil if the statement is not a bare
// expression.
func trailingBindValueExpr(stmtCtx grammar.IStatementContext) grammar.IExpressionContext {
	sc, ok := stmtCtx.(*grammar.StatementContext)
	if !ok || sc == nil {
		return nil
	}
	dc, ok := sc.Declaration().(*grammar.DeclarationContext)
	if !ok || dc == nil {
		return nil
	}
	ss, ok := dc.SimpleStatement().(*grammar.SimpleStatementContext)
	if !ok || ss == nil {
		return nil
	}
	return ss.Expression()
}

// bindEntry is one `bind`/`also` clause: a name and its monadic RHS.
type bindEntry struct {
	name    string
	exprCtx grammar.IExpressionContext
	typeCtx grammar.ITypeContext
	ctx     antlr.ParserRuleContext
}

// preppedBind is a bindEntry after its RHS has been transformed and typed.
type preppedBind struct {
	name           string
	recvExpr       ast.Expr
	recvType       transpiler.Type
	lookupBaseName string
	elemType       transpiler.Type
}

// collectBindGroup splits the leading `bind` and any consecutive `also` clauses
// into one group, returning that group and the statements after it.
func collectBindGroup(stmts []grammar.IStatementContext) ([]bindEntry, []grammar.IStatementContext) {
	bd := bindDeclFromStatement(stmts[0])
	group := []bindEntry{{bd.Identifier().GetText(), bd.Expression(), bd.Type_(), bd}}
	i := 1
	for i < len(stmts) {
		ad := alsoDeclFromStatement(stmts[i])
		if ad == nil {
			break
		}
		group = append(group, bindEntry{ad.Identifier().GetText(), ad.Expression(), ad.Type_(), ad})
		i++
	}
	return group, stmts[i:]
}

// checkBindGroupIndependence rejects a clause that references a binding
// introduced by a *sibling* clause of the same product group. A group is a
// leading `bind` plus one or more `also` clauses; its clauses are evaluated
// independently (concurrently for Future, error-accumulating for Validated), so
// a sibling's bound value is genuinely not available. The clauses' RHS were
// transformed before any group name was added to scope, so a same-group name
// appearing in a clause can only be a forward reference — unless that name also
// names a variable already in the enclosing scope (a legitimate outer binding
// the sibling would merely shadow), which is left alone. Emitting a clear
// diagnostic here replaces the raw Go "undefined: x" that the generated code
// would otherwise produce far from the source.
func (t *galaASTTransformer) checkBindGroupIndependence(group []bindEntry, prepped []preppedBind) error {
	if len(group) < 2 {
		return nil // a lone `bind` has no sibling to leak from.
	}
	for i := range group {
		siblings := make(map[string]bool)
		for j := range group {
			if j == i {
				continue
			}
			name := group[j].name
			if name == "" || name == "_" || t.isNameInScope(name) {
				continue
			}
			siblings[name] = true
		}
		if ref := firstReferencedName(prepped[i].recvExpr, siblings); ref != "" {
			return t.semanticErrorAt(group[i].ctx,
				"cannot reference `"+ref+"` here: clauses in a `bind`/`also` group are "+
					"evaluated independently, so a binding from one clause is not in scope "+
					"for its siblings. Move this clause to its own `bind` if it must see `"+ref+"`.")
		}
	}
	return nil
}

// isNameInScope reports whether name resolves to a variable in the current
// lexical scope chain. Group names are added to scope only after independence
// checking, so during that check this distinguishes an outer binding from a
// not-yet-bound same-group sibling.
func (t *galaASTTransformer) isNameInScope(name string) bool {
	for s := t.currentScope; s != nil; s = s.parent {
		if _, ok := s.vals[name]; ok {
			return true
		}
	}
	return false
}

// firstReferencedName returns the first identifier in expr whose name is in
// names, or "" if none.
func firstReferencedName(expr ast.Expr, names map[string]bool) string {
	if expr == nil || len(names) == 0 {
		return ""
	}
	found := ""
	ast.Inspect(expr, func(n ast.Node) bool {
		if found != "" {
			return false
		}
		if id, ok := n.(*ast.Ident); ok && names[id.Name] {
			found = id.Name
			return false
		}
		return true
	})
	return found
}

// desugarBindChain lowers a run of statements beginning with a `bind` into a
// FlatMap/Zip call expression of type resultType (the enclosing monad M[R]).
func (t *galaASTTransformer) desugarBindChain(stmts []grammar.IStatementContext, resultType transpiler.Type) (ast.Expr, error) {
	group, rest := collectBindGroup(stmts)

	prepped := make([]preppedBind, len(group))
	for i, e := range group {
		p, err := t.prepBindEntry(e, resultType)
		if err != nil {
			return nil, err
		}
		prepped[i] = p
	}

	if err := t.checkBindGroupIndependence(group, prepped); err != nil {
		return nil, err
	}

	// An `also` group whose monad provides a Zip combinator uses it (Future runs
	// concurrently, Validated accumulates errors). Otherwise — a lone `bind`, or a
	// fail-fast monad with no Zip — it lowers to sequential FlatMap, which yields
	// the same result for fail-fast types.
	if n := len(prepped); n >= 2 && n <= 10 {
		zipName := "Zip" + strconv.Itoa(n)
		if t.monadHasMethod(prepped[0].lookupBaseName, zipName) {
			return t.buildAlsoZip(prepped, rest, resultType, zipName)
		}
	}
	return t.buildSequential(prepped, rest, resultType)
}

// prepBindEntry transforms and types one clause's RHS and validates bindability.
func (t *galaASTTransformer) prepBindEntry(e bindEntry, resultType transpiler.Type) (preppedBind, error) {
	recvExpr, err := t.transformExpression(e.exprCtx)
	if err != nil {
		return preppedBind{}, err
	}
	recvType, lookupBaseName := t.resolveReceiverTypeAndLookupKey(recvExpr)
	// Normalize a current-package-qualified key (e.g. `main.Box`) to its bare
	// form (`Box`) so metadata lookup and the emitted `Box_FlatMap` name match.
	// std keys keep their `std.` prefix; imported packages keep theirs.
	lookupBaseName = strings.TrimPrefix(lookupBaseName, t.packageName+".")

	// Structural bindability check: M must expose a FlatMap method. No type is
	// special-cased — std and user monads resolve through the same metadata.
	// Fail with a clear diagnostic here rather than letting a missing method
	// surface as a cryptic Go compile error in the generated FlatMap call.
	typeMeta, _ := t.getTypeMetaResolved(lookupBaseName)
	if typeMeta == nil || typeMeta.Methods["FlatMap"] == nil {
		base := strings.TrimPrefix(recvType.BaseName(), t.packageName+".")
		return preppedBind{}, t.semanticErrorAt(e.ctx,
			"cannot `bind`/`also` over "+recvType.String()+": it is not a bindable monad. "+
				"A bindable type must define a FlatMap method, e.g. "+
				"`func (m "+base+"[T]) FlatMap[U](f func(T) "+base+"[U]) "+base+"[U]`")
	}

	// The bound value's element type A. An explicit annotation on the clause wins.
	elemType := t.monadElemType(recvType)
	if e.typeCtx != nil {
		te, terr := t.transformType(e.typeCtx)
		if terr != nil {
			return preppedBind{}, terr
		}
		elemType = t.astTypeToTranspilerType(te)
	}

	// Same-monad requirement: the clause's monad must match the block's monad.
	// (Heterogeneous lift is a separate feature.) Compare normalized base names.
	resultBase := strings.TrimPrefix(resultType.BaseName(), t.packageName+".")
	if !resultType.IsNil() && lookupBaseName != resultBase {
		return preppedBind{}, t.semanticErrorAt(e.ctx, "cannot `bind` a "+lookupBaseName+" inside a "+resultBase+" block (heterogeneous bind is not supported)")
	}

	return preppedBind{e.name, recvExpr, recvType, lookupBaseName, elemType}, nil
}

// buildSequential chains the clauses as nested FlatMaps (each bound name stays in
// scope for the rest), then the continuation. Used for a lone `bind` and as the
// fail-fast fallback for an `also` group with no Zip.
func (t *galaASTTransformer) buildSequential(prepped []preppedBind, rest []grammar.IStatementContext, resultType transpiler.Type) (ast.Expr, error) {
	p := prepped[0]
	// The bound name is a GALA `val` (immutable), so register it with addVal and
	// have reads emit `.Get()`. The FlatMap callback param, however, is a RAW `A`,
	// so we bind it to a distinct raw parameter and rebind that raw value to the
	// Immutable-backed val (`name := NewImmutable(<rawParam>)`) at the top of the
	// lambda body — the same shape a plain `val name = <expr>` produces.
	rawParam := bindRawParamName(p.name)
	t.addVal(p.name, p.elemType)

	var body *ast.BlockStmt
	var err error
	if len(prepped) > 1 {
		inner, ierr := t.buildSequential(prepped[1:], rest, resultType)
		if ierr != nil {
			return nil, ierr
		}
		body = &ast.BlockStmt{List: []ast.Stmt{&ast.ReturnStmt{Results: []ast.Expr{inner}}}}
	} else {
		body, err = t.buildBindBody(rest, resultType)
		if err != nil {
			return nil, err
		}
	}
	body.List = append([]ast.Stmt{t.bindRebindStmt(p.name, rawParam)}, body.List...)

	lambda := t.bindLambda(rawParam, p.elemType, resultType, body)
	return t.emitGenericMethodFreeFunc("FlatMap", p.recvExpr, p.recvType, p.lookupBaseName, t.resultElemTypeArgs(resultType), nil, []ast.Expr{lambda}, false), nil
}

// bindRawParamName returns the internal name of the raw FlatMap callback
// parameter that backs the immutable bound `name`.
func bindRawParamName(name string) string {
	return "_bind_" + name
}

// bindRebindStmt builds `name := NewImmutable(rawParam)` — it lifts the raw
// FlatMap callback value into the Immutable[T] storage a GALA `val` expects, so
// the bound name behaves exactly like any other immutable local.
func (t *galaASTTransformer) bindRebindStmt(name, rawParam string) ast.Stmt {
	t.needsStdImport = true
	return &ast.AssignStmt{
		Lhs: []ast.Expr{ast.NewIdent(name)},
		Tok: token.DEFINE,
		Rhs: []ast.Expr{&ast.CallExpr{
			Fun:  t.stdIdent(transpiler.FuncNewImmutable),
			Args: []ast.Expr{ast.NewIdent(rawParam)},
		}},
	}
}

// buildAlsoZip lowers an `also` group to `M_Zip{N}(e0, e1, ...).FlatMap((g) => {
// a0 := g.V1; a1 := g.V2; ...; <continuation> })`. The Zip runs the clauses
// independently (per the type: concurrently for Future, accumulating for
// Validated); the FlatMap threads all bound names into the continuation.
func (t *galaASTTransformer) buildAlsoZip(prepped []preppedBind, rest []grammar.IStatementContext, resultType transpiler.Type, zipName string) (ast.Expr, error) {
	n := len(prepped)

	// Zip call: method type args are the element types of clauses 1..n-1; the
	// receiver's own type args (e.g. E, A of Validated[E, A]) are appended by
	// emitGenericMethodFreeFunc.
	var zipTypeArgs []ast.Expr
	var zipArgs []ast.Expr
	for i := 1; i < n; i++ {
		zipTypeArgs = append(zipTypeArgs, t.typeToExpr(prepped[i].elemType))
		zipArgs = append(zipArgs, prepped[i].recvExpr)
	}
	zipCall := t.emitGenericMethodFreeFunc(zipName, prepped[0].recvExpr, prepped[0].recvType, prepped[0].lookupBaseName, zipTypeArgs, nil, zipArgs, false)

	// The Zip result element is a std tuple TupleN[A0, A1, ...]; the whole Zip
	// result is M[TupleN]. Build both as transpiler types.
	tupleName := "std.Tuple"
	if n >= 3 {
		tupleName = "std.Tuple" + strconv.Itoa(n)
	}
	elemStrs := make([]string, n)
	for i, p := range prepped {
		elemStrs[i] = p.elemType.String()
	}
	tupleType := transpiler.ParseType(tupleName + "[" + strings.Join(elemStrs, ", ") + "]")
	zipResultType := t.withElemType(prepped[0].recvType, tupleType)

	// FlatMap lambda: func(g TupleN) M[R] { a0 := g.V1; ...; <continuation> }.
	const tupleParam = "_zip"
	lambdaBody := &ast.BlockStmt{}
	for i, p := range prepped {
		// std tuples store their fields Immutable-wrapped (Tuple.Vi is
		// Immutable[Ai]), so binding the field straight to a GALA `val` needs no
		// extra wrapping — the same shape transformValTuplePattern produces for
		// `val (a, b) = tuple`. Registering the name as a val (not a raw var) is
		// what makes reads emit `.Get()`, unwrapping the field exactly once; a raw
		// var would leave the field Immutable-wrapped and a downstream constructor
		// would wrap it a second time (Immutable[Immutable[T]]).
		t.addVal(p.name, p.elemType)
		lambdaBody.List = append(lambdaBody.List, &ast.AssignStmt{
			Lhs: []ast.Expr{ast.NewIdent(p.name)},
			Tok: token.DEFINE,
			Rhs: []ast.Expr{&ast.SelectorExpr{X: ast.NewIdent(tupleParam), Sel: ast.NewIdent("V" + strconv.Itoa(i+1))}},
		})
	}
	restBody, err := t.buildBindBody(rest, resultType)
	if err != nil {
		return nil, err
	}
	lambdaBody.List = append(lambdaBody.List, restBody.List...)

	lambda := t.bindLambda(tupleParam, tupleType, resultType, lambdaBody)
	return t.emitGenericMethodFreeFunc("FlatMap", zipCall, zipResultType, prepped[0].lookupBaseName, t.resultElemTypeArgs(resultType), nil, []ast.Expr{lambda}, false), nil
}

// bindLambda builds `func(param paramType) M[R] { body }`.
func (t *galaASTTransformer) bindLambda(param string, paramType, resultType transpiler.Type, body *ast.BlockStmt) *ast.FuncLit {
	return &ast.FuncLit{
		Type: &ast.FuncType{
			Params: &ast.FieldList{List: []*ast.Field{{
				Names: []*ast.Ident{ast.NewIdent(param)},
				Type:  t.typeToExpr(paramType),
			}}},
			Results: &ast.FieldList{List: []*ast.Field{{Type: t.typeToExpr(resultType)}}},
		},
		Body: body,
	}
}

// resultElemTypeArgs returns the FlatMap method-level type arg [R] (the result
// element of M[R]); the receiver element is appended by the emitter.
func (t *galaASTTransformer) resultElemTypeArgs(resultType transpiler.Type) []ast.Expr {
	if rElem := t.monadElemType(resultType); !rElem.IsNil() {
		return []ast.Expr{t.typeToExpr(rElem)}
	}
	return nil
}

// withElemType returns monadType with its single type argument replaced by elem
// (e.g. Validated[E, A] with elem=Tuple[..] -> conceptually M[Tuple]). Only the
// last type argument is replaced, which matches the M[T] single-element shape.
func (t *galaASTTransformer) withElemType(monadType, elem transpiler.Type) transpiler.Type {
	if gt, ok := monadType.(transpiler.GenericType); ok {
		params := append([]transpiler.Type{}, gt.Params...)
		if len(params) > 0 {
			params[len(params)-1] = elem
		} else {
			params = []transpiler.Type{elem}
		}
		return transpiler.GenericType{Base: gt.Base, Params: params}
	}
	return monadType
}

// monadHasMethod reports whether the type resolved from lookupBaseName exposes a
// method with the given name.
func (t *galaASTTransformer) monadHasMethod(lookupBaseName, method string) bool {
	typeMeta, _ := t.getTypeMetaResolved(lookupBaseName)
	return typeMeta != nil && typeMeta.Methods[method] != nil
}

// buildBindBody builds the body block of a bind continuation lambda from the
// statements following a `bind`. Regular statements are emitted as-is; a nested
// `bind` recurses; the trailing statement becomes `return <value>`.
func (t *galaASTTransformer) buildBindBody(stmts []grammar.IStatementContext, resultType transpiler.Type) (*ast.BlockStmt, error) {
	body := &ast.BlockStmt{}
	for i, s := range stmts {
		if alsoDeclFromStatement(s) != nil {
			return nil, t.semanticErrorAt(s.(*grammar.StatementContext), "`also` must directly follow a `bind` or `also`")
		}
		if bindDeclFromStatement(s) != nil {
			expr, err := t.desugarBindChain(stmts[i:], resultType)
			if err != nil {
				return nil, err
			}
			body.List = append(body.List, &ast.ReturnStmt{Results: []ast.Expr{expr}})
			return body, nil
		}
		if i == len(stmts)-1 {
			expr, err := t.transformTrailingBindValue(s, resultType)
			if err != nil {
				return nil, err
			}
			body.List = append(body.List, &ast.ReturnStmt{Results: []ast.Expr{expr}})
			return body, nil
		}
		stmt, err := t.transformStatement(s.(*grammar.StatementContext))
		if err != nil {
			return nil, err
		}
		body.List = append(body.List, stmt)
	}
	return body, nil
}

// transformTrailingBindValue transforms the block's trailing value expression
// (the monad's result) with resultType pushed as the expected type so that
// constructors like Success/Some infer their type argument.
func (t *galaASTTransformer) transformTrailingBindValue(stmtCtx grammar.IStatementContext, resultType transpiler.Type) (ast.Expr, error) {
	exprCtx := trailingBindValueExpr(stmtCtx)
	if exprCtx == nil {
		return nil, t.semanticErrorAt(stmtCtx.(*grammar.StatementContext), "a `bind` block must end with a value expression")
	}
	if !resultType.IsNil() {
		release := t.expectedArgTypes.push(resultType)
		defer release()
	}
	expr, err := t.transformExpression(exprCtx)
	if err != nil {
		return nil, err
	}
	return t.unwrapImmutable(expr), nil
}

// monadElemType returns the value element type of a monad type — the LAST type
// argument. For a single-arg monad (Try[T], Option[T]) that is T; for a two-arg
// monad (Either[A, B], Validated[E, A]) the value is the last arg (B / A), while
// the leading arg (the error/left side) is fixed. NilType if there are no args.
func (t *galaASTTransformer) monadElemType(monadType transpiler.Type) transpiler.Type {
	args := t.getReceiverTypeArgStrings(monadType)
	if len(args) == 0 {
		return transpiler.NilType{}
	}
	return transpiler.ParseType(args[len(args)-1])
}
