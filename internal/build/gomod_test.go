package build

import (
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"

	"martianoff/gala/internal/depman/mod"
	"martianoff/gala/internal/stdlib"
	"martianoff/gala/internal/transpiler/analyzer"
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

// TestAnalyzerGeneratedGoVersionMatches: the analyzer picks the Go SDK it
// reads types from by the go line GALA's go.mod files declare (a go on PATH
// older than it switches toolchain), and keeps its own copy of that version.
func TestAnalyzerGeneratedGoVersionMatches(t *testing.T) {
	assert.Equal(t, stdlib.GoVersion, analyzer.GeneratedGoVersion)
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
