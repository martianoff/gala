package build

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/mod/modfile"

	"martianoff/gala/internal/depman/mod"
	"martianoff/gala/internal/stdlib"
)

// GoModGenerator generates go.mod files for build workspaces.
type GoModGenerator struct {
	config *Config
	// projectDir is the directory containing the user's gala.mod; it is used
	// to resolve relative paths in local replace directives. A zero value
	// disables local-replace resolution (the generator falls back to the
	// module cache path).
	projectDir string
}

// NewGoModGenerator creates a new go.mod generator.
func NewGoModGenerator(config *Config) *GoModGenerator {
	return &GoModGenerator{config: config}
}

// SetProjectDir configures the project directory used to resolve relative
// paths in replace directives. Must be called before GenerateGoMod if the
// project's gala.mod uses local replaces.
func (g *GoModGenerator) SetProjectDir(dir string) {
	g.projectDir = dir
}

// effectiveDepDir returns the on-disk source directory for a GALA dep,
// honoring replace directives in the active gala.mod. Callers that don't
// have a project directory (empty projectDir) always see the cached
// module path.
func (g *GoModGenerator) effectiveDepDir(galaMod *mod.File, req mod.Require) string {
	return resolveEffectiveDepDir(g.config, galaMod, g.projectDir, req)
}

// resolveEffectiveDepDir is the single source of truth for translating a
// GALA require into a source directory. It first checks the active gala.mod
// for a matching replace directive — local replacements resolve relative
// to projectDir, while module replacements redirect to the replacement's
// cached path — and falls back to the normal cache location for unmatched
// requires.
func resolveEffectiveDepDir(config *Config, galaMod *mod.File, projectDir string, req mod.Require) string {
	if rep, ok := galaMod.ReplaceFor(req.Path, req.Version); ok {
		if rep.New.IsLocal() {
			return replacementDir(projectDir, rep.New.Path)
		}
		return config.GalaModulePath(rep.New.Path, rep.New.Version)
	}
	return config.GalaModulePath(req.Path, req.Version)
}

// goReplacements returns the goReplacementFunc that applies galaMod's replace
// directives to a required Go module, as the build workspace's go.mod does
// (GoModGenerator.goReplaces). A replacement without a version is a directory,
// as Go reads it, resolved against projectDir.
func goReplacements(galaMod *mod.File, projectDir string) goReplacementFunc {
	return func(req mod.Require) (mod.Require, string) {
		rep, ok := galaMod.ReplaceFor(req.Path, req.Version)
		switch {
		case !ok:
			return req, ""
		case rep.New.IsLocal():
			return req, replacementDir(projectDir, rep.New.Path)
		default:
			req.Path, req.Version = rep.New.Path, rep.New.Version
			return req, ""
		}
	}
}

// replacementDir resolves the directory of a directory replacement: a relative
// one against projectDir, the directory of the gala.mod that declares it.
func replacementDir(projectDir, path string) string {
	if !filepath.IsAbs(path) && projectDir != "" {
		path = filepath.Join(projectDir, path)
	}
	return filepath.Clean(path)
}

// GalaModulePrefix starts the import path of every package of the GALA
// module, the stdlib's among them. A build workspace requires the ones it
// needs itself, so a Go requirement under this prefix is left out.
const GalaModulePrefix = "martianoff/gala/"

// StdlibPackages lists all GALA stdlib packages.
var StdlibPackages = []string{
	"std",
	"go_interop",
	"go_builtins",
	"resource",
	"collection_immutable",
	"collection_mutable",
	"concurrent",
	"validation",
	"lazy",
	"stream",
	"strings",
	"time_utils",
	"regex",
	"io",
	"json",
	"path",
	"fs",
	"subprocess",
	"yaml",
	"test",
	"crypto",
}

