package transformer

import (
	"fmt"
	"go/ast"
	"strings"

	"github.com/antlr4-go/antlr/v4"

	"martianoff/gala/galaerr"
	"martianoff/gala/internal/parser/grammar"
	"martianoff/gala/internal/transpiler"
)

// Go multi-value call in a single-value position
//
// GALA has no multi-value expressions: a function returns one value, and
// several values travel together as a Tuple. A Go call that returns more than
// one value therefore has exactly three GALA spellings:
//
//	val a, b = goCall()      // bind every result by name
//	Try(goCall())            // (T, error) / (A, B, error) → Try[T] / Try[Tuple[A, B]]
//	val v = goCall()         // (T, error) only: the error panics
//
// Anywhere else the call used to be emitted verbatim, and the transformer typed
// it as its FIRST result. Matching on it treated the subject as a Tuple of that
// first type, so the build failed on generated code the author never wrote:
//
//	os.ReadFile(p) match { case (data, nil) => ... }
//	→ obj.V1 undefined (type []byte has no field or method V1)
//
// The same miscompile hit tuple destructuring, if-expression branches,
// expression-lambda bodies, single-name bindings of a call without an error
// result, and arguments that must be one value. These positions are now
// rejected in GALA as GALA-E0049, naming the call and pointing at the form that
// does say what was meant. The sole argument of a Go callee is left alone: Go
// spreads it over the parameters (`fmt.Println(strconv.Atoi(s))`).

// goMultiValueResultCount returns how many values expr yields when it is a
// call to a Go function or method with two or more results, and 0 otherwise.
// It also reports whether the last result is `error`, which decides the hint.
func (t *galaASTTransformer) goMultiValueResultCount(expr ast.Expr) (int, bool) {
	for {
		paren, ok := expr.(*ast.ParenExpr)
		if !ok {
			break
		}
		expr = paren.X
	}
	if t.isImmutableUnwrapCall(expr) {
		// `v.Get()` reading a val through its Immutable wrapper yields one
		// value, even when the wrapped Go type has a multi-value Get method.
		return 0, false
	}
	returns := t.resolveGoCallReturnTypes(expr)
	if len(returns) < 2 {
		return 0, false
	}
	last := returns[len(returns)-1]
	return len(returns), last != nil && last.String() == "error"
}

