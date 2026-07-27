package build

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"martianoff/gala/internal/depman/fetch"
	"martianoff/gala/internal/depman/mod"
	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/analyzer"
	"martianoff/gala/internal/transpiler/generator"
	"martianoff/gala/internal/transpiler/transformer"
)

// Builder orchestrates the build process for GALA projects.
type Builder struct {
	config         *Config
	workspace      *Workspace
	galaMod        *mod.File
	stdlibVersion  string
	verbose        bool
	transpiledDeps map[string]string // modulePath -> transpiled directory
	sourceDir      string            // override source directory (for running subdir files)
}

// SetSourceDir sets an override source directory for compilation.
// When set, the builder compiles files from this directory (as package main)
// instead of the project root. The project root's library files are transpiled
// first and made available as a local dependency.
func (b *Builder) SetSourceDir(dir string) {
	b.sourceDir = dir
}

// NewBuilder creates a new builder for the given project directory.
func NewBuilder(projectDir string, stdlibVersion string, verbose bool) (*Builder, error) {
	config := DefaultConfig()

	// Ensure all directories exist
	if err := config.EnsureDirs(); err != nil {
		return nil, fmt.Errorf("creating directories: %w", err)
	}

	// Create workspace
	workspace, err := NewWorkspace(config, projectDir)
	if err != nil {
		return nil, fmt.Errorf("creating workspace: %w", err)
	}

	// Load gala.mod (optional — standalone .gala files can run without one)
	galaModPath := filepath.Join(projectDir, "gala.mod")
	var galaMod *mod.File
	if _, statErr := os.Stat(galaModPath); statErr == nil {
		galaMod, err = mod.ParseFile(galaModPath)
		if err != nil {
			return nil, fmt.Errorf("parsing gala.mod: %w", err)
		}
	} else {
		// Create a minimal in-memory gala.mod for standalone files
		galaMod = &mod.File{
			Module: mod.Module{Path: "gala-standalone"},
			Gala:   stdlibVersion,
		}
	}

	return &Builder{
		config:        config,
		workspace:     workspace,
		galaMod:       galaMod,
		stdlibVersion: stdlibVersion,
		verbose:       verbose,
	}, nil
}

// Build executes the full build process and returns the path to the output binary.
// If outputPath is empty, uses the module name. If it's an absolute path, uses it directly.
// Otherwise, treats it as relative to the project directory.
// For library packages (non-main), Build performs a compile check and returns "" with no error.
func (b *Builder) Build(outputPath string) (string, error) {
	// Step 0: Verify Go toolchain is on PATH before doing any work.
	// GALA transpiles to Go, so `go build` is a hard prerequisite. Without
	// this check users see a cryptic `exec: "go": executable file not found`
	// error only after transpilation completes.
	if err := ensureGoToolchain(); err != nil {
		return "", err
	}

	// Step 1: Ensure workspace exists
	if b.verbose {
		fmt.Printf("Using workspace: %s\n", b.workspace.Dir)
	}
	if err := b.workspace.Ensure(); err != nil {
		return "", fmt.Errorf("ensuring workspace: %w", err)
	}

	// Step 1.5: Invalidate workspace if gala version changed
	versionFile := filepath.Join(b.workspace.Dir, ".gala-version")
	if oldVer, err := os.ReadFile(versionFile); err != nil || string(oldVer) != b.stdlibVersion {
		if b.verbose && err == nil {
			fmt.Printf("GALA version changed (%s -> %s), invalidating workspace\n", string(oldVer), b.stdlibVersion)
		}
		os.RemoveAll(b.workspace.GenDir)
		os.MkdirAll(b.workspace.GenDir, 0755)
		os.RemoveAll(b.workspace.DepsDir)
		os.MkdirAll(b.workspace.DepsDir, 0755)
		// Remove stale hash files
		os.Remove(filepath.Join(b.workspace.Dir, ".gala-source-hash"))
		os.Remove(filepath.Join(b.workspace.Dir, ".gala-deps-hash"))
		os.Remove(filepath.Join(b.workspace.Dir, "go.mod"))
		os.Remove(filepath.Join(b.workspace.Dir, "go.sum"))
		os.WriteFile(versionFile, []byte(b.stdlibVersion), 0644)
	}

	// Step 2: Ensure stdlib is extracted to versioned cache
	if err := b.ensureStdlib(); err != nil {
		return "", fmt.Errorf("ensuring stdlib: %w", err)
	}

	// Step 2.5: Fetch missing GALA dependencies
	if err := b.ensureDeps(); err != nil {
		return "", fmt.Errorf("fetching dependencies: %w", err)
	}

	// Step 2.6: Transpile GALA dependencies
	if err := b.transpileDeps(); err != nil {
		return "", fmt.Errorf("transpiling dependencies: %w", err)
	}

	// Step 3: Transpile .gala files to workspace
	if err := b.transpile(); err != nil {
		return "", fmt.Errorf("transpiling: %w", err)
	}

	// Step 4: Generate go.mod in workspace
	if err := b.generateGoMod(); err != nil {
		return "", fmt.Errorf("generating go.mod: %w", err)
	}

	// Step 4.5: Check if library package — if so, compile-check only
	// Skip this check in multi-package mode (sourceDir set) since the consumer is package main
	if b.sourceDir == "" || b.sourceDir == b.workspace.ProjectDir {
		isLib, pkgName := b.isLibraryPackage()
		if isLib {
			if b.verbose {
				fmt.Printf("Package %q is a library, running compile check...\n", pkgName)
			}
			if err := b.goCompileCheck(); err != nil {
				return "", fmt.Errorf("go build (compile check): %w", err)
			}
			return "", nil
		}
	}

	// Step 5: Run go build (executable)
	finalPath, err := b.goBuild(outputPath)
	if err != nil {
		return "", fmt.Errorf("go build: %w", err)
	}

	return finalPath, nil
}

// ensureDeps fetches any GALA dependencies that are not yet cached locally.
// Requirements covered by a `replace` directive pointing to a local path are
// served from that directory instead of being fetched, mirroring Go's
// `go mod` replace semantics. Non-local replaces (module => module@ver) still
// fetch, but using the replacement coordinates.
func (b *Builder) ensureDeps() error {
	galaReqs := b.galaMod.GalaRequires()
	if len(galaReqs) == 0 {
		return nil
	}

	config := fetch.DefaultConfig()
	cache := fetch.NewCache(config)
	fetcher := fetch.NewGitFetcher(cache)

	for _, req := range galaReqs {
		if _, isLocal, ok := b.resolveReplace(req); ok {
			// Local replaces need no fetch — the source is already on disk.
			if isLocal {
				if b.verbose {
					fmt.Printf("Replace: %s@%s => local path (fetch skipped)\n",
						req.Path, req.Version)
				}
				continue
			}
			// Module replace: fetch the replacement coordinates if not cached.
			// fall through with the effective path below
		}

		modDir := b.effectiveDepDir(req)
		if _, err := os.Stat(modDir); err == nil {
			continue // Already cached or local replacement present
		}

		fetchPath, fetchVersion := req.Path, req.Version
		if newPath, newVersion, isModuleReplace := b.resolveModuleReplace(req); isModuleReplace {
			fetchPath, fetchVersion = newPath, newVersion
		}

		if b.verbose {
			fmt.Printf("Fetching %s@%s...\n", fetchPath, fetchVersion)
		}
		if _, _, err := fetcher.Fetch(fetchPath, fetchVersion); err != nil {
			return fmt.Errorf("fetching %s@%s: %w", fetchPath, fetchVersion, err)
		}
	}
	return nil
}

// resolveReplace reports whether `req` has a matching replace directive in
// the active gala.mod, and (if so) returns the resolved source path, an
// isLocal flag, and ok=true. A replace matches when Old.Path equals req.Path
// and Old.Version is either empty (wildcard) or equals req.Version.
func (b *Builder) resolveReplace(req mod.Require) (path string, isLocal bool, ok bool) {
	if b.galaMod == nil {
		return "", false, false
	}
	for _, rep := range b.galaMod.Replace {
		if rep.Old.Path != req.Path {
			continue
		}
		if rep.Old.Version != "" && rep.Old.Version != req.Version {
			continue
		}
		if rep.New.IsLocal() {
			resolved := rep.New.Path
			if !filepath.IsAbs(resolved) {
				resolved = filepath.Join(b.workspace.ProjectDir, resolved)
			}
			return filepath.Clean(resolved), true, true
		}
		return b.config.GalaModulePath(rep.New.Path, rep.New.Version), false, true
	}
	return "", false, false
}

