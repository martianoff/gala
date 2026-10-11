package transpiler

import (
	"fmt"
	"strings"
)

// Packages that share a name: metadata is keyed "pkg.Name" by package name,
// which cannot tell two packages of one name apart. When a compilation unit
// reaches more than one package of a name (the package being compiled and an
// import, or two imports), each imported one is recorded under its own
// package key instead (see PackageKey): its types, functions and companions
// are keyed, and its types named, by that key, so every lookup keyed by
// package name finds the right package. The package's import still binds its
// own name in source and emits the file's qualifier; a key never reaches the
// generated Go.

// PackageKey is the key the package pkgName at importPath is recorded under
// when another package of its name is reached too: the name, "__", and the
// path with every byte but a letter or digit written as `_` and its hex code.
// It is an identifier, as a package name is, distinct for every path, and its
// path part never holds "__".
func PackageKey(pkgName, importPath string) string {
	var b strings.Builder
	b.WriteString(pkgName)
	b.WriteString("__")
	for i := 0; i < len(importPath); i++ {
		c := importPath[i]
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' {
			b.WriteByte(c)
		} else {
			fmt.Fprintf(&b, "_%02x", c)
		}
	}
	return b.String()
}

// PackageKeyName is the package name a PackageKey of the package at
// importPath was made from; any other name is returned as it is.
func PackageKeyName(key, importPath string) string {
	return strings.TrimSuffix(key, PackageKey("", importPath))
}

// PackageDisplayName is the name to show for the package pkg: the package
// name of a key (see PackageKey), else pkg itself.
func PackageDisplayName(pkg string) string {
	// A key's path part escapes the path's separators, so it holds a `_`; a
	// package name that merely contains "__" keeps its last part plain.
	if i := strings.LastIndex(pkg, "__"); i > 0 && strings.Contains(pkg[i+2:], "_") {
		return pkg[:i]
	}
	return pkg
}

// ApplyPackageKeys records in Packages the key of every keyed package (see
// PackageKeys), the name its metadata goes by.
// A package Packages does not hold is left out: its metadata was loaded
// without making it an import.
func (r *RichAST) ApplyPackageKeys() {
	for path, key := range r.PackageKeys {
		if _, ok := r.Packages[path]; ok {
			r.Packages[path] = key
		}
	}
}

// RenamePackages returns r with each package named in renames (old name ->
// new) recorded under its new name: its declarations re-keyed and every type
// naming it renamed, other packages' declarations left as they are. r is not
// modified; what changes is copied.
func (r *RichAST) RenamePackages(renames map[string]string) *RichAST {
	out := *r
	rn := packageRenamer(renames)
	out.PackageName = rn.pkg(r.PackageName)
	out.Types = mapEntries(r.Types, rn.key, rn.typeMeta)
	out.Functions = mapEntries(r.Functions, rn.key, rn.function)
	out.CompanionObjects = mapEntries(r.CompanionObjects, rn.key, rn.companion)
	out.TypeAliases = mapEntries(r.TypeAliases, rn.key, rn.typ)
	out.PackageVals = mapEntries(r.PackageVals, same, rn.packageVal)
	out.Packages = mapEntries(r.Packages, same, rn.pkg)
	out.GoExports = withRenamedKeys(r.GoExports, rn.pkg)
	out.GoTypeInfo = rn.goTypeInfo(r.GoTypeInfo)
	return &out
}

// RenamePackagesIn is RenamePackages applied to r itself, its imported
// bindings included: for a package merged into r under its name before
// another package of the name reached r.
func (r *RichAST) RenamePackagesIn(renames map[string]string) {
	renamed := r.RenamePackages(renames)
	r.Types, r.Functions, r.CompanionObjects = renamed.Types, renamed.Functions, renamed.CompanionObjects
	r.TypeAliases, r.PackageVals, r.Packages = renamed.TypeAliases, renamed.PackageVals, renamed.Packages
	r.GoExports, r.GoTypeInfo = renamed.GoExports, renamed.GoTypeInfo
	rn := packageRenamer(renames)
	r.ImportAliases = mapEntries(r.ImportAliases, same, rn.pkg)
	for path, vals := range r.ImportedVals {
		r.ImportedVals[path] = mapEntries(vals, same, rn.packageVal)
	}
}

func same(k string) string { return k }

// mapEntries returns m with every key passed through key and every value
// through value; nil for a nil m.
func mapEntries[V any](m map[string]V, key func(string) string, value func(V) V) map[string]V {
	if m == nil {
		return nil
	}
	out := make(map[string]V, len(m))
	for k, v := range m {
		out[key(k)] = value(v)
	}
	return out
}

// packageRenamer rewrites package names (old -> new) in metadata.
type packageRenamer map[string]string

func (rn packageRenamer) pkg(p string) string {
	if to, ok := rn[p]; ok {
		return to
	}
	return p
}

