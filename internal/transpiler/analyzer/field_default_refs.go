package analyzer

import (
	"fmt"
	"iter"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"martianoff/gala/galaerr"
	galaparser "martianoff/gala/internal/parser"
	"martianoff/gala/internal/parser/native"
	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/registry"
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
// type as GALA-E0098 rather than GALA-E0023. A name that resolves to a
// package-level val spelled like a field is GALA-E0098 too: the author almost
// certainly meant the field.

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

// fieldDefaultsMaySpellFieldsNative reports whether a field default of a
// struct shorthand or sealed type declaration of root, a file's native tree,
// may spell a field of its type: its source text holds the field's name as a
// word. It is a cheap superset of what GALA-E0098 reports, so the LSP
// analyzer builds the checker only for a file that may need it.
func fieldDefaultsMaySpellFieldsNative(root native.GalaNodeRef) bool {
	for decl := range nativeTopLevelDecls(root) {
		if k := decl.Kind(); k != native.GalaKIND_structShorthandDeclaration && k != native.GalaKIND_sealedTypeDeclaration {
			continue
		}
		for f := range fieldDefaultsNative(decl) {
			text := native.ExpressionNode{GalaNodeRef: f.def}.SourceText()
			if slices.ContainsFunc(f.site.fields, func(name string) bool { return containsWord(text, name) }) {
				return true
			}
		}
	}
	return false
}

// containsWord reports whether word occurs in text with no identifier
// character on either side.
func containsWord(text, word string) bool {
	isIdent := func(r rune) bool { return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r) }
	for i := 0; ; {
		j := strings.Index(text[i:], word)
		if j < 0 {
			return false
		}
		start, end := i+j, i+j+len(word)
		before, _ := utf8.DecodeLastRuneInString(text[:start])
		after, _ := utf8.DecodeRuneInString(text[end:])
		if (start == 0 || !isIdent(before)) && (end == len(text) || !isIdent(after)) {
			return true
		}
		i = start + 1
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

// reportFieldNamedVal reports GALA-E0098 for name, a reference at at that
// resolves, when the walk is in a field default and name is a package-level
// val or var (of this package, or of a package the file dot-imports) spelled
// like a field of the type. No field is in scope there, so the default reads
// the val: almost certainly a mistake. A type, a function or a package
// qualifier spelled like a field is no val and stays allowed, as does a
// qualified `pkg.W`, which the walk does not report as W.
func (c *undefChecker) reportFieldNamedVal(name string, at scopewalk.Site) {
	f := c.fieldDefault
	if f == nil || at.NoSite() || !slices.Contains(f.fields, name) || c.importsQualifier(name) {
		return
	}
	kind := c.packageValKind(name)
	if kind == "" {
		return
	}
	c.errs = append(c.errs, galaerr.NewCodedSemanticError(galaerr.CodeFieldDefaultReadsField, at.Line, at.Column,
		fmt.Sprintf("the default value of field %s of %s reads package-level %s %s, which has the same name as field %s", f.field, f.owner, kind, name, name),
		fmt.Sprintf("rename the %s or the field; a field default cannot read a field, so %s here is the %s", kind, name, kind),
	).WithSpan(at.Column+utf8.RuneCountInString(at.Text)))
}

// importsQualifier reports whether name is the qualifier of a package the
// file imports by name: `time` in `time.Now()`.
func (c *undefChecker) importsQualifier(name string) bool {
	for _, imp := range c.imports {
		if !imp.IsDot && imp.SpelledName() == name {
			return true
		}
	}
	return false
}

// packageValKind is "val" or "var" when name, unqualified, is a package-level
// val or var: of this file (a tuple pattern or an embed), of this package
// (GALA or its hand-written Go), of a package the file dot-imports, or of the
// prelude; and "" otherwise.
func (c *undefChecker) packageValKind(name string) string {
	kindOf := func(pv *transpiler.PackageValMetadata) string {
		if pv.IsVal {
			return "val"
		}
		return "var"
	}
	if c.fileVals[name] {
		return "val"
	}
	if pv := c.rich.PackageVals[name]; pv != nil {
		return kindOf(pv)
	}
	goPkgs := map[string]bool{c.rich.PackageName: true}
	var galaPaths []string
	for _, imp := range c.imports {
		if imp.IsDot {
			galaPaths = append(galaPaths, imp.Path)
			goPkgs[transpiler.LastPathSegment(imp.Path)] = true
		}
	}
	for _, p := range registry.Global.PreludePackages() {
		galaPaths = append(galaPaths, p.ImportPath)
	}
	for _, path := range galaPaths {
		if pv := c.rich.ImportedVals[path][name]; pv != nil {
			return kindOf(pv)
		}
	}
	if gi := c.rich.GoTypeInfo; gi != nil {
		for pkg := range goPkgs {
			if _, ok := gi.Variables[pkg+"."+name]; ok {
				return "var"
			}
		}
	}
	return ""
}
