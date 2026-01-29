package mod

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParse_SimpleModule(t *testing.T) {
	content := `module github.com/user/project

gala 1.0
`
	f, err := Parse(content)
	require.NoError(t, err)
	assert.Equal(t, "github.com/user/project", f.Module.Path)
	assert.Equal(t, "1.0", f.Gala)
}

func TestParse_WithRequires(t *testing.T) {
	content := `module github.com/user/project

gala 1.0

require (
	github.com/example/utils v1.2.3
	github.com/example/math v2.0.0
)
`
	f, err := Parse(content)
	require.NoError(t, err)
	assert.Len(t, f.Require, 2)
	assert.Equal(t, "github.com/example/utils", f.Require[0].Path)
	assert.Equal(t, "v1.2.3", f.Require[0].Version)
	assert.False(t, f.Require[0].Indirect)
	assert.Equal(t, "github.com/example/math", f.Require[1].Path)
	assert.Equal(t, "v2.0.0", f.Require[1].Version)
}

func TestParse_WithIndirectRequires(t *testing.T) {
	content := `module github.com/user/project

require (
	github.com/example/direct v1.0.0
)

require (
	github.com/example/indirect v1.0.0 // indirect
)
`
	f, err := Parse(content)
	require.NoError(t, err)
	assert.Len(t, f.Require, 2)

	direct := f.DirectRequires()
	assert.Len(t, direct, 1)
	assert.Equal(t, "github.com/example/direct", direct[0].Path)

	indirect := f.IndirectRequires()
	assert.Len(t, indirect, 1)
	assert.Equal(t, "github.com/example/indirect", indirect[0].Path)
}

func TestParse_SingleLineRequire(t *testing.T) {
	content := `module github.com/user/project
require github.com/example/utils v1.2.3
`
	f, err := Parse(content)
	require.NoError(t, err)
	assert.Len(t, f.Require, 1)
	assert.Equal(t, "github.com/example/utils", f.Require[0].Path)
	assert.Equal(t, "v1.2.3", f.Require[0].Version)
}

func TestParse_WithReplace(t *testing.T) {
	content := `module github.com/user/project

replace github.com/example/utils => ../local-utils
`
	f, err := Parse(content)
	require.NoError(t, err)
	assert.Len(t, f.Replace, 1)
	assert.Equal(t, "github.com/example/utils", f.Replace[0].Old.Path)
	assert.Equal(t, "", f.Replace[0].Old.Version)
	assert.Equal(t, "../local-utils", f.Replace[0].New.Path)
}

func TestParse_WithVersionedReplace(t *testing.T) {
	content := `module github.com/user/project

replace github.com/example/utils v1.0.0 => github.com/fork/utils v1.1.0
`
	f, err := Parse(content)
	require.NoError(t, err)
	assert.Len(t, f.Replace, 1)
	assert.Equal(t, "github.com/example/utils", f.Replace[0].Old.Path)
	assert.Equal(t, "v1.0.0", f.Replace[0].Old.Version)
	assert.Equal(t, "github.com/fork/utils", f.Replace[0].New.Path)
	assert.Equal(t, "v1.1.0", f.Replace[0].New.Version)
}

func TestParse_WithExclude(t *testing.T) {
	content := `module github.com/user/project

exclude github.com/example/deprecated v0.9.0
`
	f, err := Parse(content)
	require.NoError(t, err)
	assert.Len(t, f.Exclude, 1)
	assert.Equal(t, "github.com/example/deprecated", f.Exclude[0].Path)
	assert.Equal(t, "v0.9.0", f.Exclude[0].Version)
}

func TestParse_CompleteFile(t *testing.T) {
	content := `module github.com/user/myproject

gala 1.0

require (
	github.com/example/utils v1.2.3
	github.com/example/math v2.0.0
)

require (
	github.com/example/indirect v1.0.0 // indirect
)

replace github.com/example/utils => ../local-utils

exclude github.com/example/deprecated v0.9.0
`
	f, err := Parse(content)
	require.NoError(t, err)

	assert.Equal(t, "github.com/user/myproject", f.Module.Path)
	assert.Equal(t, "1.0", f.Gala)
	assert.Len(t, f.Require, 3)
	assert.Len(t, f.Replace, 1)
	assert.Len(t, f.Exclude, 1)
}

func TestParse_Error_UnknownDirective(t *testing.T) {
	content := `module github.com/user/project
unknown directive
`
	_, err := Parse(content)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unknown directive")
}

func TestParse_Error_MissingModulePath(t *testing.T) {
	content := `module
`
	_, err := Parse(content)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "requires a path")
}

func TestFormat_SimpleModule(t *testing.T) {
	f := NewFile("github.com/user/project")
	f.Gala = "1.0"

	output := Format(f)
	expected := `module github.com/user/project

gala 1.0
`
	assert.Equal(t, expected, output)
}

