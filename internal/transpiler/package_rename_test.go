package transpiler

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPackageKey(t *testing.T) {
	key := PackageKey("util", "example.com/same-name/a/util")
	assert.Equal(t, "util__example_2ecom_2fsame_2dname_2fa_2futil", key)
	assert.NotEqual(t, PackageKey("x", "a/b-c"), PackageKey("x", "a/b_c"))
	assert.Equal(t, "my__pkg", PackageDisplayName(PackageKey("my__pkg", "a/b")))
	assert.Equal(t, "my__pkg", PackageDisplayName("my__pkg"))
	assert.Equal(t, "util", PackageKeyName(key, "example.com/same-name/a/util"))
	assert.Equal(t, "util", PackageDisplayName(key))
	assert.Equal(t, "collection_immutable", PackageDisplayName("collection_immutable"))
}

// RenamePackages renames the package's own declarations and every type that
// names it, and leaves other packages' alone.
func TestRenamePackage(t *testing.T) {
	config := NamedType{Package: "util", Name: "Config"}
	other := NamedType{Package: "std", Name: "Option"}
	r := &RichAST{
		PackageName: "util",
		Types: map[string]*TypeMetadata{
			"util.Config": {
				Name: "Config", Package: "util",
				Fields:         map[string]Type{"Next": GenericType{Base: other, Params: []Type{config}}},
				Methods:        map[string]*MethodMetadata{"With": {Name: "With", Package: "util", ParamTypes: []Type{PointerType{Elem: config}}, ReturnType: config}},
				SealedVariants: []SealedVariant{{Name: "V", FieldTypes: []Type{ArrayType{Elem: BasicType{Name: "util.Config"}}}}},
			},
			"std.Option": {Name: "Option", Package: "std"},
		},
		Functions:        map[string]*FunctionMetadata{"util.New": {Name: "New", Package: "util", ReturnType: config}},
		CompanionObjects: map[string]*CompanionObjectMetadata{"util.Some": {Name: "Some", Package: "util", TargetType: "util.Config"}},
	}

	out := r.RenamePackages(map[string]string{"util": "k"})

	assert.Equal(t, "k", out.PackageName)
	require.Contains(t, out.Types, "k.Config")
	assert.Contains(t, out.Types, "std.Option")
	assert.NotContains(t, out.Types, "util.Config")
	tm := out.Types["k.Config"]
	assert.Equal(t, "k", tm.Package)
	assert.Equal(t, "std.Option[k.Config]", tm.Fields["Next"].String())
	assert.Equal(t, "*k.Config", tm.Methods["With"].ParamTypes[0].String())
	assert.Equal(t, "k.Config", tm.Methods["With"].ReturnType.String())
	assert.Equal(t, "[]k.Config", tm.SealedVariants[0].FieldTypes[0].String())
	assert.Equal(t, "k.Config", out.Functions["k.New"].ReturnType.String())
	assert.Equal(t, "k.Config", out.CompanionObjects["k.Some"].TargetType)

	// r itself is unchanged.
	assert.Equal(t, "util", r.PackageName)
	assert.Equal(t, "util.Config", r.Types["util.Config"].Methods["With"].ReturnType.String())
}
