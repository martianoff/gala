package build

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"martianoff/gala/internal/depman/mod"
	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/analyzer"
	"martianoff/gala/internal/transpiler/generator"
	"martianoff/gala/internal/transpiler/transformer"
)

// DepTranspiler handles transpilation of GALA library dependencies.
type DepTranspiler struct {
	config        *Config
	workspace     *Workspace
	galaMod       *mod.File
	stdlibVersion string
	verbose       bool
}

// NewDepTranspiler creates a new dependency transpiler.
func NewDepTranspiler(config *Config, workspace *Workspace, galaMod *mod.File, stdlibVersion string, verbose bool) *DepTranspiler {
	return &DepTranspiler{
		config:        config,
		workspace:     workspace,
		galaMod:       galaMod,
		stdlibVersion: stdlibVersion,
		verbose:       verbose,
	}
}

// TranspileDeps transpiles all GALA dependencies and returns a map of
// internalModulePath -> transpiled directory path.
// The map is keyed by the dependency's declared module path (from its gala.mod),
// which is the path used in generated Go imports.
func (dt *DepTranspiler) TranspileDeps() (map[string]string, error) {
	// Collect all GALA deps (direct + transitive)
	allDeps := make(map[string]mod.Require)
	visited := make(map[string]bool)
	dt.collectGalaDeps(dt.galaMod, allDeps, visited)

	if len(allDeps) == 0 {
		return nil, nil
	}

	if dt.verbose {
		fmt.Printf("Found %d GALA dependencies to transpile\n", len(allDeps))
	}

	transpiledDirs := make(map[string]string)

	for _, dep := range allDeps {
		dir, err := dt.transpileSingleDep(dep, transpiledDirs)
		if err != nil {
			return nil, fmt.Errorf("transpiling dependency %s@%s: %w", dep.Path, dep.Version, err)
		}
		if dir != "" {
			// Key by the dep's internal module path (from its gala.mod),
			// since that's what the transpiled Go code imports.
			modPath := dt.resolveInternalModulePath(dep)
			transpiledDirs[modPath] = dir
		}
	}

	return transpiledDirs, nil
}

// resolveInternalModulePath reads a dependency's gala.mod to get its declared
// module path. Falls back to dep.Path if not found.
func (dt *DepTranspiler) resolveInternalModulePath(dep mod.Require) string {
	return resolveDepInternalModulePathAt(dt.effectiveDepDir(dep), dep)
}

// effectiveDepDir returns the source directory for a dependency, honoring
// local replace directives in the active gala.mod. Replace entries that
// point to another module version still fall back to the cache.
func (dt *DepTranspiler) effectiveDepDir(dep mod.Require) string {
	return resolveEffectiveDepDir(dt.config, dt.galaMod, dt.workspace.ProjectDir, dep)
}

// collectGalaDeps recursively collects all GALA dependencies.
func (dt *DepTranspiler) collectGalaDeps(f *mod.File, allDeps map[string]mod.Require, visited map[string]bool) {
	if f == nil {
		return
	}

	for _, req := range f.Require {
		// Skip Go dependencies
		if req.Go {
			continue
		}

		key := req.Path + "@" + req.Version
		if visited[key] {
			continue
		}
		visited[key] = true

		// Check if cached dir (or local replacement) has .gala files
		cachedDir := dt.effectiveDepDir(req)
		galaFiles, err := findGalaFiles(cachedDir)
		if err != nil || len(galaFiles) == 0 {
			// No .gala files — pure Go package, skip transpilation
			continue
		}

		allDeps[req.Path] = req

		// Read dep's gala.mod for transitive deps
		depGalaModPath := filepath.Join(cachedDir, "gala.mod")
		if depMod, err := mod.ParseFile(depGalaModPath); err == nil {
			dt.collectGalaDeps(depMod, allDeps, visited)
		}
	}
}

