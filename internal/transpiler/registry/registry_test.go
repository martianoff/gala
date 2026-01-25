package registry

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewRegistry(t *testing.T) {
	r := NewRegistry()
	assert.NotNil(t, r)
	assert.Empty(t, r.PreludePackages())
}

func TestRegisterPrelude(t *testing.T) {
	r := NewRegistry()

	info := PackageInfo{
		Name:       "test",
		ImportPath: "example/test",
		Types:      []string{"Foo", "Bar"},
		Functions:  []string{"NewFoo"},
		Companions: []string{"Foo"},
	}

	r.RegisterPrelude(info)

	// Verify package is registered
	assert.True(t, r.IsPreludePackage("test"))
	assert.False(t, r.IsPreludePackage("unknown"))

	// Verify types are indexed
	pkg, ok := r.IsPreludeType("Foo")
	assert.True(t, ok)
	assert.Equal(t, "test", pkg.Name)

	pkg, ok = r.IsPreludeType("Bar")
	assert.True(t, ok)
	assert.Equal(t, "test", pkg.Name)

	_, ok = r.IsPreludeType("Unknown")
	assert.False(t, ok)

	// Verify functions are indexed
	pkg, ok = r.IsPreludeFunction("NewFoo")
	assert.True(t, ok)
	assert.Equal(t, "test", pkg.Name)

	// Verify companions are indexed
	pkg, ok = r.IsPreludeCompanion("Foo")
	assert.True(t, ok)
	assert.Equal(t, "test", pkg.Name)
}

func TestCheckConflict(t *testing.T) {
	r := NewRegistry()
	r.RegisterPrelude(PackageInfo{
		Name:       "std",
		ImportPath: "martianoff/gala/std",
		Types:      []string{"Option", "Either"},
		Functions:  []string{"Some", "None"},
	})

	tests := []struct {
		name       string
		checkName  string
		currentPkg string
		wantError  bool
	}{
		{
			name:       "no conflict with different name",
			checkName:  "MyType",
			currentPkg: "main",
			wantError:  false,
		},
		{
			name:       "conflict with type",
			checkName:  "Option",
			currentPkg: "main",
			wantError:  true,
		},
		{
			name:       "conflict with function",
			checkName:  "Some",
			currentPkg: "main",
			wantError:  true,
		},
		{
			name:       "prelude package can define its exports",
			checkName:  "Option",
			currentPkg: "std",
			wantError:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := r.CheckConflict(tt.checkName, tt.currentPkg)
			if tt.wantError {
				assert.Error(t, err)
				var conflictErr *ConflictError
				assert.ErrorAs(t, err, &conflictErr)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestQualifyName(t *testing.T) {
	r := NewRegistry()
	r.RegisterPrelude(PackageInfo{
		Name:       "std",
		ImportPath: "martianoff/gala/std",
		Types:      []string{"Option"},
		Functions:  []string{"Some"},
	})

	assert.Equal(t, "std.Option", r.QualifyName("Option"))
	assert.Equal(t, "std.Some", r.QualifyName("Some"))
	assert.Equal(t, "Unknown", r.QualifyName("Unknown"))
}

func TestGetImportPath(t *testing.T) {
	r := NewRegistry()
	r.RegisterPrelude(PackageInfo{
		Name:       "std",
		ImportPath: "martianoff/gala/std",
		Types:      []string{"Option"},
	})

	assert.Equal(t, "martianoff/gala/std", r.GetImportPath("Option"))
	assert.Equal(t, "", r.GetImportPath("Unknown"))
}

func TestDefaultRegistry(t *testing.T) {
	r := DefaultRegistry()

	// Should have std as prelude
	assert.True(t, r.IsPreludePackage("std"))

	// Should have std types
	pkg, ok := r.IsPreludeType("Option")
	require.True(t, ok)
	assert.Equal(t, "std", pkg.Name)
	assert.Equal(t, "martianoff/gala/std", pkg.ImportPath)

	// Should have std functions
	pkg, ok = r.IsPreludeFunction("NewImmutable")
	require.True(t, ok)
	assert.Equal(t, "std", pkg.Name)

	// Should have std companions
	pkg, ok = r.IsPreludeCompanion("Some")
	require.True(t, ok)
	assert.Equal(t, "std", pkg.Name)
}

func TestGlobalRegistry(t *testing.T) {
	// Global should be pre-initialized
	assert.NotNil(t, Global)
	assert.True(t, Global.IsPreludePackage("std"))
}

func TestIsStdType(t *testing.T) {
	assert.True(t, IsStdType("Option"))
	assert.True(t, IsStdType("Immutable"))
	assert.True(t, IsStdType("Either"))
	assert.True(t, IsStdType("Try"))
	assert.True(t, IsStdType("Tuple"))
	assert.False(t, IsStdType("Unknown"))
}

func TestIsStdFunction(t *testing.T) {
	assert.True(t, IsStdFunction("NewImmutable"))
	assert.True(t, IsStdFunction("Some"))
	assert.True(t, IsStdFunction("None"))
	assert.False(t, IsStdFunction("Unknown"))
}

func TestIsStdCompanion(t *testing.T) {
	assert.True(t, IsStdCompanion("Some"))
	assert.True(t, IsStdCompanion("None"))
	assert.True(t, IsStdCompanion("Left"))
	assert.True(t, IsStdCompanion("Right"))
	assert.False(t, IsStdCompanion("Unknown"))
}

func TestCheckStdConflict(t *testing.T) {
	err := CheckStdConflict("Option", "main")
	assert.Error(t, err)

	err = CheckStdConflict("Option", "std")
	assert.NoError(t, err)

	err = CheckStdConflict("MyType", "main")
	assert.NoError(t, err)
}

func TestConflictError(t *testing.T) {
	err := &ConflictError{
		Name:        "Option",
		Kind:        "type",
		PackageName: "std",
	}
	assert.Equal(t, "type 'Option' conflicts with std library export; choose a different name", err.Error())
}

func TestStdPackageInfo(t *testing.T) {
	info := StdPackageInfo()

	assert.Equal(t, "std", info.Name)
	assert.Equal(t, "martianoff/gala/std", info.ImportPath)
	assert.True(t, info.IsPrelude)

	// Verify key types are present
	assert.Contains(t, info.Types, "Option")
	assert.Contains(t, info.Types, "Immutable")
	assert.Contains(t, info.Types, "Either")
	assert.Contains(t, info.Types, "Try")

	// Verify key functions are present
	assert.Contains(t, info.Functions, "NewImmutable")
	assert.Contains(t, info.Functions, "Some")
	assert.Contains(t, info.Functions, "None")

	// Verify companions are present
	assert.Contains(t, info.Companions, "Some")
	assert.Contains(t, info.Companions, "None")
	assert.Contains(t, info.Companions, "Left")
	assert.Contains(t, info.Companions, "Right")
}
