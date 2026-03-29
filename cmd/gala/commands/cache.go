package commands

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

var cacheCmd = &cobra.Command{
	Use:   "cache",
	Short: "Manage the analysis cache",
	Long: `Manage the GALA analysis cache.

Subcommands:
  clean    Remove all cached analysis data

Examples:
  gala cache clean    # Clear the analysis cache`,
}

var cacheCleanCmd = &cobra.Command{
	Use:   "clean",
	Short: "Clear the analysis cache",
	Long: `Clear all cached analysis data from .gala/cache/.

This forces the transpiler to re-analyze all packages on the next build.
Useful when upgrading the transpiler or debugging stale cache issues.`,
	Run: func(cmd *cobra.Command, args []string) {
		cwd, err := os.Getwd()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		cacheDir := filepath.Join(cwd, ".gala", "cache")
		if _, err := os.Stat(cacheDir); os.IsNotExist(err) {
			fmt.Println("No analysis cache found.")
			return
		}
		if err := os.RemoveAll(cacheDir); err != nil {
			fmt.Fprintf(os.Stderr, "Error cleaning cache: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("Analysis cache cleared.")
	},
}

func init() {
	cacheCmd.AddCommand(cacheCleanCmd)
}