// resolveModuleReplace returns the replacement module coordinates (path,
// version) for a non-local replace directive, or ok=false if none applies.
func (b *Builder) resolveModuleReplace(req mod.Require) (path, version string, ok bool) {
	if b.galaMod == nil {
		return "", "", false
	}
	for _, rep := range b.galaMod.Replace {
		if rep.Old.Path != req.Path {
			continue
		}
		if rep.Old.Version != "" && rep.Old.Version != req.Version {
			continue
		}
		if rep.New.IsLocal() {
			return "", "", false
		}
		return rep.New.Path, rep.New.Version, true
	}
	return "", "", false
}

// effectiveDepDir returns the on-disk directory where a dependency's source
// files can be read from — the local replacement path if one is configured,
// otherwise the standard module cache location.
func (b *Builder) effectiveDepDir(req mod.Require) string {
	return resolveEffectiveDepDir(b.config, b.galaMod, b.workspace.ProjectDir, req)
}

// goModuleSrcDirs maps each Go (`// go`) dependency's module path to the
// on-disk directory holding its .go source, so the analyzer can resolve
// third-party Go types module-aware (go/importer's source mode can't — see
// analyzer.goSrcDirs). The source is parsed, not compiled, so either cache
// works: GALA's own dep cache (populated by `gala mod add --go` / auto-fetch,
// source-only) or the Go module cache (populated by `go build`). We prefer the
// GALA cache because it is available at transpile time, which runs before the
// `go build` step that fills GOMODCACHE. Returns nil when there are no Go deps
// or none are cached yet (the analyzer then leaves Go resolution to the
// importer, which is correct for stdlib-only code).
func (b *Builder) goModuleSrcDirs() map[string]string {
	return GoModuleSrcDirs(b.galaMod, b.config)
}

// GoModuleSrcDirs maps each Go module required by galaMod to its on-disk .go
// source directory in the dependency cache. It checks the GALA dep cache (which
// stores the module path verbatim) and the Go module cache (which case-escapes
// uppercase letters). Returns nil when there are no Go requires or none resolve
// to a directory with Go sources. Shared by the CLI builder and the LSP so both
// resolve third-party Go types the same way.
func GoModuleSrcDirs(galaMod *mod.File, config *Config) map[string]string {
	if galaMod == nil || config == nil {
		return nil
	}
	reqs := galaMod.GoRequires()
	if len(reqs) == 0 {
		return nil
	}
	dirs := make(map[string]string, len(reqs))
	for _, req := range reqs {
		// GALA dep cache stores the module path verbatim; the Go module
		// cache case-escapes it (uppercase letter c -> "!"+lower(c)).
		galaCand := filepath.Join(config.GalaPkgDir, filepath.FromSlash(req.Path)+"@"+req.Version)
		goCand := filepath.Join(config.GoPkgDir, filepath.FromSlash(escapeGoModulePath(req.Path))+"@"+req.Version)
		for _, cand := range []string{galaCand, goCand} {
			if dirHasGoFiles(cand) {
				dirs[req.Path] = cand
				break
			}
		}
	}
	if len(dirs) == 0 {
		return nil
	}
	return dirs
}

// escapeGoModulePath applies the Go module cache path escaping: every
// uppercase ASCII letter is replaced with "!" followed by its lowercase form
// (e.g. github.com/BurntSushi/toml -> github.com/!burnt!sushi/toml). This
// mirrors golang.org/x/mod/module.EscapePath for the common case without
// taking on the dependency.
func escapeGoModulePath(p string) string {
	hasUpper := false
	for i := 0; i < len(p); i++ {
		if p[i] >= 'A' && p[i] <= 'Z' {
			hasUpper = true
			break
		}
	}
	if !hasUpper {
		return p
	}
	var sb strings.Builder
	for i := 0; i < len(p); i++ {
		c := p[i]
		if c >= 'A' && c <= 'Z' {
			sb.WriteByte('!')
			sb.WriteByte(c + ('a' - 'A'))
		} else {
			sb.WriteByte(c)
		}
	}
	return sb.String()
}

// dirHasGoFiles reports whether dir exists and contains at least one
// non-test, non-generated .go source file.
func dirHasGoFiles(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasSuffix(name, ".go") && !strings.HasSuffix(name, "_test.go") && !strings.HasSuffix(name, ".gen.go") {
			return true
		}
	}
	return false
}

// ensureStdlib makes the versioned stdlib cache match the embedded snapshot,
// re-extracting it when the cached copy was produced by a different snapshot.
// The work is shared with Config.EnsureStdlib (used by `gala transpile` and the
// LSP) so both entry points target the same directory and apply the same
// freshness check.
func (b *Builder) ensureStdlib() error {
	stdlibDir, extracted, err := b.config.ensureStdlibExtracted(b.stdlibVersion)
	if err != nil {
		return err
	}
	if b.verbose {
		if extracted {
			fmt.Printf("Extracted stdlib to: %s\n", stdlibDir)
		} else {
			fmt.Printf("Stdlib already extracted at: %s\n", stdlibDir)
		}
	}
	return nil
}