// transpileSingleDep transpiles a single GALA dependency and returns the output directory.
// Subpackages (e.g. gala-tui's state/) are transpiled into matching subdirectories
// of outDir so the dep's own go.mod covers them as part of the same Go module.
func (dt *DepTranspiler) transpileSingleDep(dep mod.Require, transpiledDirs map[string]string) (string, error) {
	srcDir := dt.effectiveDepDir(dep)

	galaFiles, err := findGalaFilesRecursive(srcDir)
	if err != nil {
		return "", fmt.Errorf("finding gala files in %s: %w", srcDir, err)
	}
	if len(galaFiles) == 0 {
		return "", nil
	}

	// Group files by subpackage directory. Each subdirectory is its own
	// GALA package — its sibling list spans only files in the same dir,
	// not the whole module. (Cross-package references go through imports.)
	filesByPackageDir := make(map[string][]string)
	for _, f := range galaFiles {
		dir := filepath.Dir(f)
		filesByPackageDir[dir] = append(filesByPackageDir[dir], f)
	}

	if dt.verbose {
		nPkgs := len(filesByPackageDir)
		fmt.Printf("  Transpiling dependency: %s@%s (%d files across %d package(s))\n",
			dep.Path, dep.Version, len(galaFiles), nPkgs)
	}

	// Set up output directory
	outDir := dt.workspace.DepModuleDir(dep.Path, dep.Version)
	if err := os.MkdirAll(outDir, 0755); err != nil {
		return "", fmt.Errorf("creating dep output dir: %w", err)
	}

	// Set up search paths: source dir, stdlib, and source dirs of dep's own GALA deps.
	// The single search path covers all subpackages because the analyzer can resolve
	// `import "github.com/foo/bar/sub"` against bar's source-cache root.
	stdlibDir := dt.config.StdlibVersionDir(dt.stdlibVersion)
	searchPaths := []string{srcDir, stdlibDir}

	// Add source dirs of dep's own GALA dependencies
	depGalaModPath := filepath.Join(srcDir, "gala.mod")
	if depMod, err := mod.ParseFile(depGalaModPath); err == nil {
		for _, depReq := range depMod.GalaRequires() {
			depSrcDir := dt.effectiveDepDir(depReq)
			searchPaths = append(searchPaths, depSrcDir)
		}
	}

	// Create transpiler pipeline with BatchAnalyzer for shared cache
	p := transpiler.NewAntlrGalaParser()
	tr := transformer.NewGalaASTTransformer()
	g := generator.NewGoCodeGenerator()
	batchAnalyzer := analyzer.NewBatchAnalyzer(p, searchPaths, srcDir)

	// Process subpackages in deterministic order for stable verbose output.
	pkgDirs := make([]string, 0, len(filesByPackageDir))
	for d := range filesByPackageDir {
		pkgDirs = append(pkgDirs, d)
	}
	sort.Strings(pkgDirs)

	for _, pkgDir := range pkgDirs {
		pkgFiles := filesByPackageDir[pkgDir]

		// Compute the relative path from srcDir to pkgDir; "" for the root package,
		// "state" for a state/ subpackage, "demo/foo" for nested demos, etc.
		relPkg, err := filepath.Rel(srcDir, pkgDir)
		if err != nil {
			return "", fmt.Errorf("computing relative path for %s: %w", pkgDir, err)
		}
		if relPkg == "." {
			relPkg = ""
		}

		// Subpackage output directory: outDir/state/, outDir/demo/, etc.
		pkgOutDir := outDir
		if relPkg != "" {
			pkgOutDir = filepath.Join(outDir, relPkg)
			if err := os.MkdirAll(pkgOutDir, 0755); err != nil {
				return "", fmt.Errorf("creating subpackage output dir %s: %w", pkgOutDir, err)
			}
		}

		for _, galaFile := range pkgFiles {
			content, err := os.ReadFile(galaFile)
			if err != nil {
				return "", fmt.Errorf("reading %s: %w", galaFile, err)
			}

			// Siblings: other files in the same subpackage directory only.
			var siblings []string
			for _, other := range pkgFiles {
				if other != galaFile {
					siblings = append(siblings, other)
				}
			}
			batchAnalyzer.SetPackageFiles(siblings)

			t := transpiler.NewGalaToGoTranspiler(p, batchAnalyzer, tr, g)

			goCode, err := t.Transpile(string(content), galaFile)
			if err != nil {
				return "", fmt.Errorf("transpiling %s: %w", galaFile, err)
			}

			outName := strings.TrimSuffix(filepath.Base(galaFile), ".gala") + ".gen.go"
			outPath := filepath.Join(pkgOutDir, outName)

			if err := os.WriteFile(outPath, []byte(goCode), 0644); err != nil {
				return "", fmt.Errorf("writing %s: %w", outPath, err)
			}

			if dt.verbose {
				if relPkg == "" {
					fmt.Printf("    %s -> %s\n", filepath.Base(galaFile), outName)
				} else {
					fmt.Printf("    %s/%s -> %s/%s\n", relPkg, filepath.Base(galaFile), relPkg, outName)
				}
			}
		}
	}

	// Copy non-.gala files (especially .go subpackages) from source to output
	if err := copyNonGalaFiles(srcDir, outDir, dt.verbose); err != nil {
		return "", fmt.Errorf("copying non-gala files for %s: %w", dep.Path, err)
	}

	// Generate go.mod for the dependency
	if err := dt.generateDepGoMod(dep, outDir, transpiledDirs); err != nil {
		return "", fmt.Errorf("generating go.mod for %s: %w", dep.Path, err)
	}

	return outDir, nil
}

