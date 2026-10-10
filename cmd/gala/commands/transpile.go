package commands

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/mod/module"

	"martianoff/gala/galaerr"
	"martianoff/gala/internal/build"
	"martianoff/gala/internal/depman/mod"
	"martianoff/gala/internal/goexport"
	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/analyzer"
	"martianoff/gala/internal/transpiler/generator"
	"martianoff/gala/internal/transpiler/transformer"
)

var (
	transpileInput        string
	transpileOutput       string
	transpileRun          bool
	transpileSearch       string
	transpilePackageFiles string
	transpileGoroot       string
	transpileStdlibModule string
)

var transpileCmd = &cobra.Command{
	Use:   "transpile [file.gala]",
	Short: "Transpile GALA source files to Go",
	Long: `Transpile GALA source files to Go code.

Outputs transpiled Go code without creating additional files.
Use 'gala build' for a complete build workflow.

--stdlib-module writes imports of GALA's standard library under another Go
module path. Use it to commit generated Go to a plain Go module (no gala.mod)
that requires the published standard library at this compiler's version:

  gala transpile -i p.gala -o p.go --stdlib-module go.gala.fyi/stdlib

Examples:
  gala transpile main.gala               # Output to stdout
  gala transpile -i main.gala -o main.go # Output to file
  gala transpile main.gala --run         # Transpile and execute (temp dir)`,
	Args: cobra.MaximumNArgs(1),
	Run:  runTranspile,
}

func init() {
	transpileCmd.Flags().StringVarP(&transpileInput, "input", "i", "", "Path to the input .gala file")
	transpileCmd.Flags().StringVarP(&transpileOutput, "output", "o", "", "Path to the output .go file")
	transpileCmd.Flags().BoolVarP(&transpileRun, "run", "r", false, "Execute the generated Go code")
	transpileCmd.Flags().StringVarP(&transpileSearch, "search", "s", ".", "Comma-separated search paths")
	transpileCmd.Flags().StringVar(&transpilePackageFiles, "package-files", "", "Comma-separated list of sibling .gala files in the same package")
	transpileCmd.Flags().StringVar(&transpileGoroot, "goroot", "", "Path to Go SDK root (for Go type inference)")
	transpileCmd.Flags().StringVar(&transpileStdlibModule, "stdlib-module", "", "Go module path to write standard library imports under, such as go.gala.fyi/stdlib (default: none, imports stay martianoff/gala)")
	addDiagnosticsJSONFlag(transpileCmd)
}

// autoResolveSearchPaths enhances search paths by auto-discovering the stdlib
// and GALA dependencies from gala.mod. This allows 'gala transpile' to work
// without manually specifying '-s /path/to/stdlib'.
func autoResolveSearchPaths(inputPath string, basePaths []string) []string {
	config := build.DefaultConfig()

	// Add stdlib path using current CLI version
	if stdlibDir, err := config.EnsureStdlib(Version); err != nil {
		fmt.Fprintf(os.Stderr, "warning: stdlib cache unavailable: %v\n", err)
	} else {
		basePaths = appendIfNew(basePaths, stdlibDir)
	}

	// Try to find gala.mod by walking up from the input file's directory
	startDir := filepath.Dir(inputPath)
	if abs, err := filepath.Abs(startDir); err == nil {
		startDir = abs
	}

	projectRoot := findGalaModDir(startDir)
	if projectRoot != "" {
		// PREPEND the consumer's gala.mod directory ahead of any other
		// search path. The transpiler's resolver walks up from each search
		// path looking for gala.mod (NewResolver.findGalaModRoot) and
		// accepts the FIRST one it finds. If projectRoot is appended after
		// dep search paths — which under Bazel typically point inside
		// external/<repo>+/ directories that themselves contain the dep's
		// own gala.mod — the dep's gala.mod gets loaded as if it were the
		// project's, completely masking the consumer's require/replace
		// directives. Without that masking the cross-module sealed-case
		// Apply lowering and Go-style struct field metadata break (zero-field
		// and fielded sealed cases lower wrongly and plain-struct lambda
		// params lose their concrete types in gala-tui consumers).
		// The .gala source's gala.mod (the one walking up from inputPath
		// finds) is unambiguously the right answer, so it must win the
		// findGalaModRoot race.
		basePaths = prependIfNew(basePaths, projectRoot)

		galaModPath := filepath.Join(projectRoot, "gala.mod")
		if galaMod, err := mod.ParseFile(galaModPath); err == nil {
			for _, req := range galaMod.GalaRequires() {
				depDir := config.GalaModulePath(req.Path, req.Version)
				if info, err := os.Stat(depDir); err == nil && info.IsDir() {
					basePaths = appendIfNew(basePaths, depDir)
				}
			}
		}
	}

	return basePaths
}

