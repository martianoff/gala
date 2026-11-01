package transformer

import (
	"fmt"
	"go/ast"
	"go/token"
	"strings"

	"martianoff/gala/galaerr"
	"martianoff/gala/internal/transpiler"
)

// A call to a function that returns nothing has no value. It may stand alone
// as a statement (or be run by `go` / `defer`); anywhere else — an argument,
// a `val` initializer, an operand, a returned value — Go rejects the generated
// code with "(no value) used as value", far from the GALA source. Whether the
// call's value is used shows only in the finished file, after tail promotion
// and hoisting have settled where each call sits, so each call of void type is
// recorded with its source as it is lowered and checkVoidCalls walks the file.

// voidCallSite is the source position and text of a recorded call, and
// whether its callee is a local value (a lambda) rather than a function.
type voidCallSite struct {
	loopControlSite
	text   string
	lambda bool
}

// recordVoidCall records call, the lowering of the postfix expression whose
// last suffix is the call c, when its type is known to be void. A call of
// unknown or any other type is not recorded, nor counted as a give-up (see
// probeExprType). c need not carry its arguments.
func (t *galaASTTransformer) recordVoidCall(c callSrc, call ast.Expr) {
	ce, ok := call.(*ast.CallExpr)
	if !ok {
		return
	}
	if _, void := t.probeExprType(ce).(transpiler.VoidType); !void {
		return
	}
	if t.voidCalls == nil {
		t.voidCalls = make(map[*ast.CallExpr]voidCallSite)
	}
	lambda := false
	if _, chained := c.prev(); !chained {
		if callee := c.primaryText(); token.IsIdentifier(callee) {
			lambda = t.isVal(callee) || t.isVar(callee)
		}
	}
	line, col := c.post.at().start()
	t.voidCalls[ce] = voidCallSite{loopControlSite{line, col}, voidCallText(c), lambda}
}

// voidCallText is how a diagnostic quotes the postfix expression ending in
// the call c: as written when it is short and on one line, and otherwise as
// its callee followed by `(...)`, or "" when that is long too.
func voidCallText(c callSrc) string {
	text := c.post.at().sourceText()
	if len(text) <= 60 && !strings.ContainsAny(text, "\n\r") {
		return text
	}
	if callee := c.calleeText(); callee != "" {
		return callee + "(...)"
	}
	return ""
}

// checkVoidCalls reports a recorded call in file whose value is used: one not
// held by an expression statement or a `go` / `defer` statement. Of nested
// ones (`id(v())`, where id's result is void because v's is) it reports the
// innermost, the call that has no value to begin with. The file is walked
// rather than the map, as in checkBranchingCalls: a lowering built for an
// alternative that was then thrown away is in the map but not in the file.
func (t *galaASTTransformer) checkVoidCalls(file *ast.File) error {
	if len(t.voidCalls) == 0 {
		return nil
	}
	var stack []ast.Node
	var found *voidCallSite
	foundDepth, done := 0, false
	ast.Inspect(file, func(n ast.Node) bool {
		if done {
			return false
		}
		if n == nil {
			stack = stack[:len(stack)-1]
			if found != nil && len(stack) < foundDepth {
				done = true
			}
			return true
		}
		stack = append(stack, n)
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		site, ok := t.voidCalls[call]
		if !ok {
			return true
		}
		switch stack[holderIndex(stack, len(stack)-1)].(type) {
		case *ast.ExprStmt, *ast.GoStmt, *ast.DeferStmt:
			return true
		}
		if found == nil {
			foundDepth = len(stack)
		}
		found = &site
		return true
	})
	if found == nil {
		return nil
	}
	return voidCallValueError(*found)
}

// voidCallValueError is the error for the recorded call at site whose value is
// used though it produces none.
func voidCallValueError(site voidCallSite) error {
	subject := "this call"
	if site.text != "" {
		subject = "`" + site.text + "`"
	}
	hint := "call it as a statement on its own line, or declare a result type on the function so it returns a value"
	if site.lambda {
		hint = "a block lambda with no declared result type returns nothing; declare its result type, as in `() Option[int] => { ... }`, or call it as a statement on its own line"
	}
	err := galaerr.NewSemanticErrorAt(site.line, site.col,
		fmt.Sprintf("%s produces no value, but its value is used", subject))
	err.Hint = hint
	return err
}
