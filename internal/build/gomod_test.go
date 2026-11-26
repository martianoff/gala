package build

import (
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/mod/modfile"
	"golang.org/x/mod/module"

	"martianoff/gala/internal/depman/mod"
	"martianoff/gala/internal/stdlib"
)

// TestWorkspaceGoModDeclaresMinGoVersion: the build workspace's go.mod is the
// main module of every program GALA builds, so its go line picks the
// program's GODEBUG defaults. It must be the oldest Go GALA supports, not an
// older release.
func TestWorkspaceGoModDeclaresMinGoVersion(t *testing.T) {
	gen := NewGoModGenerator(&Config{})
	content := gen.GenerateGoMod(&mod.File{Module: mod.Module{Path: "example.com/app"}}, "v0.0.0", nil)
	assert.Contains(t, content, "\ngo "+MinGoVersion+"\n")
}

// TestStdlibPackagesMatchEmbedded guards against drift between the stdlib
// embedding (cmd/stdlib_gen → internal/stdlib.PackageImportPaths) and the
// build-workspace generator (StdlibPackages / StdlibImportPaths here).
//
// External `gala build` consumers only see a package if it's registered in
// these gomod.go lists — adding a stdlib package elsewhere without updating
// them produces `package martianoff/gala/X is not in std` at consumer-build
// time. Keep both views in sync.
func TestStdlibPackagesMatchEmbedded(t *testing.T) {
	embeddedNames := make([]string, 0, len(stdlib.PackageImportPaths))
	for name := range stdlib.PackageImportPaths {
		embeddedNames = append(embeddedNames, name)
	}
	sort.Strings(embeddedNames)

	registeredNames := append([]string(nil), StdlibPackages...)
	sort.Strings(registeredNames)

	assert.Equal(t, embeddedNames, registeredNames,
		"StdlibPackages must match stdlib.PackageImportPaths keys; "+
			"add the missing package(s) to internal/build/gomod.go")

	for name, importPath := range stdlib.PackageImportPaths {
		assert.Equal(t, importPath, StdlibImportPaths[name],
			"StdlibImportPaths[%q] must match stdlib.PackageImportPaths[%q]",
			name, name)
	}
	for name := range StdlibImportPaths {
		_, ok := stdlib.PackageImportPaths[name]
		assert.True(t, ok,
			"StdlibImportPaths contains %q but stdlib.PackageImportPaths does not",
			name)
	}
}

// gala.mod's replace directives for Go (`// go`) modules reach the build
// workspace's go.mod, which is the main module of the build and so the only
// one whose replaces Go applies. A directory replacement is resolved against
// the project directory; a module replacement is written as it is.
func TestWorkspaceGoModAppliesGoModuleReplaces(t *testing.T) {
	projectDir := t.TempDir()
	galaMod := &mod.File{
		Module: mod.Module{Path: "example.com/app"},
		Require: []mod.Require{
			{Path: "example.com/greet", Version: "v0.1.0", Go: true},
			{Path: "github.com/google/uuid", Version: "v1.3.0", Go: true},
			{Path: "example.com/pinned", Version: "v1.0.0", Go: true},
		},
		Replace: []mod.Replace{
			{Old: mod.ModuleVersion{Path: "example.com/greet"}, New: mod.ModuleVersion{Path: "../greet"}},
			{Old: mod.ModuleVersion{Path: "github.com/google/uuid"}, New: mod.ModuleVersion{Path: "github.com/google/uuid", Version: "v1.6.0"}},
			{Old: mod.ModuleVersion{Path: "example.com/pinned", Version: "v1.0.0"}, New: mod.ModuleVersion{Path: "example.com/fork", Version: "v1.1.0"}},
			// The workspace replaces the standard library already; a second
			// replace of it would make go reject the go.mod.
			{Old: mod.ModuleVersion{Path: "martianoff/gala/std"}, New: mod.ModuleVersion{Path: "../gala/std"}},
		},
	}
	gen := NewGoModGenerator(&Config{StdlibDir: t.TempDir()})
	gen.SetProjectDir(projectDir)
	f, err := modfile.Parse("go.mod", []byte(gen.GenerateGoMod(galaMod, "v0.0.0", nil)), nil)
	require.NoError(t, err)

	got := map[string]module.Version{}
	for _, r := range f.Replace {
		got[r.Old.String()] = r.New
	}
	assert.Equal(t, module.Version{Path: filepath.ToSlash(filepath.Join(filepath.Dir(projectDir), "greet"))}, got["example.com/greet"])
	assert.Equal(t, module.Version{Path: "github.com/google/uuid", Version: "v1.6.0"}, got["github.com/google/uuid"])
	assert.Equal(t, module.Version{Path: "example.com/fork", Version: "v1.1.0"}, got["example.com/pinned@v1.0.0"])
}