// StdlibImportPaths maps package names to their import paths.
var StdlibImportPaths = map[string]string{
	"std":                  "martianoff/gala/std",
	"go_interop":           "martianoff/gala/go_interop",
	"go_builtins":          "martianoff/gala/go_builtins",
	"resource":             "martianoff/gala/resource",
	"collection_immutable": "martianoff/gala/collection_immutable",
	"collection_mutable":   "martianoff/gala/collection_mutable",
	"concurrent":           "martianoff/gala/concurrent",
	"validation":           "martianoff/gala/validation",
	"lazy":                 "martianoff/gala/lazy",
	"stream":               "martianoff/gala/stream",
	"strings":              "martianoff/gala/strings",
	"time_utils":           "martianoff/gala/time_utils",
	"regex":                "martianoff/gala/regex",
	"io":                   "martianoff/gala/io",
	"json":                 "martianoff/gala/json",
	"path":                 "martianoff/gala/path",
	"fs":                   "martianoff/gala/fs",
	"subprocess":           "martianoff/gala/subprocess",
	"yaml":                 "martianoff/gala/yaml",
	"test":                 "martianoff/gala/test",
	"crypto":               "martianoff/gala/crypto",
}

// GenerateGoMod generates a go.mod file content for the workspace.
// It uses absolute paths to the global caches, avoiding symlinks.
// transpiledDeps maps internalModulePath -> transpiled directory for GALA deps.
func (g *GoModGenerator) GenerateGoMod(galaMod *mod.File, stdlibVersion string, transpiledDeps map[string]string) string {
	var sb strings.Builder

	// Header
	sb.WriteString("// Code generated by GALA build system. DO NOT EDIT.\n")
	sb.WriteString("module " + WorkspaceModule + "\n\n")
	sb.WriteString("go " + stdlib.GoVersion + "\n\n")

	// Collect all requires
	var stdlibReqs []string

	// Add stdlib requires
	for _, pkg := range StdlibPackages {
		importPath := StdlibImportPaths[pkg]
		stdlibReqs = append(stdlibReqs, importPath)
	}

	galaReqs, goReqs := g.workspaceRequires(galaMod)

	// Build mapping from require path (GitHub URL) -> internal module path
	// by reading each dep's gala.mod. The transpiled Go code uses the internal
	// module path for imports, so the go.mod must use it too.
	internalModulePaths := g.resolveInternalModulePaths(galaMod, galaReqs)

	// Write require block
	sb.WriteString("require (\n")

	// Stdlib requires
	for _, importPath := range stdlibReqs {
		sb.WriteString(fmt.Sprintf("\t%s v0.0.0\n", importPath))
	}

	// GALA package requires — use internal module path
	for _, req := range galaReqs {
		modPath := g.effectiveModulePath(req.Path, internalModulePaths)
		sb.WriteString(fmt.Sprintf("\t%s v0.0.0\n", modPath))
	}

	// Go package requires (including transitive)
	for _, req := range goReqs {
		sb.WriteString(fmt.Sprintf("\t%s %s\n", req.Path, req.Version))
	}

	sb.WriteString(")\n\n")

	// Write replace directives with absolute paths
	sb.WriteString("// Replace directives using absolute paths to global cache\n")

	// Stdlib replaces
	stdlibDir := g.config.StdlibVersionDir(stdlibVersion)
	for _, pkg := range StdlibPackages {
		importPath := StdlibImportPaths[pkg]
		absPath := filepath.Join(stdlibDir, pkg)
		// Convert to forward slashes for go.mod compatibility
		absPath = filepath.ToSlash(absPath)
		sb.WriteString(fmt.Sprintf("replace %s => %s\n", importPath, absPath))
	}

	sb.WriteString("\n")

	// GALA package replaces — use internal module path
	for _, req := range galaReqs {
		modPath := g.effectiveModulePath(req.Path, internalModulePaths)
		if transpiledDeps != nil {
			if dir, ok := transpiledDeps[modPath]; ok {
				// Use transpiled directory in workspace
				absPath := filepath.ToSlash(dir)
				sb.WriteString(fmt.Sprintf("replace %s => %s\n", modPath, absPath))
				continue
			}
		}
		// Fallback to source cache (pure Go package or no transpilation needed).
		// effectiveDepDir consults replace directives so a local replace still
		// wins even when the dep is not transpiled (e.g. pure-Go library).
		absPath := filepath.ToSlash(g.effectiveDepDir(galaMod, req))
		sb.WriteString(fmt.Sprintf("replace %s => %s\n", modPath, absPath))
	}

	// Go modules are fetched into GOMODCACHE, except where gala.mod replaces
	// one. The workspace is the main module of the build, the only one whose
	// replace directives Go applies, so gala.mod's are written here.
	for _, rep := range goReplacesOf(galaMod, g.galaManagedModules(galaReqs, internalModulePaths)) {
		if rep.New.IsLocal() {
			dir := replacementDir(g.projectDir, rep.New.Path)
			rep.New.Path = filepath.ToSlash(dir)
			// The workspace's go.mod does not change when the replacement's
			// go.mod does, yet its requirements are the build's: a comment
			// with its hash makes the build tidy the workspace again.
			sb.WriteString(fmt.Sprintf("// %s go.mod %s\n", rep.Old.Path, fileFingerprint(filepath.Join(dir, "go.mod"))))
		}
		// Quoted where go.mod needs it (a project directory with a space).
		rep.Old.Path, rep.New.Path = modfile.AutoQuote(rep.Old.Path), modfile.AutoQuote(rep.New.Path)
		sb.WriteString("replace " + mod.FormatReplaceEntry(rep) + "\n")
	}

	return sb.String()
}

