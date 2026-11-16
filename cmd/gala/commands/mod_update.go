package commands

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"golang.org/x/mod/semver"

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

	fetcher := fetch.NewGitFetcher(fetch.NewCache(nil))
	galaSum := loadGalaSum()

	updated := 0
	for _, modulePath := range modulesToUpdate {
		req := galaMod.GetRequire(modulePath)
		if req == nil {
			fmt.Fprintf(os.Stderr, "Warning: %s is not in gala.mod, skipping\n", modulePath)
			continue
		}

		oldVersion := req.Version
		fmt.Printf("Checking %s...\n", modulePath)
		latest, err := latestVersion(fetcher, downloadGoModule, *req)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to fetch %s: %v\n", modulePath, err)
			continue
		}
		changed, err := updateRequire(galaMod, galaSum, req, latest)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to update %s: %v\n", modulePath, err)
			continue
		}
		if !changed {
			fmt.Printf("  %s is already at latest (%s)\n", modulePath, oldVersion)
			continue
		}
		fmt.Printf("  %s: %s -> %s\n", modulePath, oldVersion, latest.version)
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

// latestModule is the latest version of a module, fetched.
type latestModule struct {
	version string
	dir     string
	hash    string // the GALA module hash; empty for a Go module
}

// latestVersion fetches the latest version of the module req requires: a Go
// module (`// go`) through the Go toolchain, as `gala mod add` resolves it,
// a GALA module from its Git repository.
func latestVersion(fetcher *fetch.GitFetcher, downloadGo goDownloader, req mod.Require) (latestModule, error) {
	if req.Go {
		m, err := downloadGo(req.Path, "latest")
		if err != nil {
			return latestModule{}, err
		}
		return latestModule{version: m.Version, dir: m.Dir}, nil
	}
	ver, dir, hash, err := fetcher.FetchLatest(req.Path)
	return latestModule{version: ver, dir: dir, hash: hash}, err
}

// updateRequire moves req to latest, and gala.sum with it, when latest is a
// newer version than the one req names; it reports whether it did. Like `go
// get -u`, it never downgrades: a pinned pseudo-version newer than the latest
// tag, or a prerelease above the latest release, stays.
func updateRequire(galaMod *mod.File, galaSum *sum.File, req *mod.Require, latest latestModule) (bool, error) {
	if semver.Compare(latest.version, req.Version) <= 0 {
		return false, nil
	}
	if req.Go {
		setGoRequire(galaMod, galaSum, req.Path, latest.version)
		return true, nil
	}
	galaSum.Remove(req.Path, req.Version)
	if err := recordGalaSum(galaSum, req.Path, latest.version, latest.dir, latest.hash); err != nil {
		return false, err
	}
	req.Version = latest.version
	return true, nil
}
