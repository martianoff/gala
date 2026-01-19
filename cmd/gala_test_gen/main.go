// gala_test_gen generates a main.go file that runs all Test* functions found in the input files.
// This enables Go-style test conventions where test functions start with "Test" and take a T parameter.
package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// testFuncRegex matches function declarations that start with Test.
// Pattern: func TestXxx(t T) T or func TestXxx(t test.T) test.T
var testFuncRegex = regexp.MustCompile(`^\s*func\s+(Test\w+)\s*\(\s*\w+\s+(?:test\.)?T\s*\)\s+(?:test\.)?T`)

func main() {
	var (
		outputPath string
		pkgName    string
	)

	flag.StringVar(&outputPath, "output", "", "Path to the output main.go file")
	flag.StringVar(&pkgName, "package", "main", "Package name for the generated file")
	flag.Parse()

	if flag.NArg() == 0 {
		fmt.Println("Usage: gala_test_gen [options] <test_files...>")
		flag.PrintDefaults()
		os.Exit(1)
	}

	// Collect all test functions from all files
	testFuncs := make(map[string][]string) // file -> []funcName
	for _, path := range flag.Args() {
		funcs, err := findTestFunctions(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error scanning %s: %v\n", path, err)
			os.Exit(1)
		}
		if len(funcs) > 0 {
			testFuncs[path] = funcs
		}
	}

	// Generate the main.go file (Go code, not GALA)
	code := generateMainFile(pkgName, testFuncs)

	if outputPath != "" {
		err := os.WriteFile(outputPath, []byte(code), 0644)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error writing output: %v\n", err)
			os.Exit(1)
		}
	} else {
		fmt.Print(code)
	}
}

func findTestFunctions(path string) ([]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var funcs []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		matches := testFuncRegex.FindStringSubmatch(line)
		if len(matches) >= 2 {
			funcs = append(funcs, matches[1])
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	return funcs, nil
}

func generateMainFile(pkgName string, testFuncs map[string][]string) string {
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("package %s\n\n", pkgName))

	// Always import std for NewImmutable
	sb.WriteString("import \"martianoff/gala/std\"\n")

	// Import test framework if not in package test (to avoid circular import)
	if pkgName != "test" {
		sb.WriteString("import . \"martianoff/gala/test\"\n")
	}
	sb.WriteString("\n")

	// Collect all functions sorted for deterministic output
	var allFuncs []string
	for _, funcs := range testFuncs {
		allFuncs = append(allFuncs, funcs...)
	}
	sort.Strings(allFuncs)

	sb.WriteString("func main() {\n")
	sb.WriteString("\tRunTests(")

	for i, funcName := range allFuncs {
		if i > 0 {
			sb.WriteString(", ")
		}
		// Generate Go struct literal syntax
		sb.WriteString(fmt.Sprintf("TestFunc{Name: std.NewImmutable(\"%s\"), F: std.NewImmutable(%s)}", funcName, funcName))
	}

	sb.WriteString(")\n")
	sb.WriteString("}\n")

	return sb.String()
}

// Helper to get the base name without extension
func baseName(path string) string {
	base := filepath.Base(path)
	ext := filepath.Ext(base)
	return base[:len(base)-len(ext)]
}
