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
// does say what was meant. The sole argument of a call is left to Go, which
// spreads it over the callee's parameters when their count matches
// (`fmt.Println(strconv.Atoi(s))`) and reports the mismatch otherwise.

// goMultiValueResultCount returns how many values expr yields when it is a
// call to a Go function or method with two or more results, and 0 otherwise.
// It also reports whether the last result is `error`, which decides the hint.
func (t *galaASTTransformer) goMultiValueResultCount(expr ast.Expr) (int, bool) {
	expr = ast.Unparen(expr)
	if t.isImmutableUnwrapCall(expr) {
		// `v.Get()` reading a val through its Immutable wrapper yields one
		// value, even when the wrapped Go type has a multi-value Get method.
		return 0, false
	}
	// The count and whether the last result is `error` (never a type
	// parameter) read off the declared signature; no instantiation needed.
	sig := t.resolveGoCallSignature(expr)
	if sig == nil || len(sig.Returns) < 2 {
		return 0, false
	}
	last := sig.Returns[len(sig.Returns)-1]
	return len(sig.Returns), last != nil && last.String() == "error"
}

// checkGoMultiValueArg rejects a Go multi-value call passed as a call argument
// that must be one value (see isSingleValueArgSlot). Lambdas are never calls.
func (t *galaASTTransformer) checkGoMultiValueArg(expr ast.Expr, exprCtx grammar.IExpressionContext, lambdaCtx *grammar.LambdaExpressionContext, argCount int, expectedType transpiler.Type) error {
	if lambdaCtx != nil || !t.isSingleValueArgSlot(argCount, expectedType) {
		return nil
	}
	return t.checkGoMultiValueInSingleValueSlot(expr, exprCtx, "this argument")
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

// goCallee names the callee of a Go call for a diagnostic. When the callee can
// be written back as GALA (`os.ReadFile`, a val's method `v.Read`), callee
// holds it and the hints quote it. When the receiver is itself an expression
// (`exec.Command("ls").Output()`), only the method is named.
type goCallee struct {
	callee string // pasteable callee, or "" when the receiver is an expression
	method string // the method name, used when callee is ""
}

// subject renders the callee as the subject of the diagnostic's message.
func (c goCallee) subject() string {
	if c.callee != "" {
		return c.callee
	}
	if c.method != "" {
		return "the `" + c.method + "` call"
	}
	return "the call"
}

// call renders a placeholder call for a hint: `os.ReadFile(...)`, or `...`.
func (c goCallee) call() string {
	if c.callee != "" {
		return c.callee + "(...)"
	}
	return "..."
}

func (t *galaASTTransformer) goMultiValueCallee(expr ast.Expr) goCallee {
	call, ok := ast.Unparen(expr).(*ast.CallExpr)
	if !ok {
		return goCallee{}
	}
	if name := t.extractFuncName(call.Fun); name != "" {
		return goCallee{callee: name}
	}
	fun, _ := splitCallFunTypeArgs(call.Fun)
	if sel, ok := fun.(*ast.SelectorExpr); ok {
		return goCallee{method: sel.Sel.Name}
	}
	return goCallee{}
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
	c := t.goMultiValueCallee(expr)
	msg := fmt.Sprintf("%s returns %d values, but %s takes a single value", c.subject(), n, slot)
	hint := goMultiValueHint(c, n, errorLast)
	if slot == slotIfBranch {
		// `if (c) fmt.Println("a") else ...` written for its effect alone is
		// still an if-expression; braced branches make it a statement.
		hint += "; if the value is not used, write an if statement with braced branches: `if (c) { ... } else { ... }`"
	}
	return t.goMultiValueError(start, stop, msg, hint)
}

// slotIfBranch names an if-expression branch in E0049 messages.
const slotIfBranch = "an if-expression branch"

// checkGoMultiValueTupleDestructure rejects `val (a, b) = goCall()`: the
// parenthesized form destructures a GALA Tuple, which a Go call does not return.
func (t *galaASTTransformer) checkGoMultiValueTupleDestructure(expr ast.Expr, ctx antlr.ParserRuleContext) error {
	n, _ := t.goMultiValueResultCount(expr)
	if n == 0 {
		return nil
	}
	c := t.goMultiValueCallee(expr)
	msg := fmt.Sprintf("%s returns %d values, not a Tuple, so it cannot be destructured with `val (...)`", c.subject(), n)
	hint := fmt.Sprintf("drop the parentheses — `val %s = %s` binds each result by name", goMultiValueNames(n), c.call())
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
// is the call's sole argument (`f(g())`), so any argument, positional or named,
// of a multi-argument call is a single-value slot, as is a parameter typed as a
// GALA Tuple — the shape an author reaching for "the call's results" would
// write. A sole argument otherwise stays with Go, which accepts the spread when
// the parameter count matches and reports the mismatch when it does not.
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
func goMultiValueHint(c goCallee, n int, errorLast bool) string {
	if errorLast {
		return fmt.Sprintf("wrap it in `Try(%s)` and match on `Success(v)` / `Failure(e)`, or bind the results with `val %s = %s`", c.call(), goMultiValueNames(n), c.call())
	}
	return fmt.Sprintf("bind the results by name first: `val %s = %s`", goMultiValueNames(n), c.call())
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
