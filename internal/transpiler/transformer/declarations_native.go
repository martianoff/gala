package transformer

import (
	"fmt"
	"go/ast"
	"strings"
	"unicode/utf8"

	"github.com/antlr4-go/antlr/v4"

	galaparser "martianoff/gala/internal/parser"
	"martianoff/gala/internal/parser/grammar"
	"martianoff/gala/internal/parser/native"
)

// The compiled file's top-level walk and declarations on galop's native tree
// (internal/parser/native): each reader here is the twin of the one it is
// named after, which reads ANTLR's tree, and fills the same tree-free value
// for the same lowering (lowerValDecl, lowerFuncDecl, lowerTypeDecl, ...).
// What the native tree does not drive yet, it hands to ANTLR's tree: the
// types, signatures, parameters, initializers and bodies inside a
// declaration, and the sealed and opaque type declarations whole, cross back
// with crossTo. The later steps of the port move that boundary down.
// TestNativeWalkAgrees holds the native walk to the ANTLR one.
//
// Children are read through the rule handles, NativeKids and Kind(), never
// through Typed(), which builds a handle per node, or the All accessors,
// which allocate.

// crossTo is the ANTLR context of native node n, a C: the crossing from the
// native tree back to ANTLR's where the port has not reached yet. The walk
// reads only bridged trees (newWalkSource), where every node has a context of
// its rule, so a missing node or one without a context of the right type is
// a bug in the walk or the bridge, raised as an internal error.
func crossTo[C antlr.ParserRuleContext](t *galaASTTransformer, n native.GalaNodeRef) C {
	ctx := t.native.Ctx(n)
	c, ok := ctx.(C)
	if !ok {
		what := "no node"
		if n.Ok() {
			what = fmt.Sprintf("node %d (kind %d) has the ANTLR context %T", n.Index(), n.Kind(), ctx)
		}
		// invariant: native-bridge — the walk reads only bridged trees.
		panic(fmt.Sprintf("native tree: %s, not %T", what, c))
	}
	return c
}

// nativeRange is ctxRange of native node n's context.
func nativeRange(n native.GalaNodeRef) srcRange {
	start := n.Start()
	r := srcRange{line: int(start.Line), col: int(start.Column)}
	if i := n.StopIndex(); i >= 0 {
		tree := n.Tree()
		stop := tree.Token(i)
		r.stopLine, r.stopEnd = int(stop.Line), int(stop.Column)+utf8.RuneCountInString(tree.Text(i))
	}
	return r
}

// nativeDeclNames is readDeclNames of the identifier list idList.
func nativeDeclNames(idList native.GalaNodeRef) []declName {
	var names []declName
	for id := range galaparser.NativeKids(idList, native.GalaKIND_identifier) {
		names = append(names, declName{text: id.GetText(), at: srcNode{node: id}})
	}
	return names
}

// nativeTypeParams is readTypeParams of tps, a typeParameters node.
func nativeTypeParams(tps native.TypeParametersNode) []typeParam {
	var params []typeParam
	for n := range galaparser.NativeKids(tps.TypeParameterList().GalaNodeRef, native.GalaKIND_typeParameter) {
		tp := native.TypeParameterNode{GalaNodeRef: n}
		params = append(params, typeParam{name: tp.Identifier(0).GetText(), constraint: tp.Identifier(1).GetText()})
	}
	return params
}

// transformTopLevelDeclarationNative is transformTopLevelDeclaration of td, a
// topLevelDeclaration node.
func (t *galaASTTransformer) transformTopLevelDeclarationNative(td native.GalaNodeRef) ([]ast.Decl, error) {
	if t.onNativeDecl != nil {
		t.onNativeDecl()
	}
	// Track position for error reporting in deeply-nested helpers
	start := td.Start()
	t.lastLine, t.lastCol = int(start.Line), int(start.Column)
	decl, ok := topDeclKid(td)
	if !ok {
		return nil, nil
	}
	switch decl.Kind() {
	case native.GalaKIND_embedDeclaration:
		return t.lowerEmbedDecl(readEmbedDeclNative(native.EmbedDeclarationNode{GalaNodeRef: decl}))
	case native.GalaKIND_valDeclaration:
		return oneDecl(t.lowerValDecl(t.readBindingDeclNative(native.ValDeclarationNode{GalaNodeRef: decl}, "val")))
	case native.GalaKIND_varDeclaration:
		return oneDecl(t.lowerVarDecl(t.readBindingDeclNative(native.VarDeclarationNode{GalaNodeRef: decl}, "var")))
	case native.GalaKIND_functionDeclaration:
		return oneDecl(t.lowerFuncDecl(t.readFuncDeclNative(native.FunctionDeclarationNode{GalaNodeRef: decl})))
	case native.GalaKIND_typeDeclaration:
		return t.lowerTypeDecl(t.readTypeDeclNative(native.TypeDeclarationNode{GalaNodeRef: decl}))
	case native.GalaKIND_structShorthandDeclaration:
		return t.lowerShorthandDecl(t.readShorthandDeclNative(native.StructShorthandDeclarationNode{GalaNodeRef: decl}))
	case native.GalaKIND_sealedTypeDeclaration:
		return t.transformSealedTypeDeclaration(crossTo[*grammar.SealedTypeDeclarationContext](t, decl))
	case native.GalaKIND_opaqueTypeDeclaration:
		return t.transformOpaqueTypeDeclaration(crossTo[*grammar.OpaqueTypeDeclarationContext](t, decl))
	}
	return nil, nil
}

