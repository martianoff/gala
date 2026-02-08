// gala_bootstrap is a minimal transpiler used for bootstrapping.
// It transpiles GALA to Go without any stdlib embedding features.
// Used internally to generate stdlib Go files, breaking the dependency cycle.
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/analyzer"
	"martianoff/gala/internal/transpiler/generator"
	"martianoff/gala/internal/transpiler/transformer"
)

func main() {
	input := flag.String("input", "", "Input .gala file")
	output := flag.String("output", "", "Output .go file")
	search := flag.String("search", ".", "Comma-separated search paths")
	packageFiles := flag.String("package-files", "", "Comma-separated list of sibling .gala files in the same package")
	flag.Parse()

	if *input == "" {
		fmt.Fprintln(os.Stderr, "Error: -input is required")
		os.Exit(1)
	}

	content, err := os.ReadFile(*input)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading input: %v\n", err)
		os.Exit(1)
	}

	paths := strings.Split(*search, ",")
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