// The workspace's go.mod changes when the go.mod of a Go module replaced by a
// directory does, so the build tidies the workspace again and picks up the
// replacement's new requirements.
func TestWorkspaceGoModTracksReplacementGoMod(t *testing.T) {
	projectDir := t.TempDir()
	greetMod := filepath.Join(projectDir, "greet", "go.mod")
	require.NoError(t, os.MkdirAll(filepath.Dir(greetMod), 0o755))
	galaMod := &mod.File{
		Module:  mod.Module{Path: "example.com/app"},
		Require: []mod.Require{{Path: "example.com/greet", Version: "v0.0.0", Go: true}},
		Replace: []mod.Replace{{Old: mod.ModuleVersion{Path: "example.com/greet"}, New: mod.ModuleVersion{Path: "./greet"}}},
	}
	gen := NewGoModGenerator(&Config{StdlibDir: t.TempDir()})
	gen.SetProjectDir(projectDir)
	generate := func(greetGoMod string) string {
		require.NoError(t, os.WriteFile(greetMod, []byte(greetGoMod), 0o644))
		return gen.GenerateGoMod(galaMod, "v0.0.0", nil)
	}

	before := generate("module example.com/greet\n\ngo 1.21\n")
	assert.Equal(t, before, generate("module example.com/greet\n\ngo 1.21\n"))
	assert.NotEqual(t, before, generate("module example.com/greet\n\ngo 1.21\n\nrequire github.com/google/uuid v1.6.0\n"))
}

// GoReplaces, which gala export checks, are the replaces the workspace go.mod
// applies to Go modules: every replace but those of the standard library and
// the GALA modules. One of a Go module gala.mod does not require applies too,
// as in Go.
func TestGoReplacesAreTheWorkspaceGoModReplaces(t *testing.T) {
	galaMod := &mod.File{
		Module: mod.Module{Path: "example.com/app"},
		Require: []mod.Require{
			{Path: "example.com/lib", Version: "v0.1.0"},
			{Path: "github.com/google/uuid", Version: "v1.3.0", Go: true},
		},
		Replace: []mod.Replace{
			{Old: mod.ModuleVersion{Path: "example.com/lib"}, New: mod.ModuleVersion{Path: "../lib"}},
			{Old: mod.ModuleVersion{Path: "github.com/google/uuid"}, New: mod.ModuleVersion{Path: "github.com/google/uuid", Version: "v1.6.0"}},
			{Old: mod.ModuleVersion{Path: "golang.org/x/text"}, New: mod.ModuleVersion{Path: "golang.org/x/text", Version: "v0.3.0"}},
			{Old: mod.ModuleVersion{Path: "martianoff/gala/std"}, New: mod.ModuleVersion{Path: "../gala/std"}},
		},
	}
	gen := NewGoModGenerator(&Config{StdlibDir: t.TempDir(), GalaPkgDir: t.TempDir()})
	gen.SetProjectDir(t.TempDir())

	var got []string
	for _, r := range gen.GoReplaces(galaMod) {
		got = append(got, r.Old.Path)
	}
	assert.Equal(t, []string{"github.com/google/uuid", "golang.org/x/text"}, got)

	f, err := modfile.Parse("go.mod", []byte(gen.GenerateGoMod(galaMod, "v0.0.0", nil)), nil)
	require.NoError(t, err)
	var written []string
	for _, r := range f.Replace {
		if r.Old.Path == "github.com/google/uuid" || r.Old.Path == "golang.org/x/text" {
			written = append(written, r.Old.Path)
		}
	}
	assert.Equal(t, got, written)
}

// The project's replace directives apply to every module of the build, not
// only to the ones it requires directly: a GALA dependency's own GALA
// dependency is read from the directory the project replaces it with, and
// the Go modules that one requires reach the workspace's go.mod.
func TestWorkspaceGoModReadsTransitiveDepFromProjectReplace(t *testing.T) {
	root := t.TempDir()
	write := func(rel, content string) {
		path := filepath.Join(root, rel)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
	}
	write("liba/gala.mod", "module example.com/liba\n\nrequire example.com/libb v0.1.0\n")
	write("libb/gala.mod", "module example.com/libb\n\nrequire example.com/gomod v1.2.0 // go\n")
	galaMod := &mod.File{
		Module:  mod.Module{Path: "example.com/app"},
		Require: []mod.Require{{Path: "example.com/liba", Version: "v0.1.0"}},
		Replace: []mod.Replace{
			{Old: mod.ModuleVersion{Path: "example.com/liba"}, New: mod.ModuleVersion{Path: "../liba"}},
			{Old: mod.ModuleVersion{Path: "example.com/libb"}, New: mod.ModuleVersion{Path: "../libb"}},
		},
	}
	gen := NewGoModGenerator(&Config{StdlibDir: t.TempDir(), GalaPkgDir: t.TempDir()})
	gen.SetProjectDir(filepath.Join(root, "app"))
	f, err := modfile.Parse("go.mod", []byte(gen.GenerateGoMod(galaMod, "v0.0.0", nil)), nil)
	require.NoError(t, err)

	var reqs []string
	for _, r := range f.Require {
		reqs = append(reqs, r.Mod.String())
	}
	assert.Contains(t, reqs, "example.com/gomod@v1.2.0")
}
