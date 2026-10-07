package transformer

import (
	"fmt"
	"go/ast"
	"go/token"
	"slices"

	"github.com/antlr4-go/antlr/v4"

	"martianoff/gala/galaerr"
	"martianoff/gala/internal/parser/grammar"
	"martianoff/gala/internal/transpiler"
)

// One GALA value returned as Go's several results
//
// The reverse of go_results.go. A function Go calls with several results —
// a method implementing io.Writer's `Write([]byte) (int, error)`, or a lambda
// passed where Go expects a `func() (T, error)` — computes, in GALA, the one
// value those results make (`(int, error)` is a Try[int], `(A, B)` a
// Tuple[A, B]), and returns it to Go spread over the results:
//
//	func (c *Counter) Write(p []byte) (int, error) {
//		_goResult := func() std.Try[int] { ...body... }()
//		if _goResult.IsFailure() {
//			return *new(int), _goResult.GetError()
//		}
//		return _goResult.Get(), nil
//	}
//
// A body that is a Go call with those results already (`() =>
// strconv.Atoi(s)`) returns the call's results as they are.

// goResultsName is the local holding the body's value while it is spread.
const goResultsName = "_goResult"

// liftDeclaredGoResults turns funcType's results, when sig declares a Go result
// list, into the one GALA value they make, the result type the body is lowered
// against, and returns the Go results for returnGoResults to restore. It
// returns nil for any other signature.
func (t *galaASTTransformer) liftDeclaredGoResults(funcType *ast.FuncType, sig *grammar.SignatureContext) (*ast.FieldList, error) {
	if sig.GoResults() == nil || funcType.Results == nil || len(funcType.Results.List) < 2 {
		return nil, nil
	}
	goResults := funcType.Results
	value, err := t.goResultsValue(goResults)
	if err != nil {
		pos := sig.GoResults().GetStart()
		return nil, galaerr.NewSemanticErrorAt(pos.GetLine(), pos.GetColumn(), err.Error())
	}
	funcType.Results = &ast.FieldList{List: []*ast.Field{{Type: t.typeToExpr(value.Type)}}}
	return goResults, nil
}

// goResultsValue is the GALA value of a Go result list: what a call of a Go
// function with these results is in GALA.
func (t *galaASTTransformer) goResultsValue(results *ast.FieldList) (transpiler.GoResultValue, error) {
	types := make([]transpiler.Type, len(results.List))
	for i, f := range results.List {
		types[i] = t.astTypeToTranspilerType(f.Type)
	}
	value, _ := transpiler.GoResultValueOf(types)
	if value.Values > transpiler.MaxGoResultValues {
		return value, fmt.Errorf("a Go result list of %d values is more than the %d a Tuple holds", value.Values, transpiler.MaxGoResultValues)
	}
	if value.Type == nil || value.Type.IsNil() {
		return value, fmt.Errorf("the type of a result in this Go result list could not be resolved")
	}
	return value, nil
}

// spreadLambdaGoResults returns lit, a lambda lowered against slotType,
// spreading its one value over Go's results when slotType is a function type
// with several (see lambdaExpectation). A result the slot leaves open (a type
// parameter of a generic Go callee) takes its type from the lambda's value:
// `Try[V]` makes `(V, error)`, `TupleN[...]` makes its N components. lambda is
// the source, for the error when the value cannot make the results.
func (t *galaASTTransformer) spreadLambdaGoResults(lit ast.Expr, slotType transpiler.Type, lambda antlr.ParserRuleContext) (ast.Expr, error) {
	fn, ok := lit.(*ast.FuncLit)
	if !ok {
		return lit, nil
	}
	ft := t.resolveTranspilerTypeAsFuncType(slotType)
	if ft == nil || len(ft.Results) < 2 {
		return lit, nil
	}
	var valueType ast.Expr
	if fn.Type.Results != nil && len(fn.Type.Results.List) == 1 {
		valueType = fn.Type.Results.List[0].Type
	}
	var value transpiler.Type = transpiler.NilType{}
	if valueType != nil {
		value = t.astTypeToTranspilerType(valueType)
	}
	results := t.goResultsOfValue(value, len(ft.Results))
	if results == nil {
		got := "no value"
		if valueType != nil {
			got = "a value of type " + value.String()
		}
		want := "a Try for `(T, error)`, a Tuple for `(A, B)`"
		if v, _ := transpiler.GoResultValueOf(ft.Results); !transpiler.ContainsUnusable(v.Type) {
			want = "a " + v.Type.String()
		}
		return nil, galaerr.NewSemanticErrorAt(lambda.GetStart().GetLine(), lambda.GetStart().GetColumn(),
			fmt.Sprintf("this lambda is passed where Go expects a function with %d results, so it must give %s, but it gives %s", len(ft.Results), want, got))
	}
	fn.Body = t.returnGoResults(fn.Body, valueType, results)
	fn.Type.Results = results
	return fn, nil
}

