package resolver

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestResolve_ExactMatch(t *testing.T) {
	r := &TypeResolver{PackageName: "mypackage"}
	known := map[string]bool{"MyType": true}
	exists := func(name string) bool { return known[name] }

	resolved, ok := r.Resolve("MyType", exists)
	assert.True(t, ok)
	assert.Equal(t, "MyType", resolved)
}

func TestResolve_StdPrefix(t *testing.T) {
	r := &TypeResolver{PackageName: "mypackage"}
	known := map[string]bool{"std.Option": true}
	exists := func(name string) bool { return known[name] }

	resolved, ok := r.Resolve("Option", exists)
	assert.True(t, ok)
	assert.Equal(t, "std.Option", resolved)
}

func TestResolve_CurrentPackage(t *testing.T) {
	r := &TypeResolver{PackageName: "server"}
	known := map[string]bool{"server.Handler": true}
	exists := func(name string) bool { return known[name] }

	resolved, ok := r.Resolve("Handler", exists)
	assert.True(t, ok)
	assert.Equal(t, "server.Handler", resolved)
}

func TestResolve_DotImport(t *testing.T) {
	r := &TypeResolver{
		PackageName: "mypackage",
		Imports: []PackageInfo{
			{PkgName: "collection_immutable", IsDot: true},
		},
	}
	known := map[string]bool{"collection_immutable.Array": true}
	exists := func(name string) bool { return known[name] }

	resolved, ok := r.Resolve("Array", exists)
	assert.True(t, ok)
	assert.Equal(t, "collection_immutable.Array", resolved)
}

func TestResolve_NamedImport(t *testing.T) {
	r := &TypeResolver{
		PackageName: "mypackage",
		Imports: []PackageInfo{
			{PkgName: "collection_mutable", IsDot: false},
		},
	}
	known := map[string]bool{"collection_mutable.Array": true}
	exists := func(name string) bool { return known[name] }

	resolved, ok := r.Resolve("Array", exists)
	assert.True(t, ok)
	assert.Equal(t, "collection_mutable.Array", resolved)
}

func TestResolve_NotFound(t *testing.T) {
	r := &TypeResolver{PackageName: "mypackage"}
	exists := func(name string) bool { return false }

	_, ok := r.Resolve("Unknown", exists)
	assert.False(t, ok)
}

func TestResolve_Precedence_StdBeforeCurrentPackage(t *testing.T) {
	r := &TypeResolver{PackageName: "mypackage"}
	known := map[string]bool{
		"std.Option":      true,
		"mypackage.Option": true,
	}
	exists := func(name string) bool { return known[name] }

	resolved, ok := r.Resolve("Option", exists)
	assert.True(t, ok)
	assert.Equal(t, "std.Option", resolved, "std should take precedence over current package")
}

func TestResolve_Precedence_DotImportBeforeNamedImport(t *testing.T) {
	r := &TypeResolver{
		PackageName: "mypackage",
		Imports: []PackageInfo{
			{PkgName: "collection_mutable", IsDot: false},
			{PkgName: "collection_immutable", IsDot: true},
		},
	}
	known := map[string]bool{
		"collection_immutable.Array": true,
		"collection_mutable.Array":   true,
	}
	exists := func(name string) bool { return known[name] }

	resolved, ok := r.Resolve("Array", exists)
	assert.True(t, ok)
	assert.Equal(t, "collection_immutable.Array", resolved, "dot import should take precedence over named import")
}

func TestResolve_Precedence_CurrentPackageBeforeDotImport(t *testing.T) {
	r := &TypeResolver{
		PackageName: "server",
		Imports: []PackageInfo{
			{PkgName: "other", IsDot: true},
		},
	}
	known := map[string]bool{
		"server.Handler": true,
		"other.Handler":  true,
	}
	exists := func(name string) bool { return known[name] }

	resolved, ok := r.Resolve("Handler", exists)
	assert.True(t, ok)
	assert.Equal(t, "server.Handler", resolved, "current package should take precedence over dot import")
}

func TestResolve_EmptyPackageName(t *testing.T) {
	r := &TypeResolver{PackageName: ""}
	known := map[string]bool{"std.Option": true}
	exists := func(name string) bool { return known[name] }

	resolved, ok := r.Resolve("Option", exists)
	assert.True(t, ok)
	assert.Equal(t, "std.Option", resolved)
}
