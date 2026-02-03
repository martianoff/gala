// Package commands provides the CLI commands for the gala tool.
package commands

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "gala [file.gala]",
	Short: "GALA language transpiler and dependency manager",
	Long: `GALA is a functional programming language that transpiles to Go.

This tool provides:
  - Build and run capabilities (no go.mod needed in project)
  - Transpilation of GALA source files to Go
  - Dependency management (gala mod)

Usage:
  gala build                    Build project to binary
  gala run                      Build and run project
  gala build -o myapp           Build with custom output name
  gala mod init                 Initialize gala.mod
  gala mod add <pkg>@<version>  Add a dependency
  gala mod tidy                 Tidy dependencies
  gala clean                    Clean build workspace
  gala version                  Print version

Legacy transpilation (creates files in project directory):
  gala transpile --local main.gala    Transpile to local directory`,
	// Accept any arguments - we'll handle .gala files
	Args: cobra.ArbitraryArgs,
	// Disable unknown command errors for backwards compatibility
	SilenceErrors: true,
	SilenceUsage:  true,
	// Run transpile by default if a .gala file is provided as argument
	RunE: func(cmd *cobra.Command, args []string) error {
		// Check if input flag is set
		if transpileInput != "" {
			runTranspile(cmd, args)
			return nil
		}

		// Check if first argument is a .gala file
		if len(args) > 0 && strings.HasSuffix(args[0], ".gala") {
			runTranspile(cmd, args)
			return nil
		}

		// No input, show help
		if len(args) == 0 {
			return cmd.Help()
		}

		// Unknown argument
		return fmt.Errorf("unknown command %q for \"gala\"\nRun 'gala --help' for usage", args[0])
	},
}

// Execute runs the root command.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
}

func init() {
	// Add subcommands
	rootCmd.AddCommand(transpileCmd)
	rootCmd.AddCommand(modCmd)
	rootCmd.AddCommand(versionCmd)
	rootCmd.AddCommand(buildCmd)
	rootCmd.AddCommand(runCmd)
	rootCmd.AddCommand(cleanCmd)

	// Add global flags that mirror transpile flags for backward compatibility
	rootCmd.Flags().StringVarP(&transpileInput, "input", "i", "", "Path to the input .gala file")
	rootCmd.Flags().StringVarP(&transpileOutput, "output", "o", "", "Path to the output .go file")
	rootCmd.Flags().BoolVarP(&transpileRun, "run", "r", false, "Execute the generated Go code")
	rootCmd.Flags().StringVarP(&transpileSearch, "search", "s", ".", "Comma-separated search paths")
}
