package commands

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"martianoff/gala/internal/depman/fetch"
	"martianoff/gala/internal/depman/mod"
	"martianoff/gala/internal/depman/sum"
)

var modVerifyCmd = &cobra.Command{
	Use:   "verify",
	Short: "Verify dependencies match gala.sum",
	Long: `Verify that all cached dependencies match their expected hashes in gala.sum.

This command checks each module in the cache against its recorded hash in gala.sum
to ensure the cached content has not been modified or corrupted.

Examples:
  gala mod verify`,
	Run: runModVerify,
}

func init() {
	modCmd.AddCommand(modVerifyCmd)
}

func runModVerify(cmd *cobra.Command, args []string) {
	// Check if gala.mod exists
	galaMod, err := mod.ParseFile("gala.mod")
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error: gala.mod not found.")
		os.Exit(1)
	}

	// Load gala.sum
	galaSum, err := sum.ParseFile("gala.sum")
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error: gala.sum not found or invalid.")
		os.Exit(1)
	}

	// Create cache
	cache := fetch.NewCache(nil)

	verified := 0
	errors := 0
	missing := 0

	// Verify each required module
	for _, req := range galaMod.Require {
		entry := galaSum.Get(req.Path, req.Version, "")
		if entry == nil {
			fmt.Printf("MISSING: %s@%s (not in gala.sum)\n", req.Path, req.Version)
			missing++
			continue
		}

		// Check if cached
		if !cache.Config().IsCached(req.Path, req.Version) {
			fmt.Printf("NOT CACHED: %s@%s\n", req.Path, req.Version)
			missing++
			continue
		}

		// Verify hash
		err := cache.Verify(req.Path, req.Version, entry.Hash)
		if err != nil {
			fmt.Printf("FAILED: %s@%s\n", req.Path, req.Version)
			if mismatch, ok := err.(*sum.HashMismatchError); ok {
				fmt.Printf("  Expected: %s\n", mismatch.Expected)
				fmt.Printf("  Actual:   %s\n", mismatch.Actual)
			}
			errors++
		} else {
			fmt.Printf("OK: %s@%s\n", req.Path, req.Version)
			verified++
		}

		// Also verify gala.mod hash if present
		modEntry := galaSum.Get(req.Path, req.Version, "/gala.mod")
		if modEntry != nil {
			modPath := cache.Config().ModulePath(req.Path, req.Version) + "/gala.mod"
			hash, err := sum.HashFile(modPath)
			if err != nil {
				fmt.Printf("  gala.mod: MISSING\n")
			} else if hash != modEntry.Hash {
				fmt.Printf("  gala.mod: HASH MISMATCH\n")
				errors++
			} else {
				fmt.Printf("  gala.mod: OK\n")
			}
		}
	}

	// Summary
	fmt.Println()
	fmt.Printf("Verified: %d, Failed: %d, Missing: %d\n", verified, errors, missing)

	if errors > 0 {
		fmt.Fprintln(os.Stderr, "\nVerification failed!")
		os.Exit(1)
	}

	if missing > 0 {
		fmt.Println("\nSome modules are not cached. Run 'gala mod download' to fetch them.")
	}
}