// key renames the package of a "pkg.Name" key or qualified type name.
func (rn packageRenamer) key(k string) string {
	if pkg, rest, ok := strings.Cut(k, "."); ok {
		if to, renamed := rn[pkg]; renamed {
			return to + "." + rest
		}
	}
	return k
}

func (rn packageRenamer) typ(t Type) Type {
	switch v := t.(type) {
	case NamedType:
		v.Package = rn.pkg(v.Package)
		return v
	case BasicType:
		v.Name = rn.key(v.Name)
		return v
	case GenericType:
		v.Base = rn.typ(v.Base)
		v.Params = rn.types(v.Params)
		return v
	case ArrayType:
		v.Elem = rn.typ(v.Elem)
		return v
	case MapType:
		v.Key, v.Elem = rn.typ(v.Key), rn.typ(v.Elem)
		return v
	case PointerType:
		v.Elem = rn.typ(v.Elem)
		return v
	case FuncType:
		v.Params, v.Results = rn.types(v.Params), rn.types(v.Results)
		return v
	}
	return t
}

func (rn packageRenamer) types(ts []Type) []Type {
	if ts == nil {
		return nil
	}
	out := make([]Type, len(ts))
	for i, t := range ts {
		out[i] = rn.typ(t)
	}
	return out
}

func (rn packageRenamer) typeMeta(m *TypeMetadata) *TypeMetadata {
	if m == nil {
		return nil
	}
	c := *m
	c.Package = rn.pkg(m.Package)
	c.Methods = mapEntries(m.Methods, same, rn.method)
	c.Fields = mapEntries(m.Fields, same, rn.typ)
	c.TypeParamConstraints = mapEntries(m.TypeParamConstraints, same, rn.key)
	if m.SealedVariants != nil {
		c.SealedVariants = make([]SealedVariant, len(m.SealedVariants))
		for i, v := range m.SealedVariants {
			v.FieldTypes = rn.types(v.FieldTypes)
			c.SealedVariants[i] = v
		}
	}
	if m.Underlying != nil {
		c.Underlying = rn.typ(m.Underlying)
	}
	if m.UnderlyingBase != nil {
		c.UnderlyingBase = rn.typ(m.UnderlyingBase)
	}
	return &c
}

func (rn packageRenamer) method(m *MethodMetadata) *MethodMetadata {
	if m == nil {
		return nil
	}
	c := *m
	c.Package = rn.pkg(m.Package)
	c.ParamTypes = rn.types(m.ParamTypes)
	if m.ReturnType != nil {
		c.ReturnType = rn.typ(m.ReturnType)
	}
	c.GoResults = rn.types(m.GoResults)
	return &c
}

func (rn packageRenamer) function(f *FunctionMetadata) *FunctionMetadata {
	if f == nil {
		return nil
	}
	c := *f
	c.Package = rn.pkg(f.Package)
	c.ParamTypes = rn.types(f.ParamTypes)
	if f.ReturnType != nil {
		c.ReturnType = rn.typ(f.ReturnType)
	}
	c.GoResults = rn.types(f.GoResults)
	return &c
}

func (rn packageRenamer) companion(co *CompanionObjectMetadata) *CompanionObjectMetadata {
	if co == nil {
		return nil
	}
	c := *co
	c.Package = rn.pkg(co.Package)
	c.TargetType = rn.key(co.TargetType)
	return &c
}

// goTypeInfo adds, under the new names, the entries a renamed package's
// hand-written Go records under its old one. The old entries stay: a Go
// package of the old name (a GALA package may share a Go package's name)
// records its own under the same keys. The types they hold are Go's, and
// keep their names.
func (rn packageRenamer) goTypeInfo(g *GoTypeInfo) *GoTypeInfo {
	if g == nil {
		return nil
	}
	c := *g
	c.Functions = withRenamedKeys(g.Functions, rn.key)
	c.Types = withRenamedKeys(g.Types, rn.key)
	c.Variables = withRenamedKeys(g.Variables, rn.key)
	c.Constants = withRenamedKeys(g.Constants, rn.key)
	c.UntypedConstants = withRenamedKeys(g.UntypedConstants, rn.key)
	c.TypeAliases = withRenamedKeys(g.TypeAliases, rn.key)
	c.GalaTypeMethods = withRenamedKeys(g.GalaTypeMethods, rn.key)
	return &c
}

// withRenamedKeys returns m with every entry whose key renames also recorded
// under the renamed key.
func withRenamedKeys[V any](m map[string]V, key func(string) string) map[string]V {
	if m == nil {
		return nil
	}
	out := make(map[string]V, len(m))
	for k, v := range m {
		out[k] = v
		if renamed := key(k); renamed != k {
			out[renamed] = v
		}
	}
	return out
}

func (rn packageRenamer) packageVal(v *PackageValMetadata) *PackageValMetadata {
	if v == nil {
		return nil
	}
	c := *v
	if v.Type != nil {
		c.Type = rn.typ(v.Type)
	}
	return &c
}
