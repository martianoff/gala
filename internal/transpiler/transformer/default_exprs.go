package transformer

import (
	"errors"
	"go/ast"
	"path/filepath"
	"strings"

	"github.com/antlr4-go/antlr/v4"

	"martianoff/gala/galaerr"
	"martianoff/gala/internal/parser/grammar"
	"martianoff/gala/internal/transpiler"
)

// Default values — for function and method parameters and for shorthand struct
// fields — are recorded by the analyzer as source text plus the position of
// their first token. They are re-parsed and lowered at every call or
// construction site that omits the argument, so a default like `time.Now()` is
// evaluated per call, and so a default declared in one package can be lowered
// in another.

// defaultSource is one declared default value, as the analyzer recorded it.
type defaultSource struct {
	text string
	pos  transpiler.SourcePos // first token of the expression; zero when unknown
	file string               // declaring source file; "" when unknown
}

// funcDefault returns the recorded default of a function's i-th parameter.
func funcDefault(m *transpiler.FunctionMetadata, i int) defaultSource {
	return defaultSource{text: m.DefaultExprs[i], pos: m.DefaultPos[i], file: m.DefinedIn}
}

// methodDefault returns the recorded default of a method's i-th parameter.
func methodDefault(m *transpiler.MethodMetadata, i int) defaultSource {
	return defaultSource{text: m.DefaultExprs[i], pos: m.DefaultPos[i], file: m.DefinedIn}
}

// parseDefaultExpr parses a default expression's source text into an ANTLR
// expression context that the normal pipeline can lower.
//
// When the declaration position is known the text is lexed behind padding that
// puts its first token back at that line and column, so every token — and so
// every diagnostic, line marker and LSP hint derived from one — carries its
// real position in the declaring file rather than one relative to the snippet.
func parseDefaultExpr(src defaultSource) grammar.IExpressionContext {
	text := src.text
	if src.pos.Line > 0 {
		text = strings.Repeat("\n", src.pos.Line-1) + strings.Repeat(" ", src.pos.Column) + text
	}
	lexer := grammar.NewgalaLexer(antlr.NewInputStream(text))
	p := grammar.NewgalaParser(antlr.NewCommonTokenStream(lexer, antlr.TokenDefaultChannel))
	p.RemoveErrorListeners()
	return p.Expression()
}

// transformDefaultExpr lowers a default value at a call or construction site.
//
// expected is the declared type of the parameter or field. It gives the
// default the same expected type an explicitly passed argument gets, so a
// lambda default takes its parameter and result types from the declaration —
// `OnOne func(int) int = (a) => a + 1` — exactly as `Hooks(OnOne = (a) => a +
// 1)` would. typeParams are the declaring function's or type's type
// parameters: an expected type that still mentions one has no meaning at the
// use site, so it is not threaded.
//
// useLine/useCol locate the call or construction. A diagnostic from a default
// declared in another file is attributed to that file; one whose declaring file
// is unknown is reported at the use site.
func (t *galaASTTransformer) transformDefaultExpr(src defaultSource, expected transpiler.Type, typeParams []string, useLine, useCol int) (ast.Expr, error) {
	exprCtx := parseDefaultExpr(src)

	local := src.pos.Line > 0 && src.file != "" && t.filePath != "" && filepath.Clean(src.file) == filepath.Clean(t.filePath)
	if !local {
		// The default's tokens do not belong to this file: keep them out of
		// this file's line map and LSP hints.
		prev := t.inForeignDefault
		t.inForeignDefault = true
		defer func() { t.inForeignDefault = prev }()
	}

	expr, err := t.transformWithDeclaredType(exprCtx, expected, typeParams)
	if err != nil && !local {
		return nil, placeForeignDefaultError(err, src, useLine, useCol)
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
	if src.file != "" && src.pos.Line > 0 {
		se.FilePath = src.file
		if se.Line == 0 {
			se.Line, se.Column = src.pos.Line, src.pos.Column
		}
	} else {
		se.Line, se.Column = useLine, useCol
	}
	return err
}

// transformWithDeclaredType lowers an expression that stands in a slot of a
// declared type. A lambda takes its parameter and result types from that type
// and, as in a typed `val` initializer, an unannotated parameter that the type
// does not cover is an error rather than `any`. Anything else goes through the
// ordinary argument path with the type as its expectation.
func (t *galaASTTransformer) transformWithDeclaredType(exprCtx grammar.IExpressionContext, declared transpiler.Type, typeParams []string) (ast.Expr, error) {
	if declared != nil {
		if inner, isSendable := transpiler.UnwrapSendable(declared); isSendable {
			declared = inner
		}
	}
	if transpiler.IsUnusable(declared) || typeMentionsTypeParam(declared, typeParams) {
		return t.transformExpression(exprCtx)
	}
	if lambdaCtx := t.findLambdaInExpression(exprCtx); lambdaCtx != nil {
		var params []transpiler.Type
		var ret ast.Expr
		if ft := t.resolveTranspilerTypeAsFuncType(declared); ft != nil {
			params = ft.Params
			ret = ExpectedVoid
			if len(ft.Results) > 0 {
				ret = t.typeToExpr(ft.Results[0])
			}
		}
		return t.transformLambdaWithExpectedType(lambdaCtx, ret, params, true)
	}
	return t.transformArgumentWithExpectedType(exprCtx, declared)
}