// goReplacesOf returns the replace directives of galaMod for Go modules: every
// one but those for a module of managed, which the build workspace replaces
// itself (the standard library packages and the GALA modules, pointed at their
// extracted or transpiled source).
func goReplacesOf(galaMod *mod.File, managed map[string]bool) []mod.Replace {
	if galaMod == nil {
		return nil
	}
	var reps []mod.Replace
	for _, rep := range galaMod.Replace {
		if !managed[rep.Old.Path] {
			reps = append(reps, rep)
		}
	}
	return reps
}

// galaManagedModules returns the modules the build workspace's go.mod
// replaces itself: the standard library packages, and the GALA modules
// galaReqs, by require path and by internal module path.
func (g *GoModGenerator) galaManagedModules(galaReqs []mod.Require, internalModulePaths map[string]string) map[string]bool {
	managed := make(map[string]bool, len(StdlibPackages)+2*len(galaReqs))
	for _, pkg := range StdlibPackages {
		managed[StdlibImportPaths[pkg]] = true
	}
	for _, req := range galaReqs {
		managed[req.Path] = true
		managed[g.effectiveModulePath(req.Path, internalModulePaths)] = true
	}
	return managed
}

// GoReplaces returns the replace directives of galaMod that the build
// workspace's go.mod applies to Go modules (see GenerateGoMod).
func (g *GoModGenerator) GoReplaces(galaMod *mod.File) []mod.Replace {
	galaReqs, _ := g.workspaceRequires(galaMod)
	return goReplacesOf(galaMod, g.galaManagedModules(galaReqs, g.resolveInternalModulePaths(galaMod, galaReqs)))
}

// fileFingerprint returns a short hash of the file at path, or "none" when it
// cannot be read.
func fileFingerprint(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return "none"
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:8])
}

// workspaceRequires returns the GALA and the Go modules the build workspace
// requires: galaMod's, and those of its GALA dependencies, transitively.
func (g *GoModGenerator) workspaceRequires(galaMod *mod.File) (galaReqs, goReqs []mod.Require) {
	// Separate GALA and Go dependencies
	if galaMod != nil {
		for _, req := range galaMod.Require {
			if req.Go {
				goReqs = append(goReqs, req)
			} else {
				galaReqs = append(galaReqs, req)
			}
		}
	}

	// Collect transitive dependencies from GALA packages
	transitiveGoDeps, transitiveGalaDeps := g.collectTransitiveDeps(galaMod, galaReqs)
	for path, version := range transitiveGoDeps {
		// Don't duplicate if already in goReqs
		found := false
		for _, req := range goReqs {
			if req.Path == path {
				found = true
				break
			}
		}
		if !found {
			goReqs = append(goReqs, mod.Require{Path: path, Version: version})
		}
	}
	// Add transitive GALA deps
	for _, req := range transitiveGalaDeps {
		found := false
		for _, existing := range galaReqs {
			if existing.Path == req.Path {
				found = true
				break
			}
		}
		if !found {
			galaReqs = append(galaReqs, req)
		}
	}
	return galaReqs, goReqs
}