// computeSourceHash computes a SHA256 hash of all inputs for cache invalidation.
// Includes .gala source files, gala.mod, and the gala version so that any
// change to sources, dependencies, or the transpiler itself triggers a rebuild.
func computeSourceHash(files []string, galaVersion string) string {
	h := sha256.New()
	h.Write([]byte("gala:" + galaVersion + "\n"))
	sorted := make([]string, len(files))
	copy(sorted, files)
	sort.Strings(sorted)
	for _, f := range sorted {
		content, err := os.ReadFile(f)
		if err != nil {
			return "" // force re-transpile on error
		}
		h.Write([]byte(f))
		h.Write(content)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// transpile transpiles all .gala files in the project to the workspace.
func (b *Builder) transpile() error {
	if b.verbose {
		fmt.Println("Transpiling GALA files...")
	}

	// If sourceDir is set and different from project root, do a multi-package build:
	// 1. Transpile the library (project root) into gen/
	// 2. Transpile the consumer (sourceDir) into gen/ as package main
	if b.sourceDir != "" && b.sourceDir != b.workspace.ProjectDir {
		return b.transpileWithSourceDir()
	}

	// Find all .gala files in the project, including subpackages. Subdirectories
	// may host their own GALA packages (e.g. `sub/lib.gala` declaring `package sub`)
	// that the root package imports; without recursion the root build would fail
	// with "package <workspace>/gen/sub is not in std" because the subpackage was
	// never transpiled.
	galaFiles, err := findGalaFilesRecursive(b.workspace.ProjectDir)
	if err != nil {
		return fmt.Errorf("finding gala files: %w", err)
	}

	if len(galaFiles) == 0 {
		return fmt.Errorf("no .gala files found in %s", b.workspace.ProjectDir)
	}

	// Check if sources have changed since last transpilation
	// Include gala.mod in hash so dep changes also invalidate the cache
	hashFile := filepath.Join(b.workspace.Dir, ".gala-source-hash")
	galaModFile := filepath.Join(b.workspace.ProjectDir, "gala.mod")
	currentHash := computeSourceHash(append(galaFiles, galaModFile), b.stdlibVersion)
	if currentHash != "" {
		if oldHash, err := os.ReadFile(hashFile); err == nil && string(oldHash) == currentHash {
			if genFiles, err := b.workspace.GenFiles(); err == nil && len(genFiles) > 0 {
				if b.verbose {
					fmt.Println("  Sources unchanged, skipping transpilation")
				}
				return nil
			}
		}
	}

	// Clean gen directory
	if err := b.workspace.CleanGen(); err != nil {
		return fmt.Errorf("cleaning gen dir: %w", err)
	}

	// Create transpiler pipeline
	stdlibDir := b.config.StdlibVersionDir(b.stdlibVersion)
	searchPaths := []string{b.workspace.ProjectDir, stdlibDir}

	for _, req := range b.galaMod.GalaRequires() {
		searchPaths = append(searchPaths, b.effectiveDepDir(req))
	}
	p := transpiler.NewAntlrGalaParser()
	tr := transformer.NewGalaASTTransformer()
	g := generator.NewGoCodeGenerator()

	// Use BatchAnalyzer to share analyzed package cache across all files.
	// This avoids redundant re-analysis of imports (std, collection_immutable, etc.).
	batchAnalyzer := analyzer.NewBatchAnalyzer(p, searchPaths, b.workspace.ProjectDir)
	batchAnalyzer.SetGoSrcDirs(b.goModuleSrcDirs())

	// Group files by their parent directory so sibling-based type resolution
	// only sees files that actually share a Go package. A root-level file is
	// not a sibling of a file in sub/ even though both belong to the same
	// GALA module.
	filesByDir := make(map[string][]string)
	for _, f := range galaFiles {
		filesByDir[filepath.Dir(f)] = append(filesByDir[filepath.Dir(f)], f)
	}

	var allEmbedPatterns []string

	for _, galaFile := range galaFiles {
		content, err := os.ReadFile(galaFile)
		if err != nil {
			return fmt.Errorf("reading %s: %w", galaFile, err)
		}

		var siblings []string
		for _, other := range filesByDir[filepath.Dir(galaFile)] {
			if other != galaFile {
				siblings = append(siblings, other)
			}
		}
		batchAnalyzer.SetPackageFiles(siblings)

		t := transpiler.NewGalaToGoTranspiler(p, batchAnalyzer, tr, g)

		goCode, err := t.Transpile(string(content), galaFile)
		if err != nil {
			return fmt.Errorf("transpiling %s: %w", galaFile, err)
		}

		// Collect embed patterns from generated Go code
		allEmbedPatterns = append(allEmbedPatterns, extractEmbedPatterns(goCode)...)

		relPath, err := filepath.Rel(b.workspace.ProjectDir, galaFile)
		if err != nil {
			relPath = filepath.Base(galaFile)
		}
		// Preserve the subdirectory layout in gen/ so each GALA subpackage
		// lands in its own directory — this is what the Go toolchain needs
		// to resolve imports like "gala-build-workspace/gen/sub".
		outName := strings.TrimSuffix(relPath, ".gala") + ".gen.go"
		outPath := filepath.Join(b.workspace.GenDir, outName)

		if err := os.MkdirAll(filepath.Dir(outPath), 0755); err != nil {
			return fmt.Errorf("creating gen dir for %s: %w", outName, err)
		}
		if err := os.WriteFile(outPath, []byte(goCode), 0644); err != nil {
			return fmt.Errorf("writing %s: %w", outName, err)
		}

		if b.verbose {
			fmt.Printf("  %s -> %s\n", relPath, outName)
		}
	}

	// Copy embed source files to the gen directory
	if len(allEmbedPatterns) > 0 {
		if err := b.copyEmbedFiles(allEmbedPatterns); err != nil {
			return fmt.Errorf("copying embed files: %w", err)
		}
	}

	// Copy local Go subpackages (e.g., httpcore/) to the gen directory
	// so they are available alongside generated .gen.go files
	if err := copyNonGalaFiles(b.workspace.ProjectDir, b.workspace.GenDir, b.verbose); err != nil {
		return fmt.Errorf("copying local Go subpackages: %w", err)
	}

	// Rewrite imports that reference the project's Go module path to use the
	// workspace module path instead, so local subpackages resolve correctly.
	if err := b.rewriteProjectModuleImports(b.workspace.GenDir); err != nil {
		return fmt.Errorf("rewriting project module imports: %w", err)
	}

	// Save source hash for next build
	if currentHash != "" {
		os.WriteFile(hashFile, []byte(currentHash), 0644)
	}

	return nil
}

// transpileWithSourceDir handles the multi-package case: when sourceDir is a
// subdirectory (e.g., examples/hello/), transpile the project library first,
// then the consumer source files on top of it.
//
// Workspace layout after transpilation:
//
//	gen/
//	  filter.gen.go          ← library (package server)
//	  server.gen.go          ← library
//	  ...
//	  httpcore/              ← local Go subpackages
//	  cmd/
//	    main/
//	      main.gen.go        ← consumer (package main)
//
// The consumer's imports of the project module are rewritten to point to gen/.
func (b *Builder) transpileWithSourceDir() error {
	if b.verbose {
		fmt.Printf("Multi-package build: library from %s, main from %s\n",
			b.workspace.ProjectDir, b.sourceDir)
	}

	// Clean gen directory
	if err := b.workspace.CleanGen(); err != nil {
		return fmt.Errorf("cleaning gen dir: %w", err)
	}

	// Create transpiler pipeline
	stdlibDir := b.config.StdlibVersionDir(b.stdlibVersion)
	searchPaths := []string{b.workspace.ProjectDir, stdlibDir}
	for _, req := range b.galaMod.GalaRequires() {
		searchPaths = append(searchPaths, b.effectiveDepDir(req))
	}
	p := transpiler.NewAntlrGalaParser()
	tr := transformer.NewGalaASTTransformer()
	g := generator.NewGoCodeGenerator()

	// Step 1: Transpile library files (project root) into gen/.
	// Recursive so subpackages (e.g., state/) are included; the consumer
	// subtree is filtered out below since those files are handled in step 2.
	// One BatchAnalyzer for all library files so analyzedPkgs (std,
	// collection_immutable, etc.) and parsedFileCache (sibling .gala
	// trees) are shared across the loop instead of paid per file.
	allLibFiles, err := findGalaFilesRecursive(b.workspace.ProjectDir)
	if err != nil {
		return fmt.Errorf("finding library files: %w", err)
	}
	consumerPrefix := b.sourceDir + string(filepath.Separator)
	var libFiles []string
	for _, f := range allLibFiles {
		if b.sourceDir != "" && b.sourceDir != b.workspace.ProjectDir {
			if f == b.sourceDir || strings.HasPrefix(f, consumerPrefix) {
				continue
			}
		}
		libFiles = append(libFiles, f)
	}
	if b.verbose {
		fmt.Printf("  Transpiling %d library files...\n", len(libFiles))
	}
	// Group library files by directory: only files in the same directory
	// share a Go package, so sibling-based type resolution must not mix
	// root-package files with subpackage files (e.g., state/).
	libFilesByDir := make(map[string][]string)
	for _, f := range libFiles {
		libFilesByDir[filepath.Dir(f)] = append(libFilesByDir[filepath.Dir(f)], f)
	}
	libBatch := analyzer.NewBatchAnalyzer(p, searchPaths, b.workspace.ProjectDir)
	libBatch.SetGoSrcDirs(b.goModuleSrcDirs())
	for _, galaFile := range libFiles {
		content, err := os.ReadFile(galaFile)
		if err != nil {
			return fmt.Errorf("reading %s: %w", galaFile, err)
		}
		var siblings []string
		for _, other := range libFilesByDir[filepath.Dir(galaFile)] {
			if other != galaFile {
				siblings = append(siblings, other)
			}
		}
		libBatch.SetPackageFiles(siblings)
		t := transpiler.NewGalaToGoTranspiler(p, libBatch, tr, g)
		goCode, err := t.Transpile(string(content), galaFile)
		if err != nil {
			return fmt.Errorf("transpiling %s: %w", galaFile, err)
		}
		// Preserve subdirectory layout in gen/ so each GALA subpackage
		// lands in its own directory — this is what the Go toolchain needs
		// to resolve imports like "gala-build-workspace/gen/<sub>".
		relPath, err := filepath.Rel(b.workspace.ProjectDir, galaFile)
		if err != nil {
			relPath = filepath.Base(galaFile)
		}
		outName := strings.TrimSuffix(relPath, ".gala") + ".gen.go"
		outPath := filepath.Join(b.workspace.GenDir, outName)
		if err := os.MkdirAll(filepath.Dir(outPath), 0755); err != nil {
			return fmt.Errorf("creating gen dir for %s: %w", outName, err)
		}
		if err := os.WriteFile(outPath, []byte(goCode), 0644); err != nil {
			return fmt.Errorf("writing %s: %w", outName, err)
		}
		if b.verbose {
			fmt.Printf("    %s -> %s\n", relPath, outName)
		}
	}

	// Copy local Go subpackages
	if err := copyNonGalaFiles(b.workspace.ProjectDir, b.workspace.GenDir, b.verbose); err != nil {
		return fmt.Errorf("copying local Go subpackages: %w", err)
	}

	// Step 2: Transpile consumer files (sourceDir) into gen/cmd/main/
	consumerFiles, err := findGalaFiles(b.sourceDir)
	if err != nil {
		return fmt.Errorf("finding consumer files: %w", err)
	}
	if len(consumerFiles) == 0 {
		return fmt.Errorf("no .gala files found in %s", b.sourceDir)
	}
	if b.verbose {
		fmt.Printf("  Transpiling %d consumer files...\n", len(consumerFiles))
	}

	consumerDir := filepath.Join(b.workspace.GenDir, "cmd", "main")
	if err := os.MkdirAll(consumerDir, 0755); err != nil {
		return fmt.Errorf("creating consumer dir: %w", err)
	}

	// Reuse libBatch for consumer files: SetPackageFiles is per-call so
	// the consumer's sibling list does not collide with the library's.
	// The win is that analyzedPkgs (std, collection_immutable, etc.) and
	// parsedFileCache (every library .gala already parsed during Step 1)
	// carry over — so the consumer's `import "<project>"` resolution does
	// not re-read or re-parse the library files when it analyzePackages
	// the project's own module.
	for _, galaFile := range consumerFiles {
		content, err := os.ReadFile(galaFile)
		if err != nil {
			return fmt.Errorf("reading %s: %w", galaFile, err)
		}
		var siblings []string
		for _, other := range consumerFiles {
			if other != galaFile {
				siblings = append(siblings, other)
			}
		}
		libBatch.SetPackageFiles(siblings)
		t := transpiler.NewGalaToGoTranspiler(p, libBatch, tr, g)
		goCode, err := t.Transpile(string(content), galaFile)
		if err != nil {
			return fmt.Errorf("transpiling %s: %w", galaFile, err)
		}
		outName := strings.TrimSuffix(filepath.Base(galaFile), ".gala") + ".gen.go"
		outPath := filepath.Join(consumerDir, outName)
		if err := os.WriteFile(outPath, []byte(goCode), 0644); err != nil {
			return fmt.Errorf("writing %s: %w", outPath, err)
		}
		if b.verbose {
			fmt.Printf("    %s -> cmd/main/%s\n", filepath.Base(galaFile), outName)
		}
	}

	// Step 3: Rewrite project module imports in consumer files.
	// The consumer may import the project using either the Go module path
	// ("github.com/martianoff/gala-server") or the GALA short path
	// ("martianoff/gala-server"). Rewrite both to the workspace path.
	projectModule := b.projectGoModulePath()
	if projectModule != "" {
		if err := rewriteImportsInDir(consumerDir, projectModule, "gala-build-workspace/gen", b.verbose); err != nil {
			return fmt.Errorf("rewriting consumer imports: %w", err)
		}
		// Also rewrite the short path (without VCS host prefix like github.com/).
		// GALA imports use "martianoff/gala-server" while go.mod uses "github.com/martianoff/gala-server".
		for _, prefix := range []string{"github.com/", "gitlab.com/", "bitbucket.org/"} {
			if strings.HasPrefix(projectModule, prefix) {
				shortPath := strings.TrimPrefix(projectModule, prefix)
				if err := rewriteImportsInDir(consumerDir, shortPath, "gala-build-workspace/gen", b.verbose); err != nil {
					return fmt.Errorf("rewriting consumer short imports: %w", err)
				}
				break
			}
		}
	}

	// Also rewrite library imports that reference the project module (for local subpackages)
	if err := b.rewriteProjectModuleImports(b.workspace.GenDir); err != nil {
		return fmt.Errorf("rewriting library imports: %w", err)
	}

	return nil
}

// extractEmbedPatterns parses //go:embed directives from generated Go code
// and returns the embed patterns.
func extractEmbedPatterns(goCode string) []string {
	var patterns []string
	for _, line := range strings.Split(goCode, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "//go:embed ") {
			pattern := strings.TrimPrefix(trimmed, "//go:embed ")
			pattern = strings.TrimSpace(pattern)
			if pattern != "" {
				patterns = append(patterns, pattern)
			}
		}
	}
	return patterns
}

// copyEmbedFiles copies files matching embed patterns from the project directory
// to the workspace gen directory, preserving relative paths.
func (b *Builder) copyEmbedFiles(patterns []string) error {
	if b.verbose {
		fmt.Println("Copying embed source files...")
	}

	for _, pattern := range patterns {
		// Resolve the glob pattern relative to the project directory
		absPattern := filepath.Join(b.workspace.ProjectDir, pattern)
		matches, err := filepath.Glob(absPattern)
		if err != nil {
			return fmt.Errorf("expanding embed pattern %q: %w", pattern, err)
		}

		if len(matches) == 0 {
			// If no glob match, the pattern might refer to a single file;
			// still try to copy it (Go compiler will report the error if missing)
			srcPath := filepath.Join(b.workspace.ProjectDir, pattern)
			if info, statErr := os.Stat(srcPath); statErr == nil && !info.IsDir() {
				matches = []string{srcPath}
			}
		}

		for _, srcPath := range matches {
			relPath, err := filepath.Rel(b.workspace.ProjectDir, srcPath)
			if err != nil {
				return fmt.Errorf("computing relative path for %s: %w", srcPath, err)
			}

			dstPath := filepath.Join(b.workspace.GenDir, relPath)

			// Create destination directory
			dstDir := filepath.Dir(dstPath)
			if err := os.MkdirAll(dstDir, 0755); err != nil {
				return fmt.Errorf("creating directory %s: %w", dstDir, err)
			}

			// Copy the file
			data, err := os.ReadFile(srcPath)
			if err != nil {
				return fmt.Errorf("reading embed file %s: %w", srcPath, err)
			}

			if err := os.WriteFile(dstPath, data, 0644); err != nil {
				return fmt.Errorf("writing embed file %s: %w", dstPath, err)
			}

			if b.verbose {
				fmt.Printf("  embed: %s\n", relPath)
			}
		}
	}

	return nil
}

// rewriteProjectModuleImports rewrites import paths in all .go files under dir
// that reference the project's Go module path, replacing them with the workspace
// module path. This allows local Go subpackages (e.g., httpcore/) to be resolved
// from the workspace gen/ directory instead of the remote registry.
//
// For example, if the project module is "github.com/user/project":
//
//	"github.com/user/project/httpcore" → "gala-build-workspace/gen/httpcore"
//	"github.com/user/project"          → "gala-build-workspace/gen"
func (b *Builder) rewriteProjectModuleImports(dir string) error {
	projectModule := b.projectGoModulePath()
	if projectModule == "" {
		return nil // No module path known, nothing to rewrite
	}

	// Check if there are any local subdirectories — whether Go or GALA —
	// that could be imported under the project module path. Pure GALA
	// subpackages still need rewriting because their imports are emitted
	// using the project module path (e.g. "github.com/user/proj/sub") and
	// must be redirected to the workspace gen/ layout before `go build`
	// can resolve them.
	hasSubDirs := false
	entries, err := os.ReadDir(b.workspace.ProjectDir)
	if err == nil {
		for _, e := range entries {
			if e.IsDir() && !strings.HasPrefix(e.Name(), ".") && e.Name() != "vendor" &&
				!strings.HasPrefix(e.Name(), "bazel-") {
				subPath := filepath.Join(b.workspace.ProjectDir, e.Name())
				if hasGoFiles(subPath) || hasGalaFilesShallow(subPath) {
					hasSubDirs = true
					break
				}
			}
		}
	}
	if !hasSubDirs {
		return nil // No local subpackages, skip rewriting
	}

	if b.verbose {
		fmt.Printf("Rewriting project module imports: %s → gala-build-workspace/gen\n", projectModule)
	}

	return rewriteImportsInDir(dir, projectModule, "gala-build-workspace/gen", b.verbose)
}

// projectGoModulePath returns the project's Go module path by reading go.mod
// or falling back to the gala.mod module declaration.
func (b *Builder) projectGoModulePath() string {
	// Try go.mod first (authoritative for Go imports)
	goModPath := filepath.Join(b.workspace.ProjectDir, "go.mod")
	if content, err := os.ReadFile(goModPath); err == nil {
		if mod := parseGoModModulePath(string(content)); mod != "" {
			return mod
		}
	}

	// Fall back to gala.mod module path
	if b.galaMod != nil && b.galaMod.Module.Path != "" {
		return b.galaMod.Module.Path
	}

	return ""
}

// parseGoModModulePath extracts the module path from go.mod content.
func parseGoModModulePath(content string) string {
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "module ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "module "))
		}
	}
	return ""
}

