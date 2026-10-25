package commands

import (
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"martianoff/gala/galaerr"
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
	builder, err := build.NewBuilderForMode(absProjectDir, Version, testVerbose, build.ModeTest)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	// Run tests
	if err := builder.Test(testVerbose); err != nil {
		if hasGalaDiagnostic(err) {
			// A compile error: frame it the way `gala build` does, each
			// diagnostic at its own file — the wrapping text names the file
			// being transpiled, which for a sibling's syntax error is not
			// the file the error is in.
			fmt.Fprintln(os.Stderr, galaerr.RenderRich(err, galaerr.Options{Color: galaerr.ColorEnabled()}))
		} else {
			fmt.Fprintf(os.Stderr, "Test failed: %v\n", err)
		}
		os.Exit(1)
	}
}

// hasGalaDiagnostic reports whether err carries a GALA compile diagnostic, as
// opposed to a failing test or a `go` tool error.
func hasGalaDiagnostic(err error) bool {
	var multi *galaerr.MultiError
	var sem *galaerr.SemanticError
	var syn *galaerr.SyntaxError
	return errors.As(err, &multi) || errors.As(err, &sem) || errors.As(err, &syn)
}
