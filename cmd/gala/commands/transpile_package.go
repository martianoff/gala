package commands

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/analyzer"
	"martianoff/gala/internal/transpiler/generator"
	"martianoff/gala/internal/transpiler/profiler"
	"martianoff/gala/internal/transpiler/transformer"
)

var (
	tpInputs  string
	tpOutputs string
	tpSearch  string
	tpGoroot  string
)

var transpilePackageCmd = &cobra.Command{
	Use:   "transpile-package",
	Short: "Transpile all GALA files in a package in one pass (faster than per-file transpile)",
	Long: `Transpile multiple GALA source files that belong to the same package in a single
invocation. This shares the analyzer cache across files, avoiding redundant
re-analysis of imports (std, collection_immutable, etc.).

Example:
  gala transpile-package \
    --inputs types.gala,request.gala,response.gala \
    --outputs types.gen.go,request.gen.go,response.gen.go \
    --search /path/to/gala`,
	Run: runTranspilePackage,
}

func init() {
	transpilePackageCmd.Flags().StringVar(&tpInputs, "inputs", "", "Comma-separated list of input .gala files")
	transpilePackageCmd.Flags().StringVar(&tpOutputs, "outputs", "", "Comma-separated list of output .go files (same order as inputs)")
	transpilePackageCmd.Flags().StringVarP(&tpSearch, "search", "s", ".", "Comma-separated search paths")
	transpilePackageCmd.Flags().StringVar(&tpGoroot, "goroot", "", "Path to Go SDK root (for Go type inference)")
}

func runTranspilePackage(cmd *cobra.Command, args []string) {
	if tpInputs == "" {
		fmt.Fprintln(os.Stderr, "Error: --inputs is required")
		os.Exit(1)
	}
	if tpOutputs == "" {
		fmt.Fprintln(os.Stderr, "Error: --outputs is required")
		os.Exit(1)
	}

	inputs := strings.Split(tpInputs, ",")
	outputs := strings.Split(tpOutputs, ",")

	if len(inputs) != len(outputs) {
		fmt.Fprintf(os.Stderr, "Error: number of inputs (%d) != outputs (%d)\n", len(inputs), len(outputs))
		os.Exit(1)
	}

	if tpGoroot != "" {
		os.Setenv("GOROOT", tpGoroot)
	}

	// Build search paths
	paths := strings.Split(tpSearch, ",")
	if len(inputs) > 0 {
		paths = autoResolveSearchPaths(inputs[0], paths)
	}

	// Create a SINGLE shared parser and batch analyzer — the key optimization.
	p := transpiler.NewAntlrGalaParser()
	batchAnalyzer := analyzer.NewBatchAnalyzer(p, paths)

	summary := profiler.NewSummary()
	batchStart := time.Now()

	hasError := false
	for i, inputPath := range inputs {
		outputPath := outputs[i]

		content, err := os.ReadFile(inputPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error reading %s: %v\n", inputPath, err)
			hasError = true
			continue
		}

		// Build package-files list: all other inputs are siblings
		var packageFiles []string
		for j, other := range inputs {
			if j != i {
				packageFiles = append(packageFiles, other)
			}
		}
		batchAnalyzer.SetPackageFiles(packageFiles)

		tr := transformer.NewGalaASTTransformer()
		g := generator.NewGoCodeGenerator()
		t := transpiler.NewGalaToGoTranspiler(p, batchAnalyzer, tr, g)

		goCode, err := t.Transpile(string(content), inputPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error transpiling %s: %v\n", inputPath, err)
			hasError = true
			continue
		}

		// Ensure output directory exists
		outDir := filepath.Dir(outputPath)
		if outDir != "" && outDir != "." {
			os.MkdirAll(outDir, 0755)
		}

		err = os.WriteFile(outputPath, []byte(goCode), 0644)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error writing %s: %v\n", outputPath, err)
			hasError = true
			continue
		}
	}

	if profiler.Enabled {
		fmt.Fprintf(os.Stderr, "\n=== BATCH TOTAL: %d files in %s ===\n", len(inputs), time.Since(batchStart).Round(time.Millisecond))
	}
	summary.Report()

	if hasError {
		os.Exit(1)
	}
}
