package build

import (
	"fmt"
	"os"
	"slices"

	"golang.org/x/mod/modfile"

	"martianoff/gala/internal/depman/mod"
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
	// ReplacedGoModules are the Go modules of the build that gala.mod
	// replaces, each once: the workspace's go.mod applies those replaces,
	// which do not reach a program importing the code. A replace of a module
	// the build does not need is not listed.
	ReplacedGoModules []string
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
	f, err := modfile.Parse("go.mod", data, nil)
	if err != nil {
		return fmt.Errorf("reading the workspace go.mod: %w", err)
	}
	return visit(Generated{GenDir: b.workspace.GenDir, GoRequires: resolvedGoRequires(f), ReplacedGoModules: b.replacedGoModules(f)})
}

// replacedGoModules returns the modules a tidied workspace go.mod requires
// that gala.mod replaces as Go modules (GoModGenerator.GoReplaces) at the
// version required, as Go applies them, each once.
func (b *Builder) replacedGoModules(workspaceGoMod *modfile.File) []string {
	gen := NewGoModGenerator(b.config)
	gen.SetProjectDir(b.workspace.ProjectDir)
	goReplaces := &mod.File{Replace: gen.GoReplaces(b.galaMod)}
	var replaced []string
	for _, r := range workspaceGoMod.Require {
		if _, ok := goReplaces.ReplaceFor(r.Mod.Path, r.Mod.Version); ok && !slices.Contains(replaced, r.Mod.Path) {
			replaced = append(replaced, r.Mod.Path)
		}
	}
	return replaced
}

// resolvedGoRequires returns a tidied workspace go.mod's requirements,
// leaving out modules replaced by a local directory.
func resolvedGoRequires(f *modfile.File) []GoRequire {
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
	return reqs
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

// ModulePath returns the project's module path: go.mod's when the project has
// one, otherwise gala.mod's.
func (b *Builder) ModulePath() string {
	return b.projectGoModulePath()
}
