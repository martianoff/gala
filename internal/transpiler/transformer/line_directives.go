package transformer

import (
	"go/ast"
	"go/token"

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
// top-level declaration. IIFE-lowered constructs (match / if-expression / bind
// synthesize relocated blocks) still receive per-statement markers inside their
// generated blocks, but because those blocks are moved relative to the GALA
// source, the mapping there is approximate — a panic inside a match arm reports a
// line near, not necessarily exactly at, the arm. This is an accepted limitation
// of this slice; per-statement mapping in ordinary function/method bodies is
// exact.

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