// generateDepGoMod generates a go.mod file for a transpiled dependency.
func (dt *DepTranspiler) generateDepGoMod(dep mod.Require, outDir string, transpiledDirs map[string]string) error {
	var sb strings.Builder

	// Use the dep's internal module path (from its gala.mod) as the Go module name,
	// since that's what the transpiled Go code imports.
	modPath := dt.resolveInternalModulePath(dep)

	sb.WriteString("// Code generated by GALA build system. DO NOT EDIT.\n")
	sb.WriteString(fmt.Sprintf("module %s\n\n", modPath))
	sb.WriteString("go 1.22\n\n")

	// Scan generated Go files for imports
	imports, err := CollectImports(outDir)
	if err != nil {
		return fmt.Errorf("collecting imports: %w", err)
	}

	// Build a mapping from require path -> internal module path for this dep's own GALA deps
	srcDir := dt.effectiveDepDir(dep)
	depGalaModPath := filepath.Join(srcDir, "gala.mod")

	// depInternalPaths maps requirePath -> internalModulePath for the dep's GALA dependencies
	depInternalPaths := make(map[string]string)
	var depGalaReqs []mod.Require
	var depGoReqs []mod.Require
	if depMod, parseErr := mod.ParseFile(depGalaModPath); parseErr == nil {
		depGalaReqs = depMod.GalaRequires()
		depGoReqs = depMod.GoRequires()
		for _, depReq := range depGalaReqs {
			internalPath := dt.resolveInternalModulePath(depReq)
			if internalPath != depReq.Path {
				depInternalPaths[depReq.Path] = internalPath
			}
		}
	}

	// Classify imports
	var stdlibReqs []string
	type galaDepEntry struct {
		req        mod.Require
		importPath string // the internal module path used in Go imports
	}
	var galaDepReqs []galaDepEntry
	// goReqs maps a resolved Go *module* path -> its required version. A scanned
	// import is a *package* path; its module may be a proper prefix of it (e.g.
	// the package golang.org/x/sys/windows belongs to module golang.org/x/sys).
	// Keying by module path also dedupes multiple imported subpackages of the
	// same module.
	goReqs := make(map[string]string)

	for _, imp := range imports {
		if IsGoStdlibImport(imp) {
			continue
		}
		if IsStdlibImport(imp) {
			stdlibReqs = append(stdlibReqs, imp)
			continue
		}
		// Skip self-prefixed imports (subpackages of the current dep, e.g.
		// gala-tui's `github.com/martianoff/gala-tui/state`). They live in
		// the same Go module as the parent and don't need a separate require.
		if imp == modPath || strings.HasPrefix(imp, modPath+"/") {
			continue
		}
		// Check if it's a known GALA dependency (match by internal module path or require path)
		found := false
		for _, depReq := range depGalaReqs {
			internalPath := depReq.Path
			if ip, ok := depInternalPaths[depReq.Path]; ok {
				internalPath = ip
			}
			if strings.HasPrefix(imp, internalPath) || strings.HasPrefix(imp, depReq.Path) {
				galaDepReqs = append(galaDepReqs, galaDepEntry{req: depReq, importPath: internalPath})
				found = true
				break
			}
		}
		if found {
			continue
		}
		// Plain Go import. Resolve it to the module that provides it using the
		// dep's declared `// go` requires (longest module-path prefix), and
		// require THAT module at its declared version. Emitting the full import
		// path as a module at a fake v0.0.0 makes `go build` try to download a
		// nonexistent module (e.g. golang.org/x/sys/windows v0.0.0). Imports
		// with no declared module prefix are left out entirely — the workspace's
		// `go mod tidy` resolves their real versions across the whole graph.
		if goModPath, version, ok := resolveGoModuleForImport(imp, depGoReqs); ok {
			goReqs[goModPath] = version
		}
	}

	// Stable, deduped ordering for the generated require block.
	goReqPaths := make([]string, 0, len(goReqs))
	for p := range goReqs {
		goReqPaths = append(goReqPaths, p)
	}
	sort.Strings(goReqPaths)

	// Write require block
	if len(stdlibReqs) > 0 || len(galaDepReqs) > 0 || len(goReqPaths) > 0 {
		sb.WriteString("require (\n")
		for _, imp := range stdlibReqs {
			sb.WriteString(fmt.Sprintf("\t%s v0.0.0\n", imp))
		}
		for _, entry := range galaDepReqs {
			sb.WriteString(fmt.Sprintf("\t%s v0.0.0\n", entry.importPath))
		}
		for _, goModPath := range goReqPaths {
			sb.WriteString(fmt.Sprintf("\t%s %s\n", goModPath, goReqs[goModPath]))
		}
		sb.WriteString(")\n\n")
	}

	// Write replace directives
	stdlibDir := dt.config.StdlibVersionDir(dt.stdlibVersion)

	// Stdlib replaces
	for _, imp := range stdlibReqs {
		// Find the package name from the import path
		for pkg, importPath := range StdlibImportPaths {
			if importPath == imp {
				absPath := filepath.ToSlash(filepath.Join(stdlibDir, pkg))
				sb.WriteString(fmt.Sprintf("replace %s => %s\n", imp, absPath))
				break
			}
		}
	}

	// GALA dep replaces — use internal module path
	for _, entry := range galaDepReqs {
		if dir, ok := transpiledDirs[entry.importPath]; ok {
			absPath := filepath.ToSlash(dir)
			sb.WriteString(fmt.Sprintf("replace %s => %s\n", entry.importPath, absPath))
		} else {
			// Fallback to source cache (or local replacement)
			absPath := filepath.ToSlash(dt.effectiveDepDir(entry.req))
			sb.WriteString(fmt.Sprintf("replace %s => %s\n", entry.importPath, absPath))
		}
	}

	goModPath := filepath.Join(outDir, "go.mod")
	return os.WriteFile(goModPath, []byte(sb.String()), 0644)
}

