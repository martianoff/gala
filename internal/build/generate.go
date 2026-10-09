package build

import (
	"fmt"

	"martianoff/gala/internal/depman/mod"
)

// WorkspaceModule is the module path of a build workspace's generated go.mod.
const WorkspaceModule = "gala-build-workspace"

// WorkspaceGenModule is the module path the project's own packages are
// compiled under in a build workspace: every import of the project's module
// in gen/ is rewritten to it.
const WorkspaceGenModule = WorkspaceModule + "/gen"

// Generate transpiles the project, and the GALA dependencies it needs, into
// the build workspace the way Build does, then calls visit with the gen/
// directory while the workspace lock is held. It stops before go.mod
// generation and the compile check that a library build ends with: visit sees
// the project's generated and hand-written Go with project imports under
// WorkspaceGenModule and standard library imports as GALA writes them.
func (b *Builder) Generate(visit func(genDir string) error) error {
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
	// The workspace go.mod and a compile check, as a library build does, so
	// only a tree that builds is handed over.
	if err := b.generateGoMod(); err != nil {
		return fmt.Errorf("generating go.mod: %w", err)
	}
	if err := b.goCompileCheck(); err != nil {
		return fmt.Errorf("go build (compile check): %w", err)
	}
	return visit(b.workspace.GenDir)
}

// GoRequires returns the Go modules the project requires, with those its
// GALA dependencies require merged in at the highest version.
func (b *Builder) GoRequires() []mod.Require {
	return goRequiresWithDeps(b.galaMod, b.effectiveDepDir)
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
