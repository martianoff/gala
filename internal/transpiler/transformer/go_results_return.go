package transformer

import (
	"fmt"
	"go/ast"
	"go/token"
	"slices"
	"strings"

	"martianoff/gala/galaerr"
	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/registry"
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
//		var _goResult std.Try[int] = func() std.Try[int] { ...body... }()
//		if _goResult.IsFailure() {
//			return *new(int), _goResult.GetError()
//		}
//		return _goResult.Get(), nil
//	}
//
// (A body that is one `return` assigns its value directly, with no thunk.)
// Two bodies return Go's results as they are instead:
//   - an EXPRESSION body that is a single Go call with those results
//     (`= strconv.Atoi(s)`, `() => strconv.Atoi(s)`) returns the call;
//   - a body whose value is the Tuple of every result, Tuple[T, error]
//     (go_interop's ResultsOf), returns its components:
//     `var _goResult std.Tuple[int, error] = …; return _goResult.V1.Get(),
//     _goResult.V2.Get()` (see goResultsBody).

// goResultsName is the local holding the body's value while it is spread.
const goResultsName = "_goResult"

// goValueName is the local holding a successful Try's Tuple while its
// components are returned.
const goValueName = "_goValue"

// liftDeclaredGoResults turns funcType's results, when sig declares a Go result
// list, into the one GALA value they make, the result type the body is lowered
// against, and returns the Go results for returnGoResults to restore. It
// returns nil for any other signature.
func (t *galaASTTransformer) liftDeclaredGoResults(funcType *ast.FuncType, sig sigSrc) (*ast.FieldList, error) {
	at, ok := sig.goResults()
	if !ok || funcType.Results == nil || len(funcType.Results.List) < 2 {
		return nil, nil
	}
	t.noteNativeShape(at, "goresults")
	goResults := funcType.Results
	value, err := t.goResultsValue(goResults)
	if err != nil {
		return nil, semanticErrorAtStart(at, err.Error())
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

// finishLambda is fn, a lambda or placeholder lambda lowered against
// slotType, as its value. Its body is led, in this order, by the nil checks
// of its Ptr parameters (entryChecks, see lowerLambda) and the line marker of
// body, the expression it is written as (exprMark; a zero body, a block
// lambda's, has none). When slotType is a function with several results, its
// one value is spread over them (spreadLambdaGoResults), which places the
// checks and the marker right above the statement holding the value. at is
// where the lambda is written.
func (t *galaASTTransformer) finishLambda(fn *ast.FuncLit, entryChecks []ast.Stmt, slotType transpiler.Type, at srcNode, body exprSrc) (ast.Expr, error) {
	if slotType != nil {
		lead := append(slices.Clone(entryChecks), t.exprMark(body))
		spread, err := t.spreadLambdaGoResults(fn, slotType, at, body.ok(), lead)
		if err != nil || spread {
			return fn, err
		}
	}
	fn.Body = withPtrEntryChecks(t.exprFuncBody(body, fn.Body), entryChecks)
	return fn, nil
}

// spreadLambdaGoResults returns lit, a lambda lowered against slotType,
// spreading its one value over Go's results when slotType is a function type
// with several (see lambdaExpectation). A result the slot leaves open (a type
// parameter of a generic Go callee) takes its type from the lambda's value:
// `Try[V]` makes `(V, error)`, `TupleN[...]` makes its N components. at is where
// the lambda is written, for the error when its value cannot make the results.
// exprBody and lead are returnGoResults'. spread reports whether fn's body was
// rewritten.
func (t *galaASTTransformer) spreadLambdaGoResults(fn *ast.FuncLit, slotType transpiler.Type, at srcNode, exprBody bool, lead []ast.Stmt) (spread bool, err error) {
	ft := t.resolveTranspilerTypeAsFuncType(slotType)
	if ft == nil || len(ft.Results) < 2 {
		return false, nil
	}
	var valueType ast.Expr
	if fn.Type.Results != nil && len(fn.Type.Results.List) == 1 {
		valueType = fn.Type.Results.List[0].Type
	}
	var value transpiler.Type = transpiler.NilType{}
	if valueType != nil {
		value = t.astTypeToTranspilerType(valueType)
	}
	// A body giving the Tuple of every result (ResultsOf) instead of the
	// Try its slot makes returns them as they are.
	if b := newGoResultsBodyOf(ft.Results, value, "this lambda"); b != nil && isStdTry(value) {
		if err := t.noteLambdaGoResults(fn.Body, b, at); err != nil {
			return false, err
		}
		if whole := b.wholeType(); whole != nil {
			value, valueType = whole, t.typeToExpr(whole)
			fn.Type.Results.List[0].Type = valueType
		}
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
		return false, semanticErrorAtStart(at,
			fmt.Sprintf("this lambda is passed where Go expects a function with %d results, so it must give %s, but it gives %s", len(ft.Results), want, got))
	}
	fn.Body = t.returnGoResults(fn.Body, exprBody, valueType, results, lead...)
	fn.Type.Results = results
	return true, nil
}

// goResultsThunk lifts expr, of type valueType, into a thunk returning the Go
// results slot — the by-name form of `() => expr` where a `func() (A, B)` is
// expected (see wrapExprAsThunkIfNeeded). The thunk has the slot's results
// when they are all known, as a `func() T` thunk has the slot's T, so a Go
// call with assignable results (`os.Open(p)` for `func() (io.Reader,
// error)`) fills it; a result left open (a type parameter of a generic Go
// callee) takes its type from expr's value. ok is false when expr's value
// cannot make the results. mark, the expression's line marker (nil for none),
// goes right above the statement holding the value, as returnGoResults
// places an expression body's.
func (t *galaASTTransformer) goResultsThunk(expr ast.Expr, valueType transpiler.Type, slot []transpiler.Type, mark ast.Stmt) (ast.Expr, bool) {
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
		Body: t.returnGoResults(body, true, t.typeToExpr(valueType), results, mark),
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
//
// The lowering is chosen by what the body is, never by how many statements
// it was lowered to:
//   - exprBody, an expression body (`= expr`, `=> expr`, a placeholder
//     lambda or a lifted thunk) that lowers to a single Go call with these
//     results, returns the call's results as they are.
//   - Any other body, a block body among them, has its one value spread: a
//     failed Try returns the zero values with its error. A body that is one
//     `return` gives the value directly; any other runs in a thunk.
//
// body holds only the body's own statements. lead (nil entries skipped) is
// what must run first: a lambda's Ptr nil checks, then the line marker of an
// expression body. It goes right above the statement that holds the body's
// value — the `return` of the Go call, the spread's `var`, or the thunk's
// first statement — so that statement carries the expression's line.
func (t *galaASTTransformer) returnGoResults(body *ast.BlockStmt, exprBody bool, valueType ast.Expr, goResults *ast.FieldList, lead ...ast.Stmt) *ast.BlockStmt {
	// lead is the caller's to give: a fresh variadic list, or finishLambda's
	// own copy of its checks.
	lead = slices.DeleteFunc(lead, func(s ast.Stmt) bool { return s == nil })
	var value ast.Expr
	if len(body.List) == 1 {
		if ret, ok := body.List[0].(*ast.ReturnStmt); ok && len(ret.Results) == 1 {
			value = ret.Results[0]
		}
	}
	// An expression that is a Go call with these results is returned as it is.
	if res := t.goResults[asCall(value)]; exprBody && res != nil && res.count() == len(goResults.List) {
		return &ast.BlockStmt{List: append(lead, &ast.ReturnStmt{Results: []ast.Expr{res.raw}})}
	}
	if value == nil {
		value = &ast.CallExpr{Fun: thunkLit(prependStmts(body, lead...), valueType)}
		lead = nil
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
	resultName, valueName := t.generatedName(goResultsName), t.generatedName(goValueName)
	// Declared with its type, so a value of another type is reported against it.
	stmts := append(lead, &ast.DeclStmt{Decl: &ast.GenDecl{Tok: token.VAR, Specs: []ast.Spec{&ast.ValueSpec{
		Names: []*ast.Ident{ast.NewIdent(resultName)}, Type: valueType, Values: []ast.Expr{value},
	}}}})
	// A Tuple of every result — the trailing error among them when the body
	// gives the Tuple ResultsOf makes — is the results as they are.
	if !fails || isResultsTuple(t.astTypeToTranspilerType(valueType), len(results)) {
		return &ast.BlockStmt{List: append(stmts, &ast.ReturnStmt{Results: tupleFieldGets(ast.NewIdent(resultName), len(results))})}
	}
	// if _goResult.IsFailure() { return *new(A), ..., _goResult.GetError() }
	failure := make([]ast.Expr, 0, len(results))
	for _, v := range values {
		failure = append(failure, zeroValueExpr(v))
	}
	failure = append(failure, methodCall(resultName, "GetError"))
	stmts = append(stmts, &ast.IfStmt{
		Cond: methodCall(resultName, "IsFailure"),
		Body: &ast.BlockStmt{List: []ast.Stmt{&ast.ReturnStmt{Results: failure}}},
	})
	success := methodCall(resultName, transpiler.MethodGet)
	successResults := []ast.Expr{success}
	if len(values) > 1 {
		// The Tuple is taken out of the Try once: _goValue := _goResult.Get().
		stmts = append(stmts, &ast.AssignStmt{Lhs: []ast.Expr{ast.NewIdent(valueName)}, Tok: token.DEFINE, Rhs: []ast.Expr{success}})
		successResults = tupleFieldGets(ast.NewIdent(valueName), len(values))
	}
	return &ast.BlockStmt{List: append(stmts, &ast.ReturnStmt{Results: append(successResults, ast.NewIdent("nil"))})}
}

// The value a Go-results body gives
//
// A body Go calls with results ending in `error` gives one GALA value. By
// default it is the Try those results make (`(T, error)` is a Try[T]): a
// failure gives Go the zero values with the error, Go's usual convention.
// Some Go APIs return a meaningful value with an error — io.Reader's Read
// (n > 0 with io.EOF), io.Writer's short write — and a body implementing one
// gives instead the Tuple of every result, Tuple[T, error], which
// go_interop's ResultsOf makes from a Go call. Go receives that Tuple's
// components as they are. Each result value of the body is checked as it is
// lowered (noteGoResultsValue); one body gives one kind.

// goResultsBody follows the result values of one Go-results body.
type goResultsBody struct {
	results []transpiler.Type // Go's results, the trailing error among them
	value   transpiler.Type   // the Try they make by default
	whole   transpiler.Type   // the Tuple of every result
	subject string            // "function F", "this lambda": the body's owner, for diagnostics
	// valueAt and wholeAt are the first result value of each kind; zero
	// when the body has none.
	valueAt, wholeAt srcNode
}

// newGoResultsBody follows a body Go calls with goResults, whose default
// value is value. It is nil when the results do not end in `error`: their
// value, a Tuple, already is every result.
func (t *galaASTTransformer) newGoResultsBody(goResults *ast.FieldList, value transpiler.Type, subject string) *goResultsBody {
	results := make([]transpiler.Type, len(goResults.List))
	for i, f := range goResults.List {
		results[i] = t.astTypeToTranspilerType(f.Type)
	}
	return newGoResultsBodyOf(results, value, subject)
}

// newGoResultsBodyOf is newGoResultsBody for result types.
func newGoResultsBodyOf(results []transpiler.Type, value transpiler.Type, subject string) *goResultsBody {
	name, fits := transpiler.TupleArityName(len(results))
	if !fits || results[len(results)-1].String() != "error" || transpiler.ContainsUnusable(value) {
		return nil
	}
	return &goResultsBody{
		results: results,
		value:   value,
		whole:   transpiler.GenericType{Base: transpiler.NamedType{Package: registry.StdPackageName, Name: name}, Params: results},
		subject: subject,
	}
}

// wholeType is the Tuple of every result when the body gives it, nil when
// it gives the default Try (or b is nil).
func (b *goResultsBody) wholeType() transpiler.Type {
	if b == nil || !b.wholeAt.ok() {
		return nil
	}
	return b.whole
}

// isResultsTuple reports whether typ is the std Tuple of n values.
func isResultsTuple(typ transpiler.Type, n int) bool {
	gen, ok := typ.(transpiler.GenericType)
	if !ok || len(gen.Params) != n {
		return false
	}
	name, fits := transpiler.TupleArityName(n)
	base, named := gen.Base.(transpiler.NamedType)
	return fits && named && base.Package == registry.StdPackageName && base.Name == name
}

// isStdTry reports whether typ is a std Try.
func isStdTry(typ transpiler.Type) bool {
	gen, ok := typ.(transpiler.GenericType)
	if !ok {
		return false
	}
	base, named := gen.Base.(transpiler.NamedType)
	return named && base.Package == registry.StdPackageName && base.Name == transpiler.TypeTry
}

// noteGoResultsValue checks expr, a result value of the body being lowered,
// written at at, when Go calls that body with results ending in `error`
// (returnSlot.goResults): it must be the Try of the results or the Tuple of
// every result, and one body gives one of them. A value whose type is not
// known, or one that may still be either (an interface, a type parameter), is
// left to Go.
func (t *galaASTTransformer) noteGoResultsValue(expr ast.Expr, at srcNode) error {
	return t.noteGoResultsValueIn(t.returnSlot.goResults, expr, at)
}

// noteGoResultsValueIn is noteGoResultsValue for the body b follows.
func (t *galaASTTransformer) noteGoResultsValueIn(b *goResultsBody, expr ast.Expr, at srcNode) error {
	if b == nil || t.isNoReturnCallExpr(expr) {
		return nil
	}
	typ := t.getExprTypeName(expr)
	if transpiler.ContainsUnusable(typ) {
		return nil
	}
	switch {
	case isResultsTuple(typ, len(b.results)):
		for i, r := range b.results {
			if t.cannotHold(r, typ.(transpiler.GenericType).Params[i]) {
				return b.wrongValueError(at, typ)
			}
		}
		if b.valueAt.ok() {
			return b.mixedError(at, typ, b.value, b.valueAt)
		}
		if !b.wholeAt.ok() {
			b.wholeAt = at
		}
	case isStdTry(typ):
		if b.wholeAt.ok() {
			return b.mixedError(at, typ, b.whole, b.wholeAt)
		}
		if !b.valueAt.ok() {
			b.valueAt = at
		}
	case t.cannotHold(b.value, typ):
		return b.wrongValueError(at, typ)
	}
	return nil
}

// noteLambdaGoResults checks each result value of body, a lambda's lowered
// body, as noteGoResultsValueIn does; at is where the lambda is written. Its
// trailing value is promoted to a `return` after lowering, so the values are
// read from the lowered body: its `return`s, never a nested function's.
func (t *galaASTTransformer) noteLambdaGoResults(body *ast.BlockStmt, b *goResultsBody, at srcNode) error {
	var err error
	ast.Inspect(body, func(n ast.Node) bool {
		if err != nil {
			return false
		}
		switch n := n.(type) {
		case *ast.FuncLit:
			return false
		case *ast.ReturnStmt:
			if len(n.Results) == 1 {
				err = t.noteGoResultsValueIn(b, n.Results[0], at)
			}
		}
		return true
	})
	return err
}

// resultsText spells the Go results: `(int, error)`.
func (b *goResultsBody) resultsText() string {
	parts := make([]string, len(b.results))
	for i, r := range b.results {
		parts[i] = displayType(r)
	}
	return "(" + strings.Join(parts, ", ") + ")"
}

// wrongValueError reports a result value of type got that is neither the
// body's Try nor the Tuple of its results.
func (b *goResultsBody) wrongValueError(at srcNode, got transpiler.Type) error {
	r := at.rng()
	return spanErrorAt(galaerr.CodeGoResultsBodyValue, r, r,
		fmt.Sprintf("%s gives Go the results %s, so its body gives a %s (on a failure Go receives the zero value with the error) or a %s of the results as they are; this value is of type %s",
			b.subject, b.resultsText(), displayType(b.value), displayType(b.whole), displayType(got)),
		fmt.Sprintf("give a %s or a %s; `Success(v)` or `Failure(err)` make the Try, and `ResultsOf(call)` from go_interop keeps both results of a Go call", displayType(b.value), displayType(b.whole)))
}

// mixedError reports a result value of type got in a body whose value
// written at otherAt is of the other kind, other.
func (b *goResultsBody) mixedError(at srcNode, got, other transpiler.Type, otherAt srcNode) error {
	r := at.rng()
	line, _ := otherAt.start()
	return spanErrorAt(galaerr.CodeGoResultsBodyValue, r, r,
		fmt.Sprintf("%s gives a %s here but a %s at line %d: a body Go calls with the results %s gives either the Try or the Tuple of the results, not both",
			b.subject, displayType(got), displayType(other), line, b.resultsText()),
		fmt.Sprintf("give one kind; a %s everywhere keeps every result as it is (`ResultsOf(call)`, or `(v, err)`)", displayType(b.whole)))
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
