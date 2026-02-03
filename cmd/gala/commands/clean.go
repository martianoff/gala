package commands

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"martianoff/gala/internal/build"
)

var (
	cleanAll   bool
	cleanStale bool
)

var cleanCmd = &cobra.Command{
	Use:   "clean",
	Short: "Clean build workspaces and caches",
	Long: `Clean removes build workspaces and cached data.

By default, only cleans the workspace for the current project.

Options:
  --all     Remove all build workspaces
  --stale   Remove workspaces older than 7 days

Examples:
  gala clean              # Clean current project's workspace
  gala clean --all        # Clean all workspaces
  gala clean --stale      # Clean stale workspaces`,
	Run: runClean,
}

func init() {
	cleanCmd.Flags().BoolVar(&cleanAll, "all", false, "Clean all workspaces")
	cleanCmd.Flags().BoolVar(&cleanStale, "stale", false, "Clean workspaces older than 7 days")
}

func runClean(cmd *cobra.Command, args []string) {
	config := build.DefaultConfig()

	if cleanAll {
		// Clean all workspaces
		fmt.Println("Cleaning all build workspaces...")
		if err := build.CleanAllWorkspaces(config); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("Done.")
		return
	}

	if cleanStale {
		// Clean stale workspaces (older than 7 days)
		fmt.Println("Cleaning stale workspaces...")
		count, err := build.CleanStaleWorkspaces(config, 7*24*time.Hour)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Cleaned %d stale workspaces.\n", count)
		return
	}

	// Clean current project's workspace
	projectDir, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	// Check if gala.mod exists
	galaModPath := filepath.Join(projectDir, "gala.mod")
	if _, err := os.Stat(galaModPath); err != nil {
		fmt.Fprintf(os.Stderr, "Error: gala.mod not found in current directory\n")
		fmt.Fprintln(os.Stderr, "Use 'gala clean --all' to clean all workspaces.")
		os.Exit(1)
	}

	workspace, err := build.FindWorkspaceByProject(config, projectDir)
	if err != nil {
		fmt.Println("No workspace found for current project.")
		return
	}

	if err := workspace.Clean(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Cleaned workspace: %s\n", workspace.Dir)
}
