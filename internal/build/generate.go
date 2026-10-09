package build

import (
	"fmt"
	"os"

	"golang.org/x/mod/modfile"
)

// WorkspaceModule is the module path of a build workspace's generated go.mod.
const WorkspaceModule = "gala-build-workspace"

// WorkspaceGenModule is the module path the project's own packages are
// compiled under in a build workspace: every import of the project's module
// in gen/ is rewritten to it.
const WorkspaceGenModule = WorkspaceModule + "/gen"

// Generated is what Generate hands its visitor.
type Generated struct {
	// GenDir holds the project's generated and hand-written Go, with project
	// imports under WorkspaceGenModule and standard library imports as GALA
	// writes them.
	GenDir string
	// GoRequires are the Go modules that code needs, as `go mod tidy`
	// resolved them for the workspace: direct and indirect requirements
	// alike, leaving out the modules the workspace serves from local
	// directories (the standard library and GALA dependencies).
	GoRequires []GoRequire
}

// GoRequire is one Go module requirement.
type GoRequire struct {
	Path, Version string
	Indirect      bool
}

// Generate transpiles the project, and the GALA dependencies it needs, into
// the build workspace the way Build does, writes the workspace go.mod and
// compile-checks the result, as a library build does, then calls visit while
// the workspace lock is held.
func (b *Builder) Generate(visit func(Generated) error) error {
	if err := ensureGoToolchain(); err != nil {
		return err
	}
	release, err := b.prepare(nil)
	if err != nil {
		return err
	}
	defer release()
	if err := b.transpile(); err != nil {
		return fmt.Errorf("transpiling: %w", err)
	}
	if err := b.generateGoMod(); err != nil {
		return fmt.Errorf("generating go.mod: %w", err)
	}
	if err := b.goCompileCheck(); err != nil {
		return fmt.Errorf("go build (compile check): %w", err)
	}
	data, err := os.ReadFile(b.workspace.GoModPath)
	if err != nil {
		return err
	}
	reqs, err := resolvedGoRequires(data)
	if err != nil {
		return fmt.Errorf("reading the workspace go.mod: %w", err)
	}
	return visit(Generated{GenDir: b.workspace.GenDir, GoRequires: reqs})
}

// resolvedGoRequires returns a tidied workspace go.mod's requirements,
// leaving out modules replaced by a local directory.
func resolvedGoRequires(goMod []byte) ([]GoRequire, error) {
	f, err := modfile.Parse("go.mod", goMod, nil)
	if err != nil {
		return nil, err
	}
	local := map[string]bool{}
	for _, r := range f.Replace {
		if r.New.Version == "" { // a directory replacement
			local[r.Old.Path] = true
		}
	}
	var reqs []GoRequire
	for _, r := range f.Require {
		if !local[r.Mod.Path] {
			reqs = append(reqs, GoRequire{Path: r.Mod.Path, Version: r.Mod.Version, Indirect: r.Indirect})
		}
	}
	return reqs, nil
}

// GalaImportPaths returns the module paths the project's GALA dependencies
// can be imported by: each requirement's path and the module path its own
// gala.mod declares, which generated code uses.
func (b *Builder) GalaImportPaths() []string {
	var paths []string
	for _, r := range b.galaMod.GalaRequires() {
		paths = append(paths, r.Path)
		if internal := resolveDepInternalModulePathAt(b.effectiveDepDir(r), r); internal != r.Path {
			paths = append(paths, internal)
		}
	}
	return paths
}

// ReplacedGoModules returns the Go modules gala.mod requires and also
// replaces.
func (b *Builder) ReplacedGoModules() []string {
	goReqs := map[string]bool{}
	for _, r := range b.galaMod.GoRequires() {
		goReqs[r.Path] = true
	}
	var replaced []string
	for _, r := range b.galaMod.Replace {
		if goReqs[r.Old.Path] {
			replaced = append(replaced, r.Old.Path)
		}
	}
	return replaced
}

// ModulePath returns the project's module path: go.mod's when the project has
// one, otherwise gala.mod's.
func (b *Builder) ModulePath() string {
	return b.projectGoModulePath()
}
