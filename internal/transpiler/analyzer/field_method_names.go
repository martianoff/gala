package analyzer

import (
	"fmt"
	"sort"

	"martianoff/gala/galaerr"
	"martianoff/gala/internal/transpiler"
)

// renameSealedFieldsNamedLikeMethods stores each case field of sealed type meta
// that shares its name with a method of the type under a Go name of its case,
// "Epsilon" + "Target", as the transformer does (transformSealedTypeDeclaration).
// A case's fields are its own, so `case Epsilon(Target int)` and a method
// `func (t Transition) Target() int` coexist; in the one Go struct holding every
// case they cannot. It runs once all of the package's methods are known, and
// renames nothing when run again.
func renameSealedFieldsNamedLikeMethods(meta *transpiler.TypeMetadata) {
	renamed := make(map[string]bool)
	for _, v := range meta.SealedVariants {
		for _, name := range v.FieldNames {
			if _, plain := meta.Fields[name]; plain && transpiler.SealedFieldNamedLikeMethod(name, meta.Methods) {
				renamed[name] = true
			}
		}
	}
	if len(renamed) == 0 {
		return
	}
	immut := make(map[string]bool, len(meta.FieldNames))
	for i, name := range meta.FieldNames {
		if i < len(meta.ImmutFlags) {
			immut[name] = meta.ImmutFlags[i]
		}
	}
	oldFields := meta.Fields
	meta.Fields = make(map[string]transpiler.Type, len(oldFields))
	meta.FieldNames = meta.FieldNames[:0:0]
	meta.ImmutFlags = meta.ImmutFlags[:0:0]
	add := func(goName string, typ transpiler.Type, immutable bool) {
		if _, dup := meta.Fields[goName]; dup {
			return // a field the cases share
		}
		meta.Fields[goName] = typ
		meta.FieldNames = append(meta.FieldNames, goName)
		meta.ImmutFlags = append(meta.ImmutFlags, immutable)
	}
	// The order analyzeSealedType gives them: each case's fields in turn.
	for _, v := range meta.SealedVariants {
		for i, name := range v.FieldNames {
			if _, plain := oldFields[name]; plain && !renamed[name] {
				add(name, oldFields[name], immut[name])
				continue
			}
			goName := v.Name + name
			typ := oldFields[goName] // already per case: its type differs between cases
			if typ == nil && i < len(v.FieldTypes) {
				typ = v.FieldTypes[i]
			}
			flag, known := immut[goName]
			if !known {
				flag = immut[name]
			}
			add(goName, typ, flag)
		}
	}
	add("_variant", oldFields["_variant"], immut["_variant"])
}

// fieldNamedLikeMethodError rejects a method declared in filePath on a type of
// package pkgName that has a field of the method's name. Go allows no field and
// method of one name, and a struct field is read by its name, so unlike a sealed
// case field it cannot be stored under another.
func fieldNamedLikeMethodError(richAST *transpiler.RichAST, pkgName, filePath string) error {
	type clash struct {
		typeName string
		method   *transpiler.MethodMetadata
	}
	var clashes []clash
	for _, meta := range richAST.Types {
		if meta.Package != pkgName || meta.IsSealed || meta.IsInterface || len(meta.Fields) == 0 {
			continue
		}
		for field := range meta.Fields {
			if m := meta.Methods[field]; m != nil && m.DefinedIn == filePath && m.Pos.Line > 0 {
				clashes = append(clashes, clash{meta.Name, m})
			}
		}
	}
	if len(clashes) == 0 {
		return nil
	}
	sort.Slice(clashes, func(i, j int) bool {
		a, b := clashes[i].method.Pos, clashes[j].method.Pos
		return a.Line < b.Line || a.Line == b.Line && a.Column < b.Column
	})
	c := clashes[0]
	return galaerr.NewCodedSemanticError(galaerr.CodeFieldNamedLikeMethod,
		c.method.Pos.Line, c.method.Pos.Column,
		fmt.Sprintf("type %s has both a field and a method named %s", c.typeName, c.method.Name),
		"rename the field or the method; Go allows no field and method of one name on a type")
}
