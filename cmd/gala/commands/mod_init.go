package commands

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"martianoff/gala/internal/depman/mod"
)

var modInitCmd = &cobra.Command{
	Use:   "init [module-path]",
	Short: "Initialize a new gala.mod file",
	Long: `Initialize a new gala.mod file in the current directory.

If a module path is not provided, gala will attempt to detect it from:
  1. An existing go.mod file in the current directory
  2. The current directory name

Examples:
  gala mod init                              # Auto-detect module path
  gala mod init github.com/user/project      # Explicit module path`,
	Args: cobra.MaximumNArgs(1),
	Run:  runModInit,
}

func runModInit(cmd *cobra.Command, args []string) {
	// Check if gala.mod already exists
	if _, err := os.Stat("gala.mod"); err == nil {
		fmt.Fprintln(os.Stderr, "Error: gala.mod already exists")
		os.Exit(1)
	}

	// Determine module path
	var modulePath string
	if len(args) > 0 {
		modulePath = args[0]
	} else {
		// Try to detect from go.mod
		modulePath = detectModulePathFromGoMod()
		if modulePath == "" {
			// Fall back to current directory name
			cwd, err := os.Getwd()
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error: failed to get current directory: %v\n", err)
				os.Exit(1)
			}
			modulePath = filepath.Base(cwd)
			fmt.Printf("Warning: No go.mod found, using directory name: %s\n", modulePath)
			fmt.Println("Consider providing an explicit module path: gala mod init <module-path>")
		}
	}

	// Validate module path
	if modulePath == "" {
		fmt.Fprintln(os.Stderr, "Error: module path cannot be empty")
		fmt.Fprintln(os.Stderr, "Usage: gala mod init <module-path>")
		os.Exit(1)
	}

	// Create gala.mod file
	f := mod.NewFile(modulePath)
	f.Gala = "1.0" // Default GALA version

	err := mod.WriteFile(f, "gala.mod")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: failed to write gala.mod: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Initialized gala.mod for module %s\n", modulePath)

	// Create empty gala.sum if it doesn't exist
	if _, err := os.Stat("gala.sum"); os.IsNotExist(err) {
		err = os.WriteFile("gala.sum", []byte{}, 0644)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to create gala.sum: %v\n", err)
		}
	}
}

// detectModulePathFromGoMod reads the module path from go.mod if it exists.
func detectModulePathFromGoMod() string {
	content, err := os.ReadFile("go.mod")
	if err != nil {
		return ""
	}

	lines := strings.Split(string(content), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "module ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "module "))
		}
	}

	return ""
}
