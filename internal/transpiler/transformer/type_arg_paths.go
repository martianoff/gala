package transformer

import (
	"martianoff/gala/internal/transpiler"
)

// Type arguments travel through call lowering as printed strings (typeSubst,
// map[string]string) and are parsed back where a parameter type is
// substituted. The printed form `sub.Command` drops the import path, so a
// type of a package this file does not import came back unqualifiable: a
// lambda parameter typed by it named a package with no import. The transformer
// remembers the import path of every package-qualified type it prints as a
// type argument, and restores it on the way back. A printed name two import
// paths share is left without one rather than guessed.

// typeArgString prints typ as a type argument, remembering the import paths
// of the package-qualified types in it.
func (t *galaASTTransformer) typeArgString(typ transpiler.Type) string {
	t.rememberTypeArgPaths(typ)
	return typ.String()
}

// typeArgStrings prints each inferred type argument (see typeArgString).
func (t *galaASTTransformer) typeArgStrings(inferred map[string]transpiler.Type) map[string]string {
	for _, typ := range inferred {
		t.rememberTypeArgPaths(typ)
	}
	return typeSubstStrings(inferred)
}

// parseTypeArg parses a printed type argument, restoring the import paths
// typeArgString remembered.
func (t *galaASTTransformer) parseTypeArg(s string) transpiler.Type {
	return t.restoreTypeArgPaths(transpiler.ParseType(s))
}

// parseTypeSubst parses every printed type argument of subst (see
// parseTypeArg).
func (t *galaASTTransformer) parseTypeSubst(subst map[string]string) map[string]transpiler.Type {
	parsed := make(map[string]transpiler.Type, len(subst))
	for name, arg := range subst {
		parsed[name] = t.parseTypeArg(arg)
	}
	return parsed
}

func (t *galaASTTransformer) rememberTypeArgPaths(typ transpiler.Type) {
	walkNamedTypes(typ, func(nt transpiler.NamedType) transpiler.NamedType {
		if nt.ImportPath == "" || nt.Package == "" {
			return nt
		}
		key := nt.Package + "." + nt.Name
		if t.typeArgPaths == nil {
			t.typeArgPaths = make(map[string]string)
		}
		if prev, seen := t.typeArgPaths[key]; seen && prev != nt.ImportPath {
			t.typeArgPaths[key] = "" // two packages print alike: ambiguous
		} else if !seen {
			t.typeArgPaths[key] = nt.ImportPath
		}
		return nt
	})
}

func (t *galaASTTransformer) restoreTypeArgPaths(typ transpiler.Type) transpiler.Type {
	if len(t.typeArgPaths) == 0 {
		return typ
	}
	return walkNamedTypes(typ, func(nt transpiler.NamedType) transpiler.NamedType {
		if nt.ImportPath == "" && nt.Package != "" && nt.Package != t.packageName {
			if path := t.typeArgPaths[nt.Package+"."+nt.Name]; path != "" {
				nt.ImportPath = path
			}
		}
		return nt
	})
}

// walkNamedTypes returns typ with every NamedType in it replaced by f's
// result.
func walkNamedTypes(typ transpiler.Type, f func(transpiler.NamedType) transpiler.NamedType) transpiler.Type {
	switch v := typ.(type) {
	case transpiler.NamedType:
		return f(v)
	case transpiler.GenericType:
		params := make([]transpiler.Type, len(v.Params))
		for i, p := range v.Params {
			params[i] = walkNamedTypes(p, f)
		}
		return transpiler.GenericType{Base: walkNamedTypes(v.Base, f), Params: params}
	case transpiler.PointerType:
		return transpiler.PointerType{Elem: walkNamedTypes(v.Elem, f)}
	case transpiler.ArrayType:
		v.Elem = walkNamedTypes(v.Elem, f)
		return v
	case transpiler.MapType:
		v.Key = walkNamedTypes(v.Key, f)
		v.Elem = walkNamedTypes(v.Elem, f)
		return v
	case transpiler.FuncType:
		params := make([]transpiler.Type, len(v.Params))
		for i, p := range v.Params {
			params[i] = walkNamedTypes(p, f)
		}
		results := make([]transpiler.Type, len(v.Results))
		for i, r := range v.Results {
			results[i] = walkNamedTypes(r, f)
		}
		v.Params, v.Results = params, results
		return v
	}
	return typ
}