// findGalaModDir walks up from dir looking for gala.mod and returns the
// directory containing it, or "" if not found.
func findGalaModDir(dir string) string {
	for {
		if _, err := os.Stat(filepath.Join(dir, "gala.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// appendIfNew appends val to slice if not already present.
func appendIfNew(slice []string, val string) []string {
	for _, s := range slice {
		if s == val {
			return slice
		}
	}
	return append(slice, val)
}

// prependIfNew prepends val to slice if not already present. Used when the
// caller needs a search path tried first regardless of how callers ordered
// the rest of the slice.
func prependIfNew(slice []string, val string) []string {
	for _, s := range slice {
		if s == val {
			return slice
		}
	}
	return append([]string{val}, slice...)
}

func runTranspile(cmd *cobra.Command, args []string) {
	tuneGCOnce()
	startCPUProfileIfRequested()
	defer stopCPUProfile()

	// Determine input file
	inputPath := transpileInput
	if inputPath == "" && len(args) > 0 {
		inputPath = args[0]
	}

	if inputPath == "" {
		fmt.Fprintln(os.Stderr, "Error: no input file specified")
		fmt.Fprintln(os.Stderr, "Usage: gala transpile [file.gala] or gala -i file.gala")
		os.Exit(1)
	}

	if err := checkStdlibModule(transpileStdlibModule); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	if transpileStdlibModule != "" && transpileRun {
		fmt.Fprintln(os.Stderr, "Error: --stdlib-module cannot be combined with --run: the generated code imports a Go module that only a go.mod requiring it can resolve")
		os.Exit(1)
	}

	// Read input file
	content, err := os.ReadFile(inputPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: failed to read input file: %v\n", err)
		os.Exit(1)
	}

	// Set GOROOT for Go type inference if provided via flag
	if transpileGoroot != "" {
		os.Setenv("GOROOT", transpileGoroot)
	}

	// Build search paths: start with user-provided, then auto-resolve stdlib and deps
	paths := strings.Split(transpileSearch, ",")
	paths = autoResolveSearchPaths(inputPath, paths)

	// Create transpiler pipeline
	p := transpiler.NewAntlrGalaParser()
	var a transpiler.Analyzer
	if transpilePackageFiles != "" {
		pkgFiles := strings.Split(transpilePackageFiles, ",")
		a = analyzer.NewGalaAnalyzerWithPackageFiles(p, paths, pkgFiles)
	} else {
		a = analyzer.NewGalaAnalyzer(p, paths)
	}
	tr := transformer.NewGalaASTTransformer()
	g := generator.NewGoCodeGenerator()
	t := transpiler.NewGalaToGoTranspiler(p, a, tr, g)

	// Transpile
	goCode, err := t.Transpile(string(content), inputPath)
	if err != nil {
		if diagnosticsJSON {
			exitBuildFailedJSON(err, "")
		}
		fmt.Fprintln(os.Stderr, galaerr.RenderRich(err, galaerr.Options{
			FallbackPath:   inputPath,
			FallbackSource: string(content),
			Color:          galaerr.ColorEnabled(),
		}))
		os.Exit(1)
	}

	if goCode, err = remapStdlibImports(inputPath, goCode, transpileStdlibModule); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	// Determine output handling
	tempDir := ""
	actualOutput := transpileOutput
	if transpileRun && transpileOutput == "" {
		tempDir, err = os.MkdirTemp("", "gala-run-*")
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: failed to create temp dir: %v\n", err)
			os.Exit(1)
		}
		defer os.RemoveAll(tempDir)
		actualOutput = filepath.Join(tempDir, "main.go")
	}

	// Write output
	if actualOutput != "" {
		err = os.WriteFile(actualOutput, []byte(goCode), 0644)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: failed to write output file: %v\n", err)
			os.Exit(1)
		}
		if !transpileRun || transpileOutput != "" {
			fmt.Printf("Generated Go code saved to %s\n", actualOutput)
		}
	} else if !transpileRun {
		fmt.Println(goCode)
	}

	// Run if requested
	if transpileRun {
		execCmd := exec.Command("go", "run", actualOutput)
		execCmd.Stdout = os.Stdout
		execCmd.Stderr = os.Stderr
		err = execCmd.Run()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: failed to run generated code: %v\n", err)
			os.Exit(1)
		}
	}
}

// remapStdlibImports rewrites the imports of GALA's standard library in the
// generated Go code to stdlibModule (--stdlib-module). An empty stdlibModule
// leaves the code as it is.
func remapStdlibImports(name, goCode, stdlibModule string) (string, error) {
	if stdlibModule == "" {
		return goCode, nil
	}
	out, err := goexport.RemapImports(name, []byte(goCode), goexport.StdlibRemap(stdlibModule))
	if err != nil {
		return "", fmt.Errorf("--stdlib-module: %w", err)
	}
	return string(out), nil
}

// checkStdlibModule rejects a --stdlib-module value the go command could not
// download, before anything is transpiled. An empty value is no rewrite.
func checkStdlibModule(stdlibModule string) error {
	if stdlibModule == "" {
		return nil
	}
	if err := module.CheckPath(stdlibModule); err != nil {
		return fmt.Errorf("--stdlib-module: %w", err)
	}
	return nil
}
