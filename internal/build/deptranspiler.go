package build

import (
	"fmt"
	"os"
	"path/filepath"
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
	cachedDir := dt.config.GalaModulePath(dep.Path, dep.Version)
	galaModPath := filepath.Join(cachedDir, "gala.mod")
	if depMod, err := mod.ParseFile(galaModPath); err == nil {
		if depMod.Module.Path != "" {
			return depMod.Module.Path
		}
	}
	return dep.Path
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

		// Check if cached dir has .gala files
		cachedDir := dt.config.GalaModulePath(req.Path, req.Version)
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
func (dt *DepTranspiler) transpileSingleDep(dep mod.Require, transpiledDirs map[string]string) (string, error) {
	srcDir := dt.config.GalaModulePath(dep.Path, dep.Version)

	galaFiles, err := findGalaFiles(srcDir)
	if err != nil {
		return "", fmt.Errorf("finding gala files in %s: %w", srcDir, err)
	}
	if len(galaFiles) == 0 {
		return "", nil
	}

	if dt.verbose {
		fmt.Printf("  Transpiling dependency: %s@%s (%d files)\n", dep.Path, dep.Version, len(galaFiles))
	}

	// Set up output directory
	outDir := dt.workspace.DepModuleDir(dep.Path, dep.Version)
	if err := os.MkdirAll(outDir, 0755); err != nil {
		return "", fmt.Errorf("creating dep output dir: %w", err)
	}

	// Set up search paths: source dir, stdlib, and source dirs of dep's own GALA deps
	stdlibDir := dt.config.StdlibVersionDir(dt.stdlibVersion)
	searchPaths := []string{srcDir, stdlibDir}

	// Add source dirs of dep's own GALA dependencies
	depGalaModPath := filepath.Join(srcDir, "gala.mod")
	if depMod, err := mod.ParseFile(depGalaModPath); err == nil {
		for _, depReq := range depMod.GalaRequires() {
			depSrcDir := dt.config.GalaModulePath(depReq.Path, depReq.Version)
			searchPaths = append(searchPaths, depSrcDir)
		}
	}

	// Create transpiler pipeline with BatchAnalyzer for shared cache
	p := transpiler.NewAntlrGalaParser()
	tr := transformer.NewGalaASTTransformer()
	g := generator.NewGoCodeGenerator()
	batchAnalyzer := analyzer.NewBatchAnalyzer(p, searchPaths, srcDir)

	for _, galaFile := range galaFiles {
		content, err := os.ReadFile(galaFile)
		if err != nil {
			return "", fmt.Errorf("reading %s: %w", galaFile, err)
		}

		// Compute sibling files for multi-file package support
		var siblings []string
		for _, other := range galaFiles {
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

		// Generate output filename
		outName := strings.TrimSuffix(filepath.Base(galaFile), ".gala") + ".gen.go"
		outPath := filepath.Join(outDir, outName)

		if err := os.WriteFile(outPath, []byte(goCode), 0644); err != nil {
			return "", fmt.Errorf("writing %s: %w", outPath, err)
		}

		if dt.verbose {
			fmt.Printf("    %s -> %s\n", filepath.Base(galaFile), outName)
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
	srcDir := dt.config.GalaModulePath(dep.Path, dep.Version)
	depGalaModPath := filepath.Join(srcDir, "gala.mod")

	// depInternalPaths maps requirePath -> internalModulePath for the dep's GALA dependencies
	depInternalPaths := make(map[string]string)
	var depGalaReqs []mod.Require
	if depMod, parseErr := mod.ParseFile(depGalaModPath); parseErr == nil {
		depGalaReqs = depMod.GalaRequires()
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
	var goReqs []string

	for _, imp := range imports {
		if IsGoStdlibImport(imp) {
			continue
		}
		if IsStdlibImport(imp) {
			stdlibReqs = append(stdlibReqs, imp)
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
		if !found {
			goReqs = append(goReqs, imp)
		}
	}

	// Write require block
	if len(stdlibReqs) > 0 || len(galaDepReqs) > 0 || len(goReqs) > 0 {
		sb.WriteString("require (\n")
		for _, imp := range stdlibReqs {
			sb.WriteString(fmt.Sprintf("\t%s v0.0.0\n", imp))
		}
		for _, entry := range galaDepReqs {
			sb.WriteString(fmt.Sprintf("\t%s v0.0.0\n", entry.importPath))
		}
		for _, imp := range goReqs {
			sb.WriteString(fmt.Sprintf("\t%s v0.0.0\n", imp))
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
			// Fallback to source cache
			absPath := filepath.ToSlash(dt.config.GalaModulePath(entry.req.Path, entry.req.Version))
			sb.WriteString(fmt.Sprintf("replace %s => %s\n", entry.importPath, absPath))
		}
	}

	goModPath := filepath.Join(outDir, "go.mod")
	return os.WriteFile(goModPath, []byte(sb.String()), 0644)
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