// hasGoFiles returns true if the directory contains any .go files.
func hasGoFiles(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".go") {
			return true
		}
	}
	return false
}

// hasGalaFilesShallow reports whether dir contains at least one .gala file at
// its top level. Used when deciding whether the project module's import paths
// need to be rewritten to point at the workspace gen/ directory — GALA
// subpackages must be covered too, not just Go subpackages.
func hasGalaFilesShallow(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".gala") {
			return true
		}
	}
	return false
}

// rewriteImportsInDir recursively scans all .go files in dir and rewrites
// import paths that start with oldModule to use newModule instead.
func rewriteImportsInDir(dir, oldModule, newModule string, verbose bool) error {
	return filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // Skip unreadable entries
		}
		if info.IsDir() {
			return nil
		}
		if !strings.HasSuffix(info.Name(), ".go") {
			return nil
		}

		content, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("reading %s: %w", path, err)
		}

		original := string(content)
		rewritten := rewriteImportsInSource(original, oldModule, newModule)

		if rewritten != original {
			if verbose {
				fmt.Printf("  rewrite imports: %s\n", path)
			}
			if err := os.WriteFile(path, []byte(rewritten), 0644); err != nil {
				return fmt.Errorf("writing %s: %w", path, err)
			}
		}

		return nil
	})
}

