package commands

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/mod/modfile"

	"martianoff/gala/internal/depman/mod"
)

var modInitCmd = &cobra.Command{
	Use:   "init [module-path]",
	Short: "Initialize a new gala.mod file",
	Long: `Initialize a new gala.mod file in the current directory.

If a module path is not provided, gala will attempt to detect it from:
  1. An existing go.mod file in the current directory
  2. The current directory name

In a Go module (a directory with a go.mod), every requirement of go.mod is
copied into gala.mod as a Go dependency (marked // go), at the version go.mod
names, so gala build uses the same versions as go build. Indirect requirements
stay marked // indirect.

gala mod init also adds .gala/, the directory gala build keeps its cache in,
to .gitignore, creating the file if there is none.

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

	// Read go.mod first: a go.mod that cannot be parsed stops the command
	// before anything is written.
	goMod, err := readGoMod("go.mod")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	// Determine module path
	var modulePath string
	if len(args) > 0 {
		modulePath = args[0]
	} else if goMod != nil && goMod.Module != nil && goMod.Module.Mod.Path != "" {
		modulePath = goMod.Module.Mod.Path
	} else {
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

	// Validate module path
	if modulePath == "" {
		fmt.Fprintln(os.Stderr, "Error: module path cannot be empty")
		fmt.Fprintln(os.Stderr, "Usage: gala mod init <module-path>")
		os.Exit(1)
	}

	// Create gala.mod file
	f := mod.NewFile(modulePath)
	f.Gala = Version // Use current GALA version
	replaces := 0
	if goMod != nil {
		f.Require = goRequirementsOf(goMod)
		replaces = len(goMod.Replace)
	}

	if err := mod.WriteFile(f, "gala.mod"); err != nil {
		fmt.Fprintf(os.Stderr, "Error: failed to write gala.mod: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Initialized gala.mod for module %s\n", modulePath)
	reportCopiedGoRequirements(f.Require, replaces)

	// Create empty gala.sum if it doesn't exist
	if _, err := os.Stat("gala.sum"); os.IsNotExist(err) {
		err = os.WriteFile("gala.sum", []byte{}, 0644)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to create gala.sum: %v\n", err)
		}
	}

	if added, err := ensureGalaGitignored(".gitignore"); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to add %s to .gitignore: %v\n", galaCacheDirIgnore, err)
	} else if added {
		fmt.Printf("Added %s to .gitignore\n", galaCacheDirIgnore)
	}
}

// readGoMod parses the go.mod at path; nil, without an error, when there is
// none.
func readGoMod(path string) (*modfile.File, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("cannot read %s: %w", path, err)
	}
	f, err := modfile.ParseLax(path, data, nil)
	if err != nil {
		return nil, fmt.Errorf("cannot parse %s: %w", path, err)
	}
	return f, nil
}

// goRequirementsOf returns the requirements of goMod as gala.mod Go
// requirements, in go.mod's order and at its versions. `gala build` takes
// the version of a Go module from gala.mod, never from go.mod, so a module
// left out would build at whatever version the build workspace's `go mod
// tidy` picks: its latest when nothing else requires it.
//
// Indirect requirements are copied too, marked indirect. Since Go 1.17 go.mod
// lists every module the build needs, and an indirect requirement is how it
// raises a dependency's dependency above the version that dependency asks for;
// without it, the build would select the lower version go build does not.
func goRequirementsOf(goMod *modfile.File) []mod.Require {
	reqs := make([]mod.Require, 0, len(goMod.Require))
	for _, r := range goMod.Require {
		reqs = append(reqs, mod.Require{Path: r.Mod.Path, Version: r.Mod.Version, Indirect: r.Indirect, Go: true})
	}
	return reqs
}

// reportCopiedGoRequirements prints the requirements gala mod init copied from
// go.mod, and the number of its replace directives, which it does not copy.
func reportCopiedGoRequirements(reqs []mod.Require, replaces int) {
	if len(reqs) > 0 {
		fmt.Printf("Copied %d requirement(s) from go.mod as Go dependencies:\n", len(reqs))
		for _, r := range reqs {
			fmt.Printf("  %s\n", mod.FormatRequireEntry(r))
		}
	}
	if replaces > 0 {
		fmt.Printf("Note: go.mod has %d replace directive(s), which were not copied; "+
			"gala build does not apply replace directives to Go dependencies.\n", replaces)
	}
}

// The .gitignore lines for the directory gala build keeps its cache and build
// workspaces in; gala new writes them too.
const (
	galaGitignoreComment = "# GALA build workspaces and cache"
	galaCacheDirIgnore   = ".gala/"
)

// ensureGalaGitignored adds galaCacheDirIgnore to the .gitignore at path,
// creating the file if there is none, and reports whether it changed the file.
// A file that already ignores the directory (with or without a leading or
// trailing slash) is left as it is. The file's line endings are kept.
func ensureGalaGitignored(path string) (added bool, err error) {
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return false, err
	}
	content := string(data)
	for _, l := range strings.Split(content, "\n") {
		if strings.Trim(strings.TrimSpace(l), "/") == ".gala" {
			return false, nil
		}
	}
	eol := "\n"
	if strings.Contains(content, "\r\n") {
		eol = "\r\n"
	}
	if content != "" && !strings.HasSuffix(content, "\n") {
		content += eol
	}
	content += galaGitignoreComment + eol + galaCacheDirIgnore + eol
	return true, os.WriteFile(path, []byte(content), 0644)
}
