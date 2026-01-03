package main

import (
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"path/filepath"

	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/generator"
	"martianoff/gala/internal/transpiler/transformer"
)

func main() {
	var (
		inputPath  string
		outputPath string
		run        bool
	)

	flag.StringVar(&inputPath, "input", "", "Path to the input .gala file")
	flag.StringVar(&outputPath, "output", "", "Path to the output .go file (optional if -run is used)")
	flag.BoolVar(&run, "run", false, "Execute the generated Go code")
	flag.Parse()

	if inputPath == "" {
		if flag.NArg() > 0 {
			inputPath = flag.Arg(0)
		} else {
			fmt.Println("Usage: gala [options] <input.gala>")
			flag.PrintDefaults()
			os.Exit(1)
		}
	}

	content, err := ioutil.ReadFile(inputPath)
	if err != nil {
		log.Fatalf("failed to read input file: %v", err)
	}

	p := transpiler.NewAntlrGalaParser()
	tr := transformer.NewGalaASTTransformer()
	g := generator.NewGoCodeGenerator()
	t := transpiler.NewGalaToGoTranspiler(p, tr, g)

	goCode, err := t.Transpile(string(content))
	if err != nil {
		log.Fatalf("transpilation failed: %v", err)
	}

	tempDir := ""
	actualOutput := outputPath
	if run && outputPath == "" {
		tempDir, err = ioutil.TempDir("", "gala-run-*")
		if err != nil {
			log.Fatalf("failed to create temp dir: %v", err)
		}
		defer os.RemoveAll(tempDir)
		actualOutput = filepath.Join(tempDir, "main.go")
	}

	if actualOutput != "" {
		err = ioutil.WriteFile(actualOutput, []byte(goCode), 0644)
		if err != nil {
			log.Fatalf("failed to write output file: %v", err)
		}
		if !run || outputPath != "" {
			fmt.Printf("Generated Go code saved to %s\n", actualOutput)
		}
	} else if !run {
		fmt.Println(goCode)
	}

	if run {
		// Run the generated go code
		// Note: Since we might need our 'std' package, we should probably run it in a way that it can find it.
		// For simplicity in the CLI, we assume 'go' is in PATH and the project is set up correctly.
		cmd := exec.Command("go", "run", actualOutput)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		err = cmd.Run()
		if err != nil {
			log.Fatalf("failed to run generated code: %v", err)
		}
	}
}