func TestFormat_WithRequires(t *testing.T) {
	f := NewFile("github.com/user/project")
	f.AddRequire("github.com/example/utils", "v1.2.3", false)
	f.AddRequire("github.com/example/math", "v2.0.0", false)

	output := Format(f)
	assert.Contains(t, output, "require (")
	assert.Contains(t, output, "github.com/example/utils v1.2.3")
	assert.Contains(t, output, "github.com/example/math v2.0.0")
}

func TestFormat_SingleRequire(t *testing.T) {
	f := NewFile("github.com/user/project")
	f.AddRequire("github.com/example/utils", "v1.2.3", false)

	output := Format(f)
	assert.Contains(t, output, "require github.com/example/utils v1.2.3")
	assert.NotContains(t, output, "require (")
}

func TestFormat_WithIndirectRequires(t *testing.T) {
	f := NewFile("github.com/user/project")
	f.AddRequire("github.com/example/direct", "v1.0.0", false)
	f.AddRequire("github.com/example/indirect", "v1.0.0", true)

	output := Format(f)
	assert.Contains(t, output, "github.com/example/direct v1.0.0")
	assert.Contains(t, output, "github.com/example/indirect v1.0.0 // indirect")
}

func TestFormat_WithReplace(t *testing.T) {
	f := NewFile("github.com/user/project")
	f.AddReplace("github.com/example/utils", "", "../local-utils", "")

	output := Format(f)
	assert.Contains(t, output, "replace github.com/example/utils => ../local-utils")
}

func TestFormat_WithExclude(t *testing.T) {
	f := NewFile("github.com/user/project")
	f.AddExclude("github.com/example/deprecated", "v0.9.0")

	output := Format(f)
	assert.Contains(t, output, "exclude github.com/example/deprecated v0.9.0")
}

func TestRoundTrip(t *testing.T) {
	original := NewFile("github.com/user/project")
	original.Gala = "1.0"
	original.AddRequire("github.com/example/utils", "v1.2.3", false)
	original.AddRequire("github.com/example/indirect", "v1.0.0", true)
	original.AddReplace("github.com/example/utils", "", "../local-utils", "")
	original.AddExclude("github.com/example/deprecated", "v0.9.0")

	formatted := Format(original)
	parsed, err := Parse(formatted)
	require.NoError(t, err)

	assert.Equal(t, original.Module.Path, parsed.Module.Path)
	assert.Equal(t, original.Gala, parsed.Gala)
	assert.Len(t, parsed.Require, 2)
	assert.Len(t, parsed.Replace, 1)
	assert.Len(t, parsed.Exclude, 1)
}

func TestFile_AddRequire_Update(t *testing.T) {
	f := NewFile("github.com/user/project")
	f.AddRequire("github.com/example/utils", "v1.0.0", false)
	f.AddRequire("github.com/example/utils", "v2.0.0", false)

	assert.Len(t, f.Require, 1)
	assert.Equal(t, "v2.0.0", f.Require[0].Version)
}

func TestFile_RemoveRequire(t *testing.T) {
	f := NewFile("github.com/user/project")
	f.AddRequire("github.com/example/utils", "v1.0.0", false)
	f.AddRequire("github.com/example/math", "v2.0.0", false)

	removed := f.RemoveRequire("github.com/example/utils")
	assert.True(t, removed)
	assert.Len(t, f.Require, 1)
	assert.Equal(t, "github.com/example/math", f.Require[0].Path)

	removed = f.RemoveRequire("github.com/example/nonexistent")
	assert.False(t, removed)
}

func TestFile_GetRequire(t *testing.T) {
	f := NewFile("github.com/user/project")
	f.AddRequire("github.com/example/utils", "v1.0.0", false)

	r := f.GetRequire("github.com/example/utils")
	assert.NotNil(t, r)
	assert.Equal(t, "v1.0.0", r.Version)

	r = f.GetRequire("github.com/example/nonexistent")
	assert.Nil(t, r)
}

func TestModuleVersion_IsLocal(t *testing.T) {
	tests := []struct {
		path     string
		version  string
		expected bool
	}{
		{"../local-utils", "", true},
		{"./local-utils", "", true},
		{"/absolute/path", "", true},
		{"github.com/example/utils", "", false},
		{"github.com/example/utils", "v1.0.0", false},
	}

	for _, tt := range tests {
		mv := ModuleVersion{Path: tt.path, Version: tt.version}
		assert.Equal(t, tt.expected, mv.IsLocal(), "path=%q version=%q", tt.path, tt.version)
	}
}

func TestParse_WithGoRequires(t *testing.T) {
	content := `module github.com/user/project

require (
	github.com/example/gala-pkg v1.0.0
	github.com/example/go-lib v2.0.0 // go
)
`
	f, err := Parse(content)
	require.NoError(t, err)
	assert.Len(t, f.Require, 2)

	// First is GALA package
	assert.Equal(t, "github.com/example/gala-pkg", f.Require[0].Path)
	assert.False(t, f.Require[0].Go)

	// Second is Go package
	assert.Equal(t, "github.com/example/go-lib", f.Require[1].Path)
	assert.True(t, f.Require[1].Go)
}

