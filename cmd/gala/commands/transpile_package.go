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
	tpScan    bool
)

var transpilePackageCmd = &cobra.Command{
	Use:   "transpile-package",
	Short: "Transpile all GALA files in a package in one pass (faster than per-file transpile)",
	Long: `Transpile multiple GALA source files that belong to the same package in a single
invocation. This shares the analyzer cache across files, avoiding redundant
re-analysis of imports (std, collection_immutable, etc.).

By default every --inputs file is treated as a sibling of every other --inputs
file. Use --scan to instead let the analyzer discover siblings by scanning the
input's directory, which also sees .gala files that were not listed in
--inputs.

Example:
  gala transpile-package \
    --inputs types.gala,request.gala,response.gala \
    --outputs types.gen.go,request.gen.go,response.gen.go \
    --search /path/to/gala

  gala transpile-package --scan \
    --inputs a.gala,b.gala \
    --outputs a.gen.go,b.gen.go \
    --search /path/to/gala`,
	Run: runTranspilePackage,
}

func init() {
	transpilePackageCmd.Flags().StringVar(&tpInputs, "inputs", "", "Comma-separated list of input .gala files")
	transpilePackageCmd.Flags().StringVar(&tpOutputs, "outputs", "", "Comma-separated list of output .go files (same order as inputs)")
	transpilePackageCmd.Flags().StringVarP(&tpSearch, "search", "s", ".", "Comma-separated search paths")
	transpilePackageCmd.Flags().StringVar(&tpGoroot, "goroot", "", "Path to Go SDK root (for Go type inference)")
	transpilePackageCmd.Flags().BoolVar(&tpScan, "scan", false, "Discover sibling .gala files by directory scan instead of treating every --inputs entry as a sibling")
}

func runTranspilePackage(cmd *cobra.Command, args []string) {
	tuneGCOnce()
	startCPUProfileIfRequested()
	defer stopCPUProfile()
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

	if err := transpilePackage(inputs, outputs, tpSearch, tpGoroot, tpScan); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

// transpilePackage transpiles each inputs[i] to outputs[i] using a single
// shared parser and batch analyzer.
//
// Sibling resolution:
//   - scan=false: every other inputs entry is a sibling of inputs[i]. This is
//     the original behavior and is appropriate when the caller has the full
//     package file list (e.g. Bazel's package_files).
//   - scan=true: the analyzer discovers siblings by scanning the input's
//     directory, resetting its checkedDirs per file. This sees .gala files
//     that are not listed in inputs (e.g. reserved names such as retry.gala)
//     and matches gala_bootstrap's batch mode and Bazel's directory scan.
func transpilePackage(inputs, outputs []string, search, goroot string, scan bool) error {
	if len(inputs) != len(outputs) {
		return fmt.Errorf("number of inputs (%d) != outputs (%d)", len(inputs), len(outputs))
	}

	if goroot != "" {
		os.Setenv("GOROOT", goroot)
	}

	// Build search paths
	paths := strings.Split(search, ",")
	if len(inputs) > 0 {
		paths = autoResolveSearchPaths(inputs[0], paths)
	}

	// Create a SINGLE shared parser and batch analyzer — the key optimization.
	p := transpiler.NewAntlrGalaParser()
	batchAnalyzer := analyzer.NewBatchAnalyzer(p, paths)

	summary := profiler.NewSummary()
	batchStart := time.Now()

	failed := 0
	for i, inputPath := range inputs {
		outputPath := outputs[i]

		content, err := os.ReadFile(inputPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error reading %s: %v\n", inputPath, err)
			failed++
			continue
		}

		if scan {
			// Directory scan for siblings; nil also resets checkedDirs so
			// each file starts from a fresh scan.
			batchAnalyzer.SetPackageFiles(nil)
		} else {
			// Build package-files list: all other inputs are siblings
			var packageFiles []string
			for j, other := range inputs {
				if j != i {
					packageFiles = append(packageFiles, other)
				}
			}
			batchAnalyzer.SetPackageFiles(packageFiles)
		}

		tr := transformer.NewGalaASTTransformer()
		g := generator.NewGoCodeGenerator()
		t := transpiler.NewGalaToGoTranspiler(p, batchAnalyzer, tr, g)

		goCode, err := t.TranspileWithSummary(string(content), inputPath, summary)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error transpiling %s: %v\n", inputPath, err)
			failed++
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
			failed++
			continue
		}
	}

	if profiler.Enabled {
		fmt.Fprintf(os.Stderr, "\n=== BATCH TOTAL: %d files in %s ===\n", len(inputs), time.Since(batchStart).Round(time.Millisecond))
	}
	summary.Report()

	if failed > 0 {
		return fmt.Errorf("%d of %d files failed to transpile", failed, len(inputs))
	}
	return nil
}
