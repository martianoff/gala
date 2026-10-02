package transformer

import (
	"fmt"
	"go/ast"
	"go/token"

	"martianoff/gala/galaerr"
	"martianoff/gala/internal/parser/grammar"
)

// Loop control: `break` and `continue`.
//
// GALA has no `break` / `continue` keyword in its grammar; each parses as a
// bare name used as a statement. It is lowered here to a real Go BranchStmt as
// soon as a statement is recognised as one, so no later step can mistake it for
// a value — the trailing statement of a block is otherwise promoted to the
// block's value (`return break`), and the value then dropped.
//
// Go resolves a `break` to the innermost enclosing for, switch or select of the
// same function. GALA lowers some constructs to a function literal — a lambda,
// a match or if-expression whose value is used — so a `break` written inside
// one of those, around which the source shows a loop, would name no loop in Go,
// or a different one. Whether each one reaches the user loop it is written in is
// checked on the finished file (checkLoopControl): only the BranchStmts lowered
// from source and the loops written in source take part, so a loop the
// transpiler synthesizes (the one tail-call elimination emits) never captures a
// user `break`.

// loopControlSite is the source position of a lowered `break` / `continue`.
type loopControlSite struct {
	line, col int
}

// loopControlKeyword reports whether exprCtx is a bare `break` or `continue`,
// and which.
// It runs on every expression statement, so it looks at the tokens rather than
// walking the precedence chain: a bare name is a single token.
func (t *galaASTTransformer) loopControlKeyword(exprCtx grammar.IExpressionContext) (token.Token, bool) {
	if exprCtx == nil {
		return token.ILLEGAL, false
	}
	start, stop := exprCtx.GetStart(), exprCtx.GetStop()
	if start == nil || stop == nil || start.GetTokenIndex() != stop.GetTokenIndex() {
		return token.ILLEGAL, false
	}
	return loopControlToken(start.GetText())
}

// loopControlToken maps the names `break` and `continue` to their Go tokens.
func loopControlToken(name string) (token.Token, bool) {
	switch name {
	case "break":
		return token.BREAK, true
	case "continue":
		return token.CONTINUE, true
	}
	return token.ILLEGAL, false
}

// lowerLoopControl lowers a statement that is a bare `break` or `continue` to a
// Go BranchStmt, recording its source position for checkLoopControl.
func (t *galaASTTransformer) lowerLoopControl(exprCtx grammar.IExpressionContext) (*ast.BranchStmt, bool) {
	tok, ok := t.loopControlKeyword(exprCtx)
	if !ok {
		return nil, false
	}
	stmt := &ast.BranchStmt{Tok: tok}
	if t.loopControlSites == nil {
		t.loopControlSites = make(map[*ast.BranchStmt]loopControlSite)
	}
	start := exprCtx.GetStart()
	t.loopControlSites[stmt] = loopControlSite{line: start.GetLine(), col: start.GetColumn()}
	return stmt, true
}

// markUserLoop records loop as a loop written in source: one a user `break` or
// `continue` can control.
func (t *galaASTTransformer) markUserLoop(loop ast.Stmt) {
	if t.userLoops == nil {
		t.userLoops = make(map[ast.Stmt]bool)
	}
	t.userLoops[loop] = true
}

// loopControlAsValueError rejects a `break` or `continue` used where a value is
// read (`val x = break`, `f(continue)`).
func (t *galaASTTransformer) loopControlAsValueError(ctx *grammar.PrimaryContext, name string) error {
	return galaerr.NewCodedSemanticError(
		galaerr.CodeLoopControlOutsideLoop,
		ctx.GetStart().GetLine(), ctx.GetStart().GetColumn(),
		fmt.Sprintf("`%s` is a statement, not a value", name),
		fmt.Sprintf("write `%s` on its own line inside a `for` loop", name))
}

// escapingLoopControl returns the first BranchStmt lowered from a source
// `break` / `continue` in stmts that is not inside a user loop or a function
// literal of stmts' own, or nil. Such a statement would have to leave the
// construct stmts belong to in order to reach its loop.
func (t *galaASTTransformer) escapingLoopControl(stmtLists ...[]ast.Stmt) *ast.BranchStmt {
	if len(t.loopControlSites) == 0 {
		return nil
	}
	c := loopControlChecker{t: t, ownFuncOnly: true}
	for _, stmts := range stmtLists {
		for _, s := range stmts {
			if s != nil && c.bad == nil {
				c.walk(s, false, false)
			}
		}
	}
	return c.bad
}

