package build

import (
	"fmt"
	"os"
	"path/filepath"

	"martianoff/gala/internal/depman/mod"
)

// WorkspaceGenModule is the module path the project's own packages are
// compiled under in a build workspace: every import of the project's module
// in gen/ is rewritten to it.
const WorkspaceGenModule = "gala-build-workspace/gen"

// Generate transpiles the project, and the GALA dependencies it needs, into
// the build workspace the way Build does, then calls visit with the gen/
// directory while the workspace lock is held. It stops before go.mod
// generation and the Go build: visit sees the project's generated and
// hand-written Go with project imports under WorkspaceGenModule and standard
// library imports as GALA writes them.
func (b *Builder) Generate(visit func(genDir string) error) error {
	if err := ensureGoToolchain(); err != nil {
		return err
	}
	if err := b.workspace.Ensure(); err != nil {
		return fmt.Errorf("ensuring workspace: %w", err)
	}
	lock, err := b.workspace.Lock(lockTimeout(workspaceLockTimeout))
	if err != nil {
		return err
	}
	defer lock.Release()

	versionFile := filepath.Join(b.workspace.Dir, ".gala-version")
	if old, err := os.ReadFile(versionFile); err != nil || string(old) != b.stdlibVersion {
		if err := b.invalidateWorkspace(versionFile); err != nil {
			return err
		}
	}

	steps := []struct {
		what string
		run  func() error
	}{
		{"ensuring stdlib", b.ensureStdlib},
		{"fetching dependencies", b.ensureDeps},
		{"transpiling dependencies", b.transpileDeps},
		{"transpiling", b.transpile},
	}
	for _, s := range steps {
		if err := s.run(); err != nil {
			return fmt.Errorf("%s: %w", s.what, err)
		}
	}
	return visit(b.workspace.GenDir)
}

// GalaMod returns the project's parsed gala.mod.
func (b *Builder) GalaMod() *mod.File {
	return b.galaMod
}

// ModulePath returns the project's module path: go.mod's when the project has
// one, otherwise gala.mod's.
func (b *Builder) ModulePath() string {
	return b.projectGoModulePath()
}