// topDeclKid is the declaration td, a topLevelDeclaration node, holds, and
// false when it holds none (parse-error recovery).
func topDeclKid(td native.GalaNodeRef) (native.GalaNodeRef, bool) {
	if td.ChildCount() == 0 {
		return native.GalaNodeRef{}, false
	}
	decl := td.Child(0).Node()
	return decl, decl.Ok()
}

// oneDecl is the result of lowering a declaration to the one Go declaration
// d, or the error err.
func oneDecl(d ast.Decl, err error) ([]ast.Decl, error) {
	if err != nil {
		return nil, err
	}
	return []ast.Decl{d}, nil
}

// readEmbedDeclNative is readEmbedDecl of n.
func readEmbedDeclNative(n native.EmbedDeclarationNode) embedDecl {
	d := embedDecl{name: n.Identifier().GetText()}
	if typ := n.Type_(); typ.Ok() {
		d.typeName = galaparser.NativeTypeText(typ)
	}
	pats := n.EmbedPatterns()
	for i := range pats.ChildCount() {
		if c := pats.Child(i); c.IsToken() {
			if term := c.Terminal(); term.Token().Type == native.GalaSTRING {
				d.patterns = append(d.patterns, term.GetText())
			}
		}
	}
	return d
}

// nativeBinding is what the native val and var declaration handles share,
// as tupleDeclaration is what their contexts share.
type nativeBinding interface {
	Ref() native.GalaNodeRef
	TuplePattern() native.TuplePatternNode
	IdentifierList() native.IdentifierListNode
	Type_() native.TypeNode
	ExpressionList() native.ExpressionListNode
}

var (
	_ nativeBinding = native.ValDeclarationNode{}
	_ nativeBinding = native.VarDeclarationNode{}
)

// bindingIdList is the identifier list of n: its own, or its tuple pattern's.
func bindingIdList(n nativeBinding) native.GalaNodeRef {
	if tp := n.TuplePattern(); tp.Ok() {
		return tp.IdentifierList().GalaNodeRef
	}
	return n.IdentifierList().GalaNodeRef
}

// readBindingDeclNative is readBindingDecl of a package-level val or var
// declaration n, keyword. The native walk reaches no declaration in a body
// yet: those are statements.
func (t *galaASTTransformer) readBindingDeclNative(n nativeBinding, keyword string) bindingDecl {
	d := bindingDecl{at: srcNode{node: n.Ref()}, topLevel: true, names: nativeDeclNames(bindingIdList(n))}
	typ, list := n.Type_(), n.ExpressionList()
	if typ.Ok() {
		d.typ = crossTo[*grammar.TypeContext](t, typ.GalaNodeRef)
	}
	if list.Ok() {
		d.exprs = crossTo[*grammar.ExpressionListContext](t, list.GalaNodeRef)
	}
	if tp := n.TuplePattern(); tp.Ok() {
		d.tuple = srcNode{node: tp.GalaNodeRef}
		if typ.Ok() {
			d.typAt = srcNode{node: typ.GalaNodeRef}
		}
		if list.Ok() {
			d.exprsAt = srcNode{node: list.GalaNodeRef}
			for range galaparser.NativeKids(list.GalaNodeRef, native.GalaKIND_expression) {
				d.nExprs++
			}
		}
	}
	if d.exprs != nil && !d.tuple.ok() && isLoneBlank(d.names) {
		d.blankForm = blankDeclarationForm(keyword, d.typ)
		d.blankHint = topLevelBlankHint
	}
	return d
}