// checkNoLoopControlInValue rejects a `break` / `continue` in the branches of a
// match or if-expression whose value is used. The construct lowers to a
// function literal that must return a value, so loop control in it can neither
// produce one nor reach the loop around it. construct names it with its
// article ("a match", "an if-expression").
func (t *galaASTTransformer) checkNoLoopControlInValue(construct string, branches ...[]ast.Stmt) error {
	if bs := t.escapingLoopControl(branches...); bs != nil {
		return t.loopControlInValueError(construct, bs)
	}
	return nil
}

// loopControlInValueError is the GALA-E0059 for bs, a `break` / `continue`
// inside construct, whose value is used (see checkNoLoopControlInValue).
func (t *galaASTTransformer) loopControlInValueError(construct string, bs *ast.BranchStmt) error {
	site := t.loopControlSites[bs]
	return galaerr.NewCodedSemanticError(
		galaerr.CodeLoopControlOutsideLoop,
		site.line, site.col,
		fmt.Sprintf("`%s` inside %s whose value is used cannot reach the loop around it", bs.Tok, construct),
		fmt.Sprintf("%s whose value is used must produce one on every path; use it as a statement, or test the condition before it and `%s` there", construct, bs.Tok))
}

// checkLoopControl verifies, on the finished file, that every `break` and
// `continue` lowered from source sits inside a loop written in source, within
// the same Go function. It reports the first one that does not.
func (t *galaASTTransformer) checkLoopControl(file *ast.File) error {
	if len(t.loopControlSites) == 0 {
		return nil
	}
	c := loopControlChecker{t: t}
	c.walk(file, false, false)
	if c.bad == nil {
		return nil
	}
	site := t.loopControlSites[c.bad]
	kw := c.bad.Tok.String()
	if c.badCrossesFunc {
		return galaerr.NewCodedSemanticError(
			galaerr.CodeLoopControlOutsideLoop,
			site.line, site.col,
			fmt.Sprintf("`%s` cannot leave the lambda it is in to reach the loop around it", kw),
			fmt.Sprintf("a lambda is a separate function; `%s` inside it cannot control the caller's loop, so iterate with a `for` loop instead, or select the elements first (Filter, TakeWhile)", kw))
	}
	return galaerr.NewCodedSemanticError(
		galaerr.CodeLoopControlOutsideLoop,
		site.line, site.col,
		fmt.Sprintf("`%s` is not inside a `for` loop", kw),
		fmt.Sprintf("`%s` controls the innermost enclosing `for` loop; move it into a loop, or return from the function instead", kw))
}

// loopControlChecker walks a file tracking, for the current Go function,
// whether a user loop encloses the node (inLoop), and whether one encloses the
// function literal the node is in (outerLoop). With ownFuncOnly set it does not
// enter function literals: their loop control is checked on its own.
type loopControlChecker struct {
	t              *galaASTTransformer
	ownFuncOnly    bool
	bad            *ast.BranchStmt
	badCrossesFunc bool
}

func (c *loopControlChecker) walk(root ast.Node, inLoop, outerLoop bool) {
	ast.Inspect(root, func(n ast.Node) bool {
		if c.bad != nil {
			return false
		}
		switch x := n.(type) {
		case *ast.FuncLit:
			if !c.ownFuncOnly {
				c.walk(x.Body, false, inLoop || outerLoop)
			}
			return false
		case *ast.ForStmt:
			if c.t.userLoops[x] {
				c.walkLoop(x.Body, inLoop, outerLoop, x.Init, x.Cond, x.Post)
				return false
			}
		case *ast.RangeStmt:
			if c.t.userLoops[x] {
				c.walkLoop(x.Body, inLoop, outerLoop, x.Key, x.Value, x.X)
				return false
			}
		case *ast.BranchStmt:
			if _, ok := c.t.loopControlSites[x]; ok && !inLoop {
				c.bad, c.badCrossesFunc = x, outerLoop
			}
		}
		return true
	})
}

// walkLoop walks a user loop: its header parts in the enclosing state and its
// body inside the loop.
func (c *loopControlChecker) walkLoop(body *ast.BlockStmt, inLoop, outerLoop bool, header ...ast.Node) {
	for _, h := range header {
		if h != nil {
			c.walk(h, inLoop, outerLoop)
		}
	}
	c.walk(body, true, outerLoop)
}
