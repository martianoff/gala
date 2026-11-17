package transformer

import (
	"fmt"
	"go/ast"
	"go/token"
	"strings"

	"martianoff/gala/internal/transpiler"
)

// Source-mapped line directives
// -----------------------------
// GALA transpiles to Go, so a runtime panic or stack trace defaults to reporting
// positions in the generated Go rather than the GALA source the developer wrote.
// To restore GALA positions, the transformer stamps each emitted statement and
// top-level declaration with a marker node encoding its originating GALA line;
// after code generation, a text pass (transpiler.insertLineDirectives) rewrites
// those markers into Go `//line <file>:<n>` directives.
//
// The marker route is used because synthetic Go AST nodes are position-less
// (token.NoPos) — the same reason //go:embed pragmas are injected as a text pass
// (see transpiler.insertEmbedDirectives). Markers survive gofmt re-parsing and
// are rewritten to column-0 `//line` directives, which the Go compiler honors.
//
// Granularity: a marker goes before
//   - each statement in a block (walkBlock),
//   - the body of a Go function whose source is an expression: an
//     expression-bodied function or method, an expression or placeholder
//     lambda, an argument lifted into a thunk (exprFuncBody),
//   - the statement holding each expression branch of an if-expression and
//     each expression arm of a match (exprMark, markExprStmts), an `else if`
//     of a discarded if-expression included,
//   - the lowered match or if-expression itself: the `if` of an
//     if-expression, the subject binding of a match (startMark), inside the
//     function literal or block it lowers to,
//   - the body and each expression branch of a self-tail-call loop (tco.go),
//   - each top-level declaration.
// A directive maps the first physical Go line of the statement's generated
// code back to its GALA line. A match or if-expression lowered to a function
// literal whose value is used also ends that literal's body in the marker of
// the line it ends on (endBranchingCalls), so the code after the call, in the
// same Go statement, counts on from the GALA line it is on.
//
// Precision:
//   - Exact for a GALA statement, branch or arm that lowers to a single Go
//     statement (one physical line, or a multi-line expression whose panic
//     originates on its first line): the panic reports the GALA line.
//   - Exact for the condition of an if-expression, the subject of a match,
//     and the code after one in its statement, when the code after it is on
//     the line it ends on.
//   - Approximate for a GALA statement that expands to MULTIPLE Go statements
//     (e.g. `use x = acquire` → `x := acquire` plus `defer x.Close()`): only the
//     first Go line carries the directive, so later Go lines auto-increment and
//     attribute to the following GALA source line.
//   - Approximate for the generated lines of a match that no marker leads:
//     the tests of its arms and a `bind` chain's plumbing count on from the
//     marker above them, so a panic there reports a line near, not
//     necessarily at, the GALA code.
//   - Approximate after a lambda or thunk inside a statement: the markers in
//     its body reset Go's line counter in the middle of the enclosing
//     statement, so the Go lines of that statement after the func literal
//     count on from the lambda's line, not the statement's. Block lambdas
//     have always done this; expression lambdas and thunks do too.
//
// These approximations are accepted limitations of this slice.

// emitLineMarkers reports whether source-mapped `//line` directives should be
// emitted. They require a known source file to point at; when transpiling an
// anonymous snippet (empty filePath, e.g. LSP completion) there is no source map
// to build, so markers are suppressed. They are also suppressed inside a default
// value declared in another file, whose line numbers are not this file's.
func (t *galaASTTransformer) emitLineMarkers() bool {
	return t.filePath != "" && (t.loweringDefault == nil || !t.loweringDefault.foreign)
}

// lineMarkerStmt builds the statement-position marker for a 1-based GALA line: a
// bare identifier expression statement (`__gala_line_<n>`). insertLineDirectives
// rewrites it into a `//line` directive. It is always emitted immediately before
// the statement it annotates and is never the trailing statement of a block
// while the file is lowered, so downstream trailing-expression-to-return
// promotion never wraps it; only endBranchingCalls, which runs once the file
// is complete, ends a block in one.
func lineMarkerStmt(line int) ast.Stmt {
	return &ast.ExprStmt{X: ast.NewIdent(transpiler.LineMarkerName(line))}
}

// lineMarkerDecl builds the top-level marker for a 1-based GALA line
// (`var __gala_line_<n> int`). A bare identifier statement is not valid at file
// scope, so top-level markers use a var declaration; insertLineDirectives
// rewrites it (dropping gofmt's separating blank line) into a `//line` directive.
func lineMarkerDecl(line int) *ast.GenDecl {
	return &ast.GenDecl{
		Tok: token.VAR,
		Specs: []ast.Spec{
			&ast.ValueSpec{
				Names: []*ast.Ident{ast.NewIdent(transpiler.LineMarkerName(line))},
				Type:  ast.NewIdent("int"),
			},
		},
	}
}

// checkReservedName rejects a user GALA identifier that intrudes on the reserved
// `__gala_line_` marker namespace. Such a name (e.g. a top-level
// `var __gala_line_7 int`) is byte-for-byte identical to a source-map marker, so
// the post-generation //line rewrite could not tell it apart and would silently
// delete the user's declaration. Rejecting it up front — with the offending
// identifier's position — turns a silent miscompile into a clear error. at is
// the identifier being declared.
func (t *galaASTTransformer) checkReservedName(name string, at srcNode) error {
	if strings.HasPrefix(name, transpiler.LineMarkerPrefix) {
		return t.semanticErrorIn(at.rng(), fmt.Sprintf(
			"identifier %q uses the reserved %q prefix (reserved for transpiler-internal source-map markers); rename it",
			name, transpiler.LineMarkerPrefix))
	}
	return nil
}

