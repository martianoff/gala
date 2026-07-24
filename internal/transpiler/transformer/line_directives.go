package transformer

import (
	"fmt"
	"go/ast"
	"go/token"
	"strings"

	"github.com/antlr4-go/antlr/v4"

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
// Granularity: one marker before each statement in a block, and one before each
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
//
// These approximations are accepted limitations of this slice.

// emitLineMarkers reports whether source-mapped `//line` directives should be
// emitted. They require a known source file to point at; when transpiling an
// anonymous snippet (empty filePath, e.g. LSP completion) there is no source map
// to build, so markers are suppressed.
func (t *galaASTTransformer) emitLineMarkers() bool {
	return t.filePath != ""
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
func lineMarkerDecl(line int) ast.Decl {
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
// identifier's position — turns a silent miscompile into a clear error. ctx
// should point at the identifier being declared.
func (t *galaASTTransformer) checkReservedName(name string, ctx antlr.ParserRuleContext) error {
	if strings.HasPrefix(name, transpiler.LineMarkerPrefix) {
		return t.semanticErrorAt(ctx, fmt.Sprintf(
			"identifier %q uses the reserved %q prefix (reserved for transpiler-internal source-map markers); rename it",
			name, transpiler.LineMarkerPrefix))
	}
	return nil
}
