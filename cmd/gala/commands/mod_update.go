package commands

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"martianoff/gala/internal/depman/fetch"
	"martianoff/gala/internal/depman/mod"
	"martianoff/gala/internal/depman/sum"
)

var modUpdateAll bool

var modUpdateCmd = &cobra.Command{
	Use:   "update [module...]",
	Short: "Update dependencies to latest versions",
	Long: `Update dependencies to their latest available versions.

By default, updates only the specified modules. Use --all to update all dependencies.

Examples:
  gala mod update github.com/example/utils    Update specific module
  gala mod update --all                       Update all dependencies`,
	Run: runModUpdate,
}

func init() {
	modUpdateCmd.Flags().BoolVar(&modUpdateAll, "all", false, "Update all dependencies")
}

func runModUpdate(cmd *cobra.Command, args []string) {
	// Check if gala.mod exists
	galaMod, err := mod.ParseFile("gala.mod")
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error: gala.mod not found. Run 'gala mod init' first.")
		os.Exit(1)
	}

	if len(args) == 0 && !modUpdateAll {
		fmt.Fprintln(os.Stderr, "Error: specify modules to update or use --all")
		os.Exit(1)
	}

	// Determine which modules to update
	var modulesToUpdate []string
	if modUpdateAll {
		for _, req := range galaMod.Require {
			modulesToUpdate = append(modulesToUpdate, req.Path)
		}
	} else {
		modulesToUpdate = args
	}

	if len(modulesToUpdate) == 0 {
		fmt.Println("No dependencies to update")
		return
	}

	// Create cache and fetcher
	cache := fetch.NewCache(nil)
	fetcher := fetch.NewGitFetcher(cache)

	// Load or create gala.sum
	galaSum, err := sum.ParseFile("gala.sum")
	if err != nil {
		galaSum = sum.NewFile()
	}

	updated := 0
	for _, modulePath := range modulesToUpdate {
		req := galaMod.GetRequire(modulePath)
		if req == nil {
			fmt.Fprintf(os.Stderr, "Warning: %s is not in gala.mod, skipping\n", modulePath)
			continue
		}

		oldVersion := req.Version

		// Fetch latest version
		fmt.Printf("Checking %s...\n", modulePath)
		newVersion, cachePath, hash, err := fetcher.FetchLatest(modulePath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to fetch %s: %v\n", modulePath, err)
			continue
		}

		if oldVersion == newVersion {
			fmt.Printf("  %s is already at latest (%s)\n", modulePath, newVersion)
			continue
		}

		// Update version in gala.mod
		req.Version = newVersion

		// Update gala.sum (remove old, add new)
		galaSum.Remove(modulePath, oldVersion)
		galaSum.Add(modulePath, newVersion, "", hash)

		// Also add gala.mod hash if present
		if info, err := cache.Info(modulePath, newVersion); err == nil && info.HasGalaMod {
			modHash, err := cache.GetGalaMod(modulePath, newVersion)
			if err == nil && modHash != nil {
				galaModHash, err := sum.HashFile(cachePath + "/gala.mod")
				if err == nil {
					galaSum.Add(modulePath, newVersion, "/gala.mod", galaModHash)
				}
			}
		}

		fmt.Printf("  %s: %s -> %s\n", modulePath, oldVersion, newVersion)
		updated++
	}

	// Write updated files
	if updated > 0 {
		if err := mod.WriteFile(galaMod, "gala.mod"); err != nil {
			fmt.Fprintf(os.Stderr, "Error: failed to write gala.mod: %v\n", err)
			os.Exit(1)
		}

		if err := sum.WriteFile(galaSum, "gala.sum"); err != nil {
			fmt.Fprintf(os.Stderr, "Error: failed to write gala.sum: %v\n", err)
			os.Exit(1)
		}
	}

	fmt.Printf("\nUpdated %d dependencies\n", updated)
}