// rewriteImportsInSource rewrites import paths in Go source code.
// Replaces imports matching oldModule or oldModule/subpkg with newModule equivalents.
func rewriteImportsInSource(source, oldModule, newModule string) string {
	lines := strings.Split(source, "\n")
	var result []string
	inImportBlock := false

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		if trimmed == "import (" {
			inImportBlock = true
			result = append(result, line)
			continue
		}
		if inImportBlock && trimmed == ")" {
			inImportBlock = false
			result = append(result, line)
			continue
		}

		if inImportBlock {
			line = rewriteImportLine(line, oldModule, newModule)
		} else if strings.HasPrefix(trimmed, "import ") && strings.Contains(trimmed, "\"") {
			line = rewriteImportLine(line, oldModule, newModule)
		}

		result = append(result, line)
	}

	return strings.Join(result, "\n")
}

// rewriteImportLine rewrites a single import line if it references oldModule.
func rewriteImportLine(line, oldModule, newModule string) string {
	// Find the quoted import path
	start := strings.Index(line, "\"")
	if start < 0 {
		return line
	}
	end := strings.Index(line[start+1:], "\"")
	if end < 0 {
		return line
	}
	end += start + 1

	importPath := line[start+1 : end]

	// Check if this import matches the old module
	if importPath == oldModule {
		return line[:start+1] + newModule + line[end:]
	}
	if strings.HasPrefix(importPath, oldModule+"/") {
		subPkg := strings.TrimPrefix(importPath, oldModule)
		return line[:start+1] + newModule + subPkg + line[end:]
	}

	return line
}

// generateGoMod generates the go.mod file in the workspace and downloads Go dependencies.
func (b *Builder) generateGoMod() error {
	if b.verbose {
		fmt.Println("Generating go.mod...")
	}

	gen := NewGoModGenerator(b.config)
	// Propagate the project directory so local replace directives (paths
	// relative to the user's gala.mod) resolve against the correct root.
	gen.SetProjectDir(b.workspace.ProjectDir)
	newContent := gen.GenerateGoMod(b.galaMod, b.stdlibVersion, b.transpiledDeps)

	// Check if go.mod content has changed
	goModChanged := true
	if existing, err := os.ReadFile(b.workspace.GoModPath); err == nil {
		if string(existing) == newContent {
			goModChanged = false
		}
	}

	if goModChanged {
		if err := os.WriteFile(b.workspace.GoModPath, []byte(newContent), 0644); err != nil {
			return err
		}

		if b.verbose {
			fmt.Println("Downloading Go dependencies...")
		}

		cmd := exec.Command("go", "mod", "tidy")
		cmd.Dir = b.workspace.Dir
		cmd.Env = append(os.Environ(), "GOMODCACHE="+b.config.GoPkgDir)

		if b.verbose {
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
		} else {
			cmd.Stderr = os.Stderr
		}

		if err := cmd.Run(); err != nil {
			return fmt.Errorf("running go mod tidy: %w", err)
		}
	} else if b.verbose {
		fmt.Println("  go.mod unchanged, skipping go mod tidy")
	}

	return nil
}

// isLibraryPackage checks whether the generated code is a library (non-main) package.
// Returns (true, packageName) for libraries, (false, "") for executables.
func (b *Builder) isLibraryPackage() (bool, string) {
	genFiles, err := b.workspace.GenFiles()
	if err != nil {
		return false, ""
	}

	// Read the first generated file to determine the package name
	var pkgName string
	for _, f := range genFiles {
		content, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(content), "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "package ") {
				pkgName = strings.TrimPrefix(line, "package ")
				break
			}
		}
		if pkgName != "" {
			break
		}
	}

	if pkgName != "" && pkgName != "main" {
		return true, pkgName
	}

	return false, ""
}

// ensureGoToolchain verifies that the Go toolchain is available on PATH.
// Pre-built GALA binaries depend on `go build` to finish the compilation pipeline,
// so if `go` is not on PATH we surface a clear actionable error before shelling
// out — the raw exec error ("executable file not found in $PATH") does not
// explain the prerequisite.
func ensureGoToolchain() error {
	if _, err := exec.LookPath("go"); err != nil {
		return fmt.Errorf("Go 1.25+ is required on your PATH to build GALA programs (GALA transpiles to Go).\nInstall Go from https://go.dev/dl/ and try again")
	}
	return nil
}

// goCompileCheck runs `go build ./...` in the workspace without producing a binary.
// This verifies that library packages compile correctly.
func (b *Builder) goCompileCheck() error {
	if err := ensureGoToolchain(); err != nil {
		return err
	}

	if b.verbose {
		fmt.Println("Running go build (compile check)...")
	}

	cmd := exec.Command("go", "build", "./gen/...")
	cmd.Dir = b.workspace.Dir
	cmd.Env = append(os.Environ(),
		"GOMODCACHE="+b.config.GoPkgDir,
	)

	if b.verbose {
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
	} else {
		cmd.Stderr = os.Stderr
	}

	return cmd.Run()
}

// goBuild runs `go build` in the workspace and returns the output path.
func (b *Builder) goBuild(outputPath string) (string, error) {
	if err := ensureGoToolchain(); err != nil {
		return "", err
	}

	if b.verbose {
		fmt.Println("Running go build...")
	}

	// Determine output path
	if outputPath == "" {
		// Use module name or directory name, in project directory
		outputPath = filepath.Join(b.workspace.ProjectDir, filepath.Base(b.workspace.ProjectDir))
	} else if !filepath.IsAbs(outputPath) {
		// Relative path - make it relative to project directory
		outputPath = filepath.Join(b.workspace.ProjectDir, outputPath)
	}

	// Add .exe extension on Windows if not present
	if isWindows() && !strings.HasSuffix(outputPath, ".exe") {
		outputPath += ".exe"
	}

	// Build command — in multi-package mode, build the consumer subpackage.
	// `go build -o <path> ./gen/...` fails with "cannot write multiple
	// packages to non-directory" when gen/ contains a main package alongside
	// library subpackages, so we narrow the target to the root gen package
	// (which is main, assuming the project itself is executable).
	buildTarget := "./gen"
	if b.sourceDir != "" && b.sourceDir != b.workspace.ProjectDir {
		buildTarget = "./gen/cmd/main"
	}
	args := []string{"build", "-o", outputPath, buildTarget}

	cmd := exec.Command("go", args...)
	cmd.Dir = b.workspace.Dir

	// Set GOMODCACHE to our Go cache
	cmd.Env = append(os.Environ(),
		"GOMODCACHE="+b.config.GoPkgDir,
	)

	if b.verbose {
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		fmt.Printf("Running: go %s\n", strings.Join(args, " "))
	} else {
		// Capture stderr for error messages
		cmd.Stderr = os.Stderr
	}

	if err := cmd.Run(); err != nil {
		return "", err
	}

	return outputPath, nil
}

// Workspace returns the builder's workspace.
func (b *Builder) Workspace() *Workspace {
	return b.workspace
}

// Config returns the builder's config.
func (b *Builder) Config() *Config {
	return b.config
}

// transpileDeps transpiles all GALA library dependencies.
func (b *Builder) transpileDeps() error {
	galaReqs := b.galaMod.GalaRequires()

	if len(galaReqs) == 0 {
		b.transpiledDeps = nil
		return nil
	}

	// Check if deps have changed by hashing gala.mod requirements and any
	// replace directives. Including replaces ensures that retargeting a dep
	// (e.g. toggling `replace X => ../localX`) invalidates the cache.
	depsHashFile := filepath.Join(b.workspace.Dir, ".gala-deps-hash")
	h := sha256.New()
	for _, req := range galaReqs {
		h.Write([]byte(req.Path + "@" + req.Version + "\n"))
	}
	for _, rep := range b.galaMod.Replace {
		h.Write([]byte("replace " + rep.Old.Path + "@" + rep.Old.Version +
			"=>" + rep.New.Path + "@" + rep.New.Version + "\n"))
	}
	currentHash := hex.EncodeToString(h.Sum(nil))

	if oldHash, err := os.ReadFile(depsHashFile); err == nil && string(oldHash) == currentHash {
		allExist := true
		b.transpiledDeps = make(map[string]string)
		for _, req := range galaReqs {
			dir := b.workspace.DepModuleDir(req.Path, req.Version)
			if info, err := os.Stat(dir); err != nil || !info.IsDir() {
				allExist = false
				break
			}
			// Key by internal module path (from dep's gala.mod), matching TranspileDeps() behavior.
			// Use the replace-aware source dir so local replaces are honored.
			modPath := resolveDepInternalModulePathAt(b.effectiveDepDir(req), req)
			b.transpiledDeps[modPath] = dir
		}
		if allExist {
			if b.verbose {
				fmt.Println("  Dependencies unchanged, skipping dep transpilation")
			}
			return nil
		}
	}

	// Clean deps dir before transpiling
	if err := b.workspace.CleanDeps(); err != nil {
		return fmt.Errorf("cleaning deps dir: %w", err)
	}

	dt := NewDepTranspiler(b.config, b.workspace, b.galaMod, b.stdlibVersion, b.verbose)
	transpiledDeps, err := dt.TranspileDeps()
	if err != nil {
		return err
	}

	b.transpiledDeps = transpiledDeps

	os.WriteFile(depsHashFile, []byte(currentHash), 0644)

	return nil
}

