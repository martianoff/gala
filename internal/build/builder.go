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
	"martianoff/gala/internal/stdlib"
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

	// Load gala.mod
	galaModPath := filepath.Join(projectDir, "gala.mod")
	galaMod, err := mod.ParseFile(galaModPath)
	if err != nil {
		return nil, fmt.Errorf("parsing gala.mod: %w", err)
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

	// Step 5: Run go build (executable)
	finalPath, err := b.goBuild(outputPath)
	if err != nil {
		return "", fmt.Errorf("go build: %w", err)
	}

	return finalPath, nil
}

// ensureDeps fetches any GALA dependencies that are not yet cached locally.
func (b *Builder) ensureDeps() error {
	galaReqs := b.galaMod.GalaRequires()
	if len(galaReqs) == 0 {
		return nil
	}

	config := fetch.DefaultConfig()
	cache := fetch.NewCache(config)
	fetcher := fetch.NewGitFetcher(cache)

	for _, req := range galaReqs {
		modDir := b.config.GalaModulePath(req.Path, req.Version)
		if _, err := os.Stat(modDir); err == nil {
			continue // Already cached
		}
		if b.verbose {
			fmt.Printf("Fetching %s@%s...\n", req.Path, req.Version)
		}
		if _, _, err := fetcher.Fetch(req.Path, req.Version); err != nil {
			return fmt.Errorf("fetching %s@%s: %w", req.Path, req.Version, err)
		}
	}
	return nil
}

