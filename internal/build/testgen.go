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

// GenerateGoTestHarness generates a _test.go file that bridges GALA tests
// with Go's testing framework. This enables internal tests (same package)
// for library packages, allowing access to unexported identifiers.
//
// The generated file uses TestMain(m *testing.M) as the entry point,
// which calls the GALA test framework's RunTests(). This is a _test.go
// file so it's only compiled by `go test`, not `go build`.
func GenerateGoTestHarness(pkgName string, testFuncs []string) string {
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("package %s\n\n", pkgName))

	sb.WriteString("import (\n")
	sb.WriteString("\t\"testing\"\n")
	sb.WriteString("\t\"martianoff/gala/std\"\n")
	sb.WriteString("\t. \"martianoff/gala/test\"\n")
	sb.WriteString(")\n\n")

	// Sort for deterministic output
	sorted := make([]string, len(testFuncs))
	copy(sorted, testFuncs)
	sort.Strings(sorted)

	// Generate TestMain that delegates to GALA's RunTests
	sb.WriteString("func TestMain(m *testing.M) {\n")
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
