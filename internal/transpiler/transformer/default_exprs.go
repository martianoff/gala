package transformer

import (
	"errors"
	"fmt"
	"go/ast"
	"path/filepath"
	"slices"

	"github.com/antlr4-go/antlr/v4"

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
	file        string                     // declaring source file; "" when unknown
	pkg         string                     // declaring package; names it borrows from there are qualified at a use site in another package
	declared    transpiler.Type            // the parameter's or field's declared type, type arguments substituted; nil when unknown
	typeParams  []string                   // type parameters of the declaration; a declared type still mentioning one is not threaded
	typeArgs    map[string]transpiler.Type // the use site's type arguments for typeParams (see bindDefaultTypeParams)
	carried     []string                   // the use site's type parameters the type arguments carry into declared (see carriedTypeParams)
	unspellable []string                   // those of carried the default's source must not spell (see substituteDeclared)

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
	// emitTypeParams is the type parameters in scope where the lowered
	// default is emitted (see shadowedByUse).
	emitTypeParams map[string]bool
}

// defaultTreeKey identifies one declared default's text at one position.
type defaultTreeKey struct {
	text string
	pos  transpiler.SourcePos
	file string
}

// shadowedByUse returns a package-level val, var, function or type — or a
// dot-imported one — that the lowered default expr reads unqualified and one
// of the use site's type parameters shadows, or "". It runs in the default's
// own scope, where such a name still resolves to its declaration.
//
// The carried type parameters (see carriedTypeParams) are no such reads: the
// lowered default spells them as the type parameters they are.
func (t *galaASTTransformer) shadowedByUse(expr ast.Expr, useSite map[string]bool, carried []string) string {
	if len(useSite) == 0 {
		return ""
	}
	bound := boundNames(expr)
	found := ""
	ast.Inspect(&ast.ParenExpr{X: expr}, func(n ast.Node) bool {
		for _, slot := range referenceSlots(n) {
			id, ok := (*slot).(*ast.Ident)
			if !ok || found != "" || !useSite[id.Name] || bound[id.Name] || slices.Contains(carried, id.Name) {
				continue
			}
			if t.isTopLevelBinding(id.Name) || t.getFunction(id.Name) != nil || t.resolveTypeMetaName(id.Name) != "" {
				found = id.Name
			}
		}
		return found == ""
	})
	return found
}

// substituteDeclared sets src's declared type to declared with the type
// arguments of subst substituted, and records those arguments and the use
// site's type parameters they carry into it (see carriedTypeParams).
func (t *galaASTTransformer) substituteDeclared(src *defaultSource, declared transpiler.Type, subst map[string]transpiler.Type) {
	src.typeArgs = subst
	src.declared = t.substituteInType(declared, subst)
	src.carried = t.carriedTypeParams(src.declared, subst)
	for _, name := range src.carried {
		// A default may spell its own type parameter that the call binds to
		// the use site's type parameter of the same name: in the generated Go
		// the name means that argument.
		if arg, ok := subst[name]; ok && arg.String() == name && slices.Contains(src.typeParams, name) {
			continue
		}
		src.unspellable = append(src.unspellable, name)
	}
}

// carriedTypeParams returns, sorted, the use site's type parameters that the
// type arguments of subst carry into a default's declared type. The default
// is lowered against them — `(a) => a` for a `func(A) A` parameter, at a call
// inside `func g[Some any]` that binds A to Some, is `func(a Some) Some` — so
// they are bound while it is, alongside its own declaration's.
func (t *galaASTTransformer) carriedTypeParams(declared transpiler.Type, subst map[string]transpiler.Type) []string {
	if len(t.activeTypeParams) == 0 {
		return nil
	}
	inArgs := map[string]bool{}
	for _, arg := range subst {
		typeNameMatches(arg, func(name string) bool {
			inArgs[name] = t.activeTypeParams[name]
			return false
		})
	}
	var carried []string
	typeNameMatches(declared, func(name string) bool {
		if inArgs[name] && !slices.Contains(carried, name) {
			carried = append(carried, name)
		}
		return false
	})
	slices.Sort(carried)
	return carried
}

// parseTypeSubst parses the type arguments of a call's type substitution.
func parseTypeSubst(subst map[string]string) map[string]transpiler.Type {
	parsed := make(map[string]transpiler.Type, len(subst))
	for name, arg := range subst {
		parsed[name] = transpiler.ParseType(arg)
	}
	return parsed
}

