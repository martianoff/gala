package commands

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"martianoff/gala/internal/build"
)

var (
	buildOutput  string
	buildVerbose bool
)

var buildCmd = &cobra.Command{
	Use:   "build [directory]",
	Short: "Build a GALA project",
	Long: `Build compiles GALA source files into an executable binary.

This command:
  1. Reads dependencies from gala.mod
  2. Transpiles .gala files to Go code (in a build workspace)
  3. Runs go build to produce a binary

The binary is placed in the current directory by default.
No go.mod or generated files are created in your project directory.

Examples:
  gala build                    # Build current directory
  gala build ./myproject        # Build specific directory
  gala build -o myapp           # Custom output name
  gala build -v                 # Verbose output`,
	Args: cobra.MaximumNArgs(1),
	Run:  runBuild,
}

func init() {
	buildCmd.Flags().StringVarP(&buildOutput, "output", "o", "", "Output binary name")
	buildCmd.Flags().BoolVarP(&buildVerbose, "verbose", "v", false, "Verbose output")
}

func runBuild(cmd *cobra.Command, args []string) {
	// Determine project directory
	projectDir := "."
	if len(args) > 0 {
		projectDir = args[0]
	}

	// Resolve to absolute path
	absProjectDir, err := filepath.Abs(projectDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	// Check gala.mod exists
	galaModPath := filepath.Join(absProjectDir, "gala.mod")
	if _, err := os.Stat(galaModPath); err != nil {
		fmt.Fprintf(os.Stderr, "Error: gala.mod not found in %s\n", absProjectDir)
		fmt.Fprintln(os.Stderr, "Run 'gala mod init' to create one.")
		os.Exit(1)
	}

	// Create builder
	builder, err := build.NewBuilder(absProjectDir, Version, buildVerbose)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	// Run build
	outputPath, err := builder.Build(buildOutput)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Build failed: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Built: %s\n", outputPath)
}
