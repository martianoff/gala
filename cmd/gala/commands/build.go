package commands

import (
	"fmt"
	"os"

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
	Long: `Build compiles GALA source files.

For executable projects (package main), produces a binary.
For library packages, performs a compile check without producing a binary.

This command:
  1. Reads dependencies from gala.mod
  2. Transpiles .gala files to Go code (in a build workspace)
  3. Runs go build to produce a binary (or compile-check for libraries)

The binary is placed in the current directory by default.
No go.mod or generated files are created in your project directory.

Examples:
  gala build                    # Build current directory
  gala build ./myproject        # Build specific directory
  gala build -o myapp           # Custom output name (executables only)
  gala build -v                 # Verbose output`,
	Args: cobra.MaximumNArgs(1),
	Run:  runBuild,
}

func init() {
	buildCmd.Flags().StringVarP(&buildOutput, "output", "o", "", "Output binary name")
	buildCmd.Flags().BoolVarP(&buildVerbose, "verbose", "v", false, "Verbose output")
}

func runBuild(cmd *cobra.Command, args []string) {
	// Determine project directory — walk up to find gala.mod
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

	if outputPath == "" {
		// Library package — compile check succeeded, no binary produced
		fmt.Println("ok (library compiled successfully)")
	} else {
		fmt.Printf("Built: %s\n", outputPath)
	}
}