// resolveInternalModulePaths reads each GALA dependency's gala.mod to discover
// its declared module path (which may differ from the GitHub URL used in the
// parent's require directive). Returns a map of requirePath -> internalModulePath.
func (g *GoModGenerator) resolveInternalModulePaths(galaMod *mod.File, galaReqs []mod.Require) map[string]string {
	result := make(map[string]string)
	for _, req := range galaReqs {
		pkgDir := g.effectiveDepDir(galaMod, req)
		galaModPath := filepath.Join(pkgDir, "gala.mod")
		if depMod, err := mod.ParseFile(galaModPath); err == nil {
			if depMod.Module.Path != "" && depMod.Module.Path != req.Path {
				result[req.Path] = depMod.Module.Path
			}
		}
	}
	return result
}

// effectiveModulePath returns the internal module path for a dependency if one
// was discovered, otherwise returns the original require path.
func (g *GoModGenerator) effectiveModulePath(reqPath string, internalPaths map[string]string) string {
	if internal, ok := internalPaths[reqPath]; ok {
		return internal
	}
	return reqPath
}

// collectTransitiveDeps scans GALA packages for their Go and GALA dependencies.
// Returns (goDeps, galaDeps). The galaMod argument is the active (root)
// gala.mod; its replace directives are consulted when resolving the source
// directory of each transitive requirement.
func (g *GoModGenerator) collectTransitiveDeps(galaMod *mod.File, galaReqs []mod.Require) (map[string]string, []mod.Require) {
	goDeps := make(map[string]string)
	var galaDeps []mod.Require
	seen := make(map[string]bool)

	for _, req := range galaReqs {
		g.collectTransitiveDepsRecursive(galaMod, req, goDeps, &galaDeps, seen)
	}

	return goDeps, galaDeps
}

// collectTransitiveDepsRecursive recursively collects transitive deps from a GALA package.
func (g *GoModGenerator) collectTransitiveDepsRecursive(galaMod *mod.File, req mod.Require, goDeps map[string]string, galaDeps *[]mod.Require, seen map[string]bool) {
	key := req.Path + "@" + req.Version
	if seen[key] {
		return
	}
	seen[key] = true

	pkgDir := g.effectiveDepDir(galaMod, req)

	// Try gala.mod first
	galaModPath := filepath.Join(pkgDir, "gala.mod")
	if depMod, err := mod.ParseFile(galaModPath); err == nil {
		for _, r := range depMod.Require {
			if r.Go {
				goDeps[r.Path] = r.Version
			} else {
				*galaDeps = append(*galaDeps, r)
				// Recurse for transitive GALA deps
				g.collectTransitiveDepsRecursive(galaMod, r, goDeps, galaDeps, seen)
			}
		}
	}

	// Also try go.mod for Go dependencies
	goModPath := filepath.Join(pkgDir, "go.mod")
	if content, err := os.ReadFile(goModPath); err == nil {
		parsedDeps := ParseGoModRequires(string(content))
		for path, version := range parsedDeps {
			// Skip GALA stdlib
			if strings.HasPrefix(path, GalaModulePrefix) {
				continue
			}
			goDeps[path] = version
		}
	}
}

// GoModRequire is one requirement of a go.mod: the module path and version,
// and the index of the line that holds them.
type GoModRequire struct {
	Line    int
	Path    string
	Version string
}

// ParseGoModRequireLines returns the requirements in go.mod content, both the
// single-line form (`require path version`) and the entries of require blocks
// (`require (`, also spelled `require(`), in file order. Comments are ignored,
// including one after a block opener or a requirement.
func ParseGoModRequireLines(content string) []GoModRequire {
	var reqs []GoModRequire
	inBlock := false
	for i, line := range strings.Split(content, "\n") {
		if idx := strings.Index(line, "//"); idx >= 0 {
			line = line[:idx]
		}
		fields := strings.Fields(strings.Replace(line, "require(", "require (", 1))
		switch {
		case len(fields) == 0:
		case inBlock && fields[0] == ")":
			inBlock = false
		case inBlock && len(fields) >= 2:
			reqs = append(reqs, GoModRequire{Line: i, Path: fields[0], Version: fields[1]})
		case fields[0] == "require" && len(fields) >= 2 && fields[1] == "(":
			inBlock = true
		case fields[0] == "require" && len(fields) >= 3:
			reqs = append(reqs, GoModRequire{Line: i, Path: fields[1], Version: fields[2]})
		}
	}
	return reqs
}

