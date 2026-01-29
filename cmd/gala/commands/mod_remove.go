package commands

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"martianoff/gala/internal/depman/mod"
	"martianoff/gala/internal/depman/sum"
)

var modRemoveCmd = &cobra.Command{
	Use:   "remove <module>",
	Short: "Remove a dependency from gala.mod",
	Long: `Remove a dependency from gala.mod and gala.sum.

This command removes the specified module from the require block in gala.mod
and removes all corresponding entries from gala.sum.

Examples:
  gala mod remove github.com/example/utils`,
	Args: cobra.ExactArgs(1),
	Run:  runModRemove,
}

func runModRemove(cmd *cobra.Command, args []string) {
	modulePath := args[0]

	// Check if gala.mod exists
	galaMod, err := mod.ParseFile("gala.mod")
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error: gala.mod not found. Run 'gala mod init' first.")
		os.Exit(1)
	}

	// Check if module is required
	existing := galaMod.GetRequire(modulePath)
	if existing == nil {
		fmt.Fprintf(os.Stderr, "Error: module %s is not in gala.mod\n", modulePath)
		os.Exit(1)
	}

	version := existing.Version

	// Remove from gala.mod
	if !galaMod.RemoveRequire(modulePath) {
		fmt.Fprintf(os.Stderr, "Error: failed to remove %s from gala.mod\n", modulePath)
		os.Exit(1)
	}

	if err := mod.WriteFile(galaMod, "gala.mod"); err != nil {
		fmt.Fprintf(os.Stderr, "Error: failed to write gala.mod: %v\n", err)
		os.Exit(1)
	}

	// Remove from gala.sum if it exists
	galaSum, err := sum.ParseFile("gala.sum")
	if err == nil {
		// Remove all entries for this module/version
		galaSum.Remove(modulePath, version)

		if err := sum.WriteFile(galaSum, "gala.sum"); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to update gala.sum: %v\n", err)
		}
	}

	fmt.Printf("Removed %s@%s\n", modulePath, version)
}
