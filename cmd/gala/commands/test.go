package commands

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"martianoff/gala/internal/build"
)

var testVerbose bool

var testCmd = &cobra.Command{
	Use:   "test [directory]",
	Short: "Run tests in a GALA project",
	Long: `Test discovers and runs GALA test functions.

This command:
  1. Finds _test.gala files in the project directory
  2. Transpiles source and test files together
  3. Auto-discovers TestXxx(t T) T functions
  4. Generates a test runner main
  5. Builds and executes the test binary

Test functions must have the signature: func TestXxx(t T) T
where T is the test context from the test framework.

For package main projects, source and test files are compiled together.
For library packages, test files are compiled as package main and import
the library under test.

Examples:
  gala test                     # Test current directory
  gala test ./myproject         # Test specific directory
  gala test -v                  # Verbose output`,
	Args: cobra.MaximumNArgs(1),
	Run:  runTest,
}

func init() {
	testCmd.Flags().BoolVarP(&testVerbose, "verbose", "v", false, "Verbose output")
}

func runTest(cmd *cobra.Command, args []string) {
	// Determine project directory
	projectDir := "."
	if len(args) > 0 {
		projectDir = args[0]
	}

	absProjectDir, err := findProjectRoot(projectDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		fmt.Fprintln(os.Stderr, "Run 'gala mod init' to create one.")
		os.Exit(1)
	}

	// Create builder
	builder, err := build.NewBuilder(absProjectDir, Version, testVerbose)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	// Run tests
	if err := builder.Test(testVerbose); err != nil {
		fmt.Fprintf(os.Stderr, "Test failed: %v\n", err)
		os.Exit(1)
	}
}