// spellsAny returns the first of names that the default's source refers to —
// as a value or as a type, not as a member, label or binding — or "".
func spellsAny(tree antlr.Tree, names []string) string {
	found := ""
	if len(names) > 0 {
		walkTree(tree, func(node antlr.Tree) {
			id, ok := node.(*grammar.IdentifierContext)
			if !ok || found != "" || !slices.Contains(names, id.GetText()) {
				return
			}
			switch parent := id.GetParent().(type) {
			case *grammar.PrimaryContext:
				found = id.GetText()
			case *grammar.QualifiedIdentifierContext:
				if parent.Identifier(0) == id {
					found = id.GetText()
				}
			case *grammar.ParameterContext:
				// `func(Some) int`: an unnamed parameter's type.
				if parent.Type_() == nil && isFuncTypeSignature(parent) {
					found = id.GetText()
				}
			}
		})
	}
	return found
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
	// The type parameters in scope where the lowered Go is emitted: a default
	// lowered inside another default's lowering is emitted where that one is.
	useSite := t.activeTypeParams
	if prev != nil {
		useSite = prev.emitTypeParams
	}
	t.loweringDefault = &defaultLowering{pkg: src.pkg, foreign: !local, useScope: t.currentScope, emitTypeParams: useSite}
	defer func() { t.loweringDefault = prev }()
	// The default is lowered in its own declaration's scope: the type
	// parameters bound at the use site do not shadow the names it reads. In
	// the generated Go they do, so a name the default reads unqualified that a
	// use-site type parameter shadows cannot be spelled there (shadowedByUse).
	// The use-site type parameters its declared type carries are the
	// exception: they are bound, and the default must not read their names.
	defer t.onlyTypeParams(append(slices.Clip(src.typeParams), src.carried...))()
	if src.recv != "" {
		t.pushScope()
		defer t.popScope()
		t.addReceiver(src.recv, src.recvType)
	}

	// Reported at the use site, in the file being transformed, wherever the
	// default was declared.
	shadowed := func(name string) error {
		err := galaerr.NewSemanticErrorAt(useLine, useCol, fmt.Sprintf(
			"the default value of a parameter reads '%s', which the type parameter '%s' of the enclosing declaration shadows here; pass the argument explicitly or rename the type parameter",
			name, name))
		err.FilePath = t.filePath
		return err
	}
	exprCtx, err := t.defaultExprTree(src)
	if err == nil {
		if name := spellsAny(exprCtx, src.unspellable); name != "" {
			err = shadowed(name)
		}
	}
	var expr ast.Expr
	if err == nil {
		expr, err = t.transformWithDeclaredType(exprCtx, src.declared, src.typeParams)
	}
	if err == nil {
		expr, err = t.qualifyDefaultExpr(expr, src.pkg)
	}
	if err == nil {
		if name := t.shadowedByUse(expr, useSite, src.carried); name != "" {
			err = shadowed(name)
		}
	}
	if err != nil {
		if !local {
			err = placeForeignDefaultError(err, src, useLine, useCol)
		}
		return nil, err
	}
	expr, err = t.bindDefaultTypeParams(expr, src, useLine, useCol)
	if err != nil {
		return nil, err
	}
	if src.recv != "" {
		expr = replaceReceiver(expr, src.recv, src.recvExpr)
	}
	return expr, nil
}

// bindDefaultTypeParams puts the use site's type arguments in place of the
// declaration's own type parameters that a lowered default spells. The default
// was lowered where they are bound; the use site binds none of them, or binds
// a type parameter of its own under the same name: `None[T]()` declared on
// `func f[T any]` is `None[int]()` at a call that binds T to int. One the use
// site leaves unbound has no type to stand for, and is reported at
// useLine/useCol.
//
// It runs after qualifying and the shadowing check, which the arguments are
// not subject to: they are already spelled in the use site's scope. A
// substituted argument is not rewritten again, and the call-site receiver is
// put in place only afterwards, so the use site's own code is never touched.
func (t *galaASTTransformer) bindDefaultTypeParams(expr ast.Expr, src defaultSource, useLine, useCol int) (ast.Expr, error) {
	if len(src.typeParams) == 0 {
		return expr, nil
	}
	bound := boundNames(expr)
	substituted := map[ast.Node]bool{}
	unbound := ""
	holder := &ast.ParenExpr{X: expr}
	ast.Inspect(holder, func(n ast.Node) bool {
		if substituted[n] {
			return false
		}
		for _, slot := range referenceSlots(n) {
			id, ok := (*slot).(*ast.Ident)
			if !ok || bound[id.Name] || !slices.Contains(src.typeParams, id.Name) {
				continue
			}
			arg := src.typeArgs[id.Name]
			switch {
			case transpiler.IsUnusable(arg):
				if unbound == "" {
					unbound = id.Name
				}
			case arg.String() != id.Name:
				// A fresh expression per slot: AST nodes are not shared.
				spelled := t.typeToExpr(arg)
				substituted[spelled] = true
				*slot = spelled
			}
		}
		return true
	})
	if unbound != "" {
		return nil, galaerr.NewCodedSemanticError(galaerr.CodeUninferredTypeArgument, useLine, useCol, fmt.Sprintf(
			"cannot infer type argument %s for a default value that names it; pass the argument, or the type arguments, explicitly",
			unbound), "")
	}
	return holder.X, nil
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
	return t.transformArgument(exprCtx, typedSlot(declared), true)
}
