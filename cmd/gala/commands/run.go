package commands

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/spf13/cobra"

	"martianoff/gala/internal/build"
)

var runVerbose bool

var runCmd = &cobra.Command{
	Use:   "run [directory] [-- args...]",
	Short: "Build and run a GALA project",
	Long: `Run builds a GALA project and executes it immediately.

Arguments after -- are passed to the executed program.

Examples:
  gala run                      # Build and run current directory
  gala run ./myproject          # Build and run specific directory
  gala run -- arg1 arg2         # Pass arguments to the program
  gala run -v                   # Verbose output`,
	Args:               cobra.ArbitraryArgs,
	DisableFlagParsing: false,
	Run:                runRun,
}

func init() {
	runCmd.Flags().BoolVarP(&runVerbose, "verbose", "v", false, "Verbose output")
}

// splitRunArgs separates the project directory from program arguments
// using cobra's ArgsLenAtDash() to locate the `--` terminator.
//
// Cobra/pflag strips the `--` terminator from the args slice before
// invoking Run, but records the split point via cmd.ArgsLenAtDash():
//   - returns -1 when no `--` was present on the command line
//   - returns the count of args *before* `--` otherwise
//
// So for `gala run main.gala -- hello world`:
//
//	args               = ["main.gala", "hello", "world"]
//	argsLenAtDash      = 1
//	projectDir         = "main.gala"
//	programArgs        = ["hello", "world"]
//
// When no `--` is present, every positional is treated as the project
// directory (only the first is used) and programArgs is empty.
func splitRunArgs(args []string, argsLenAtDash int) (projectDir string, programArgs []string) {
	projectDir = "."
	if argsLenAtDash >= 0 {
		// A `--` separator was present on the command line.
		if argsLenAtDash > 0 {
			projectDir = args[0]
		}
		programArgs = args[argsLenAtDash:]
	} else if len(args) > 0 {
		projectDir = args[0]
	}
	return projectDir, programArgs
}

func runRun(cmd *cobra.Command, args []string) {
	projectDir, programArgs := splitRunArgs(args, cmd.ArgsLenAtDash())

	// Resolve project root and optional source directory.
	// If the argument is a .gala file or subdirectory, find gala.mod for deps
	// but compile from the file's directory (multi-package build).
	// Matching `go run main.go` semantics: if no gala.mod exists and the
	// argument is a single .gala file, fall back to an ephemeral module
	// rooted at the file's directory. The Builder already synthesizes an
	// in-memory gala.mod when none is on disk.
	absProjectDir, ephemeral, err := resolveRunTarget(projectDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		fmt.Fprintln(os.Stderr, "Run 'gala mod init <module-path>' to create one. Example: gala mod init example.com/myapp")
		os.Exit(1)
	}
	if ephemeral && runVerbose {
		fmt.Fprintf(os.Stderr, "No gala.mod found; running %s in ephemeral module at %s\n", projectDir, absProjectDir)
	}

	// Create builder
	builder, err := build.NewBuilder(absProjectDir, Version, runVerbose)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	// If the argument points to a subdirectory or file, set it as source dir
	// for multi-package builds (e.g., `gala run examples/hello/main.gala`)
	absArg, _ := filepath.Abs(projectDir)
	if info, err := os.Stat(absArg); err == nil && !info.IsDir() {
		absArg = filepath.Dir(absArg)
	}
	if absArg != absProjectDir {
		builder.SetSourceDir(absArg)
	}

	// Build to the workspace directory (not project dir)
	tempOutput := filepath.Join(builder.Workspace().Dir, "run-output")

	// Run build with absolute path to workspace
	outputPath, err := builder.Build(tempOutput)
	if err != nil {
		exitBuildFailed(cmd, err)
	}

	if outputPath == "" {
		fmt.Fprintln(os.Stderr, "Error: nothing to run — this project has no 'package main'.")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "  gala build   compile-check library packages")
		os.Exit(1)
	}

	// Execute the built binary
	execCmd := exec.Command(outputPath, programArgs...)
	execCmd.Stdin = os.Stdin
	execCmd.Stdout = os.Stdout
	execCmd.Stderr = os.Stderr
	execCmd.Dir = absProjectDir // Run from project directory

	if err := execCmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			os.Exit(exitErr.ExitCode())
		}
		fmt.Fprintf(os.Stderr, "Error running program: %v\n", err)
		os.Exit(1)
	}
}
