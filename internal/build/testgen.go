package build

import (
	"bufio"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
)

// testFuncRegex matches GALA test function declarations that start with Test.
// Pattern: func TestXxx(t T) T or func TestXxx(t test.T) test.T
var testFuncRegex = regexp.MustCompile(`^\s*func\s+(Test\w+)\s*\(\s*\w+\s+(?:test\.)?T\s*\)\s+(?:test\.)?T`)

// FindTestFunctions scans a .gala file for test function declarations.
// It returns the names of all functions matching func TestXxx(t T) T.
func FindTestFunctions(path string) ([]string, error) {
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

// GenerateTestMain generates a Go main() file that calls RunTests with all
// discovered test functions. The generated code imports the test framework
// and std library.
func GenerateTestMain(testFuncs []string) string {
	var sb strings.Builder

	sb.WriteString("package main\n\n")

	// Always import std for NewImmutable
	sb.WriteString("import \"martianoff/gala/std\"\n")
	sb.WriteString("import . \"martianoff/gala/test\"\n")
	sb.WriteString("\n")

	// Sort for deterministic output
	sorted := make([]string, len(testFuncs))
	copy(sorted, testFuncs)
	sort.Strings(sorted)

	sb.WriteString("func main() {\n")
	sb.WriteString("\tRunTests(")

	for i, funcName := range sorted {
		if i > 0 {
			sb.WriteString(", ")
		}
		sb.WriteString(fmt.Sprintf("TestFunc{Name: std.NewImmutable(\"%s\"), F: std.NewImmutable(%s)}", funcName, funcName))
	}

	sb.WriteString(")\n")
	sb.WriteString("}\n")

	return sb.String()
}