// exprFuncBody is body, a Go block holding the lowering of the expression e,
// led by the line marker of e (markExprStmts). Such a block is the body of a
// Go function whose GALA source is an expression: an expression-bodied
// function or method (`= expr`), an expression lambda (`=> expr`), a
// placeholder lambda (`f(_ + 1)`) or an argument lifted into a thunk
// (`Try(expr)`). A block body marks each of its statements (walkBlock), but an
// expression body has no statement of its own to mark: without this marker
// the Go statement it lowers to sits on the line after the enclosing
// directive, so Go reports a panic in it one GALA line late.
//
// The marker sits directly above the statement holding the expression, below
// the nil checks of Ptr parameters (withPtrEntryChecks), which a function
// gets after this call. A body whose value is spread over Go's results is
// not marked here: returnGoResults takes the marker (and a lambda's checks)
// and puts it right above the statement that holds the value, inside the
// spread's thunk when there is one (see finishLambda, lowerFuncDecl and,
// for an argument lifted into a thunk, goResultsThunk).
//
// A zero e (a block lambda's expression) leaves body as it is.
func (t *galaASTTransformer) exprFuncBody(e exprSrc, body *ast.BlockStmt) *ast.BlockStmt {
	if body == nil || len(body.List) == 0 {
		return body
	}
	mark := t.exprMark(e)
	if mark == nil {
		return body
	}
	return prependStmts(body, mark)
}

// markExprStmts is stmts, the lowering of the expression e, led by the line
// marker of e (exprMark). Expression arms of a match and expression branches
// of a discarded if-expression mark their statements with it, and the
// self-tail-call loop its body and branches (tco.go).
func (t *galaASTTransformer) markExprStmts(e exprSrc, stmts ...ast.Stmt) []ast.Stmt {
	return markedStmts(t.exprMark(e), stmts...)
}

// markedStmts is stmts led by mark, in a new slice; stmts as they are when
// there is no marker or no statement, so a marker never ends a block.
func markedStmts(mark ast.Stmt, stmts ...ast.Stmt) []ast.Stmt {
	if mark == nil || len(stmts) == 0 {
		return stmts
	}
	return append(append(make([]ast.Stmt, 0, len(stmts)+1), mark), stmts...)
}

// exprMark is the line marker of the expression e, or nil when markers are
// off or e is zero. An if-expression branch that is an expression carries it
// as its mark (lowerIfExprBranchValue), which withValue puts right above the
// statement that returns, stores or runs the branch's value.
func (t *galaASTTransformer) exprMark(e exprSrc) ast.Stmt {
	if !e.ok() {
		return nil
	}
	return t.startMark(e.at())
}

// startMark is the line marker of the line at starts on, or nil when markers
// are off.
func (t *galaASTTransformer) startMark(at srcNode) ast.Stmt {
	if !t.emitLineMarkers() {
		return nil
	}
	line, _ := at.start()
	return lineMarkerStmt(line)
}

// stopLine is the line at ends on, the last line of its last token (a
// multi-line string spans several), or 0 when markers are off: the line the
// code after it in the same Go statement is on (see endBranchingCalls).
func (t *galaASTTransformer) stopLine(at srcNode) int {
	i := at.node.StopIndex()
	if !t.emitLineMarkers() || i < 0 {
		return 0
	}
	tree := at.node.Tree()
	return int(tree.Token(i).Line) + strings.Count(tree.Text(i), "\n")
}

// endBranchingCalls ends the body of each function literal a match or
// if-expression whose value is used lowered to (recordBranchingCall) in the
// line marker of the line the construct ends on. The markers of its branches
// and arms reset Go's line counter inside the literal, and the Go code after
// the call, on the line that closes the literal, would count on from the last
// of them: `(if (c) a else b) + 100 / n` would report a panic in `100 / n`
// on the else branch's line plus one. The marker is added once the file is
// complete: no lowering reads the literal's body after that, so none takes
// the marker for the body's trailing statement. The file is walked, as
// checkBranchingCalls walks it, so a lowering built for an alternative and
// then thrown away is left alone.
func (t *galaASTTransformer) endBranchingCalls(file *ast.File) {
	if len(t.branchingCalls) == 0 {
		return
	}
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if lit, isLit := call.Fun.(*ast.FuncLit); isLit && t.branchingCalls[call].end > 0 {
			lit.Body.List = append(lit.Body.List, lineMarkerStmt(t.branchingCalls[call].end))
		}
		return true
	})
}

// withLead is expr with its body led by mark, an expression's line marker
// (exprMark), when expr is a func literal with a body and mark is not nil;
// any other expr is returned as it is. It marks a thunk an argument is lifted
// into (wrapExprAsThunkIfNeeded), as exprFuncBody marks an expression body.
func withLead(expr ast.Expr, mark ast.Stmt) ast.Expr {
	if lit, ok := expr.(*ast.FuncLit); ok && mark != nil && lit.Body != nil && len(lit.Body.List) > 0 {
		lit.Body = prependStmts(lit.Body, mark)
	}
	return expr
}