// ensureStdlib extracts the stdlib to the versioned cache if not present.
func (b *Builder) ensureStdlib() error {
	stdlibDir := b.config.StdlibVersionDir(b.stdlibVersion)

	// Check if already extracted
	markerPath := filepath.Join(stdlibDir, ".stdlib-extracted")
	if _, err := os.Stat(markerPath); err == nil {
		if b.verbose {
			fmt.Printf("Stdlib already extracted at: %s\n", stdlibDir)
		}
		return nil
	}

	if b.verbose {
		fmt.Printf("Extracting stdlib to: %s\n", stdlibDir)
	}

	// Extract stdlib (includes go.mod files for each package)
	if err := stdlib.ExtractTo(stdlibDir); err != nil {
		return fmt.Errorf("extracting stdlib: %w", err)
	}

	// Write marker file
	if err := os.WriteFile(markerPath, []byte(b.stdlibVersion), 0644); err != nil {
		return fmt.Errorf("writing marker: %w", err)
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

	// Find all .gala files in the project
	galaFiles, err := findGalaFiles(b.workspace.ProjectDir)
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
		searchPaths = append(searchPaths, b.config.GalaModulePath(req.Path, req.Version))
	}
	p := transpiler.NewAntlrGalaParser()
	tr := transformer.NewGalaASTTransformer()
	g := generator.NewGoCodeGenerator()

	var allEmbedPatterns []string

	for _, galaFile := range galaFiles {
		content, err := os.ReadFile(galaFile)
		if err != nil {
			return fmt.Errorf("reading %s: %w", galaFile, err)
		}

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
			return fmt.Errorf("transpiling %s: %w", galaFile, err)
		}

		// Collect embed patterns from generated Go code
		allEmbedPatterns = append(allEmbedPatterns, extractEmbedPatterns(goCode)...)

		relPath, err := filepath.Rel(b.workspace.ProjectDir, galaFile)
		if err != nil {
			relPath = filepath.Base(galaFile)
		}
		outName := strings.TrimSuffix(relPath, ".gala") + ".gen.go"
		outName = strings.ReplaceAll(outName, string(filepath.Separator), "_")

		if err := b.workspace.WriteGenFile(outName, []byte(goCode)); err != nil {
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

	// Save source hash for next build
	if currentHash != "" {
		os.WriteFile(hashFile, []byte(currentHash), 0644)
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

// generateGoMod generates the go.mod file in the workspace and downloads Go dependencies.
func (b *Builder) generateGoMod() error {
	if b.verbose {
		fmt.Println("Generating go.mod...")
	}

	gen := NewGoModGenerator(b.config)
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

// goCompileCheck runs `go build ./...` in the workspace without producing a binary.
// This verifies that library packages compile correctly.
func (b *Builder) goCompileCheck() error {
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

	// Build command
	args := []string{"build", "-o", outputPath, "./gen/..."}

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

	// Check if deps have changed by hashing gala.mod requirements
	depsHashFile := filepath.Join(b.workspace.Dir, ".gala-deps-hash")
	h := sha256.New()
	for _, req := range galaReqs {
		h.Write([]byte(req.Path + "@" + req.Version + "\n"))
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
			// Key by internal module path (from dep's gala.mod), matching TranspileDeps() behavior
			modPath := resolveDepInternalModulePath(b.config, req)
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
	cachedDir := config.GalaModulePath(dep.Path, dep.Version)
	galaModPath := filepath.Join(cachedDir, "gala.mod")
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

	// Step 3: Find source and test files
	sourceFiles, err := findGalaFiles(b.workspace.ProjectDir)
	if err != nil {
		return fmt.Errorf("finding source files: %w", err)
	}

	testFiles, err := findGalaTestFiles(b.workspace.ProjectDir)
	if err != nil {
		return fmt.Errorf("finding test files: %w", err)
	}

	if len(testFiles) == 0 {
		return fmt.Errorf("no _test.gala files found in %s", b.workspace.ProjectDir)
	}

	// Step 4: Determine package layout
	// Test files in GALA are always package main and import the library under test.
	// Source files can be package main (executable) or a library package.
	// For package main projects: transpile source + test files together.
	// For library projects: transpile source files as library, test files separately as main.
	isLib := false
	if len(sourceFiles) > 0 {
		pkgName := detectPackageName(sourceFiles[0])
		isLib = pkgName != "" && pkgName != "main"
	}

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

	// Step 8: Generate test_main.go
	testMainCode := GenerateTestMain(allTestFuncs)
	if err := b.workspace.WriteGenFile("test_main.gen.go", []byte(testMainCode)); err != nil {
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

	args := []string{"build", "-o", testBinary, "./gen/..."}
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

	return nil
}

// transpileTestMain transpiles source + test files together for package main projects.
// All files are transpiled into gen/ as package main.
func (b *Builder) transpileTestMain(sourceFiles, testFiles []string) error {
	allFiles := append(sourceFiles, testFiles...)
	return b.transpileFiles(allFiles, allFiles)
}

// transpileTestLibrary transpiles source files as a library and test files as package main.
// Source files go into gen/lib/ as the library package.
// Test files go into gen/ as package main (they import the library).
func (b *Builder) transpileTestLibrary(sourceFiles, testFiles []string) error {
	// For library packages, test files are standalone package main files.
	// They import the library via dot-import. We only need to transpile the test files
	// into gen/ as package main. The library is already available as a dependency
	// (either via stdlib, deps, or we transpile it into a subdirectory).

	// Create lib subdirectory in gen
	libDir := filepath.Join(b.workspace.GenDir, "lib")
	if err := os.MkdirAll(libDir, 0755); err != nil {
		return fmt.Errorf("creating lib dir: %w", err)
	}

	// Transpile source files into gen/lib/
	if err := b.transpileFilesToDir(sourceFiles, sourceFiles, libDir); err != nil {
		return fmt.Errorf("transpiling source files: %w", err)
	}

	// Transpile test files into gen/ (they are package main)
	// Test files see source files as siblings for type resolution
	allFiles := append(sourceFiles, testFiles...)
	if err := b.transpileFilesToDir(testFiles, allFiles, b.workspace.GenDir); err != nil {
		return fmt.Errorf("transpiling test files: %w", err)
	}

	return nil
}

// transpileFiles transpiles the given files into the workspace gen directory.
// allSiblings is the full list of files for sibling-based type resolution.
func (b *Builder) transpileFiles(files []string, allSiblings []string) error {
	return b.transpileFilesToDir(files, allSiblings, b.workspace.GenDir)
}

// transpileFilesToDir transpiles the given files into the specified output directory.
func (b *Builder) transpileFilesToDir(files []string, allSiblings []string, outDir string) error {
	stdlibDir := b.config.StdlibVersionDir(b.stdlibVersion)
	searchPaths := []string{b.workspace.ProjectDir, stdlibDir}

	for _, req := range b.galaMod.GalaRequires() {
		searchPaths = append(searchPaths, b.config.GalaModulePath(req.Path, req.Version))
	}

	p := transpiler.NewAntlrGalaParser()
	tr := transformer.NewGalaASTTransformer()
	g := generator.NewGoCodeGenerator()

	for _, galaFile := range files {
		content, err := os.ReadFile(galaFile)
		if err != nil {
			return fmt.Errorf("reading %s: %w", galaFile, err)
		}

		var siblings []string
		for _, other := range allSiblings {
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
			return fmt.Errorf("transpiling %s: %w", galaFile, err)
		}

		relPath, err := filepath.Rel(b.workspace.ProjectDir, galaFile)
		if err != nil {
			relPath = filepath.Base(galaFile)
		}
		outName := strings.TrimSuffix(relPath, ".gala") + ".gen.go"
		outName = strings.ReplaceAll(outName, string(filepath.Separator), "_")

		outPath := filepath.Join(outDir, outName)
		if err := os.WriteFile(outPath, []byte(goCode), 0644); err != nil {
			return fmt.Errorf("writing %s: %w", outPath, err)
		}

		if b.verbose {
			fmt.Printf("  %s -> %s\n", relPath, outName)
		}
	}

	return nil
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

// isWindows returns true if running on Windows.
func isWindows() bool {
	return os.PathSeparator == '\\'
}
