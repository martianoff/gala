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

	// Cache each directory's sibling set: every input in the same directory
	// gets the same package-file list.
	siblingsByDir := make(map[string][]string)

	for i, in := range inList {
		content, err := os.ReadFile(in)
		if err != nil {
			return fmt.Errorf("reading %s: %w", in, err)
		}

		// Provide the package's sibling .gala files explicitly. Relying on
		// the analyzer's directory scan instead is not enough: that scan is
		// skipped for files in a `main` or `test` package, because those may
		// be independent programs sharing a directory. A library can be named
		// `test` too (the shipped test framework), and its files must see one
		// another. Passing the directory's non-test .gala files keeps the
		// batch path's visibility equal to the single-file directory scan.
		dir := filepath.Dir(in)
		siblings, ok := siblingsByDir[dir]
		if !ok {
			siblings = packageSiblings(dir)
			siblingsByDir[dir] = siblings
		}
		a.SetPackageFiles(siblings)

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

// packageSiblings returns the .gala files that form the package in dir: every
// non-test .gala file, in directory order. `_test.gala` files are excluded
// because they are transpiled separately (nix skips them when building the
// input list, and Bazel's package_files never include them), and because a
// test file may declare a different package than its siblings.
//
// On a directory-read error it returns nil, which restores the analyzer's
// directory scan.
func packageSiblings(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var siblings []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || filepath.Ext(name) != ".gala" || strings.HasSuffix(name, "_test.gala") {
			continue
		}
		siblings = append(siblings, filepath.Join(dir, name))
	}
	return siblings
}
