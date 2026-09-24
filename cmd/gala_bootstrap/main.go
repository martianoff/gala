// gala_bootstrap is a minimal transpiler used for bootstrapping.
// It transpiles GALA to Go without any stdlib embedding features.
// Used internally to generate stdlib Go files, breaking the dependency cycle.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/analyzer"
	"martianoff/gala/internal/transpiler/generator"
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

	for i, in := range inList {
		content, err := os.ReadFile(in)
		if err != nil {
			return fmt.Errorf("reading %s: %w", in, err)
		}

		a.SetPackageFiles(siblingsFor(inList, in))

		tr := transformer.NewGalaASTTransformer()
		g := generator.NewGoCodeGenerator()
		t := transpiler.NewGalaToGoTranspiler(p, a, tr, g)

		goCode, err := t.Transpile(string(content), in)
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

func siblingsFor(inList []string, current string) []string {
	var siblings []string
	for _, other := range inList {
		if other != current && filepath.Dir(other) == filepath.Dir(current) {
			siblings = append(siblings, other)
		}
	}
	return siblings
}
