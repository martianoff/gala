package transpiler

import (
	"strings"
)

// Packages that share a name: metadata is keyed "pkg.Name" by package name,
// which cannot tell two packages of one name apart. When a compilation unit
// reaches more than one package of a name (the package being compiled and an
// import, or two imports), each imported one is recorded under its own
// package key instead (see PackageKey): its types, functions and companions
// are keyed, and its types named, by that key, so every lookup keyed by
// package name finds the right package. The package's import still emits the
// file's qualifier for it; a key never reaches the generated Go.

// PackageKey is the key the package pkgName at importPath is recorded under
// when another package of its name is reached too. It names the package and
// its whole path, and is an identifier, as a package name is.
func PackageKey(pkgName, importPath string) string {
	var b strings.Builder
	b.WriteString(pkgName)
	b.WriteString("__")
	for _, r := range importPath {
		if r == '_' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	return b.String()
}

// PackageKeyName is the package name a PackageKey of the package at
// importPath was made from.
func PackageKeyName(key, importPath string) string {
	return strings.TrimSuffix(key, PackageKey("", importPath))
}

// PackageDisplayName is the name to show for the package pkg: the package
// name of a key (see PackageKey), else pkg itself.
func PackageDisplayName(pkg string) string {
	if name, _, keyed := strings.Cut(pkg, "__"); keyed && name != "" {
		return name
	}
	return pkg
}

// ApplyPackageKeys records in Packages the key of every keyed package (see
// PackageKeys), the name its metadata goes by.
func (r *RichAST) ApplyPackageKeys() {
	for path, key := range r.PackageKeys {
		if r.Packages == nil {
			r.Packages = make(map[string]string)
		}
		r.Packages[path] = key
	}
}

// RenamePackage returns r with the package from recorded as to: every
// declaration of from re-keyed and every type of from renamed, the
// declarations of other packages left as they are. r is not modified; what
// changes is copied.
func (r *RichAST) RenamePackage(from, to string) *RichAST {
	out := *r
	if out.PackageName == from {
		out.PackageName = to
	}
	rn := packageRenamer{from: from, to: to}
	if r.Types != nil {
		out.Types = make(map[string]*TypeMetadata, len(r.Types))
		for k, v := range r.Types {
			out.Types[rn.key(k)] = rn.typeMeta(v)
		}
	}
	if r.Functions != nil {
		out.Functions = make(map[string]*FunctionMetadata, len(r.Functions))
		for k, v := range r.Functions {
			out.Functions[rn.key(k)] = rn.function(v)
		}
	}
	if r.CompanionObjects != nil {
		out.CompanionObjects = make(map[string]*CompanionObjectMetadata, len(r.CompanionObjects))
		for k, v := range r.CompanionObjects {
			out.CompanionObjects[rn.key(k)] = rn.companion(v)
		}
	}
	if r.TypeAliases != nil {
		out.TypeAliases = make(map[string]Type, len(r.TypeAliases))
		for k, v := range r.TypeAliases {
			out.TypeAliases[rn.key(k)] = rn.typ(v)
		}
	}
	if r.PackageVals != nil {
		out.PackageVals = make(map[string]*PackageValMetadata, len(r.PackageVals))
		for k, v := range r.PackageVals {
			out.PackageVals[k] = rn.packageVal(v)
		}
	}
	if r.Packages != nil {
		out.Packages = make(map[string]string, len(r.Packages))
		for k, v := range r.Packages {
			out.Packages[k] = rn.pkg(v)
		}
	}
	return &out
}

// RenamePackageIn records, in r itself, the package from as to (see
// RenamePackage): for a package merged into r before another package of its
// name reached r.
func (r *RichAST) RenamePackageIn(from, to string) {
	renamed := r.RenamePackage(from, to)
	r.Types, r.Functions, r.CompanionObjects = renamed.Types, renamed.Functions, renamed.CompanionObjects
	r.TypeAliases, r.PackageVals, r.Packages = renamed.TypeAliases, renamed.PackageVals, renamed.Packages
	for path, funcs := range r.ImportedFuncs {
		r.ImportedFuncs[path] = renamed.renamedFuncs(funcs, from, to)
	}
	for path, vals := range r.ImportedVals {
		r.ImportedVals[path] = renamed.renamedVals(vals, from, to)
	}
}

func (*RichAST) renamedFuncs(funcs map[string]*FunctionMetadata, from, to string) map[string]*FunctionMetadata {
	rn := packageRenamer{from: from, to: to}
	out := make(map[string]*FunctionMetadata, len(funcs))
	for k, v := range funcs {
		out[k] = rn.function(v)
	}
	return out
}

func (*RichAST) renamedVals(vals map[string]*PackageValMetadata, from, to string) map[string]*PackageValMetadata {
	rn := packageRenamer{from: from, to: to}
	out := make(map[string]*PackageValMetadata, len(vals))
	for k, v := range vals {
		out[k] = rn.packageVal(v)
	}
	return out
}

// packageRenamer rewrites the package from to to in metadata.
type packageRenamer struct{ from, to string }

func (rn packageRenamer) pkg(p string) string {
	if p == rn.from {
		return rn.to
	}
	return p
}

// key renames a "pkg.Name" key (or a qualified type name) of from.
func (rn packageRenamer) key(k string) string {
	if rest, ok := strings.CutPrefix(k, rn.from+"."); ok {
		return rn.to + "." + rest
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
	if m.Methods != nil {
		c.Methods = make(map[string]*MethodMetadata, len(m.Methods))
		for k, v := range m.Methods {
			c.Methods[k] = rn.method(v)
		}
	}
	if m.Fields != nil {
		c.Fields = make(map[string]Type, len(m.Fields))
		for k, v := range m.Fields {
			c.Fields[k] = rn.typ(v)
		}
	}
	if m.TypeParamConstraints != nil {
		c.TypeParamConstraints = make(map[string]string, len(m.TypeParamConstraints))
		for k, v := range m.TypeParamConstraints {
			c.TypeParamConstraints[k] = rn.key(v)
		}
	}
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
