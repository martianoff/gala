package transformer

import (
	"fmt"
	"go/ast"
	"go/token"
	"slices"
	"strings"

	"martianoff/gala/galaerr"
	"martianoff/gala/internal/transpiler"
)

// One GALA value returned as Go's several results
//
// The reverse of go_results.go. A function Go calls with several results —
// a method implementing io.Writer's `Write([]byte) (int, error)`, or a lambda
// passed where Go expects a `func() (T, error)` — computes, in GALA, the one
// value those results make (`(int, error)` is a Try[int], `(A, B)` a
// Tuple[A, B]), and returns it to Go spread over the results. A Try is
// spread once, from a local of its type:
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
//   - a body whose values are Tuples of every result — a `(A, B)` result
//     list's value, or the Tuple[T, error] go_interop's ResultsOf makes —
//     has each `return` spread where it is, from a local of the value's own
//     type, so a component only assignable to its result (an *os.File for
//     an io.Reader) is assigned there:
//
//	{
//		_goResult := ResultsOf(os.Open(p))
//		return _goResult.V1.Get(), _goResult.V2.Get()
//	}
//
// Which of the two a body with an `error` result gives is decided by its
// result values (see goResultsBody).

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
	value, _ := transpiler.GoResultValueOf(t.fieldListTypes(results))
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
// where the lambda is written; b follows its result values (see
// goResultsBodyFor), nil for a lambda of no Go-results slot.
func (t *galaASTTransformer) finishLambda(fn *ast.FuncLit, entryChecks []ast.Stmt, slotType transpiler.Type, at srcNode, body exprSrc, b *goResultsBody) (ast.Expr, error) {
	if slotType != nil {
		lead := append(slices.Clone(entryChecks), t.exprMark(body))
		spread, err := t.spreadLambdaGoResults(fn, slotType, at, body.ok(), b, lead)
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
// exprBody, b and lead are returnGoResults'. spread reports whether fn's body
// was rewritten.
func (t *galaASTTransformer) spreadLambdaGoResults(fn *ast.FuncLit, slotType transpiler.Type, at srcNode, exprBody bool, b *goResultsBody, lead []ast.Stmt) (spread bool, err error) {
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
	fn.Body = t.returnGoResults(fn.Body, exprBody, valueType, results, b, lead...)
	fn.Type.Results = results
	return true, nil
}

// goResultsThunk lifts expr, of type valueType and written at at, into a
// thunk returning the Go results slot — the by-name form of `() => expr` where
// a `func() (A, B)` is expected (see wrapExprAsThunkIfNeeded). The thunk has
// the slot's results when they are all known, as a `func() T` thunk has the
// slot's T, so a Go call with assignable results (`os.Open(p)` for `func()
// (io.Reader, error)`) fills it, and expr is then a result value of a
// Go-results body like a lambda's; a result left open (a type parameter of a
// generic Go callee) takes its type from expr's value. ok is false when expr's
// value cannot make the results. mark, the expression's line marker (nil for
// none), goes right above the statement holding the value, as returnGoResults
// places an expression body's.
func (t *galaASTTransformer) goResultsThunk(expr ast.Expr, valueType transpiler.Type, slot []transpiler.Type, mark ast.Stmt, at srcNode) (ast.Expr, bool, error) {
	var results *ast.FieldList
	var b *goResultsBody
	if v, _ := transpiler.GoResultValueOf(slot); !transpiler.ContainsUnusable(v.Type) && !t.hasTypeParams(v.Type) {
		valueType = v.Type
		results = t.resultFieldList(slot)
		b = newGoResultsBody(slot, v.Type, "this argument")
		if err := t.noteGoResultsValue(b, expr, at); err != nil {
			return nil, false, err
		}
	} else {
		results = t.goResultsOfValue(valueType, len(slot))
	}
	if results == nil {
		return expr, false, nil
	}
	body := &ast.BlockStmt{List: []ast.Stmt{&ast.ReturnStmt{Results: []ast.Expr{expr}}}}
	return &ast.FuncLit{
		Type: &ast.FuncType{Params: &ast.FieldList{}, Results: results},
		Body: t.returnGoResults(body, true, t.typeToExpr(valueType), results, b, mark),
	}, true, nil
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

// fieldListTypes is the types of a function type's result list, one per field.
func (t *galaASTTransformer) fieldListTypes(results *ast.FieldList) []transpiler.Type {
	types := make([]transpiler.Type, len(results.List))
	for i, f := range results.List {
		types[i] = t.astTypeToTranspilerType(f.Type)
	}
	return types
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
// return that value spread over goResults (see the comment at the top). b
// follows the body's result values when the results end in `error` (nil
// otherwise, and for a slot whose results are not all known).
//
// The lowering is chosen by what the body is, never by how many statements
// it was lowered to:
//   - exprBody, an expression body (`= expr`, `=> expr`, a placeholder
//     lambda or a lifted thunk) that lowers to a single Go call with these
//     results, returns the call's results as they are.
//   - A body whose values are Tuples of every result — results without an
//     `error`, or a body b saw give the Tuple of all of them — has each
//     `return` spread where it is.
//   - Any other body has its one value, a Try, spread: a failed Try returns
//     the zero values with its error. A body that is one `return` gives the
//     value directly; any other runs in a thunk.
//
// body holds only the body's own statements. lead (nil entries skipped) is
// what must run first: a lambda's Ptr nil checks, then the line marker of an
// expression body. It goes right above the statement that holds the body's
// value — the `return` of the Go call, the spread's `var`, or the thunk's
// first statement — so that statement carries the expression's line.
func (t *galaASTTransformer) returnGoResults(body *ast.BlockStmt, exprBody bool, valueType ast.Expr, goResults *ast.FieldList, b *goResultsBody, lead ...ast.Stmt) *ast.BlockStmt {
	lead = slices.DeleteFunc(slices.Clone(lead), func(s ast.Stmt) bool { return s == nil })
	value := soleReturnValue(body)
	// An expression that is a Go call with these results is returned as it is.
	if res := t.goResults[asCall(value)]; exprBody && res != nil && res.count() == len(goResults.List) {
		return &ast.BlockStmt{List: append(lead, &ast.ReturnStmt{Results: []ast.Expr{res.raw}})}
	}
	results := make([]ast.Expr, len(goResults.List))
	for i, f := range goResults.List {
		results[i] = f.Type
	}
	fails := t.astTypeToTranspilerType(results[len(results)-1]).String() == "error"
	if !fails || b.givesWhole() {
		return &ast.BlockStmt{List: append(lead, t.spreadTupleReturns(body.List, len(results))...)}
	}
	if value == nil {
		value = &ast.CallExpr{Fun: thunkLit(prependStmts(body, lead...), valueType)}
		lead = nil
	}
	values := results[:len(results)-1]
	resultName, valueName := t.generatedName(goResultsName), t.generatedName(goValueName)
	// Declared with its type, so a value of another type is reported against it.
	stmts := append(lead, &ast.DeclStmt{Decl: &ast.GenDecl{Tok: token.VAR, Specs: []ast.Spec{&ast.ValueSpec{
		Names: []*ast.Ident{ast.NewIdent(resultName)}, Type: valueType, Values: []ast.Expr{value},
	}}}})
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

// soleReturnValue is v when body is exactly `return v`, nil otherwise.
func soleReturnValue(body *ast.BlockStmt) ast.Expr {
	if body == nil || len(body.List) != 1 {
		return nil
	}
	if ret, ok := body.List[0].(*ast.ReturnStmt); ok && len(ret.Results) == 1 {
		return ret.Results[0]
	}
	return nil
}

// spreadTupleReturns rewrites each `return v` of stmts, v a Tuple of n
// values, to return v's components where it is:
//
//	_goResult := v
//	return _goResult.V1.Get(), …
//
// The local takes v's own type, so each component is assigned to its result
// on its own and need only be assignable to it. The local's statement takes
// the `return`'s place, below its line marker. The `return`s of a nested
// function are its own.
func (t *galaASTTransformer) spreadTupleReturns(stmts []ast.Stmt, n int) []ast.Stmt {
	out := make([]ast.Stmt, 0, len(stmts))
	for _, s := range stmts {
		if ret, ok := s.(*ast.ReturnStmt); ok && len(ret.Results) == 1 {
			out = append(out, t.spreadTupleReturn(ret, n)...)
			continue
		}
		out = append(out, t.spreadTupleReturnsIn(s, n))
	}
	return out
}

// spreadTupleReturn is the statements returning the components of ret's one
// value, a Tuple of n values (see spreadTupleReturns).
func (t *galaASTTransformer) spreadTupleReturn(ret *ast.ReturnStmt, n int) []ast.Stmt {
	name := t.generatedName(goResultsName)
	return []ast.Stmt{
		&ast.AssignStmt{Lhs: []ast.Expr{ast.NewIdent(name)}, Tok: token.DEFINE, Rhs: ret.Results},
		&ast.ReturnStmt{Results: tupleFieldGets(ast.NewIdent(name), n)},
	}
}

// spreadTupleReturnsIn is spreadTupleReturns inside one statement that is not
// a `return`.
func (t *galaASTTransformer) spreadTupleReturnsIn(s ast.Stmt, n int) ast.Stmt {
	switch s := s.(type) {
	case *ast.ReturnStmt:
		if len(s.Results) == 1 {
			return &ast.BlockStmt{List: t.spreadTupleReturn(s, n)}
		}
	case *ast.BlockStmt:
		s.List = t.spreadTupleReturns(s.List, n)
	case *ast.IfStmt:
		s.Body.List = t.spreadTupleReturns(s.Body.List, n)
		if s.Else != nil {
			s.Else = t.spreadTupleReturnsIn(s.Else, n)
		}
	case *ast.ForStmt:
		s.Body.List = t.spreadTupleReturns(s.Body.List, n)
	case *ast.RangeStmt:
		s.Body.List = t.spreadTupleReturns(s.Body.List, n)
	case *ast.SwitchStmt:
		s.Body.List = t.spreadTupleReturns(s.Body.List, n)
	case *ast.TypeSwitchStmt:
		s.Body.List = t.spreadTupleReturns(s.Body.List, n)
	case *ast.SelectStmt:
		s.Body.List = t.spreadTupleReturns(s.Body.List, n)
	case *ast.CaseClause:
		s.Body = t.spreadTupleReturns(s.Body, n)
	case *ast.CommClause:
		s.Body = t.spreadTupleReturns(s.Body, n)
	case *ast.LabeledStmt:
		s.Stmt = t.spreadTupleReturnsIn(s.Stmt, n)
	}
	return s
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
// components as they are.
//
// Each result value of such a body — a function's or lambda's trailing value
// or `return` value, each branch or arm of an if or match giving one, a
// by-name argument — is lowered against the body's slot, which carries the
// body's goResultsBody (slot.goResults), and checked where it is written
// (lowerGoResultsValue). One body gives one kind.

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

// newGoResultsBody follows a body Go calls with results, whose default value
// is value. It is nil when the results do not end in `error` (their value, a
// Tuple, already is every result) or when one is not known.
func newGoResultsBody(results []transpiler.Type, value transpiler.Type, subject string) *goResultsBody {
	if len(results) < 2 || !isErrorType(results[len(results)-1]) || transpiler.ContainsUnusable(value) {
		return nil
	}
	whole, fits := transpiler.TupleOf(results)
	if !fits {
		return nil
	}
	return &goResultsBody{results: results, value: value, whole: whole, subject: subject}
}

// goResultsBodyFor follows the body of a lambda filling a slot of type
// slotType, when it is a function type with results ending in `error`.
func (t *galaASTTransformer) goResultsBodyFor(slotType transpiler.Type) *goResultsBody {
	ft := t.resolveTranspilerTypeAsFuncType(slotType)
	if ft == nil {
		return nil
	}
	v, ok := transpiler.GoResultValueOf(ft.Results)
	if !ok || t.hasTypeParams(v.Type) {
		return nil
	}
	return newGoResultsBody(ft.Results, v.Type, "this lambda")
}

// givesWhole reports whether the body gives the Tuple of every result.
func (b *goResultsBody) givesWhole() bool {
	return b != nil && b.wholeAt.ok()
}

// isWholeTupleLiteral reports whether e is a tuple literal of one element per
// result, `(n, nil)`: it is lowered against the Tuple of the results, so its
// `nil` is the error.
func (b *goResultsBody) isWholeTupleLiteral(e exprSrc) bool {
	p := e.solePostfix()
	if !p.ok() || !p.alone() {
		return false
	}
	_, n := p.parenFirst()
	return n == len(b.results)
}

// lowerGoResultsValue lowers e, a result value of the Go-results body s
// belongs to (s.goResults), against s, and checks it (noteGoResultsValue).
// A tuple literal of every result is lowered against their Tuple.
func (t *galaASTTransformer) lowerGoResultsValue(e exprSrc, s slot, strict bool) (ast.Expr, error) {
	b := s.goResults
	s.goResults = nil
	if b.isWholeTupleLiteral(e) {
		s.typ = b.whole
	}
	expr, err := t.lowerAgainst(e, s, strict)
	if err != nil {
		return nil, err
	}
	return expr, t.noteGoResultsValue(b, expr, e.at())
}

// noteGoResultsValue checks expr, a result value of the body b follows,
// written at at: it must be the Try of the results or the Tuple of every
// result, through any alias, and one body gives one of them. A value whose
// type is not known, or one that may still be either (an interface, a type
// parameter), is left to Go.
func (t *galaASTTransformer) noteGoResultsValue(b *goResultsBody, expr ast.Expr, at srcNode) error {
	if b == nil || t.isNoReturnCallExpr(expr) {
		return nil
	}
	written := t.getExprTypeName(expr)
	if transpiler.ContainsUnusable(written) {
		return nil
	}
	typ := t.followAliasChain(written)
	if components, ok := transpiler.TupleComponents(typ, len(b.results)); ok {
		for i, r := range b.results {
			if t.cannotHold(r, components[i]) {
				return b.wrongValueError(at, written)
			}
		}
		if b.valueAt.ok() {
			return b.mixedError(at, written, b.value, b.valueAt)
		}
		if !b.wholeAt.ok() {
			b.wholeAt = at
		}
		return nil
	}
	if _, ok := transpiler.TryElem(typ); ok {
		if b.wholeAt.ok() {
			return b.mixedError(at, written, b.whole, b.wholeAt)
		}
		if !b.valueAt.ok() {
			b.valueAt = at
		}
		return nil
	}
	if t.cannotHold(b.value, typ) {
		return b.wrongValueError(at, written)
	}
	return nil
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
		fmt.Sprintf("%s gives Go the results %s, so its value is a %s (on a failure Go receives the zero value with the error) or a %s of the results as they are; this value is of type %s",
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
