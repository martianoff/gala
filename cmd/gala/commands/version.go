package commands

import (
	"fmt"

	"github.com/spf13/cobra"
)

// Version information - can be set at build time
var (
	Version   = "dev"
	GitCommit = "unknown"
	BuildDate = "unknown"
)

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print the version of GALA",
	Long:  `Print the version information for the GALA compiler.`,
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("GALA version %s\n", Version)
		if GitCommit != "unknown" {
			fmt.Printf("  Git commit: %s\n", GitCommit)
		}
		if BuildDate != "unknown" {
			fmt.Printf("  Build date: %s\n", BuildDate)
		}
	},
}
