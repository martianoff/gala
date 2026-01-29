package commands

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

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

Examples:
  gala transpile main.gala                    # Output to stdout
  gala transpile -i main.gala -o main.go      # Output to file
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
