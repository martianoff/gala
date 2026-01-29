package commands

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"martianoff/gala/internal/depman/fetch"
	"martianoff/gala/internal/depman/mod"
	"martianoff/gala/internal/depman/sum"
)

var modAddIsGo bool

var modAddCmd = &cobra.Command{
	Use:   "add <module>[@version]",
	Short: "Add a dependency to gala.mod",
	Long: `Add a dependency to gala.mod and download it to the cache.

The module path can optionally include a version specifier:
  - module@v1.2.3    Specific version
  - module@^1.0.0    Compatible version (caret constraint)
  - module@latest    Latest available version
  - module           Same as @latest

Use --go flag to mark a dependency as a Go package (not GALA).
Go dependencies are tracked in gala.mod but not transpiled.

Examples:
  gala mod add github.com/example/gala-utils
  gala mod add github.com/example/gala-utils@v1.2.3
  gala mod add github.com/example/go-lib@v2.0.0 --go`,
	Args: cobra.ExactArgs(1),
	Run:  runModAdd,
}

func init() {
	modAddCmd.Flags().BoolVar(&modAddIsGo, "go", false, "Mark as a Go (not GALA) dependency")
}

func runModAdd(cmd *cobra.Command, args []string) {
	// Parse module path and version
	modulePath, versionSpec := parseModuleArg(args[0])

	// Check if gala.mod exists
	galaMod, err := mod.ParseFile("gala.mod")
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error: gala.mod not found. Run 'gala mod init' first.")
		os.Exit(1)
	}

	// Check if already required
	if existing := galaMod.GetRequire(modulePath); existing != nil {
		fmt.Printf("Module %s already required at version %s\n", modulePath, existing.Version)
		fmt.Println("Updating to new version...")
	}

	// Create cache and fetcher
	cache := fetch.NewCache(nil)
	fetcher := fetch.NewGitFetcher(cache)

	var version string
	var cachePath string
	var hash string

	// Determine version to fetch
	if versionSpec == "" || versionSpec == "latest" {
		fmt.Printf("Fetching latest version of %s...\n", modulePath)
		ver, path, h, err := fetcher.FetchLatest(modulePath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: failed to fetch module: %v\n", err)
			os.Exit(1)
		}
		version = ver
		cachePath = path
		hash = h
	} else {
		fmt.Printf("Fetching %s@%s...\n", modulePath, versionSpec)
		path, h, err := fetcher.Fetch(modulePath, versionSpec)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: failed to fetch module: %v\n", err)
			os.Exit(1)
		}
		version = versionSpec
		cachePath = path
		hash = h
	}

	// Auto-detect if this is a Go package (no .gala files or gala.mod in cache)
	isGoPackage := modAddIsGo
	if !isGoPackage {
		isGoPackage = !hasGalaFiles(cachePath)
	}

	// Update gala.mod
	galaMod.AddRequire(modulePath, version, false)
	// Mark as Go package if detected
	if isGoPackage {
		if req := galaMod.GetRequire(modulePath); req != nil {
			req.Go = true
		}
	}
	if err := mod.WriteFile(galaMod, "gala.mod"); err != nil {
		fmt.Fprintf(os.Stderr, "Error: failed to write gala.mod: %v\n", err)
		os.Exit(1)
	}

	// Update gala.sum
	galaSum, err := sum.ParseFile("gala.sum")
	if err != nil {
		galaSum = sum.NewFile()
	}
	galaSum.Add(modulePath, version, "", hash)

	// Also add gala.mod hash if present
	if info, err := cache.Info(modulePath, version); err == nil && info.HasGalaMod {
		modHash, err := cache.GetGalaMod(modulePath, version)
		if err == nil && modHash != nil {
			// Compute hash of the gala.mod content
			galaModHash, err := sum.HashFile(cachePath + "/gala.mod")
			if err == nil {
				galaSum.Add(modulePath, version, "/gala.mod", galaModHash)
			}
		}
	}

	if err := sum.WriteFile(galaSum, "gala.sum"); err != nil {
		fmt.Fprintf(os.Stderr, "Error: failed to write gala.sum: %v\n", err)
		os.Exit(1)
	}

	if isGoPackage {
		fmt.Printf("Added %s@%s (Go dependency)\n", modulePath, version)
	} else {
		fmt.Printf("Added %s@%s\n", modulePath, version)
	}
	fmt.Printf("  -> %s\n", cachePath)
}

// hasGalaFiles checks if a directory contains .gala files or gala.mod.
func hasGalaFiles(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if name == "gala.mod" || strings.HasSuffix(name, ".gala") {
			return true
		}
	}

	return false
}

// parseModuleArg parses a module argument like "github.com/example/utils@v1.2.3"
// Returns the module path and version specifier (empty if not specified).
func parseModuleArg(arg string) (modulePath, versionSpec string) {
	if idx := strings.LastIndex(arg, "@"); idx > 0 {
		return arg[:idx], arg[idx+1:]
	}
	return arg, ""
}
