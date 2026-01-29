package commands

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"martianoff/gala/internal/depman/mod"
	"martianoff/gala/internal/stdlib"
	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/analyzer"
	"martianoff/gala/internal/transpiler/generator"
	"martianoff/gala/internal/transpiler/transformer"
)

var (
	transpileInput  string
	transpileOutput string
	transpileRun    bool
	transpileSearch string
)

var transpileCmd = &cobra.Command{
	Use:   "transpile [file.gala]",
	Short: "Transpile GALA source files to Go",
	Long: `Transpile GALA source files to Go code.

The GALA stdlib is automatically extracted alongside the output file.

Examples:
  gala transpile main.gala                    # Output to stdout
  gala transpile -i main.gala -o main.go      # Output to file with stdlib
  gala transpile main.gala --run              # Transpile and execute
  gala -i main.gala -o main.go                # Shorthand (same as transpile)`,
	Args: cobra.MaximumNArgs(1),
	Run:  runTranspile,
}

func init() {
	transpileCmd.Flags().StringVarP(&transpileInput, "input", "i", "", "Path to the input .gala file")
	transpileCmd.Flags().StringVarP(&transpileOutput, "output", "o", "", "Path to the output .go file")
	transpileCmd.Flags().BoolVarP(&transpileRun, "run", "r", false, "Execute the generated Go code")
	transpileCmd.Flags().StringVarP(&transpileSearch, "search", "s", ".", "Comma-separated search paths")
}

func runTranspile(cmd *cobra.Command, args []string) {
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

	// Read input file
	content, err := os.ReadFile(inputPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: failed to read input file: %v\n", err)
		os.Exit(1)
	}

	// Create transpiler pipeline
	p := transpiler.NewAntlrGalaParser()
	paths := strings.Split(transpileSearch, ",")
	a := analyzer.NewGalaAnalyzer(p, paths)
	tr := transformer.NewGalaASTTransformer()
	g := generator.NewGoCodeGenerator()
	t := transpiler.NewGalaToGoTranspiler(p, a, tr, g)

	// Transpile
	goCode, err := t.Transpile(string(content), inputPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: transpilation failed: %v\n", err)
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

		// Always extract stdlib when outputting to a file
		outputDir := filepath.Dir(actualOutput)
		if outputDir == "" || outputDir == "." {
			outputDir, _ = os.Getwd()
		}
		if err := extractStdlib(outputDir); err != nil {
			fmt.Fprintf(os.Stderr, "Error: failed to extract stdlib: %v\n", err)
			os.Exit(1)
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

// extractStdlib extracts the embedded stdlib packages to the output directory
// and automatically manages go.mod.
func extractStdlib(outputDir string) error {
	stdlibDir := filepath.Join(outputDir, "_gala")

	// Extract all stdlib packages
	if err := stdlib.ExtractTo(stdlibDir); err != nil {
		return fmt.Errorf("extracting stdlib: %w", err)
	}

	fmt.Printf("Extracted GALA stdlib to %s\n", stdlibDir)

	// Manage go.mod automatically
	goModPath := filepath.Join(outputDir, "go.mod")
	if _, err := os.Stat(goModPath); err == nil {
		// go.mod exists, update it
		if err := updateGoMod(goModPath); err != nil {
			return fmt.Errorf("updating go.mod: %w", err)
		}
		fmt.Println("Updated go.mod with GALA stdlib")
	} else {
		// go.mod doesn't exist, create it
		moduleName := detectModuleName(outputDir)
		if err := createGoMod(goModPath, moduleName); err != nil {
			return fmt.Errorf("creating go.mod: %w", err)
		}
		fmt.Printf("Created go.mod for module %s\n", moduleName)
	}

	return nil
}

// detectModuleName tries to detect a reasonable module name from the directory.
func detectModuleName(dir string) string {
	// Use directory name as module name
	return filepath.Base(dir)
}

// createGoMod creates a new go.mod file with stdlib dependencies.
// It also reads gala.mod if present and adds any Go dependencies.
// Note: This doesn't add replace directives - those are added by updateGoMod
// when building a project that uses the stdlib directly.
func createGoMod(goModPath, moduleName string) error {
	var sb strings.Builder

	// Header
	sb.WriteString("// Code generated by gala transpiler. DO NOT EDIT.\n")
	sb.WriteString("module " + moduleName + "\n\ngo 1.21\n\n")

	// Stdlib section - only require, no replace (replace is added by updateGoMod for projects)
	sb.WriteString("// GALA dependencies below. DO NOT EDIT.\n")
	sb.WriteString("require (\n")
	for _, importPath := range stdlib.PackageImportPaths {
		sb.WriteString("\t" + importPath + " v0.0.0\n")
	}

	// Check for gala.mod and add Go dependencies
	galaModPath := filepath.Join(filepath.Dir(goModPath), "gala.mod")
	if galaMod, err := mod.ParseFile(galaModPath); err == nil {
		for _, req := range galaMod.Require {
			if req.Go {
				sb.WriteString("\t" + req.Path + " " + req.Version + "\n")
			}
		}
	}

	sb.WriteString(")\n")
	sb.WriteString("// End GALA dependencies.\n")

	return os.WriteFile(goModPath, []byte(sb.String()), 0644)
}

// updateGoMod updates an existing go.mod with stdlib require and replace directives.
// It adds a separate GALA stdlib section, preserving any existing GALA-managed deps section.
func updateGoMod(goModPath string) error {
	content, err := os.ReadFile(goModPath)
	if err != nil {
		return err
	}

	const stdlibMarkerStart = "// GALA stdlib dependencies below. DO NOT EDIT."
	const stdlibMarkerEnd = "// End GALA stdlib dependencies."

	existingContent := string(content)

	// Remove any existing stdlib section
	if startIdx := strings.Index(existingContent, stdlibMarkerStart); startIdx != -1 {
		if endIdx := strings.Index(existingContent, stdlibMarkerEnd); endIdx != -1 {
			existingContent = existingContent[:startIdx] + existingContent[endIdx+len(stdlibMarkerEnd):]
		}
	}

	// Also remove old stdlib replace directives (from before we had markers)
	lines := strings.Split(existingContent, "\n")
	var newLines []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		isStdlibReplace := false
		for _, importPath := range stdlib.PackageImportPaths {
			if strings.HasPrefix(trimmed, "replace "+importPath) {
				isStdlibReplace = true
				break
			}
		}
		if !isStdlibReplace {
			newLines = append(newLines, line)
		}
	}
	existingContent = strings.Join(newLines, "\n")

	// Clean up multiple empty lines
	for strings.Contains(existingContent, "\n\n\n") {
		existingContent = strings.ReplaceAll(existingContent, "\n\n\n", "\n\n")
	}
	existingContent = strings.TrimRight(existingContent, "\n")

	// Build new content with stdlib section
	var sb strings.Builder
	sb.WriteString(existingContent)
	sb.WriteString("\n\n")
	sb.WriteString(stdlibMarkerStart)
	sb.WriteString("\n// Code generated by gala transpiler. DO NOT EDIT.\n")
	sb.WriteString("require (\n")
	for _, importPath := range stdlib.PackageImportPaths {
		sb.WriteString("\t" + importPath + " v0.0.0\n")
	}
	sb.WriteString(")\n\n")
	sb.WriteString(stdlib.GenerateGoModReplace("_gala"))
	sb.WriteString(stdlibMarkerEnd)
	sb.WriteString("\n")

	return os.WriteFile(goModPath, []byte(sb.String()), 0644)
}
