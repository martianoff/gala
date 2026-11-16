package commands

import (
	"cmp"
	"fmt"
	"os"
	"path/filepath"
	"slices"
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

A module with no tagged version resolves to the pseudo-version of its latest
commit (v0.0.0-20220603013715-3360bf22bea4), and a pseudo-version can be
given as the version.

A Go module (one with no .gala files or gala.mod) is tracked in gala.mod but
not transpiled. The Go toolchain resolves and downloads it, as 'go get' does.
Use --go to skip looking for a GALA module first.

Examples:
  gala mod add github.com/example/gala-utils
  gala mod add github.com/example/gala-utils@v1.2.3
  gala mod add github.com/example/go-lib@v2.0.0 --go
  gala mod add golang.org/x/text@latest --go`,
	Args: cobra.ExactArgs(1),
	Run:  runModAdd,
}

func init() {
	modAddCmd.Flags().BoolVar(&modAddIsGo, "go", false, "Mark as a Go (not GALA) dependency")
}

func runModAdd(cmd *cobra.Command, args []string) {
	modulePath, query := parseModuleArg(args[0])

	galaMod, err := mod.ParseFile("gala.mod")
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error: gala.mod not found. Run 'gala mod init' first.")
		os.Exit(1)
	}
	if existing := galaMod.GetRequire(modulePath); existing != nil {
		fmt.Printf("Module %s already required at version %s\n", modulePath, existing.Version)
		fmt.Println("Updating to new version...")
	}

	// Read gala.sum before changing anything.
	galaSum := loadGalaSum()
	fetcher := fetch.NewGitFetcher(fetch.NewCache(nil))

	fmt.Printf("Fetching %s@%s...\n", modulePath, cmp.Or(query, "latest"))
	added, err := addModule(galaMod, galaSum, fetcher, downloadGoModule, modulePath, query, modAddIsGo)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: failed to fetch module: %v\n", err)
		os.Exit(1)
	}
	if err := mod.WriteFile(galaMod, "gala.mod"); err != nil {
		fmt.Fprintf(os.Stderr, "Error: failed to write gala.mod: %v\n", err)
		os.Exit(1)
	}
	if err := sum.WriteFile(galaSum, "gala.sum"); err != nil {
		fmt.Fprintf(os.Stderr, "Error: failed to write gala.sum: %v\n", err)
		os.Exit(1)
	}

	if added.goModule {
		fmt.Printf("Added %s@%s (Go dependency)\n", modulePath, added.version)
	} else {
		fmt.Printf("Added %s@%s\n", modulePath, added.version)
	}
	fmt.Printf("  -> %s\n", added.dir)
}

// addedModule is the requirement addModule recorded.
type addedModule struct {
	version  string
	dir      string // where the module's source was downloaded
	goModule bool
}

// goDownloader resolves modulePath@query with the Go toolchain and downloads
// that version (see fetch.DownloadGoModule).
type goDownloader func(modulePath, query string) (*fetch.GoModule, error)

// addModule requires modulePath, at the version query resolves to (latest when
// empty), in galaMod and records its checksums in galaSum.
//
// A GALA module is fetched from its Git repository. A Go module is resolved
// and downloaded by the Go toolchain (downloadGo), as `go get` does and as the
// build downloads Go modules, so it resolves the same way whether or not
// isGo says it is one: a vanity path (golang.org/x/text) names no Git
// repository, and only Go knows where it lives. Without isGo, a module is a
// Go module when the Git fetch fails or brings back no .gala file or gala.mod.
func addModule(galaMod *mod.File, galaSum *sum.File, fetcher *fetch.GitFetcher, downloadGo goDownloader, modulePath, query string, isGo bool) (addedModule, error) {
	var gitErr error
	if !isGo {
		var ver, dir, hash string
		ver, dir, hash, gitErr = fetchGitModule(fetcher, modulePath, query)
		if gitErr == nil && hasGalaFiles(dir) {
			galaMod.AddRequire(modulePath, ver, false)
			galaMod.GetRequire(modulePath).Go = false
			if err := recordGalaSum(galaSum, modulePath, ver, dir, hash); err != nil {
				return addedModule{}, err
			}
			return addedModule{version: ver, dir: dir}, nil
		}
	}
	m, err := downloadGo(modulePath, cmp.Or(query, "latest"))
	if err != nil {
		if gitErr != nil {
			return addedModule{}, fmt.Errorf("%w (as a GALA module from Git: %v)", err, gitErr)
		}
		return addedModule{}, err
	}
	// Added by name, so a direct requirement.
	setGoRequire(galaMod, galaSum, modulePath, m.Version).Indirect = false
	return addedModule{version: m.Version, dir: m.Dir, goModule: true}, nil
}

// fetchGitModule fetches modulePath at the version query names, its latest
// version when query is empty or "latest", from its Git repository. It
// returns the version, its cache directory and its hash.
func fetchGitModule(fetcher *fetch.GitFetcher, modulePath, query string) (string, string, string, error) {
	if query == "" || query == "latest" {
		return fetcher.FetchLatest(modulePath)
	}
	dir, hash, err := fetcher.Fetch(modulePath, query)
	return query, dir, hash, err
}

// recordGalaSum records the hash of GALA module modulePath at ver, fetched
// into dir, in galaSum, and the hash of its gala.mod when it has one.
func recordGalaSum(galaSum *sum.File, modulePath, ver, dir, hash string) error {
	galaSum.Add(modulePath, ver, "", hash)
	galaModPath := filepath.Join(dir, "gala.mod")
	if _, err := os.Stat(galaModPath); err != nil {
		return nil
	}
	galaModHash, err := sum.HashFile(galaModPath)
	if err != nil {
		return fmt.Errorf("failed to hash %s: %w", galaModPath, err)
	}
	galaSum.Add(modulePath, ver, "/gala.mod", galaModHash)
	return nil
}

// downloadGoModule resolves modulePath@query with the Go toolchain and
// downloads it into the Go module cache the build reads Go modules from.
func downloadGoModule(modulePath, query string) (*fetch.GoModule, error) {
	return fetch.DownloadGoModule(fetch.GoModCacheDir(fetch.DefaultGalaHome()), modulePath, query)
}

// setGoRequire requires the Go module modulePath at ver in galaMod, and
// returns the requirement. Its checksum is go.sum's to keep, not gala.sum's
// (see galaSumFor), so gala.sum loses every entry it has for the module, such
// as the ones it had while the module was required as a GALA module.
func setGoRequire(galaMod *mod.File, galaSum *sum.File, modulePath, ver string) *mod.Require {
	galaSum.Entries = slices.DeleteFunc(galaSum.Entries, func(e sum.Entry) bool { return e.Path == modulePath })
	req := galaMod.GetRequire(modulePath)
	if req == nil {
		galaMod.AddRequire(modulePath, ver, false)
		req = galaMod.GetRequire(modulePath)
	}
	req.Version = ver
	req.Go = true
	return req
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

// loadGalaSum reads the project's gala.sum (an absent file reads as empty) for
// a command that rewrites it. A gala.sum that cannot be parsed stops the
// command: rewriting it from what could be read would silently drop every
// other module's checksum.
func loadGalaSum() *sum.File {
	galaSum, err := sum.ParseFile("gala.sum")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: cannot read gala.sum: %v\n", err)
		os.Exit(1)
	}
	return galaSum
}
