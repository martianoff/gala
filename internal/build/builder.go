package build

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

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
func (b *Builder) Build(outputPath string) (string, error) {
	// Step 1: Ensure workspace exists
	if b.verbose {
		fmt.Printf("Using workspace: %s\n", b.workspace.Dir)
	}
	if err := b.workspace.Ensure(); err != nil {
		return "", fmt.Errorf("ensuring workspace: %w", err)
	}

	// Step 2: Ensure stdlib is extracted to versioned cache
	if err := b.ensureStdlib(); err != nil {
		return "", fmt.Errorf("ensuring stdlib: %w", err)
	}

	// Step 2.5: Transpile GALA dependencies
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

	// Step 5: Run go build
	finalPath, err := b.goBuild(outputPath)
	if err != nil {
		return "", fmt.Errorf("go build: %w", err)
	}

	return finalPath, nil
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

// transpile transpiles all .gala files in the project to the workspace.
func (b *Builder) transpile() error {
	if b.verbose {
		fmt.Println("Transpiling GALA files...")
	}

	// Clean gen directory
	if err := b.workspace.CleanGen(); err != nil {
		return fmt.Errorf("cleaning gen dir: %w", err)
	}

	// Find all .gala files in the project
	galaFiles, err := findGalaFiles(b.workspace.ProjectDir)
	if err != nil {
		return fmt.Errorf("finding gala files: %w", err)
	}

	if len(galaFiles) == 0 {
		return fmt.Errorf("no .gala files found in %s", b.workspace.ProjectDir)
	}

	// Create transpiler pipeline
	// Include stdlib directory in search paths so analyzer can find std package types
	stdlibDir := b.config.StdlibVersionDir(b.stdlibVersion)
	searchPaths := []string{b.workspace.ProjectDir, stdlibDir}

	// Add GALA dependency source dirs so the analyzer can resolve types from deps
	for _, req := range b.galaMod.GalaRequires() {
		searchPaths = append(searchPaths, b.config.GalaModulePath(req.Path, req.Version))
	}
	p := transpiler.NewAntlrGalaParser()
	tr := transformer.NewGalaASTTransformer()
	g := generator.NewGoCodeGenerator()

	// Transpile each file, passing sibling files for cross-file type resolution
	for _, galaFile := range galaFiles {
		content, err := os.ReadFile(galaFile)
		if err != nil {
			return fmt.Errorf("reading %s: %w", galaFile, err)
		}

		// Compute sibling files (all other .gala files in the same package)
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

		// Generate output filename
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

	return nil
}

// generateGoMod generates the go.mod file in the workspace and downloads Go dependencies.
func (b *Builder) generateGoMod() error {
	if b.verbose {
		fmt.Println("Generating go.mod...")
	}

	gen := NewGoModGenerator(b.config)
	if err := gen.WriteGoMod(b.workspace, b.galaMod, b.stdlibVersion, b.transpiledDeps); err != nil {
		return err
	}

	// Run go mod tidy to download dependencies and create proper go.sum
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

	return nil
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
	return nil
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

// isWindows returns true if running on Windows.
func isWindows() bool {
	return os.PathSeparator == '\\'
}
