package transformer

import (
	"errors"
	"go/ast"
	"path/filepath"

	"martianoff/gala/galaerr"
	"martianoff/gala/internal/parser"
	"martianoff/gala/internal/parser/grammar"
	"martianoff/gala/internal/transpiler"
)

// Default values — for function and method parameters and for shorthand struct
// fields — are recorded by the analyzer as source text plus the position of
// their first token (transpiler.DefaultExpr). They are re-parsed and lowered at
// every call or construction site that omits the argument, so a default like
// `time.Now()` is evaluated per call, and so a default declared in one package
// can be lowered in another.

// defaultSource is one declared default value together with what lowering it
// needs to know about its declaration.
type defaultSource struct {
	transpiler.DefaultExpr
	file       string          // declaring source file; "" when unknown
	pkg        string          // declaring package; names it borrows from there are qualified at a use site in another package
	declared   transpiler.Type // the parameter's or field's declared type, type arguments substituted; nil when unknown
	typeParams []string        // type parameters of the declaration; a declared type still mentioning one is not threaded
}

// defaultTreeKey identifies one declared default's text at one position.
type defaultTreeKey struct {
	text string
	pos  transpiler.SourcePos
	file string
}

// defaultExprTree parses a default's recorded text at its recorded position,
// once per default per file: lowering is per use site, but the parse tree is
// the same at every one of them.
func (t *galaASTTransformer) defaultExprTree(src defaultSource) (grammar.IExpressionContext, error) {
	key := defaultTreeKey{text: src.Text, pos: src.Pos, file: src.file}
	if tree, ok := t.defaultTrees[key]; ok {
		return tree, nil
	}
	tree, err := parser.ParseExpressionAt(src.Text, src.Pos.Line, src.Pos.Column, "default value")
	if err != nil {
		return nil, err
	}
	if t.defaultTrees == nil {
		t.defaultTrees = make(map[defaultTreeKey]grammar.IExpressionContext)
	}
	t.defaultTrees[key] = tree
	return tree, nil
}

// transformDefaultExpr lowers a default value at a call or construction site.
//
// The declared type is the default's expected type, exactly as it is for an
// argument passed explicitly: a lambda default takes its parameter and result
// types from it — `OnOne func(int) int = (a) => a + 1` lowers as
// `Hooks(OnOne = (a) => a + 1)` would — and `nil` stays `nil`.
//
// A default declared in another package is lowered in THIS package's scope, so
// the names it borrows from its own package are qualified afterwards (see
// qualifyDefaultExpr). useLine/useCol locate the call or construction: a
// diagnostic from a default declared in another file is attributed to that
// file, or to the use site when the declaring file is unknown.
func (t *galaASTTransformer) transformDefaultExpr(src defaultSource, useLine, useCol int) (ast.Expr, error) {
	local := src.Pos.Line > 0 && src.file != "" && t.filePath != "" && filepath.Clean(src.file) == filepath.Clean(t.filePath)
	if !local {
		// The default's tokens do not belong to this file: keep them out of
		// this file's line map and LSP hints.
		prev := t.inForeignDefault
		t.inForeignDefault = true
		defer func() { t.inForeignDefault = prev }()
	}

	exprCtx, err := t.defaultExprTree(src)
	var expr ast.Expr
	if err == nil {
		expr, err = t.transformWithDeclaredType(exprCtx, src.declared, src.typeParams)
	}
	if err == nil {
		expr, err = t.qualifyDefaultExpr(expr, src.pkg)
	}
	if err != nil && !local {
		err = placeForeignDefaultError(err, src, useLine, useCol)
	}
	return expr, err
}

// placeForeignDefaultError locates a diagnostic about a default value declared
// outside the file being transformed. With the declaring file known it names
// that file (and, for an error that carries no position of its own, the
// default's first token); otherwise it falls back to the use site.
func placeForeignDefaultError(err error, src defaultSource, useLine, useCol int) error {
	var se *galaerr.SemanticError
	if !errors.As(err, &se) || se.FilePath != "" {
		return err
	}
	if src.file != "" && src.Pos.Line > 0 {
		se.FilePath = src.file
		if se.Line == 0 {
			se.Line, se.Column = src.Pos.Line, src.Pos.Column
		}
	} else {
		se.Line, se.Column = useLine, useCol
	}
	return err
}

// transformWithDeclaredType lowers an expression that stands in a slot of a
// declared type. As in a typed `val` initializer, an unannotated lambda
// parameter that the type does not cover is an error rather than `any`.
//
// A bare `nil` is returned as is: it is already a value of the declared type
// (the absent function, pointer, ...), and must not be lifted into a thunk
// `func() T { return nil }` by the by-name sugar that an explicit argument to a
// zero-argument function parameter gets.
func (t *galaASTTransformer) transformWithDeclaredType(exprCtx grammar.IExpressionContext, declared transpiler.Type, typeParams []string) (ast.Expr, error) {
	if declared != nil {
		if inner, isSendable := transpiler.UnwrapSendable(declared); isSendable {
			declared = inner
		}
	}
	if transpiler.IsUnusable(declared) || typeMentionsTypeParam(declared, typeParams) {
		return t.transformExpression(exprCtx)
	}
	if exprCtx.GetText() == "nil" {
		return ast.NewIdent("nil"), nil
	}
	return t.transformArgument(exprCtx, declared, true)
}
