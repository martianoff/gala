package analyzer

import (
	galaparser "martianoff/gala/internal/parser"
	"martianoff/gala/internal/parser/native"
	"martianoff/gala/internal/transpiler/scopewalk"
	"martianoff/gala/internal/transpiler/scopewalk/nativewalk"
)

// The undefined-symbol check (GALA-E0023) on galop's native tree: each
// function here is the twin of the one it is named after, which reads ANTLR's
// tree, and makes the same decisions, through the same checker methods, in
// the same order. The check reads a file's native tree exactly when the
// file's declarations were collected on it (see collect_native.go), and then
// reads no ANTLR context. TestUndefinedCheckAgrees holds the pair together.

// walkSourceFileNative is walkSourceFile of root, a file's native tree.
func (c *undefChecker) walkSourceFileNative(root native.GalaNodeRef) {
	w := c.walker
	nw := nativewalk.New(w, bindFunctionDeclarationScopeNative)
	w.PushScope()
	defer w.PopScope()
	c.bindFileLevelNamesNative(root)

	for decl := range nativeTopLevelDecls(root) {
		switch decl.Kind() {
		case native.GalaKIND_functionDeclaration:
			nw.WalkFunctionDeclaration(native.FunctionDeclarationNode{GalaNodeRef: decl})
		case native.GalaKIND_valDeclaration:
			nw.Walk(native.ValDeclarationNode{GalaNodeRef: decl}.ExpressionList().GalaNodeRef)
		case native.GalaKIND_varDeclaration:
			nw.Walk(native.VarDeclarationNode{GalaNodeRef: decl}.ExpressionList().GalaNodeRef)
		case native.GalaKIND_structShorthandDeclaration:
			d := native.StructShorthandDeclarationNode{GalaNodeRef: decl}
			w.PushScope()
			bindTypeParametersNative(w, d.TypeParameters())
			nativewalk.BindParameters(w, d.Parameters())
			nw.WalkParameterDefaults(d.Parameters())
			w.PopScope()
		}
	}
}

// bindFileLevelNamesNative is bindFileLevelNames of root.
func (c *undefChecker) bindFileLevelNamesNative(root native.GalaNodeRef) {
	for _, d := range c.rich.EmbedDirectives {
		c.walker.Bind(d.VarName)
	}
	for decl := range nativeTopLevelDecls(root) {
		var tp native.TuplePatternNode
		switch decl.Kind() {
		case native.GalaKIND_valDeclaration:
			tp = native.ValDeclarationNode{GalaNodeRef: decl}.TuplePattern()
		case native.GalaKIND_varDeclaration:
			tp = native.VarDeclarationNode{GalaNodeRef: decl}.TuplePattern()
		case native.GalaKIND_embedDeclaration:
			nativewalk.BindID(c.walker, native.EmbedDeclarationNode{GalaNodeRef: decl}.Identifier())
		}
		nativewalk.BindIdentifierList(c.walker, tp.IdentifierList())
	}
}

// bindFunctionDeclarationScopeNative is bindFunctionDeclarationScope of fn.
func bindFunctionDeclarationScopeNative(w *scopewalk.Walker, fn native.FunctionDeclarationNode) {
	bindTypeParametersNative(w, fn.TypeParameters())
	recv := fn.Receiver()
	if !recv.Ok() {
		return
	}
	nativewalk.BindID(w, recv.Identifier())
	nativewalk.BindIdentifiersIn(w, recv.Type_().GalaNodeRef)
}

// bindTypeParametersNative is bindTypeParameters of tps.
func bindTypeParametersNative(w *scopewalk.Walker, tps native.TypeParametersNode) {
	for p := range galaparser.NativeKids(tps.TypeParameterList().GalaNodeRef, native.GalaKIND_typeParameter) {
		nativewalk.BindID(w, native.TypeParameterNode{GalaNodeRef: p}.Identifier(0))
	}
}

// checkTypeNamesNative is checkTypeNames of root.
func (c *undefChecker) checkTypeNamesNative(root native.GalaNodeRef) {
	c.inTypePass = true
	defer func() { c.inTypePass = false }()
	typeParams := collectFileTypeBindersNative(root)
	var walk func(n native.GalaNodeRef, extra map[string]bool)
	walk = func(n native.GalaNodeRef, extra map[string]bool) {
		if n.Kind() == native.GalaKIND_functionDeclaration {
			if recv := (native.FunctionDeclarationNode{GalaNodeRef: n}).Receiver(); recv.Ok() {
				var sigExtra map[string]bool
				if rt := recv.Type_(); rt.Ok() {
					sigExtra = c.receiverImports(receiverBaseTypeNameNative(rt))
				}
				for i := range n.ChildCount() {
					child := n.Child(i).Node()
					if !child.Ok() {
						continue
					}
					switch child.Kind() {
					case native.GalaKIND_block, native.GalaKIND_expression:
						walk(child, nil)
					default:
						walk(child, sigExtra)
					}
				}
				return
			}
		}
		if n.Kind() == native.GalaKIND_type {
			c.checkTypeNameNative(native.TypeNode{GalaNodeRef: n}, typeParams, extra)
		}
		c.checkBareTypePositionsNative(n, typeParams, extra)
		skip := tupleDestructureTypeNative(n)
		for i := range n.ChildCount() {
			if child := n.Child(i).Node(); child.Ok() && child != skip {
				walk(child, extra)
			}
		}
	}
	walk(root, nil)
}

