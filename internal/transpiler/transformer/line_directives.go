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
// Granularity: one marker before each statement in a block, one before the body
// of a Go function whose source is an expression (exprFuncBody), and one before each
// top-level declaration. A directive maps the first physical Go line of the
// statement's generated code back to its GALA line.
//
// Precision:
//   - Exact for a GALA statement that lowers to a single Go statement (one
//     physical line, or a multi-line expression whose panic originates on its
//     first line): the panic reports the GALA line.
//   - Approximate for a GALA statement that expands to MULTIPLE Go statements
//     (e.g. `use x = acquire` → `x := acquire` plus `defer x.Close()`): only the
//     first Go line carries the directive, so later Go lines auto-increment and
//     attribute to the following GALA source line.
//   - Approximate for IIFE-lowered constructs (match / if-expression / bind
//     synthesize relocated blocks): they still receive per-statement markers
//     inside their generated blocks, but because those blocks are moved relative
//     to the GALA source, a panic inside a match arm reports a line near, not
//     necessarily exactly at, the arm.
//   - Approximate after a lambda or thunk inside a statement: the markers in
//     its body reset Go's line counter in the middle of the enclosing
//     statement, so the Go lines of that statement after the func literal
//     count on from the lambda's line, not the statement's. Block lambdas
//     have always done this; expression lambdas and thunks now do too.
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
// the statement it annotates and is never the trailing statement of a block, so
// downstream trailing-expression-to-return promotion never wraps it.
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
// The marker must sit directly above the statement holding the expression,
// and the Go results rewrite (returnGoResults) must read the body as it did
// before markers, so ordering is part of the contract:
//   - The rewrite runs first and the marker is added to what it produced: it
//     decides by the body's statements, and a marker would add one. (It does
//     see walkBlock's per-statement markers in a block body, as it always
//     has.)
//   - The nil checks of Ptr parameters (withPtrEntryChecks) go above the
//     marker. A function gets them after this call. A lambda's checks are
//     already above its expression when the rewrite reads it, so finishLambda
//     adds checks and marker together before the rewrite, which moves both
//     into its thunk.
//
// A zero e (a block lambda's expression) leaves body as it is.
func (t *galaASTTransformer) exprFuncBody(e exprSrc, body *ast.BlockStmt) *ast.BlockStmt {
	if body == nil {
		return body
	}
	return &ast.BlockStmt{Lbrace: body.Lbrace, List: t.markExprStmts(e, body.List), Rbrace: body.Rbrace}
}

// markExprStmts is stmts, the lowering of the expression e, led by the line
// marker of e; stmts as they are when markers are off, e is zero or there are
// none. The self-tail-call loop marks its body and branches with it (tco.go).
func (t *galaASTTransformer) markExprStmts(e exprSrc, stmts []ast.Stmt) []ast.Stmt {
	if len(stmts) == 0 || !t.emitLineMarkers() || !e.ok() {
		return stmts
	}
	line, _ := e.at().start()
	return append([]ast.Stmt{lineMarkerStmt(line)}, stmts...)
}

// exprFuncLit is expr with its body led by e's line marker (exprFuncBody)
// when expr is a func literal whose source is the expression e; any other
// expr is returned as it is.
func (t *galaASTTransformer) exprFuncLit(e exprSrc, expr ast.Expr) ast.Expr {
	if lit, ok := expr.(*ast.FuncLit); ok {
		lit.Body = t.exprFuncBody(e, lit.Body)
	}
	return expr
}