// readFuncDeclNative is readFuncDecl of n.
func (t *galaASTTransformer) readFuncDeclNative(n native.FunctionDeclarationNode) funcDecl {
	d := funcDecl{name: n.Identifier().GetText(), sig: crossTo[*grammar.SignatureContext](t, n.Signature().GalaNodeRef)}
	if b := n.Block(); b.Ok() {
		d.block = crossTo[*grammar.BlockContext](t, b.GalaNodeRef)
	}
	if e := n.Expression(); e.Ok() {
		d.expr = crossTo[*grammar.ExpressionContext](t, e.GalaNodeRef)
	}
	if r := n.Receiver(); r.Ok() {
		d.recv = &receiverDecl{
			name:  r.Identifier().GetText(),
			at:    srcNode{node: r.GalaNodeRef},
			isVar: r.VAR().Ok(),
			typ:   crossTo[*grammar.TypeContext](t, r.Type_().GalaNodeRef),
		}
	}
	if tps := n.TypeParameters(); tps.Ok() {
		d.typeParams = nativeTypeParams(tps)
	}
	return d
}

// readTypeDeclNative is readTypeDecl of n.
func (t *galaASTTransformer) readTypeDeclNative(n native.TypeDeclarationNode) typeDecl {
	id := n.Identifier()
	d := typeDecl{name: id.GetText(), nameAt: srcNode{node: id.GalaNodeRef}}
	if tps := n.TypeParameters(); tps.Ok() {
		d.typeParams = nativeTypeParams(tps)
	}
	if st := n.StructType(); st.Ok() {
		d.kind = structTypeDecl
		for f := range galaparser.NativeKids(st.GalaNodeRef, native.GalaKIND_structField) {
			field := native.StructFieldNode{GalaNodeRef: f}
			d.fields = append(d.fields, structFieldDecl{
				name:  field.Identifier().GetText(),
				isVar: field.VAR().Ok(),
				typ:   crossTo[*grammar.TypeContext](t, field.Type_().GalaNodeRef),
			})
		}
	} else if it := n.InterfaceType(); it.Ok() {
		d.kind = interfaceTypeDecl
		for m := range galaparser.NativeKids(it.GalaNodeRef, native.GalaKIND_methodSpec) {
			spec := native.MethodSpecNode{GalaNodeRef: m}
			ms := methodSpecDecl{name: spec.Identifier().GetText(), sig: crossTo[*grammar.SignatureContext](t, spec.Signature().GalaNodeRef)}
			if tps := spec.TypeParameters(); tps.Ok() {
				ms.typeParams = nativeTypeParams(tps)
			}
			d.methods = append(d.methods, ms)
		}
	} else if alias := n.TypeAlias(); alias.Ok() {
		d.kind = aliasTypeDecl
		if typ := alias.Type_(); typ.Ok() {
			d.aliasType = crossTo[*grammar.TypeContext](t, typ.GalaNodeRef)
		} else if aid := alias.Identifier(); aid.Ok() {
			d.aliasIdent = aid.GetText()
		}
	}
	return d
}

// readShorthandDeclNative is readShorthandDecl of n.
func (t *galaASTTransformer) readShorthandDeclNative(n native.StructShorthandDeclarationNode) shorthandDecl {
	id := n.Identifier()
	d := shorthandDecl{at: srcNode{node: n.GalaNodeRef}, name: id.GetText(), nameAt: srcNode{node: id.GalaNodeRef}}
	if tps := n.TypeParameters(); tps.Ok() {
		d.typeParams = nativeTypeParams(tps)
	}
	for p := range galaparser.NativeKids(n.Parameters().ParameterList().GalaNodeRef, native.GalaKIND_parameter) {
		param := native.ParameterNode{GalaNodeRef: p}
		pd := paramDecl{
			ctx:   crossTo[*grammar.ParameterContext](t, p),
			name:  param.Identifier().GetText(),
			isVar: param.VAR().Ok(),
			isVal: param.VAL().Ok(),
		}
		if typ := param.Type_(); typ.Ok() {
			pd.typ = crossTo[*grammar.TypeContext](t, typ.GalaNodeRef)
		}
		d.params = append(d.params, pd)
	}
	return d
}

// readImportDeclNative is readImportDecl of n.
func readImportDeclNative(n native.GalaNodeRef) []importSpecDecl {
	var specs []importSpecDecl
	for s := range galaparser.NativeKids(n, native.GalaKIND_importSpec) {
		spec := native.ImportSpecNode{GalaNodeRef: s}
		d := importSpecDecl{path: strings.Trim(spec.STRING().GetText(), "\"")}
		switch {
		case spec.Identifier().Ok():
			d.kind, d.alias = importAliased, spec.Identifier().GetText()
		case s.ChildCount() > 1:
			d.kind = importUnnamed
			if c := s.Child(0); c.IsToken() && c.Terminal().GetText() == "." {
				d.kind = importDot
			}
		}
		specs = append(specs, d)
	}
	return specs
}
