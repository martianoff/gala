package analyzer

import (
	"fmt"
	"slices"
	"unicode/utf8"

	"martianoff/gala/galaerr"
	galaparser "martianoff/gala/internal/parser"
	"martianoff/gala/internal/parser/native"
	"martianoff/gala/internal/transpiler/scopewalk"
	"martianoff/gala/internal/transpiler/scopewalk/nativewalk"
)

// A field default may not read a field (GALA-E0098)
//
// The default of a shorthand struct's field or a sealed case's field is
// evaluated on its own, as in Scala, where `case class Rect(w: Int, h: Int =
// w)` does not compile: `struct Rect(W int, H int = W)` is an error. A
// function's default, by contrast, may read the parameters before it
// (GALA-E0094 covers the later ones). A package-level name or constant is not
// a field, so a field default may read one; a name a lambda in the default
// binds is the lambda's.

// checkFieldDefaultsNative reports GALA-E0098 for the first field default of
// root, a file's native tree, that reads a field of its own struct or sealed
// case, or nil when none does.
func checkFieldDefaultsNative(root native.GalaNodeRef) error {
	for decl := range nativeTopLevelDecls(root) {
		switch decl.Kind() {
		case native.GalaKIND_structShorthandDeclaration:
			d := native.StructShorthandDeclarationNode{GalaNodeRef: decl}
			var fields []fieldDefault
			for _, p := range nativeParams(d.Parameters()) {
				fields = append(fields, fieldDefault{name: galaparser.NativeText(p.Identifier().GalaNodeRef), def: p.ParamDefault()})
			}
			if err := fieldDefaultReadsField(galaparser.NativeText(d.Identifier().GalaNodeRef), fields); err != nil {
				return err
			}
		case native.GalaKIND_sealedTypeDeclaration:
			for scNode := range galaparser.NativeKids(decl, native.GalaKIND_sealedCase) {
				sc := native.SealedCaseNode{GalaNodeRef: scNode}
				var fields []fieldDefault
				for fcNode := range galaparser.NativeKids(sc.SealedCaseFieldList().GalaNodeRef, native.GalaKIND_sealedCaseField) {
					fc := native.SealedCaseFieldNode{GalaNodeRef: fcNode}
					fields = append(fields, fieldDefault{name: galaparser.NativeText(fc.Identifier().GalaNodeRef), def: fc.ParamDefault()})
				}
				if err := fieldDefaultReadsField(galaparser.NativeText(sc.Identifier().GalaNodeRef), fields); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// fieldDefault is a field of a struct or sealed case and its default, if it
// declares one.
type fieldDefault struct {
	name string
	def  native.ParamDefaultNode
}

// fieldDefaultReadsField is GALA-E0098 for the first default of fields, the
// fields of typeName, that reads one of them, or nil.
func fieldDefaultReadsField(typeName string, fields []fieldDefault) error {
	names := make([]string, len(fields))
	for i, f := range fields {
		names[i] = f.name
	}
	for _, f := range fields {
		if !f.def.Ok() {
			continue
		}
		ref := &firstFieldRef{fields: names}
		// The options undefined_symbol.go's pass walks with: interpolated
		// strings are walked, and nothing that is not a value is reported.
		nativewalk.New(scopewalk.New(ref, undefWalkOptions()), nil).Walk(f.def.Expression().GalaNodeRef)
		if ref.name == "" || ref.at.NoSite() {
			continue
		}
		msg := fmt.Sprintf("the default value of field %s reads field %s of %s", f.name, ref.name, typeName)
		if ref.name == f.name {
			msg = fmt.Sprintf("the default value of field %s of %s reads %s itself", f.name, typeName, f.name)
		}
		return galaerr.NewCodedSemanticError(galaerr.CodeFieldDefaultReadsField, ref.at.Line, ref.at.Column, msg,
			fmt.Sprintf("a field default may not read a field of its type; give %s a default that reads no field, or compute it in a constructor function instead", f.name),
		).WithSpan(ref.at.Column + utf8.RuneCountInString(ref.at.Text))
	}
	return nil
}

// firstFieldRef is a scopewalk.Visitor that keeps the first reference to one
// of fields.
type firstFieldRef struct {
	fields []string
	name   string
	at     scopewalk.Site
}

func (r *firstFieldRef) Reference(name string, at scopewalk.Site, _ scopewalk.Use) {
	if r.name == "" && slices.Contains(r.fields, name) {
		r.name, r.at = name, at
	}
}