// resolveDepInternalModulePath reads a dependency's gala.mod to get its declared
// module path. Falls back to dep.Path if not found.
func resolveDepInternalModulePath(config *Config, dep mod.Require) string {
	return resolveDepInternalModulePathAt(config.GalaModulePath(dep.Path, dep.Version), dep)
}

// resolveDepInternalModulePathAt is like resolveDepInternalModulePath but uses
// an explicit source directory, making it safe to use with replace directives
// that point outside the module cache.
func resolveDepInternalModulePathAt(srcDir string, dep mod.Require) string {
	galaModPath := filepath.Join(srcDir, "gala.mod")
	if depMod, err := mod.ParseFile(galaModPath); err == nil {
		if depMod.Module.Path != "" {
			return depMod.Module.Path
		}
	}
	return dep.Path
}

// findGalaFiles finds all .gala files in the given directory (non-recursive for now).
func findGalaFiles(dir string) ([]string, error) {
	var files []string

	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if strings.HasSuffix(entry.Name(), ".gala") && !strings.HasSuffix(entry.Name(), "_test.gala") {
			files = append(files, filepath.Join(dir, entry.Name()))
		}
	}

	return files, nil
}

// findGalaFilesRecursive finds all .gala files recursively.
func findGalaFilesRecursive(dir string) ([]string, error) {
	var files []string

	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Skip hidden directories and common non-source directories
		if info.IsDir() {
			name := info.Name()
			if name != "." && (strings.HasPrefix(name, ".") || name == "vendor" ||
				name == "testdata" || strings.HasPrefix(name, "bazel-") || name == "_gala") {
				return filepath.SkipDir
			}
			return nil
		}

		// Only process .gala files (skip test files)
		if strings.HasSuffix(path, ".gala") && !strings.HasSuffix(path, "_test.gala") {
			files = append(files, path)
		}

		return nil
	})

	return files, err
}

// Test runs the test flow: transpile source + test files, discover test functions,
// generate a test main, build, and execute the test binary.
// If verbose is true, passes -v-style output. Returns the exit code from the test run.
func (b *Builder) Test(verbose bool) error {
	// Step 1: Ensure workspace exists
	if b.verbose {
		fmt.Printf("Using workspace: %s\n", b.workspace.Dir)
	}
	if err := b.workspace.Ensure(); err != nil {
		return fmt.Errorf("ensuring workspace: %w", err)
	}

	// Step 1.5: Invalidate workspace if gala version changed
	versionFile := filepath.Join(b.workspace.Dir, ".gala-version")
	if oldVer, err := os.ReadFile(versionFile); err != nil || string(oldVer) != b.stdlibVersion {
		if b.verbose && err == nil {
			fmt.Printf("GALA version changed (%s -> %s), invalidating workspace\n", string(oldVer), b.stdlibVersion)
		}
		os.RemoveAll(b.workspace.GenDir)
		os.MkdirAll(b.workspace.GenDir, 0755)
		os.RemoveAll(b.workspace.DepsDir)
		os.MkdirAll(b.workspace.DepsDir, 0755)
		os.Remove(filepath.Join(b.workspace.Dir, ".gala-source-hash"))
		os.Remove(filepath.Join(b.workspace.Dir, ".gala-deps-hash"))
		os.Remove(filepath.Join(b.workspace.Dir, "go.mod"))
		os.Remove(filepath.Join(b.workspace.Dir, "go.sum"))
		os.WriteFile(versionFile, []byte(b.stdlibVersion), 0644)
	}

	// Step 2: Ensure stdlib is extracted
	if err := b.ensureStdlib(); err != nil {
		return fmt.Errorf("ensuring stdlib: %w", err)
	}

	// Step 2.5: Fetch missing GALA dependencies
	if err := b.ensureDeps(); err != nil {
		return fmt.Errorf("fetching dependencies: %w", err)
	}

	// Step 2.6: Transpile GALA dependencies
	if err := b.transpileDeps(); err != nil {
		return fmt.Errorf("transpiling dependencies: %w", err)
	}

	// Step 3: Find source and test files. We recurse so that subpackages
	// (e.g. `state/state.gala`, `state/state_test.gala`) are picked up
	// alongside the project root — otherwise `go test ./gen/...` would
	// silently miss them, or the root package would fail to import them.
	sourceFiles, err := findGalaFilesRecursive(b.workspace.ProjectDir)
	if err != nil {
		return fmt.Errorf("finding source files: %w", err)
	}

	testFiles, err := findGalaTestFilesRecursive(b.workspace.ProjectDir)
	if err != nil {
		return fmt.Errorf("finding test files: %w", err)
	}

	if len(testFiles) == 0 {
		// Match `go test ./...` behavior: report that no tests were found and
		// exit successfully. Running `gala test` in a freshly-initialised project
		// that has source files but no tests yet is a normal state, not a build
		// failure.
		label := b.galaMod.Module.Path
		if label == "" || label == "gala-standalone" {
			label = b.workspace.ProjectDir
		}
		fmt.Printf("?   \t%s\t[no test files]\n", label)
		return nil
	}

	// Step 4: Determine package layout based on the project root package.
	// Test files in GALA are always package main and import the library under test.
	// Source files can be package main (executable) or a library package.
	// For package main projects: transpile source + test files together.
	// For library projects: transpile source files as library, test files separately as main.
	// We look at a file that actually lives in the project root (not a
	// subpackage) to classify the project — subpackages always are libraries
	// and must not influence the root classification.
	rootPkgName := rootPackageName(sourceFiles, b.workspace.ProjectDir)
	isLib := rootPkgName != "" && rootPkgName != "main"

	// Always force-retranspile for tests (test files change independently)
	if err := b.workspace.CleanGen(); err != nil {
		return fmt.Errorf("cleaning gen dir: %w", err)
	}

	// Step 5: Transpile files
	if isLib {
		if err := b.transpileTestLibrary(sourceFiles, testFiles); err != nil {
			return fmt.Errorf("transpiling: %w", err)
		}
	} else {
		if err := b.transpileTestMain(sourceFiles, testFiles); err != nil {
			return fmt.Errorf("transpiling: %w", err)
		}
	}

	// Step 6: Generate go.mod
	if err := b.generateGoMod(); err != nil {
		return fmt.Errorf("generating go.mod: %w", err)
	}

	// Step 7: Discover test functions from .gala test files
	var allTestFuncs []string
	for _, tf := range testFiles {
		funcs, err := FindTestFunctions(tf)
		if err != nil {
			return fmt.Errorf("scanning %s for test functions: %w", tf, err)
		}
		allTestFuncs = append(allTestFuncs, funcs...)
	}

	if len(allTestFuncs) == 0 {
		return fmt.Errorf("no TestXxx functions found in test files")
	}

	if b.verbose {
		fmt.Printf("Found %d test function(s): %s\n", len(allTestFuncs), strings.Join(allTestFuncs, ", "))
	}

	// Step 8: Generate test harness
	if isLib {
		// For library packages: generate a _test.go harness per package that
		// hosts test files. Each harness uses Go's testing framework with
		// TestMain and references only the TestXxx functions declared in its
		// own package — otherwise the root harness would fail to compile when
		// a subpackage owns a test function that the root package cannot see.
		// Tests run via `go test ./gen/...`.
		if err := b.writeLibraryTestHarnesses(testFiles); err != nil {
			return fmt.Errorf("writing test harnesses: %w", err)
		}
		if b.verbose {
			fmt.Println("Generated test harnesses")
		}

		// Step 9: Run tests via `go test`
		args := []string{"test", "-count=1"}
		if verbose {
			args = append(args, "-v")
		}
		args = append(args, "./gen/...")
		cmd := exec.Command("go", args...)
		cmd.Dir = b.workspace.Dir
		cmd.Env = append(os.Environ(), "GOMODCACHE="+b.config.GoPkgDir)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr

		if b.verbose {
			fmt.Printf("Running: go %s\n", strings.Join(args, " "))
		}

		if err := cmd.Run(); err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok {
				return fmt.Errorf("tests failed (exit code %d)", exitErr.ExitCode())
			}
			return fmt.Errorf("go test: %w", err)
		}
	} else {
		// For main packages: generate test_main.gen.go with func main() and
		// build a test binary.
		testMainCode := GenerateTestMain(allTestFuncs)
		testMainPath := filepath.Join(b.workspace.GenDir, "test_main.gen.go")
		if err := os.WriteFile(testMainPath, []byte(testMainCode), 0644); err != nil {
			return fmt.Errorf("writing test_main.gen.go: %w", err)
		}
		if b.verbose {
			fmt.Println("Generated test_main.gen.go")
		}

		// Step 9: Build the test binary
		testBinary := filepath.Join(b.workspace.Dir, "test-binary")
		if isWindows() {
			testBinary += ".exe"
		}

		// Build only the synthesized main package at gen/ root. Library
		// subpackages under gen/<sub>/ are pulled in transitively via
		// imports; targeting "./gen/..." would ask `go build -o` to write
		// multiple package outputs, which fails.
		args := []string{"build", "-o", testBinary, "./gen"}
		cmd := exec.Command("go", args...)
		cmd.Dir = b.workspace.Dir
		cmd.Env = append(os.Environ(), "GOMODCACHE="+b.config.GoPkgDir)

		if b.verbose {
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
			fmt.Printf("Running: go %s\n", strings.Join(args, " "))
		} else {
			cmd.Stderr = os.Stderr
		}

		if err := cmd.Run(); err != nil {
			return fmt.Errorf("go build (test): %w", err)
		}

		// Step 10: Execute the test binary
		if b.verbose {
			fmt.Println("Running tests...")
			fmt.Println()
		}

		execCmd := exec.Command(testBinary)
		execCmd.Stdin = os.Stdin
		execCmd.Stdout = os.Stdout
		execCmd.Stderr = os.Stderr
		execCmd.Dir = b.workspace.ProjectDir

		if err := execCmd.Run(); err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok {
				return fmt.Errorf("tests failed (exit code %d)", exitErr.ExitCode())
			}
			return fmt.Errorf("running test binary: %w", err)
		}
	}

	return nil
}