// resolveGoModuleForImport maps a scanned Go import (a package path) to the
// declared Go module that provides it. A module path can be a proper prefix of
// the import path — e.g. the package "golang.org/x/sys/windows" is provided by
// module "golang.org/x/sys". It matches against the dep's declared `// go`
// requires, choosing the longest module-path prefix (the same rule Go uses),
// and returns that module's path, its declared version, and whether a match was
// found. Imports with no declared module prefix return ok=false so the caller
// can omit them and let the workspace's `go mod tidy` resolve them globally.
func resolveGoModuleForImport(importPath string, goReqs []mod.Require) (string, string, bool) {
	bestPath := ""
	bestVersion := ""
	for _, r := range goReqs {
		if importPath == r.Path || strings.HasPrefix(importPath, r.Path+"/") {
			if len(r.Path) > len(bestPath) {
				bestPath = r.Path
				bestVersion = r.Version
			}
		}
	}
	if bestPath == "" {
		return "", "", false
	}
	return bestPath, bestVersion, true
}

// copyNonGalaFiles copies non-.gala files and subdirectories from srcDir to dstDir.
// This ensures that pure Go subpackages within a GALA module are available in the
// transpiled output directory alongside the generated .gen.go files.
func copyNonGalaFiles(srcDir, dstDir string, verbose bool) error {
	return filepath.Walk(srcDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			// Skip entries that can't be stat'd (e.g., Bazel junctions on Windows
			// give "Incorrect function" when filepath.Walk tries to read them).
			// The bazel-* directory name check below would handle these, but
			// filepath.Walk reports the error before we can check the name.
			return nil
		}

		// Skip the root directory itself
		if path == srcDir {
			return nil
		}

		relPath, err := filepath.Rel(srcDir, path)
		if err != nil {
			return err
		}

		// Skip symlinks entirely — Bazel creates symlinks (Linux) or junctions
		// (Windows) that may point to nonexistent targets. filepath.Walk uses
		// os.Lstat so it sees symlinks as entries but os.ReadFile would fail.
		if info.Mode()&os.ModeSymlink != 0 {
			return nil
		}

		// Skip bazel directories/junctions regardless of how the OS reports them.
		// On Windows, junctions may not have ModeDir set, so check the name
		// before the IsDir() gate.
		name := info.Name()
		if strings.HasPrefix(name, "bazel-") {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		// Skip hidden directories, vendor, testdata
		if info.IsDir() {
			if strings.HasPrefix(name, ".") || name == "vendor" ||
				name == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}

		// Skip .gala files (already transpiled), .gen.go files (stale transpiler
		// output that may exist in the project dir), and gala.mod
		if strings.HasSuffix(info.Name(), ".gala") || strings.HasSuffix(info.Name(), ".gen.go") ||
			info.Name() == "gala.mod" {
			return nil
		}

		// Skip go.mod and go.sum from source (we generate our own)
		if info.Name() == "go.mod" || info.Name() == "go.sum" {
			return nil
		}

		dstPath := filepath.Join(dstDir, relPath)

		// Ensure destination directory exists
		if err := os.MkdirAll(filepath.Dir(dstPath), 0755); err != nil {
			return fmt.Errorf("creating directory for %s: %w", relPath, err)
		}

		// Skip if destination already exists (don't overwrite .gen.go files)
		if _, err := os.Stat(dstPath); err == nil {
			return nil
		}

		// Copy the file
		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("reading %s: %w", path, err)
		}

		if err := os.WriteFile(dstPath, data, 0644); err != nil {
			return fmt.Errorf("writing %s: %w", dstPath, err)
		}

		if verbose {
			fmt.Printf("    copy: %s\n", relPath)
		}

		return nil
	})
}