// checkTypeNameNative is checkTypeName of t.
func (c *undefChecker) checkTypeNameNative(t native.TypeNode, typeParams, extra map[string]bool) {
	qi := t.QualifiedIdentifier()
	if !qi.Ok() {
		return
	}
	name, at := nativewalk.IdentifierSite(qi.Identifier(0).GalaNodeRef)
	if !qi.Identifier(1).Ok() {
		c.checkBareTypeName(name, at, typeParams, extra)
		return
	}
	c.checkTypeQualifier(name, at)
}

// checkBareTypePositionsNative is checkBareTypePositions of n.
func (c *undefChecker) checkBareTypePositionsNative(n native.GalaNodeRef, typeParams, extra map[string]bool) {
	check := func(id native.GalaNodeRef) {
		name, at := nativewalk.IdentifierSite(id)
		c.checkBareTypeName(name, at, typeParams, extra)
	}
	switch n.Kind() {
	case native.GalaKIND_typeAlias:
		if id := (native.TypeAliasNode{GalaNodeRef: n}).Identifier(); id.Ok() {
			check(id.GalaNodeRef)
		}
	case native.GalaKIND_typeParameter:
		// The constraint, of a parameter of exactly two identifiers.
		tp := native.TypeParameterNode{GalaNodeRef: n}
		if second := tp.Identifier(1); second.Ok() && !tp.Identifier(2).Ok() {
			check(second.GalaNodeRef)
		}
	case native.GalaKIND_type:
		params := native.TypeNode{GalaNodeRef: n}.Signature().Parameters()
		for _, p := range nativeParams(params) {
			if id := p.Identifier(); id.Ok() && !p.Type_().Ok() {
				check(id.GalaNodeRef)
			}
		}
	}
}

// tupleDestructureTypeNative is tupleDestructureType of n: the type node, or
// no node.
func tupleDestructureTypeNative(n native.GalaNodeRef) native.GalaNodeRef {
	var tuple native.TuplePatternNode
	var typ native.TypeNode
	switch n.Kind() {
	case native.GalaKIND_valDeclaration:
		d := native.ValDeclarationNode{GalaNodeRef: n}
		tuple, typ = d.TuplePattern(), d.Type_()
	case native.GalaKIND_varDeclaration:
		d := native.VarDeclarationNode{GalaNodeRef: n}
		tuple, typ = d.TuplePattern(), d.Type_()
	default:
		return native.GalaNodeRef{}
	}
	if tuple.Ok() && typ.Ok() {
		return typ.GalaNodeRef
	}
	return native.GalaNodeRef{}
}

// receiverBaseTypeNameNative is receiverBaseTypeName of t.
func receiverBaseTypeNameNative(t native.TypeNode) string {
	elem, _ := galaparser.NativePointerElem(t)
	return receiverBaseTypeOf(galaparser.NativeTypeText(elem))
}

// collectReceiverTypeArgsNative is collectReceiverTypeArgs of n.
func collectReceiverTypeArgsNative(n native.GalaNodeRef, out map[string]bool) {
	n.Walk(func(n native.GalaNodeRef) bool {
		if n.Kind() != native.GalaKIND_typeArguments {
			return true
		}
		list := native.TypeArgumentsNode{GalaNodeRef: n}.TypeList()
		for t := range galaparser.NativeKids(list.GalaNodeRef, native.GalaKIND_type) {
			qi := native.TypeNode{GalaNodeRef: t}.QualifiedIdentifier()
			if id, ok := galaparser.OnlyNativeKid(qi.GalaNodeRef, native.GalaKIND_identifier); ok {
				out[galaparser.NativeText(id)] = true
			}
		}
		return false
	})
}

// collectFileTypeBindersNative is collectFileTypeBinders of root.
func collectFileTypeBindersNative(root native.GalaNodeRef) map[string]bool {
	out := make(map[string]bool)
	bind := func(id native.IdentifierNode) {
		if id.Ok() {
			out[galaparser.NativeText(id.GalaNodeRef)] = true
		}
	}
	root.Walk(func(n native.GalaNodeRef) bool {
		switch n.Kind() {
		case native.GalaKIND_typeParameter:
			bind(native.TypeParameterNode{GalaNodeRef: n}.Identifier(0))
			return false
		case native.GalaKIND_receiver:
			if rt := (native.ReceiverNode{GalaNodeRef: n}).Type_(); rt.Ok() {
				collectReceiverTypeArgsNative(rt.GalaNodeRef, out)
			}
			return false
		case native.GalaKIND_typeDeclaration:
			bind(native.TypeDeclarationNode{GalaNodeRef: n}.Identifier())
		case native.GalaKIND_structShorthandDeclaration:
			bind(native.StructShorthandDeclarationNode{GalaNodeRef: n}.Identifier())
		case native.GalaKIND_sealedTypeDeclaration:
			bind(native.SealedTypeDeclarationNode{GalaNodeRef: n}.Identifier())
		case native.GalaKIND_opaqueTypeDeclaration:
			bind(native.OpaqueTypeDeclarationNode{GalaNodeRef: n}.Identifier())
		case native.GalaKIND_sealedCase:
			bind(native.SealedCaseNode{GalaNodeRef: n}.Identifier())
		}
		return true
	})
	return out
}
