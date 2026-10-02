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

	// A method parameter's default may use the method's receiver. It is
	// lowered with recv bound to recvType, as in the method body, and recvExpr
	// — the call-site receiver — is then put in its place.
	recv     string
	recvType transpiler.Type
	recvExpr ast.Expr
}

// defaultLowering is set on the transformer while a declared default is being
// lowered at a use site. A default is lowered once per use site, but it is ONE
// declaration: nothing it binds (its lambda parameters, a method's receiver)
// is recorded as a variable of the enclosing function, and its lambda
// parameter hints are recorded once, from the declaration (see
// recordDefaultLambdaHints). A foreign default's tokens carry another file's
// positions, so no line markers are emitted for them either.
type defaultLowering struct {
	pkg     string // declaring package: bare function names resolve there (functionByName); they are qualified once the whole default is lowered
	foreign bool   // declared in a file other than the one being transformed
	// useScope is the use site's scope. Its bindings, and those of every
	// scope around it, were not in scope where the default was written, so
	// they do not shadow the functions the default names (see shadowingScope).
	useScope *scope
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
	prev := t.loweringDefault
	t.loweringDefault = &defaultLowering{pkg: src.pkg, foreign: !local, useScope: t.currentScope}
	defer func() { t.loweringDefault = prev }()
	if src.recv != "" {
		t.pushScope()
		defer t.popScope()
		t.addVar(src.recv, src.recvType)
	}

	exprCtx, err := t.defaultExprTree(src)
	var expr ast.Expr
	if err == nil {
		expr, err = t.transformWithDeclaredType(exprCtx, src.declared, src.typeParams)
	}
	if err == nil {
		expr, err = t.qualifyDefaultExpr(expr, src.pkg)
	}
	if err != nil {
		if !local {
			err = placeForeignDefaultError(err, src, useLine, useCol)
		}
		return nil, err
	}
	if src.recv != "" {
		expr = replaceReceiver(expr, src.recv, src.recvExpr)
	}
	return expr, nil
}

// recordDefaultLambdaHints records the LSP inlay hints for a lambda default's
// unannotated parameters, from the declaration: each takes its type from the
// declared parameter or field type. A default is lowered once per use site
// that omits it — possibly with different type arguments — so hints recorded
// there would repeat, and could disagree, on the same parameter.
func (t *galaASTTransformer) recordDefaultLambdaHints(param *grammar.ParameterContext, declared transpiler.Type) {
	if t.lspVarTypes == nil || param.ParamDefault() == nil {
		return
	}
	lambdaCtx := t.findLambdaInExpression(param.ParamDefault().(*grammar.ParamDefaultContext).Expression())
	if lambdaCtx == nil || lambdaCtx.Parameters().(*grammar.ParametersContext).ParameterList() == nil {
		return
	}
	_, expected, ok := t.lambdaExpectation(declared)
	if !ok {
		return
	}
	for i, p := range lambdaCtx.Parameters().(*grammar.ParametersContext).ParameterList().(*grammar.ParameterListContext).AllParameter() {
		lp := p.(*grammar.ParameterContext)
		if lp.Type_() != nil || i >= len(expected) || transpiler.IsUnusableOrAny(expected[i]) {
			continue
		}
		pos := transpiler.PosFromToken(lp.Identifier().GetStart())
		t.lspLambdaParamHints = append(t.lspLambdaParamHints, transpiler.LambdaParamHint{
			Line:   pos.Line,
			Column: pos.Column,
			Name:   lp.Identifier().GetText(),
			Type:   expected[i],
		})
	}
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
	return t.transformArgument(exprCtx, argSlot(declared), true)
}
