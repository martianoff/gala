package analyzer

import (
	"fmt"
	"go/token"

	"martianoff/gala/galaerr"
	"martianoff/gala/internal/parser/grammar"
	"martianoff/gala/internal/transpiler/transformer"

	"github.com/antlr4-go/antlr/v4"
)

// GALA-E0055: a name spelled like a Go keyword.
//
// GALA emits every name the author wrote into the generated Go as written. The
// GALA grammar reserves some of Go's keywords itself (`func`, `type`, `if`,
// `for`, `map`, ...), so those never reach an identifier position. The rest —
// `break`, `chan`, `const`, `continue`, `default`, `defer`, `fallthrough`,
// `go`, `goto`, `select`, `switch` — lex as ordinary identifiers, and a val,
// parameter, field, function, type or import alias spelled like one produced
// Go that does not parse. The author then saw the internal GALA-E0017 ("the
// generated Go is not parseable") instead of a diagnostic about their source.
//
// Such a name is rejected, not renamed. Renaming (`go` -> `go_`) would be
// invisible for a local but not for an exported function, a struct field or a
// package-level val, which Go code, JSON field names and reflection see; and
// `break` / `continue` already mean loop control when written as a statement,
// so a binding of that name could never be referred to unambiguously. A rename
// the author chooses keeps every position consistent with no hidden mapping.
//
// Two uses are left to other checks: any use of `break` / `continue` other
// than as a declared name is loop control, which the transformer checks
// (GALA-E0059 where it cannot reach a loop), and a bare `defer`, `go`, `goto`, `fallthrough`, `select` or
// `chan` statement is GALA-E0036's, which names the GALA replacement.

// bareStatementKeywords are the keywords a bare statement may consist of
// without being a name: loop control, and GALA-E0036's statement keywords.
// Any other keyword standing alone as a statement (`switch`) is still E0055.
var bareStatementKeywords = func() map[string]bool {
	out := transformer.ForbiddenStatementKeywords()
	out["break"] = true
	out["continue"] = true
	return out
}()

// checkGoKeywordNames reports the first identifier in sf spelled like a Go
// keyword, preferring a declaration over a use, so a function named `go` that
// is called above its declaration is reported where it is declared — the place
// the rename starts. It returns nil when the file has none.
func checkGoKeywordNames(sf *grammar.SourceFileContext) error {
	var firstUse, firstDecl *grammar.IdentifierContext
	var visit func(antlr.Tree)
	visit = func(node antlr.Tree) {
		if firstDecl != nil {
			return
		}
		if id, ok := node.(*grammar.IdentifierContext); ok {
			name := id.GetText()
			if !token.IsKeyword(name) || (bareStatementKeywords[name] && isBareStatement(id)) {
				return
			}
			isDecl := declaresName(id)
			if !isDecl && (name == "break" || name == "continue") && !inPattern(id) {
				// Loop control in any other position (`val x = break`, an
				// if-expression branch) is the transformer's GALA-E0059,
				// which knows whether a loop can be reached from there.
				return
			}
			if isDecl {
				firstDecl = id
			} else if firstUse == nil {
				firstUse = id
			}
			return
		}
		for _, child := range node.GetChildren() {
			visit(child)
		}
	}
	visit(sf)

	id := firstDecl
	if id == nil {
		id = firstUse
	}
	if id == nil {
		return nil
	}
	name := id.GetText()
	tok := id.GetStart()
	msg, hint := keywordUseDiagnostic(name)
	if firstDecl != nil {
		msg = fmt.Sprintf("%q is a Go keyword and cannot be used as a name", name)
		hint = fmt.Sprintf("rename it; GALA compiles to Go, where %q is reserved, so nothing a GALA "+
			"program declares can have that name", name)
	}
	return galaerr.NewCodedSemanticError(
		galaerr.CodeGoKeywordAsName,
		tok.GetLine(), tok.GetColumn(), msg, hint,
	).WithSpan(tok.GetColumn() + len(name))
}

// keywordUseDiagnostic words the error for a keyword used without a
// declaration — `go(f)`, `Println(default)`, a bare `switch` — where the
// author wrote Go rather than a name. A keyword GALA-E0036 knows keeps that
// check's replacement hint.
func keywordUseDiagnostic(name string) (msg, hint string) {
	msg = fmt.Sprintf("%q is a Go keyword and is not part of GALA", name)
	if suggestion, ok := transformer.ForbiddenStatementKeywordSuggestion(name); ok {
		return msg, suggestion
	}
	return msg, fmt.Sprintf("GALA has no `%s`; if this refers to something you declared, "+
		"rename that declaration", name)
}

// inPattern reports whether id is part of a case pattern, where a name is a
// binding (`case break =>`, `case Some(break) =>`), never loop control. Call
// arguments are patterns in the grammar too, so only the pattern of a case
// clause counts.
func inPattern(id *grammar.IdentifierContext) bool {
	var prev antlr.Tree = id
	for node := id.GetParent(); node != nil; prev, node = node, node.GetParent() {
		if _, ok := node.(*grammar.CaseClauseContext); ok {
			_, viaPattern := prev.(grammar.IPatternContext)
			return viaPattern
		}
	}
	return false
}

// isBareStatement reports whether id is a whole statement on its own: `break`
// in a loop body or a match arm. The identifier's ancestors up to that
// statement each wrap a single child, so the walk climbs while that holds.
// A skipped `defer` / `go` / ... statement is left to GALA-E0036 (the
// transformer's checkForbiddenStatementKeyword), which inspects statements
// of this shape.
func isBareStatement(id *grammar.IdentifierContext) bool {
	var node antlr.Tree = id
	for {
		parent := node.GetParent()
		if parent == nil || parent.GetChildCount() != 1 {
			return false
		}
		if _, ok := parent.(*grammar.SimpleStatementContext); ok {
			return true
		}
		node = parent
	}
}

// declaresName reports whether id is the name a declaration introduces, as
// opposed to a reference. Pattern bindings (`case go =>`) are expressions in
// the grammar and count as uses; they are still reported, only after any
// declaration.
func declaresName(id *grammar.IdentifierContext) bool {
	switch parent := id.GetParent().(type) {
	case *grammar.PackageClauseContext,
		*grammar.ImportSpecContext,
		*grammar.EmbedDeclarationContext,
		*grammar.StructShorthandDeclarationContext,
		*grammar.SealedTypeDeclarationContext,
		*grammar.OpaqueTypeDeclarationContext,
		*grammar.SealedCaseContext,
		*grammar.SealedCaseFieldContext,
		*grammar.TypeDeclarationContext,
		*grammar.StructFieldContext,
		*grammar.MethodSpecContext,
		*grammar.FunctionDeclarationContext,
		*grammar.ReceiverContext,
		*grammar.ParameterContext,
		*grammar.IdentifierListContext,
		*grammar.BindDeclarationContext,
		*grammar.AlsoDeclarationContext,
		*grammar.UseDeclarationContext,
		*grammar.TypedPatternContext:
		return true
	case *grammar.TypeParameterContext:
		// `[T any]`: the first identifier is the parameter, the second its
		// constraint.
		return parent.Identifier(0) == id
	}
	return false
}
