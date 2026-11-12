package analyzer

import (
	"fmt"
	"iter"
	"slices"
	"unicode/utf8"

	"martianoff/gala/galaerr"
	galaparser "martianoff/gala/internal/parser"
	"martianoff/gala/internal/parser/native"
	"martianoff/gala/internal/transpiler/scopewalk"
)

// A field default reads no field (GALA-E0098)
//
// The default of a shorthand struct's field or a sealed case's field is
// evaluated with no field of its type in scope, as in Scala, where
// `case class Rect(w: Int, h: Int = w)` does not compile: a name in it means
// what it means outside the type, a package-level name, a type, an import. A
// function's default, by contrast, may read the parameters before it
// (GALA-E0094 covers the later ones).
//
// The undefined-symbol check walks each field default with no field bound,
// and reports a name it cannot resolve that is spelled like a field of the
// type as GALA-E0098 rather than GALA-E0023.

// fieldDefaultSite is the field whose default the undefined-symbol walk is
// in: its name, its type's, and the names of the type's fields.
type fieldDefaultSite struct {
	field, owner string
	fields       []string
}

// fieldDefaultNative is the default of a field, at site.
type fieldDefaultNative struct {
	site fieldDefaultSite
	def  native.GalaNodeRef
}

// declTypeParametersNative is the type parameters of decl, a struct shorthand
// or sealed type declaration.
func declTypeParametersNative(decl native.GalaNodeRef) native.TypeParametersNode {
	if decl.Kind() == native.GalaKIND_structShorthandDeclaration {
		return native.StructShorthandDeclarationNode{GalaNodeRef: decl}.TypeParameters()
	}
	return native.SealedTypeDeclarationNode{GalaNodeRef: decl}.TypeParameters()
}

// fieldDefaultsNative is the field defaults of decl, a struct shorthand or a
// sealed type declaration (each of its cases'), in source order.
func fieldDefaultsNative(decl native.GalaNodeRef) iter.Seq[fieldDefaultNative] {
	type field struct {
		name string
		def  native.ParamDefaultNode
	}
	return func(yield func(fieldDefaultNative) bool) {
		emit := func(owner string, fields []field) bool {
			names := make([]string, len(fields))
			for i, f := range fields {
				names[i] = f.name
			}
			for _, f := range fields {
				if f.def.Ok() && !yield(fieldDefaultNative{site: fieldDefaultSite{field: f.name, owner: owner, fields: names}, def: f.def.Expression().GalaNodeRef}) {
					return false
				}
			}
			return true
		}
		if decl.Kind() == native.GalaKIND_structShorthandDeclaration {
			d := native.StructShorthandDeclarationNode{GalaNodeRef: decl}
			var fields []field
			for _, p := range nativeParams(d.Parameters()) {
				fields = append(fields, field{galaparser.NativeText(p.Identifier().GalaNodeRef), p.ParamDefault()})
			}
			emit(galaparser.NativeText(d.Identifier().GalaNodeRef), fields)
			return
		}
		for scNode := range galaparser.NativeKids(decl, native.GalaKIND_sealedCase) {
			sc := native.SealedCaseNode{GalaNodeRef: scNode}
			var fields []field
			for fcNode := range galaparser.NativeKids(sc.SealedCaseFieldList().GalaNodeRef, native.GalaKIND_sealedCaseField) {
				fc := native.SealedCaseFieldNode{GalaNodeRef: fcNode}
				fields = append(fields, field{galaparser.NativeText(fc.Identifier().GalaNodeRef), fc.ParamDefault()})
			}
			if !emit(galaparser.NativeText(sc.Identifier().GalaNodeRef), fields) {
				return
			}
		}
	}
}

// reportFieldRead reports GALA-E0098 for name, a reference at at that no
// scope binds and nothing resolves, when the walk is in a field default and
// name is spelled like a field of its type. It reports whether it did.
func (c *undefChecker) reportFieldRead(name string, at scopewalk.Site) bool {
	f := c.fieldDefault
	if f == nil || !slices.Contains(f.fields, name) {
		return false
	}
	if at.NoSite() {
		return true
	}
	msg := fmt.Sprintf("the default value of field %s reads field %s of %s", f.field, name, f.owner)
	if name == f.field {
		msg = fmt.Sprintf("the default value of field %s of %s reads %s itself", f.field, f.owner, name)
	}
	c.errs = append(c.errs, galaerr.NewCodedSemanticError(galaerr.CodeFieldDefaultReadsField, at.Line, at.Column, msg,
		fmt.Sprintf("a field default may not read a field of its type; give %s a default that reads no field, or compute it in a constructor function instead", f.field),
	).WithSpan(at.Column+utf8.RuneCountInString(at.Text)))
	return true
}