func TestParse_WithGoAndIndirectRequires(t *testing.T) {
	content := `module github.com/user/project

require (
	github.com/example/gala-pkg v1.0.0
	github.com/example/go-lib v2.0.0 // go
	github.com/example/indirect-go v1.0.0 // indirect, go
)
`
	f, err := Parse(content)
	require.NoError(t, err)
	assert.Len(t, f.Require, 3)

	// GALA package
	assert.Equal(t, "github.com/example/gala-pkg", f.Require[0].Path)
	assert.False(t, f.Require[0].Go)
	assert.False(t, f.Require[0].Indirect)

	// Go package (direct)
	assert.Equal(t, "github.com/example/go-lib", f.Require[1].Path)
	assert.True(t, f.Require[1].Go)
	assert.False(t, f.Require[1].Indirect)

	// Go package (indirect)
	assert.Equal(t, "github.com/example/indirect-go", f.Require[2].Path)
	assert.True(t, f.Require[2].Go)
	assert.True(t, f.Require[2].Indirect)
}

func TestFormat_WithGoRequires(t *testing.T) {
	f := NewFile("github.com/user/project")
	f.AddRequire("github.com/example/gala-pkg", "v1.0.0", false)

	// Add Go dependency by modifying the require entry
	f.AddRequire("github.com/example/go-lib", "v2.0.0", false)
	if req := f.GetRequire("github.com/example/go-lib"); req != nil {
		req.Go = true
	}

	output := Format(f)
	assert.Contains(t, output, "github.com/example/gala-pkg v1.0.0")
	assert.Contains(t, output, "github.com/example/go-lib v2.0.0 // go")
}

func TestFormat_WithGoAndIndirectRequires(t *testing.T) {
	f := NewFile("github.com/user/project")

	// Add indirect Go dependency
	f.AddRequire("github.com/example/indirect-go", "v1.0.0", true)
	if req := f.GetRequire("github.com/example/indirect-go"); req != nil {
		req.Go = true
	}

	output := Format(f)
	assert.Contains(t, output, "github.com/example/indirect-go v1.0.0 // indirect, go")
}

func TestFile_GalaRequires(t *testing.T) {
	f := NewFile("github.com/user/project")
	f.AddRequire("github.com/example/gala-pkg", "v1.0.0", false)
	f.AddRequire("github.com/example/go-lib", "v2.0.0", false)
	if req := f.GetRequire("github.com/example/go-lib"); req != nil {
		req.Go = true
	}

	galaReqs := f.GalaRequires()
	assert.Len(t, galaReqs, 1)
	assert.Equal(t, "github.com/example/gala-pkg", galaReqs[0].Path)
}

func TestFile_GoRequires(t *testing.T) {
	f := NewFile("github.com/user/project")
	f.AddRequire("github.com/example/gala-pkg", "v1.0.0", false)
	f.AddRequire("github.com/example/go-lib", "v2.0.0", false)
	if req := f.GetRequire("github.com/example/go-lib"); req != nil {
		req.Go = true
	}

	goReqs := f.GoRequires()
	assert.Len(t, goReqs, 1)
	assert.Equal(t, "github.com/example/go-lib", goReqs[0].Path)
}

func TestRoundTrip_WithGoRequires(t *testing.T) {
	original := NewFile("github.com/user/project")
	original.Gala = "1.0"
	original.AddRequire("github.com/example/gala-pkg", "v1.0.0", false)
	original.AddRequire("github.com/example/go-lib", "v2.0.0", false)
	if req := original.GetRequire("github.com/example/go-lib"); req != nil {
		req.Go = true
	}
	original.AddRequire("github.com/example/indirect-go", "v1.0.0", true)
	if req := original.GetRequire("github.com/example/indirect-go"); req != nil {
		req.Go = true
	}

	formatted := Format(original)
	parsed, err := Parse(formatted)
	require.NoError(t, err)

	assert.Len(t, parsed.Require, 3)

	// Check GALA package
	galaReq := parsed.GetRequire("github.com/example/gala-pkg")
	require.NotNil(t, galaReq)
	assert.False(t, galaReq.Go)
	assert.False(t, galaReq.Indirect)

	// Check direct Go package
	goReq := parsed.GetRequire("github.com/example/go-lib")
	require.NotNil(t, goReq)
	assert.True(t, goReq.Go)
	assert.False(t, goReq.Indirect)

	// Check indirect Go package
	indirectGoReq := parsed.GetRequire("github.com/example/indirect-go")
	require.NotNil(t, indirectGoReq)
	assert.True(t, indirectGoReq.Go)
	assert.True(t, indirectGoReq.Indirect)
}