// goResultsThunk lifts expr, of type valueType, into a thunk returning the Go
// results slot — the by-name form of `() => expr` where a `func() (A, B)` is
// expected (see wrapExprAsThunkIfNeeded). The thunk has the slot's results
// when they are all known, as a `func() T` thunk has the slot's T, so a Go
// call with assignable results (`os.Open(p)` for `func() (io.Reader,
// error)`) fills it; a result left open (a type parameter of a generic Go
// callee) takes its type from expr's value. ok is false when expr's value
// cannot make the results.
func (t *galaASTTransformer) goResultsThunk(expr ast.Expr, valueType transpiler.Type, slot []transpiler.Type) (ast.Expr, bool) {
	var results *ast.FieldList
	if v, _ := transpiler.GoResultValueOf(slot); !transpiler.ContainsUnusable(v.Type) && !t.hasTypeParams(v.Type) {
		valueType = v.Type
		results = t.resultFieldList(slot)
	} else {
		results = t.goResultsOfValue(valueType, len(slot))
	}
	if results == nil {
		return expr, false
	}
	body := &ast.BlockStmt{List: []ast.Stmt{&ast.ReturnStmt{Results: []ast.Expr{expr}}}}
	return &ast.FuncLit{
		Type: &ast.FuncType{Params: &ast.FieldList{}, Results: results},
		Body: t.returnGoResults(body, t.typeToExpr(valueType), results),
	}, true
}

// goResultsOfValue lists the n Go results a value of valueType is spread over
// (see transpiler.GoResultsOf), or nil when it cannot make them.
func (t *galaASTTransformer) goResultsOfValue(valueType transpiler.Type, n int) *ast.FieldList {
	if transpiler.ContainsUnusable(valueType) {
		return nil
	}
	types, ok := transpiler.GoResultsOf(valueType, n)
	if !ok || slices.ContainsFunc(types, transpiler.ContainsUnusable) {
		return nil
	}
	return t.resultFieldList(types)
}

// resultFieldList is a function type's result list of types.
func (t *galaASTTransformer) resultFieldList(types []transpiler.Type) *ast.FieldList {
	results := &ast.FieldList{List: make([]*ast.Field, len(types))}
	for i, typ := range types {
		results.List[i] = &ast.Field{Type: t.typeToExpr(typ)}
	}
	return results
}

// returnGoResults rewrites body, which returns one value of valueType, to
// return that value spread over goResults (see the comment at the top).
func (t *galaASTTransformer) returnGoResults(body *ast.BlockStmt, valueType ast.Expr, goResults *ast.FieldList) *ast.BlockStmt {
	var value ast.Expr
	if len(body.List) == 1 {
		if ret, ok := body.List[0].(*ast.ReturnStmt); ok && len(ret.Results) == 1 {
			value = ret.Results[0]
		}
	}
	// A Go call with these results is returned as it is.
	if res := t.goResults[asCall(value)]; res != nil && res.count() == len(goResults.List) {
		return &ast.BlockStmt{List: []ast.Stmt{&ast.ReturnStmt{Results: []ast.Expr{res.raw}}}}
	}
	if value == nil {
		value = &ast.CallExpr{Fun: thunkLit(body, valueType)}
	}
	results := make([]ast.Expr, len(goResults.List))
	for i, f := range goResults.List {
		results[i] = f.Type
	}
	fails := len(results) > 0 && t.astTypeToTranspilerType(results[len(results)-1]).String() == "error"
	values := results
	if fails {
		values = results[:len(results)-1]
	}
	// Declared with its type, so a value of another type is reported against it.
	stmts := []ast.Stmt{&ast.DeclStmt{Decl: &ast.GenDecl{Tok: token.VAR, Specs: []ast.Spec{&ast.ValueSpec{
		Names: []*ast.Ident{ast.NewIdent(goResultsName)}, Type: valueType, Values: []ast.Expr{value},
	}}}}}
	if !fails {
		return &ast.BlockStmt{List: append(stmts, &ast.ReturnStmt{Results: tupleFieldGets(ast.NewIdent(goResultsName), len(values))})}
	}
	// if _goResult.IsFailure() { return *new(A), ..., _goResult.GetError() }
	failure := make([]ast.Expr, 0, len(results))
	for _, v := range values {
		failure = append(failure, &ast.StarExpr{X: &ast.CallExpr{Fun: ast.NewIdent("new"), Args: []ast.Expr{v}}})
	}
	failure = append(failure, methodCall(goResultsName, "GetError"))
	stmts = append(stmts, &ast.IfStmt{
		Cond: methodCall(goResultsName, "IsFailure"),
		Body: &ast.BlockStmt{List: []ast.Stmt{&ast.ReturnStmt{Results: failure}}},
	})
	success := methodCall(goResultsName, transpiler.MethodGet)
	var successResults []ast.Expr
	if len(values) == 1 {
		successResults = []ast.Expr{success}
	} else {
		successResults = tupleFieldGets(success, len(values))
	}
	return &ast.BlockStmt{List: append(stmts, &ast.ReturnStmt{Results: append(successResults, ast.NewIdent("nil"))})}
}

// tupleFieldGets reads the n components of the Tuple tuple: tuple.V1.Get(), ….
// A Tuple's fields are vals, held as Immutable.
func tupleFieldGets(tuple ast.Expr, n int) []ast.Expr {
	out := make([]ast.Expr, n)
	for i := range out {
		field := &ast.SelectorExpr{X: tuple, Sel: ast.NewIdent(fmt.Sprintf("V%d", i+1))}
		out[i] = &ast.CallExpr{Fun: &ast.SelectorExpr{X: field, Sel: ast.NewIdent(transpiler.MethodGet)}}
	}
	return out
}
