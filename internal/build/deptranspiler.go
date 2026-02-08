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
// modulePath -> transpiled directory path.
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
			transpiledDirs[dep.Path] = dir
		}
	}

	return transpiledDirs, nil
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

	// Create transpiler pipeline
	p := transpiler.NewAntlrGalaParser()
	tr := transformer.NewGalaASTTransformer()
	g := generator.NewGoCodeGenerator()

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

		var a transpiler.Analyzer
		if len(siblings) > 0 {
			a = analyzer.NewGalaAnalyzerWithPackageFiles(p, searchPaths, siblings)
		} else {
			a = analyzer.NewGalaAnalyzer(p, searchPaths)
		}
		t := transpiler.NewGalaToGoTranspiler(p, a, tr, g)

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

	// Generate go.mod for the dependency
	if err := dt.generateDepGoMod(dep, outDir, transpiledDirs); err != nil {
		return "", fmt.Errorf("generating go.mod for %s: %w", dep.Path, err)
	}

	return outDir, nil
}

// generateDepGoMod generates a go.mod file for a transpiled dependency.
func (dt *DepTranspiler) generateDepGoMod(dep mod.Require, outDir string, transpiledDirs map[string]string) error {
	var sb strings.Builder

	sb.WriteString("// Code generated by GALA build system. DO NOT EDIT.\n")
	sb.WriteString(fmt.Sprintf("module %s\n\n", dep.Path))
	sb.WriteString("go 1.21\n\n")

	// Scan generated Go files for imports
	imports, err := CollectImports(outDir)
	if err != nil {
		return fmt.Errorf("collecting imports: %w", err)
	}

	// Classify imports
	var stdlibReqs []string
	var galaDepReqs []mod.Require
	var goReqs []string

	for _, imp := range imports {
		if IsGoStdlibImport(imp) {
			continue
		}
		if IsStdlibImport(imp) {
			stdlibReqs = append(stdlibReqs, imp)
			continue
		}
		// Check if it's a known GALA dependency
		found := false
		srcDir := dt.config.GalaModulePath(dep.Path, dep.Version)
		depGalaModPath := filepath.Join(srcDir, "gala.mod")
		if depMod, parseErr := mod.ParseFile(depGalaModPath); parseErr == nil {
			for _, depReq := range depMod.GalaRequires() {
				if strings.HasPrefix(imp, depReq.Path) {
					galaDepReqs = append(galaDepReqs, depReq)
					found = true
					break
				}
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
		for _, req := range galaDepReqs {
			sb.WriteString(fmt.Sprintf("\t%s %s\n", req.Path, req.Version))
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

	// GALA dep replaces
	for _, req := range galaDepReqs {
		if dir, ok := transpiledDirs[req.Path]; ok {
			absPath := filepath.ToSlash(dir)
			sb.WriteString(fmt.Sprintf("replace %s => %s\n", req.Path, absPath))
		} else {
			// Fallback to source cache
			absPath := filepath.ToSlash(dt.config.GalaModulePath(req.Path, req.Version))
			sb.WriteString(fmt.Sprintf("replace %s => %s\n", req.Path, absPath))
		}
	}

	goModPath := filepath.Join(outDir, "go.mod")
	return os.WriteFile(goModPath, []byte(sb.String()), 0644)
}
