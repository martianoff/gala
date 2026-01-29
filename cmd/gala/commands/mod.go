package commands

import (
	"github.com/spf13/cobra"
)

var modCmd = &cobra.Command{
	Use:   "mod",
	Short: "GALA module dependency management",
	Long: `GALA module dependency management commands.

Commands:
  init      Initialize a new gala.mod file
  add       Add a dependency
  remove    Remove a dependency
  update    Update dependencies
  tidy      Sync gala.mod with imports
  graph     Print dependency tree
  verify    Verify gala.sum hashes`,
	Run: func(cmd *cobra.Command, args []string) {
		cmd.Help()
	},
}

func init() {
	modCmd.AddCommand(modInitCmd)
	modCmd.AddCommand(modAddCmd)
	modCmd.AddCommand(modRemoveCmd)
	modCmd.AddCommand(modUpdateCmd)
	modCmd.AddCommand(modTidyCmd)
	modCmd.AddCommand(modVerifyCmd)
	modCmd.AddCommand(modGraphCmd)
}
