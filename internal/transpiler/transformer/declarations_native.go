package transformer

import (
	"go/ast"
	"strings"
	"unicode/utf8"

	"github.com/antlr4-go/antlr/v4"

	galaparser "martianoff/gala/internal/parser"
	"martianoff/gala/internal/parser/native"
)

// The compiled file's top-level walk and declarations on galop's native tree
// (internal/parser/native): each reader here is the twin of the one it is
// named after, which reads ANTLR's tree, and fills the same tree-free value
// for the same lowering (lowerValDecl, lowerFuncDecl, lowerTypeDecl, ...).
// A declaration's signature, parameters, initializers and expression body are
// read natively too (decls_source.go), as are types (types_source.go), the
// sealed and opaque type declarations (sealed_source.go), and block bodies
// down to their statements (statements_source.go). Nothing the native walk
// reads crosses from a node back to ANTLR's tree.
// TestNativeWalkAgrees holds the native walk to the ANTLR one.
//
// Children are read through the rule handles, NativeKids and Kind(), never
// through Typed(), which builds a handle per node, or the All accessors,
// which allocate.

// nativeNodeOf is ctx's node on the native tree, of kind, when the file's
// walk reads that tree (t.native is set only for a bridged tree), and false
// otherwise: the bridge for the lowerings entered with an ANTLR context (see
// lambdas_source.go). A nil ctx, typed or not, has no node.
func (t *galaASTTransformer) nativeNodeOf(ctx antlr.ParserRuleContext, kind native.GalaKind) (native.GalaNodeRef, bool) {
	if t.native == nil {
		return native.GalaNodeRef{}, false
	}
	n := t.native.NodeOfKind(ctx, kind)
	return n, n.Ok()
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

// nativeDeclNames is ctxDeclNames of the identifier list idList.
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
	decl, ok := firstKidNode(td)
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
		return t.lowerTypeDecl(t.readTypeDeclNative(native.TypeDeclarationNode{GalaNodeRef: decl}), false)
	case native.GalaKIND_structShorthandDeclaration:
		return t.lowerShorthandDecl(t.readShorthandDeclNative(native.StructShorthandDeclarationNode{GalaNodeRef: decl}))
	case native.GalaKIND_sealedTypeDeclaration:
		t.noteNativeType(srcNode{node: decl})
		return t.lowerSealedDecl(readSealedDeclNative(native.SealedTypeDeclarationNode{GalaNodeRef: decl}))
	case native.GalaKIND_opaqueTypeDeclaration:
		t.noteNativeType(srcNode{node: decl})
		return t.lowerOpaqueDecl(readOpaqueDeclNative(native.OpaqueTypeDeclarationNode{GalaNodeRef: decl}))
	}
	return nil, nil
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

// readBindingDeclNative is readBindingDecl of a val or var declaration n,
// keyword: a package-level one or a statement in a body.
func (t *galaASTTransformer) readBindingDeclNative(n nativeBinding, keyword string) bindingDecl {
	d := bindingDecl{at: srcNode{node: n.Ref()}, topLevel: isTopLevelDeclNative(n.Ref()), names: nativeDeclNames(bindingIdList(n))}
	typ, list := n.Type_(), n.ExpressionList()
	if typ.Ok() {
		d.typ = nativeType(typ)
	}
	if list.Ok() {
		d.exprs = nativeExprList(list.GalaNodeRef)
	}
	if tp := n.TuplePattern(); tp.Ok() {
		d.tuple = srcNode{node: tp.GalaNodeRef}
	}
	if d.exprs.ok() && !d.tuple.ok() && isLoneBlank(d.names) {
		d.blankForm = blankDeclarationForm(keyword, d.typ)
		d.blankHint = nativeBlankDeclarationHint(n.Ref(), d.topLevel)
	}
	return d
}

// readFuncDeclNative is readFuncDecl of n.
func (t *galaASTTransformer) readFuncDeclNative(n native.FunctionDeclarationNode) funcDecl {
	d := funcDecl{name: n.Identifier().GetText(), sig: nativeSig(n.Signature())}
	if b := n.Block(); b.Ok() {
		d.block = blockSrc{node: b.GalaNodeRef}
	}
	if e := n.Expression(); e.Ok() {
		d.expr = nativeExpr(e.GalaNodeRef)
	}
	if r := n.Receiver(); r.Ok() {
		d.recv = &receiverDecl{
			name:  r.Identifier().GetText(),
			at:    srcNode{node: r.GalaNodeRef},
			isVar: r.VAR().Ok(),
			typ:   nativeType(r.Type_()),
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
				typ:   nativeType(field.Type_()),
			})
		}
	} else if it := n.InterfaceType(); it.Ok() {
		d.kind = interfaceTypeDecl
		for m := range galaparser.NativeKids(it.GalaNodeRef, native.GalaKIND_methodSpec) {
			spec := native.MethodSpecNode{GalaNodeRef: m}
			ms := methodSpecDecl{name: spec.Identifier().GetText(), sig: nativeSig(spec.Signature())}
			if tps := spec.TypeParameters(); tps.Ok() {
				ms.typeParams = nativeTypeParams(tps)
			}
			d.methods = append(d.methods, ms)
		}
	} else if alias := n.TypeAlias(); alias.Ok() {
		d.kind = aliasTypeDecl
		if typ := alias.Type_(); typ.Ok() {
			d.aliasType = nativeType(typ)
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
	d.params = readParamsNative(n.Parameters())
	return d
}

// readParamsNative is readParams of ps.
func readParamsNative(ps native.ParametersNode) []paramDecl {
	var out []paramDecl
	for p := range galaparser.NativeKids(ps.ParameterList().GalaNodeRef, native.GalaKIND_parameter) {
		param := native.ParameterNode{GalaNodeRef: p}
		pd := paramDecl{
			at:         srcNode{node: p},
			isVar:      param.VAR().Ok(),
			isVal:      param.VAL().Ok(),
			variadic:   param.ELLIPSIS().Ok(),
			hasDefault: param.ParamDefault().Ok(),
			def:        nativeDefault(param.ParamDefault()),
		}
		// A parameter can be nameless, as in `(...int) => 0`.
		if id := param.Identifier(); id.Ok() {
			pd.nameAt, pd.name = srcNode{node: id.GalaNodeRef}, id.GetText()
		}
		if typ := param.Type_(); typ.Ok() {
			pd.typ = nativeType(typ)
		}
		out = append(out, pd)
	}
	return out
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
