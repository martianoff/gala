package transformer

import (
	galaparser "martianoff/gala/internal/parser"
	"martianoff/gala/internal/parser/grammar"
	"martianoff/gala/internal/parser/native"
)

// Sealed and opaque type declarations as their lowering reads them
// (lowerSealedDecl, lowerOpaqueDecl), from either tree: each reader here has
// an ANTLR half and a native half that fill the same tree-free value (see
// declarations_native.go). A sealed case field's default is not read: the
// analyzer records it, and the lowering reads it from the type's metadata.

// sealedDecl is a sealed type declaration.
type sealedDecl struct {
	name   string
	at     srcNode // the declaration
	nameAt srcNode
	// generic reports that it declares typeParams.
	generic    bool
	typeParams []typeParam
	cases      []sealedCaseDecl
}

// sealedCaseDecl is a case of a sealed type declaration.
type sealedCaseDecl struct {
	name      string
	at        srcNode // the case
	nameAt    srcNode
	isDefault bool
	fields    []sealedCaseFieldDecl
}

// sealedCaseFieldDecl is a field of a sealed type's case.
type sealedCaseFieldDecl struct {
	name string
	at   srcNode
	typ  typeSrc
}

// readSealedDecl reads a sealed type declaration on ANTLR's tree.
func readSealedDecl(ctx *grammar.SealedTypeDeclarationContext) sealedDecl {
	id := ctx.Identifier()
	d := sealedDecl{name: id.GetText(), at: srcNode{ctx: ctx}, nameAt: srcNode{ctx: id}}
	if tps := ctx.TypeParameters(); tps != nil {
		d.generic, d.typeParams = true, readTypeParams(tps.(*grammar.TypeParametersContext))
	}
	for _, caseCtx := range ctx.AllSealedCase() {
		sc := caseCtx.(*grammar.SealedCaseContext)
		c := sealedCaseDecl{name: sc.Identifier().GetText(), at: srcNode{ctx: sc}, nameAt: srcNode{ctx: sc.Identifier()}, isDefault: sc.DEFAULT() != nil}
		if list := sc.SealedCaseFieldList(); list != nil {
			for _, fieldCtx := range list.(*grammar.SealedCaseFieldListContext).AllSealedCaseField() {
				fc := fieldCtx.(*grammar.SealedCaseFieldContext)
				c.fields = append(c.fields, sealedCaseFieldDecl{name: fc.Identifier().GetText(), at: srcNode{ctx: fc}, typ: antlrType(fc.Type_())})
			}
		}
		d.cases = append(d.cases, c)
	}
	return d
}

// readSealedDeclNative is readSealedDecl of n.
func readSealedDeclNative(n native.SealedTypeDeclarationNode) sealedDecl {
	id := n.Identifier()
	d := sealedDecl{name: id.GetText(), at: srcNode{node: n.GalaNodeRef}, nameAt: srcNode{node: id.GalaNodeRef}}
	if tps := n.TypeParameters(); tps.Ok() {
		d.generic, d.typeParams = true, nativeTypeParams(tps)
	}
	for c := range galaparser.NativeKids(n.GalaNodeRef, native.GalaKIND_sealedCase) {
		sc := native.SealedCaseNode{GalaNodeRef: c}
		cid := sc.Identifier()
		cd := sealedCaseDecl{name: cid.GetText(), at: srcNode{node: c}, nameAt: srcNode{node: cid.GalaNodeRef}, isDefault: sc.DEFAULT().Ok()}
		for f := range galaparser.NativeKids(sc.SealedCaseFieldList().GalaNodeRef, native.GalaKIND_sealedCaseField) {
			field := native.SealedCaseFieldNode{GalaNodeRef: f}
			cd.fields = append(cd.fields, sealedCaseFieldDecl{name: field.Identifier().GetText(), at: srcNode{node: f}, typ: nativeType(field.Type_())})
		}
		d.cases = append(d.cases, cd)
	}
	return d
}

// opaqueDecl is an opaque type declaration.
type opaqueDecl struct {
	name string
	// generic reports that it declares typeParams.
	generic    bool
	typeParams []typeParam
	// underlying is the type it is declared over.
	underlying typeSrc
}

// readOpaqueDecl reads an opaque type declaration on ANTLR's tree.
func readOpaqueDecl(ctx *grammar.OpaqueTypeDeclarationContext) opaqueDecl {
	d := opaqueDecl{name: ctx.Identifier().GetText(), underlying: antlrType(ctx.Type_())}
	if tps := ctx.TypeParameters(); tps != nil {
		d.generic, d.typeParams = true, readTypeParams(tps.(*grammar.TypeParametersContext))
	}
	return d
}

// readOpaqueDeclNative is readOpaqueDecl of n.
func readOpaqueDeclNative(n native.OpaqueTypeDeclarationNode) opaqueDecl {
	d := opaqueDecl{name: n.Identifier().GetText(), underlying: nativeType(n.Type_())}
	if tps := n.TypeParameters(); tps.Ok() {
		d.generic, d.typeParams = true, nativeTypeParams(tps)
	}
	return d
}
