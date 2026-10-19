package transformer

import (
	"fmt"
	"go/ast"

	"martianoff/gala/galaerr"
	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/registry"
)

// Default values. GALA code never creates an implicit zero value: where it
// needs a value of a type and none is written (an uninitialized `var`, a field
// a construction leaves out), it uses the type's default value, and reports a
// type that has none.
//
//   - A sealed type's default value is its default case, built with its
//     fields' declared defaults; a sealed type without a default case has none.
//   - A struct's default value has each field at its declared default, or at
//     its type's default value; a struct holding a type with none has none.
//   - Every other type's default value is Go's zero value: numbers, strings,
//     slices, maps, pointers, functions, Go types and type parameters.
//
// Only a value Go creates by itself (a fresh slice slot, a missed map lookup,
// decoding) can hold a zero value no default case built.

// defaultValue returns the expression GALA uses for a value of typ where its
// code needs one and none is written, nil when Go's zero value is that value,
// and GALA-E0075 when typ has none. seen guards recursive types.
func (t *galaASTTransformer) defaultValue(typ transpiler.Type, line, col int, seen map[string]bool) (ast.Expr, error) {
	if transpiler.IsUnusable(typ) {
		return nil, nil
	}
	typ = t.followAliasChain(typ)
	if transpiler.IsPtr(typ) {
		name := transpiler.DisplayType(typ)
		return nil, galaerr.NewCodedSemanticError(galaerr.CodeNoDefaultValue, line, col,
			fmt.Sprintf("%s has no default value: a Ptr is never nil", name),
			fmt.Sprintf("give the value a pointer (`&x`); %s", ptrMayBeAbsentHint))
	}
	switch typ.(type) {
	case transpiler.PointerType, transpiler.ArrayType, transpiler.MapType, transpiler.FuncType:
		return nil, nil
	}
	if t.activeTypeParams[typ.BaseName()] {
		return nil, nil
	}
	meta := t.getTypeMeta(typ.BaseName())
	if meta == nil || meta.IsOpaque {
		return nil, nil
	}
	key := typ.String()
	if seen[key] {
		return nil, nil
	}
	seen[key] = true
	defer delete(seen, key)

	var args []transpiler.Type
	if gen, ok := typ.(transpiler.GenericType); ok {
		args = gen.Params
	}
	if meta.IsSealed {
		return t.sealedDefaultValue(typ, meta, args, line, col, seen)
	}
	resolved := t.resolveStructTypeName(typ.BaseName())
	fields := t.structFields[resolved]
	if len(fields) == 0 {
		return nil, nil
	}
	typeExpr := t.typeToExpr(typ)
	elts, err := t.fillOmittedStructFields(stripPackagePrefix(typ.BaseName()), resolved, fields,
		func(int, string) bool { return false }, t.structTypeArgSubst(typeExpr, resolved), line, col, false, seen)
	if err != nil || len(elts) == 0 {
		return nil, err
	}
	return &ast.CompositeLit{Type: typeExpr, Elts: elts}, nil
}

// sealedDefaultValue is defaultValue of the sealed type typ (meta, with type
// arguments args): its default case built with its fields' declared defaults
// and, for the others, their types' default values; nil when every field is
// at Go's zero value, which tag 0 already makes that case.
func (t *galaASTTransformer) sealedDefaultValue(typ transpiler.Type, meta *transpiler.TypeMetadata, args []transpiler.Type, line, col int, seen map[string]bool) (ast.Expr, error) {
	var dv *transpiler.SealedVariant
	for i := range meta.SealedVariants {
		if meta.SealedVariants[i].IsDefault {
			dv = &meta.SealedVariants[i]
		}
	}
	name := t.displayTypeName(meta)
	if dv == nil {
		return nil, galaerr.NewCodedSemanticError(galaerr.CodeNoDefaultValue, line, col,
			fmt.Sprintf("%s has no default value: it is a sealed type without a default case", name),
			fmt.Sprintf("give the value one of %s's cases, or mark a case of %s `default`", name, name))
	}
	if len(dv.FieldTypes) == 0 {
		return nil, nil
	}
	var apply *transpiler.MethodMetadata
	if comp := t.getTypeMeta(ownFunctionKey(meta.Package, dv.Name)); comp != nil {
		apply = comp.Methods["Apply"]
	}
	companion := transpiler.Type(transpiler.NamedType{Package: meta.Package, Name: dv.Name})
	if t.isLocalPackage(meta.Package) {
		companion = transpiler.BasicType{Name: dv.Name}
	}
	if len(args) > 0 {
		companion = transpiler.GenericType{Base: companion, Params: args}
	}
	companionExpr := t.typeToExpr(companion)

	values := make([]ast.Expr, len(dv.FieldTypes))
	needed := false
	for i, ft := range dv.FieldTypes {
		if apply != nil {
			if _, ok := apply.DefaultExprs[i]; ok {
				v, err := t.methodDefaultArg(apply, i, &ast.CompositeLit{Type: companionExpr}, companion, nil, line, col)
				if err != nil {
					return nil, err
				}
				values[i], needed = v, true
				continue
			}
		}
		fieldType := t.substituteConcreteTypes(ft, meta.TypeParams, args)
		v, err := t.defaultValue(fieldType, line, col, seen)
		if err != nil {
			return nil, err
		}
		if v != nil {
			needed = true
		} else {
			v = zeroValueExpr(t.typeToExpr(fieldType))
		}
		values[i] = v
	}
	if !needed {
		return nil, nil
	}
	return applyCall(companionExpr, values), nil
}

// zeroValueExpr is Go's zero value of the type typeExpr names: `*new(T)`.
func zeroValueExpr(typeExpr ast.Expr) ast.Expr {
	return &ast.StarExpr{X: &ast.CallExpr{Fun: ast.NewIdent("new"), Args: []ast.Expr{typeExpr}}}
}

// displayTypeName names the type meta describes as a diagnostic shows it.
func (t *galaASTTransformer) displayTypeName(meta *transpiler.TypeMetadata) string {
	if t.isLocalPackage(meta.Package) || meta.Package == registry.StdPackageName {
		return meta.Name
	}
	return transpiler.PackageDisplayName(meta.Package) + "." + meta.Name
}