// ParseGoModRequires extracts require statements from go.mod content, as a
// module path -> version map.
func ParseGoModRequires(content string) map[string]string {
	deps := make(map[string]string)
	for _, r := range ParseGoModRequireLines(content) {
		deps[r.Path] = r.Version
	}
	return deps
}

// WriteGoMod writes the go.mod file to the workspace.
func (g *GoModGenerator) WriteGoMod(workspace *Workspace, galaMod *mod.File, stdlibVersion string, transpiledDeps map[string]string) error {
	content := g.GenerateGoMod(galaMod, stdlibVersion, transpiledDeps)
	return os.WriteFile(workspace.GoModPath, []byte(content), 0644)
}

// GenerateGoSum generates a minimal go.sum file.
// For now, this is empty as we use replace directives for GALA deps.
func (g *GoModGenerator) GenerateGoSum(galaMod *mod.File) string {
	// Go dependencies will have their checksums added by `go mod tidy`
	// GALA dependencies use replace directives so don't need checksums
	return ""
}

// WriteGoSum writes the go.sum file to the workspace.
func (g *GoModGenerator) WriteGoSum(workspace *Workspace, galaMod *mod.File) error {
	content := g.GenerateGoSum(galaMod)
	return os.WriteFile(workspace.GoSumPath, []byte(content), 0644)
}

// CollectImports scans generated Go files and returns all import paths.
func CollectImports(genDir string) ([]string, error) {
	entries, err := os.ReadDir(genDir)
	if err != nil {
		return nil, err
	}

	imports := make(map[string]bool)

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") {
			continue
		}

		content, err := os.ReadFile(filepath.Join(genDir, entry.Name()))
		if err != nil {
			continue
		}

		// Simple import extraction
		extractImports(string(content), imports)
	}

	var result []string
	for imp := range imports {
		result = append(result, imp)
	}
	sort.Strings(result)

	return result, nil
}

// CollectImportsRecursive returns every import path in the .go files under dir,
// including subdirectories.
//
// CollectImports deliberately reads only dir's own files: go.mod generation
// cares about the root package's imports. An invariant that must hold over the
// whole generated tree has to walk it — a project laying its packages out under
// gen/<pkg>/ keeps nothing at the root for the shallow read to see.
func CollectImportsRecursive(dir string) ([]string, error) {
	imports := make(map[string]bool)

	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".go") {
			return nil
		}
		content, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil // an unreadable file is the build's problem to report, not this scan's
		}
		extractImports(string(content), imports)
		return nil
	})
	if err != nil {
		return nil, err
	}

	result := make([]string, 0, len(imports))
	for imp := range imports {
		result = append(result, imp)
	}
	sort.Strings(result)

	return result, nil
}

// extractImports extracts import paths from Go source code.
func extractImports(content string, imports map[string]bool) {
	lines := strings.Split(content, "\n")
	inImportBlock := false

	for _, line := range lines {
		line = strings.TrimSpace(line)

		if line == "import (" {
			inImportBlock = true
			continue
		}
		if inImportBlock && line == ")" {
			inImportBlock = false
			continue
		}

		// Single import
		if strings.HasPrefix(line, "import ") && !strings.Contains(line, "(") {
			if start := strings.Index(line, "\""); start >= 0 {
				if end := strings.LastIndex(line, "\""); end > start {
					imports[line[start+1:end]] = true
				}
			}
			continue
		}

		// Import block entry
		if inImportBlock {
			// Handle aliased imports: alias "path"
			if start := strings.Index(line, "\""); start >= 0 {
				if end := strings.LastIndex(line, "\""); end > start {
					imports[line[start+1:end]] = true
				}
			}
		}
	}
}

// IsStdlibImport returns true if the import path is a GALA stdlib package.
func IsStdlibImport(importPath string) bool {
	for _, stdlibImport := range StdlibImportPaths {
		if importPath == stdlibImport {
			return true
		}
	}
	return false
}

// IsGoStdlibImport returns true if the import path is a Go standard library package.
func IsGoStdlibImport(importPath string) bool {
	// Go stdlib packages don't contain dots in their path
	return !strings.Contains(importPath, ".")
}
