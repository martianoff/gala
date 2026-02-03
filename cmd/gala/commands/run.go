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

func runRun(cmd *cobra.Command, args []string) {
	// Separate project directory from program arguments
	projectDir := "."
	var programArgs []string

	// Find -- separator
	dashDashIdx := -1
	for i, arg := range args {
		if arg == "--" {
			dashDashIdx = i
			break
		}
	}

	if dashDashIdx >= 0 {
		// Arguments before -- are for us
		if dashDashIdx > 0 {
			projectDir = args[0]
		}
		// Arguments after -- are for the program
		programArgs = args[dashDashIdx+1:]
	} else if len(args) > 0 {
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
	builder, err := build.NewBuilder(absProjectDir, Version, runVerbose)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	// Build to the workspace directory (not project dir)
	tempOutput := filepath.Join(builder.Workspace().Dir, "run-output")

	// Run build with absolute path to workspace
	outputPath, err := builder.Build(tempOutput)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Build failed: %v\n", err)
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