// isImmutableUnwrapCall reports whether expr is the `.Get()` that
// unwrapImmutable inserts to read a val through its std.Immutable wrapper.
func (t *galaASTTransformer) isImmutableUnwrapCall(expr ast.Expr) bool {
	call, ok := expr.(*ast.CallExpr)
	if !ok || len(call.Args) != 0 {
		return false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != transpiler.MethodGet {
		return false
	}
	// A val's scope entry records the unwrapped type, so the wrapper is
	// recognised by the name being a val. A user-written `v.Get()` on a val
	// arrives as `v.Get().Get()`, whose receiver is a call, not the name.
	if id, ok := sel.X.(*ast.Ident); ok && t.isVal(id.Name) {
		return true
	}
	return t.isImmutableType(t.getExprTypeName(sel.X))
}

// goMultiValueCallName renders the callee of a Go call for a diagnostic:
// `os.ReadFile`, or `.Method` when the receiver is itself an expression.
func (t *galaASTTransformer) goMultiValueCallName(expr ast.Expr) string {
	for {
		paren, ok := expr.(*ast.ParenExpr)
		if !ok {
			break
		}
		expr = paren.X
	}
	call, ok := expr.(*ast.CallExpr)
	if !ok {
		return "the call"
	}
	if name := t.extractFuncName(call.Fun); name != "" {
		return name
	}
	fun, _ := splitCallFunTypeArgs(call.Fun)
	if sel, ok := fun.(*ast.SelectorExpr); ok {
		return "." + sel.Sel.Name
	}
	return "the call"
}

// checkGoMultiValueInSingleValueSlot rejects a Go multi-value call standing
// where GALA needs exactly one value. slot names the position for the message
// ("a match subject", "an if-expression branch", ...). It returns nil when expr
// is not a multi-value Go call.
func (t *galaASTTransformer) checkGoMultiValueInSingleValueSlot(expr ast.Expr, ctx antlr.ParserRuleContext, slot string) error {
	if ctx == nil {
		return t.checkGoMultiValueInSpan(expr, nil, nil, slot)
	}
	return t.checkGoMultiValueInSpan(expr, ctx.GetStart(), ctx.GetStop(), slot)
}

// checkGoMultiValueInSpan is checkGoMultiValueInSingleValueSlot for a source
// span that is not a single parse-tree node, such as a match subject built
// from a primary expression and its postfix suffixes.
func (t *galaASTTransformer) checkGoMultiValueInSpan(expr ast.Expr, start, stop antlr.Token, slot string) error {
	n, errorLast := t.goMultiValueResultCount(expr)
	if n == 0 {
		return nil
	}
	name := t.goMultiValueCallName(expr)
	msg := fmt.Sprintf("%s returns %d values, but %s takes a single value", name, n, slot)
	return t.goMultiValueError(start, stop, msg, goMultiValueHint(name, n, errorLast))
}

// checkGoMultiValueTupleDestructure rejects `val (a, b) = goCall()`: the
// parenthesized form destructures a GALA Tuple, which a Go call does not return.
func (t *galaASTTransformer) checkGoMultiValueTupleDestructure(expr ast.Expr, ctx antlr.ParserRuleContext) error {
	n, _ := t.goMultiValueResultCount(expr)
	if n == 0 {
		return nil
	}
	name := t.goMultiValueCallName(expr)
	msg := fmt.Sprintf("%s returns %d values, not a Tuple, so it cannot be destructured with `val (...)`", name, n)
	hint := fmt.Sprintf("drop the parentheses — `val %s = %s(...)` binds each result by name", goMultiValueNames(n), name)
	return t.goMultiValueError(ctx.GetStart(), ctx.GetStop(), msg, hint)
}

// goMultiValueError builds the GALA-E0049 diagnostic, underlining start..stop
// when the span sits on one line.
func (t *galaASTTransformer) goMultiValueError(start, stop antlr.Token, msg, hint string) error {
	line, col := t.lastLine, t.lastCol
	if start != nil {
		line, col = start.GetLine(), start.GetColumn()
	}
	err := galaerr.NewCodedSemanticError(galaerr.CodeGoMultiValueInSingleValueSlot, line, col, msg, hint)
	if start != nil && stop != nil && stop.GetLine() == line {
		err = err.WithSpan(stop.GetColumn() + len([]rune(stop.GetText())))
	}
	return err
}

// isSingleValueArgSlot reports whether a call argument must be exactly one
// value. Go spreads a multi-value call over a callee's parameters only when it
// is the call's sole argument (`f(g())`), so any argument of a multi-argument
// call is a single-value slot, as is a parameter typed as a GALA Tuple — the
// shape an author reaching for "the call's results" would write.
func (t *galaASTTransformer) isSingleValueArgSlot(argCount int, expectedType transpiler.Type) bool {
	if argCount > 1 {
		return true
	}
	if expectedType == nil || expectedType.IsNil() {
		return false
	}
	if gen, ok := expectedType.(transpiler.GenericType); ok {
		return t.isTupleTypeName(gen.Base.String())
	}
	return false
}

// exprCtxAt returns the i-th expression of an expression list, falling back to
// the list itself, so a diagnostic points at the offending initializer.
func exprCtxAt(list grammar.IExpressionListContext, i int) antlr.ParserRuleContext {
	if list == nil {
		return nil
	}
	if exprs := list.AllExpression(); i < len(exprs) {
		return exprs[i]
	}
	return list
}

// goMultiValueHint names the GALA form for a Go call's results: Try when the
// last result is an error, a multi-name binding otherwise.
func goMultiValueHint(name string, n int, errorLast bool) string {
	if errorLast {
		return fmt.Sprintf("wrap it in `Try(%s(...))` and match on `Success(v)` / `Failure(e)`, or bind the results with `val %s = %s(...)`", name, goMultiValueNames(n), name)
	}
	return fmt.Sprintf("bind the results by name first: `val %s = %s(...)`", goMultiValueNames(n), name)
}

// goMultiValueNames renders placeholder binding names for n results:
// `a, b` / `a, b, c` / ...
func goMultiValueNames(n int) string {
	names := make([]string, n)
	for i := range names {
		names[i] = string(rune('a' + i))
	}
	return strings.Join(names, ", ")
}