// transpileTestMain transpiles source + test files together for package main projects.
// All files are transpiled into gen/ as package main.
func (b *Builder) transpileTestMain(sourceFiles, testFiles []string) error {
	allFiles := append(sourceFiles, testFiles...)
	if err := b.transpileFiles(allFiles, allFiles); err != nil {
		return err
	}

	// The test binary introduces its own synthesized `func main()` (see
	// GenerateTestMain) that drives the test runner. If the user's source
	// also declares `func main()` we'd get a "main redeclared in this block"
	// error at `go build` time. Rename any user-supplied `func main()` out
	// of the way so the generated test_main.gen.go can claim it. This only
	// affects the test-binary build — the user's regular `gala build` output
	// is unaffected because Build() does not call this path.
	sourceOutNames := make(map[string]bool, len(sourceFiles))
	for _, src := range sourceFiles {
		sourceOutNames[testGenFileName(b.workspace.ProjectDir, src)] = true
	}
	if err := renameUserMainInDir(b.workspace.GenDir, sourceOutNames, b.verbose); err != nil {
		return fmt.Errorf("renaming user main for test binary: %w", err)
	}

	// Copy local Go subpackages to gen/ so the test binary compiles
	// when it references types from local Go packages (e.g., httpcore/).
	if err := copyNonGalaFiles(b.workspace.ProjectDir, b.workspace.GenDir, b.verbose); err != nil {
		return fmt.Errorf("copying local Go subpackages: %w", err)
	}

	// Rewrite imports that reference the project's Go module path so that
	// go mod tidy doesn't try to fetch the project module from the remote registry.
	if err := b.rewriteProjectModuleImports(b.workspace.GenDir); err != nil {
		return fmt.Errorf("rewriting project module imports: %w", err)
	}

	return nil
}

// testGenFileName mirrors the naming that transpileFilesToDir applies when
// emitting .gen.go outputs. The returned path is relative to the gen dir and
// uses OS-native separators, mirroring the subdirectory layout of the
// original .gala source so subpackages land in their own gen/<sub>/ folder.
func testGenFileName(projectDir, galaFile string) string {
	relPath, err := filepath.Rel(projectDir, galaFile)
	if err != nil {
		relPath = filepath.Base(galaFile)
	}
	return strings.TrimSuffix(relPath, ".gala") + ".gen.go"
}

// renameUserMainInDir scans .gen.go files under dir (recursively) and renames
// any top-level `func main()` declaration to `_galaUserMain` so it does not
// conflict with the synthesized test runner's main(). The renamed function
// is never called by anyone — the Go compiler tree-shakes it out — but
// preserving it keeps other symbols in the same file (type decls, helpers,
// etc.) compiling cleanly.
//
// Only files whose path (relative to dir, using the OS separator) appears in
// sourceNames are touched; generated test_main.gen.go and the transpiled test
// files must keep their declarations.
func renameUserMainInDir(dir string, sourceNames map[string]bool, verbose bool) error {
	funcMainRegex := "func main()"
	return filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || !strings.HasSuffix(info.Name(), ".gen.go") {
			return nil
		}
		relPath, relErr := filepath.Rel(dir, path)
		if relErr != nil {
			relPath = info.Name()
		}
		if !sourceNames[relPath] {
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("reading %s: %w", path, err)
		}
		text := string(content)
		// Only touch a top-of-line `func main()` signature. Using a plain
		// string match (plus a newline prefix to guard against accidental
		// substring hits inside strings) is sufficient because the transpiler
		// emits `func main()` on its own line at indent 0 when the user
		// writes a package main with a main entry point.
		idx := strings.Index(text, "\n"+funcMainRegex)
		if idx < 0 && !strings.HasPrefix(text, funcMainRegex) {
			return nil
		}
		rewritten := strings.Replace(text, "\nfunc main()", "\nfunc _galaUserMain()", 1)
		if strings.HasPrefix(rewritten, "func main()") {
			rewritten = "func _galaUserMain()" + rewritten[len("func main()"):]
		}
		if rewritten == text {
			return nil
		}
		if err := os.WriteFile(path, []byte(rewritten), 0644); err != nil {
			return fmt.Errorf("writing %s: %w", path, err)
		}
		if verbose {
			fmt.Printf("  test: renamed user func main() in %s\n", relPath)
		}
		return nil
	})
}

// writeLibraryTestHarnesses emits one gala_test_harness_test.go per package
// that contains at least one TestXxx function. The harness lives next to the
// transpiled files in gen/ (preserving subpackage layout) so that `go test
// ./gen/...` picks up each package's tests independently. Each harness only
// references tests declared in its own source directory — bundling all test
// funcs into a single root-level harness would fail to compile when a
// subpackage owns a test that the root package cannot see.
func (b *Builder) writeLibraryTestHarnesses(testFiles []string) error {
	// Group tests by the gen subdirectory they will land in.
	type bucket struct {
		pkgName string
		funcs   []string
	}
	byDir := make(map[string]*bucket)
	for _, tf := range testFiles {
		funcs, err := FindTestFunctions(tf)
		if err != nil {
			return fmt.Errorf("scanning %s for test functions: %w", tf, err)
		}
		if len(funcs) == 0 {
			continue
		}
		relDir, err := filepath.Rel(b.workspace.ProjectDir, filepath.Dir(tf))
		if err != nil {
			relDir = "."
		}
		pkgName := detectPackageName(tf)
		key := filepath.Clean(relDir)
		if bkt, ok := byDir[key]; ok {
			bkt.funcs = append(bkt.funcs, funcs...)
		} else {
			byDir[key] = &bucket{pkgName: pkgName, funcs: append([]string(nil), funcs...)}
		}
	}

	for relDir, bkt := range byDir {
		if bkt.pkgName == "" {
			continue
		}
		harnessDir := b.workspace.GenDir
		if relDir != "." && relDir != "" {
			harnessDir = filepath.Join(b.workspace.GenDir, relDir)
		}
		if err := os.MkdirAll(harnessDir, 0755); err != nil {
			return fmt.Errorf("creating harness dir %s: %w", harnessDir, err)
		}
		harnessPath := filepath.Join(harnessDir, "gala_test_harness_test.go")
		harnessCode := GenerateGoTestHarness(bkt.pkgName, bkt.funcs)
		if err := os.WriteFile(harnessPath, []byte(harnessCode), 0644); err != nil {
			return fmt.Errorf("writing %s: %w", harnessPath, err)
		}
	}
	return nil
}

// transpileTestLibrary transpiles source files and test files into gen/ as the
// same package. This enables internal tests that can access unexported identifiers.
// Test files are transpiled alongside source files in the same directory.
func (b *Builder) transpileTestLibrary(sourceFiles, testFiles []string) error {
	// Transpile source files into gen/ as the library package
	if err := b.transpileFilesToDir(sourceFiles, sourceFiles, b.workspace.GenDir); err != nil {
		return fmt.Errorf("transpiling source files: %w", err)
	}

	// Copy local Go subpackages to gen/ so the library compiles
	if err := copyNonGalaFiles(b.workspace.ProjectDir, b.workspace.GenDir, b.verbose); err != nil {
		return fmt.Errorf("copying local Go subpackages: %w", err)
	}

	// Transpile test files into gen/ (same directory, same package as library).
	// Test files see source files as siblings for type resolution.
	allFiles := append(sourceFiles, testFiles...)
	if err := b.transpileFilesToDir(testFiles, allFiles, b.workspace.GenDir); err != nil {
		return fmt.Errorf("transpiling test files: %w", err)
	}

	// Rewrite imports that reference the project's Go module path.
	// This covers both source and test files in gen/ uniformly.
	if err := b.rewriteProjectModuleImports(b.workspace.GenDir); err != nil {
		return fmt.Errorf("rewriting project module imports: %w", err)
	}

	return nil
}

