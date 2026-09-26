// gala_bootstrap is a minimal transpiler used for bootstrapping.
// It transpiles GALA to Go without any stdlib embedding features. Unlike
// cmd/gala it does not import internal/stdlib, so it can be built from the
// same tree whose stdlib it transpiles: Bazel uses it to generate the stdlib
// Go files that the full transpiler embeds, and nix/gala.nix uses it as the
// useLocalBootstrap escape hatch.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/analyzer"
	"martianoff/gala/internal/transpiler/generator"
	"martianoff/gala/internal/transpiler/profiler"
	"martianoff/gala/internal/transpiler/transformer"
)

func main() {
	input := flag.String("input", "", "Input .gala file")
	output := flag.String("output", "", "Output .go file")
	inputs := flag.String("inputs", "", "Comma-separated input .gala files (batch mode, alternative to -input)")
	outputs := flag.String("outputs", "", "Comma-separated output .go files, same order as -inputs")
	search := flag.String("search", ".", "Comma-separated search paths")
	packageFiles := flag.String("package-files", "", "Comma-separated list of sibling .gala files in the same package")
	goroot := flag.String("goroot", "", "Path to Go SDK root (for Go type inference)")
	flag.Parse()

	// Set GOROOT for Go type inference if provided
	if *goroot != "" {
		os.Setenv("GOROOT", *goroot)
	}

	paths := strings.Split(*search, ",")

	if *inputs != "" {
		if *input != "" || *output != "" {
			fmt.Fprintln(os.Stderr, "Error: -inputs/-outputs cannot be combined with -input/-output")
			os.Exit(1)
		}
		if *packageFiles != "" {
			fmt.Fprintln(os.Stderr, "Error: -inputs/-outputs cannot be combined with -package-files")
			os.Exit(1)
		}
		if err := runBatch(strings.Split(*inputs, ","), strings.Split(*outputs, ","), paths); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		return
	}

	if *input == "" {
		fmt.Fprintln(os.Stderr, "Error: -input is required")
		os.Exit(1)
	}

	content, err := os.ReadFile(*input)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading input: %v\n", err)
		os.Exit(1)
	}

	p := transpiler.NewAntlrGalaParser()
	var a transpiler.Analyzer
	if *packageFiles != "" {
		pkgFiles := strings.Split(*packageFiles, ",")
		a = analyzer.NewGalaAnalyzerWithPackageFiles(p, paths, pkgFiles)
	} else {
		a = analyzer.NewGalaAnalyzer(p, paths)
	}
	tr := transformer.NewGalaASTTransformer()
	g := generator.NewGoCodeGenerator()
	t := transpiler.NewGalaToGoTranspiler(p, a, tr, g)

	goCode, err := t.Transpile(string(content), *input)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	if *output != "" {
		if err := os.WriteFile(*output, []byte(goCode), 0644); err != nil {
			fmt.Fprintf(os.Stderr, "Error writing output: %v\n", err)
			os.Exit(1)
		}
	} else {
		fmt.Print(goCode)
	}
}

func runBatch(inList, outList, paths []string) error {
	if len(inList) != len(outList) {
		return fmt.Errorf("number of inputs (%d) != outputs (%d)", len(inList), len(outList))
	}

	p := transpiler.NewAntlrGalaParser()
	a := analyzer.NewBatchAnalyzer(p, paths)
	summary := profiler.NewSummary()
	batchStart := time.Now()
	defer func() {
		if profiler.Enabled {
			fmt.Fprintf(os.Stderr, "\n=== BATCH TOTAL: %d files in %s ===\n", len(inList), time.Since(batchStart).Round(time.Millisecond))
		}
		summary.Report()
	}()

	for i, in := range inList {
		content, err := os.ReadFile(in)
		if err != nil {
			return fmt.Errorf("reading %s: %w", in, err)
		}

		// Pass no explicit package files: that would replace the analyzer's
		// directory scan rather than add to it, hiding same-directory .gala
		// files that are not in inList. Clearing also resets checkedDirs, so
		// each file gets a fresh scan. This matches Bazel, which passes no
		// explicit list at all.
		a.SetPackageFiles(nil)

		tr := transformer.NewGalaASTTransformer()
		g := generator.NewGoCodeGenerator()
		t := transpiler.NewGalaToGoTranspiler(p, a, tr, g)

		goCode, err := t.TranspileWithSummary(string(content), in, summary)
		if err != nil {
			return fmt.Errorf("%s: %w", in, err)
		}

		if dir := filepath.Dir(outList[i]); dir != "." {
			if err := os.MkdirAll(dir, 0755); err != nil {
				return fmt.Errorf("creating %s: %w", dir, err)
			}
		}
		if err := os.WriteFile(outList[i], []byte(goCode), 0644); err != nil {
			return fmt.Errorf("writing %s: %w", outList[i], err)
		}
	}
	return nil
}