// rewriteTestFilesAsMain rewrites all .gen.go files in the given directory:
// changes "package <name>" to "package main" and adds a dot-import of the
// library from gala-build-workspace/gen.
func rewriteTestFilesAsMain(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".gen.go") {
			continue
		}

		filePath := filepath.Join(dir, entry.Name())
		content, err := os.ReadFile(filePath)
		if err != nil {
			return fmt.Errorf("reading %s: %w", entry.Name(), err)
		}

		rewritten := rewritePackageToMain(string(content))
		if err := os.WriteFile(filePath, []byte(rewritten), 0644); err != nil {
			return fmt.Errorf("writing %s: %w", entry.Name(), err)
		}
	}

	return nil
}

// rewritePackageToMain changes the package declaration to "main" and injects
// a dot-import of the library package (gala-build-workspace/gen).
func rewritePackageToMain(code string) string {
	lines := strings.Split(code, "\n")
	var result []string
	packageReplaced := false
	importAdded := false

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		// Replace package declaration
		if !packageReplaced && strings.HasPrefix(trimmed, "package ") {
			result = append(result, "package main")
			packageReplaced = true
			continue
		}

		// After seeing import block, inject dot-import as first entry
		if !importAdded && packageReplaced {
			if trimmed == "import (" {
				result = append(result, line)
				result = append(result, "\t. \"gala-build-workspace/gen\"")
				importAdded = true
				continue
			}
			// Single import statement — convert to block with our dot-import
			if strings.HasPrefix(trimmed, "import ") && strings.Contains(trimmed, "\"") {
				start := strings.Index(trimmed, "\"")
				end := strings.LastIndex(trimmed, "\"")
				if start >= 0 && end > start {
					impPath := trimmed[start+1 : end]
					result = append(result, "import (")
					result = append(result, "\t. \"gala-build-workspace/gen\"")
					if strings.Contains(trimmed[:start], ".") {
						result = append(result, fmt.Sprintf("\t. \"%s\"", impPath))
					} else {
						alias := strings.TrimSpace(trimmed[len("import "):start])
						if alias != "" {
							result = append(result, fmt.Sprintf("\t%s \"%s\"", alias, impPath))
						} else {
							result = append(result, fmt.Sprintf("\t\"%s\"", impPath))
						}
					}
					result = append(result, ")")
					importAdded = true
					continue
				}
			}
		}

		result = append(result, line)
	}

	// If no import was found at all, add one after the package line
	if !importAdded {
		var final []string
		for _, line := range result {
			final = append(final, line)
			if strings.TrimSpace(line) == "package main" {
				final = append(final, "")
				final = append(final, "import . \"gala-build-workspace/gen\"")
			}
		}
		result = final
	}

	return strings.Join(result, "\n")
}

// transpileFiles transpiles the given files into the workspace gen directory.
// allSiblings is the full list of files for sibling-based type resolution.
func (b *Builder) transpileFiles(files []string, allSiblings []string) error {
	return b.transpileFilesToDir(files, allSiblings, b.workspace.GenDir)
}

// transpileFilesToDir transpiles the given files into the specified output directory.
//
// When the input set spans multiple source directories (e.g. a library with
// subpackages), each file is only given siblings from the SAME directory.
// Mixing files from different Go-level packages into one sibling list would
// feed the batch analyzer parse trees that disagree on `package <name>`, which
// in turn bubbles up as an ANTLR prediction-context panic once the analyzer
// tries to cross-reference those trees. The sub-directory layout is preserved
// under outDir so that `go test ./...` can see each subpackage as its own Go
// package.
func (b *Builder) transpileFilesToDir(files []string, allSiblings []string, outDir string) error {
	stdlibDir := b.config.StdlibVersionDir(b.stdlibVersion)
	searchPaths := []string{b.workspace.ProjectDir, stdlibDir}

	for _, req := range b.galaMod.GalaRequires() {
		searchPaths = append(searchPaths, b.effectiveDepDir(req))
	}

	p := transpiler.NewAntlrGalaParser()
	tr := transformer.NewGalaASTTransformer()
	g := generator.NewGoCodeGenerator()
	batchAnalyzer := analyzer.NewBatchAnalyzer(p, searchPaths, b.workspace.ProjectDir)
	batchAnalyzer.SetGoSrcDirs(b.goModuleSrcDirs())

	// Group sibling candidates by source directory so we can cheaply look up
	// "other .gala files in my own package" without re-scanning allSiblings on
	// every iteration.
	siblingsByDir := make(map[string][]string)
	for _, s := range allSiblings {
		d := filepath.Dir(s)
		siblingsByDir[d] = append(siblingsByDir[d], s)
	}

	for _, galaFile := range files {
		content, err := os.ReadFile(galaFile)
		if err != nil {
			return fmt.Errorf("reading %s: %w", galaFile, err)
		}

		var siblings []string
		for _, other := range siblingsByDir[filepath.Dir(galaFile)] {
			if other != galaFile {
				siblings = append(siblings, other)
			}
		}
		batchAnalyzer.SetPackageFiles(siblings)

		t := transpiler.NewGalaToGoTranspiler(p, batchAnalyzer, tr, g)

		goCode, err := t.Transpile(string(content), galaFile)
		if err != nil {
			return fmt.Errorf("transpiling %s: %w", galaFile, err)
		}

		relPath, err := filepath.Rel(b.workspace.ProjectDir, galaFile)
		if err != nil {
			relPath = filepath.Base(galaFile)
		}
		// Preserve subdirectory layout under outDir. Each GALA subpackage
		// lands in its own directory so Go can resolve imports of the form
		// "gala-build-workspace/gen/<sub>" cleanly.
		outName := strings.TrimSuffix(relPath, ".gala") + ".gen.go"
		outPath := filepath.Join(outDir, outName)

		if err := os.MkdirAll(filepath.Dir(outPath), 0755); err != nil {
			return fmt.Errorf("creating gen dir for %s: %w", outName, err)
		}
		if err := os.WriteFile(outPath, []byte(goCode), 0644); err != nil {
			return fmt.Errorf("writing %s: %w", outPath, err)
		}

		if b.verbose {
			fmt.Printf("  %s -> %s\n", relPath, outName)
		}
	}

	return nil
}

// rootPackageName returns the GALA package name declared by a source file
// that lives in projectDir (i.e. not under a subdirectory). If no such file
// exists the function falls back to the first source file, preserving the
// original single-package behaviour. Returns "" when no source files exist.
func rootPackageName(sourceFiles []string, projectDir string) string {
	absRoot, _ := filepath.Abs(projectDir)
	for _, f := range sourceFiles {
		abs, _ := filepath.Abs(f)
		if filepath.Dir(abs) == absRoot {
			return detectPackageName(f)
		}
	}
	if len(sourceFiles) > 0 {
		return detectPackageName(sourceFiles[0])
	}
	return ""
}

// detectPackageName reads the first line matching "package <name>" from a .gala file.
func detectPackageName(galaFile string) string {
	content, err := os.ReadFile(galaFile)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(content), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "package ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "package "))
		}
	}
	return ""
}

// findGalaTestFiles finds all _test.gala files in the given directory (non-recursive).
func findGalaTestFiles(dir string) ([]string, error) {
	var files []string

	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if strings.HasSuffix(entry.Name(), "_test.gala") {
			files = append(files, filepath.Join(dir, entry.Name()))
		}
	}

	return files, nil
}

// findGalaTestFilesRecursive finds all _test.gala files recursively, skipping
// the same set of hidden/build directories as findGalaFilesRecursive. This is
// used by `gala test` so that tests declared in a subpackage (e.g.
// state/state_test.gala) are discovered alongside tests at the project root.
func findGalaTestFilesRecursive(dir string) ([]string, error) {
	var files []string

	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if info.IsDir() {
			name := info.Name()
			if name != "." && (strings.HasPrefix(name, ".") || name == "vendor" ||
				name == "testdata" || strings.HasPrefix(name, "bazel-") || name == "_gala") {
				return filepath.SkipDir
			}
			return nil
		}

		if strings.HasSuffix(path, "_test.gala") {
			files = append(files, path)
		}

		return nil
	})

	return files, err
}

// isWindows returns true if running on Windows.
func isWindows() bool {
	return os.PathSeparator == '\\'
}
