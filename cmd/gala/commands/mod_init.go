package commands

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"martianoff/gala/internal/build"
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
stay marked // indirect. go.mod's replace directives are copied too. A GALA
standard library package is not copied, and is listed. The copy is made once:
after changing a version in go.mod, run gala mod add <path>@<version> --go.

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
	goMod, err := build.ReadGoMod("go.mod")
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
		if goMod != nil {
			fmt.Printf("Warning: go.mod has no module directive, using directory name: %s\n", modulePath)
		} else {
			fmt.Printf("Warning: No go.mod found, using directory name: %s\n", modulePath)
		}
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
	var skipped []string
	if goMod != nil {
		f.Require, f.Replace, skipped = build.GoRequirementsOf(goMod)
	}

	if err := mod.WriteFile(f, "gala.mod"); err != nil {
		fmt.Fprintf(os.Stderr, "Error: failed to write gala.mod: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Initialized gala.mod for module %s\n", modulePath)
	reportCopiedGoRequirements(f.Require, f.Replace, skipped)

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

// reportCopiedGoRequirements prints the requirements and replace directives
// gala mod init copied from go.mod and those it did not copy, with the reason.
func reportCopiedGoRequirements(reqs []mod.Require, replaces []mod.Replace, skipped []string) {
	if len(reqs) > 0 {
		fmt.Printf("Copied %d requirement(s) from go.mod as Go dependencies:\n", len(reqs))
		for _, r := range reqs {
			fmt.Printf("  %s\n", mod.FormatRequireEntry(r))
		}
	}
	if len(replaces) > 0 {
		fmt.Printf("Copied %d replace directive(s) from go.mod:\n", len(replaces))
		for _, r := range replaces {
			fmt.Printf("  %s\n", mod.FormatReplaceEntry(r))
		}
	}
	if len(skipped) > 0 {
		fmt.Printf("Did not copy %d line(s) of go.mod:\n", len(skipped))
		for _, s := range skipped {
			fmt.Printf("  %s\n", s)
		}
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
// Nothing is changed when the file already has a .gala line (with or without
// a leading or trailing slash), or, in a Git work tree, when a .gitignore rule
// Git applies there ignores the directory, such as one in a parent
// directory's .gitignore. The file's line endings are kept.
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
	if gitIgnores(filepath.Dir(path), galaCacheDirIgnore) {
		return false, nil
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

// gitIgnores asks Git whether a .gitignore file of the work tree around dir
// (in dir or a parent directory) ignores rel. A rule only this user has (in
// core.excludesFile or .git/info/exclude) does not count, since the
// repository's other users do not share it, and neither does a negated rule.
// Outside a work tree, or without git on PATH, it reports false.
func gitIgnores(dir, rel string) bool {
	// -n lists the rule that decides rel, ignoring or not.
	cmd := exec.Command("git", "check-ignore", "-v", "-n", rel)
	cmd.Dir = dir
	out, _ := cmd.Output()
	return sharedIgnoreRule(string(out))
}

// sharedIgnoreRule reports whether a `git check-ignore -v -n` line,
// "<source>:<line>:<pattern>\t<path>", names an ignoring (not negated) rule
// of a .gitignore file of the work tree, which git names by a path relative
// to the work tree. A source with unusual characters is C-quoted; no rule at
// all reads "::\t<path>".
func sharedIgnoreRule(line string) bool {
	match, _, _ := strings.Cut(strings.TrimSpace(line), "\t")
	var source, rest string
	if strings.HasPrefix(match, `"`) {
		quoted, err := strconv.QuotedPrefix(match)
		if err != nil {
			return false
		}
		source, _ = strconv.Unquote(quoted)
		rest = strings.TrimPrefix(match[len(quoted):], ":")
	} else {
		// The source may hold a colon (C:/...), so it ends at the first
		// ".gitignore:"; any other source is not a .gitignore file.
		end := strings.Index(match, ".gitignore:")
		if end < 0 {
			return false
		}
		source, rest = match[:end+len(".gitignore")], match[end+len(".gitignore:"):]
	}
	_, pattern, _ := strings.Cut(rest, ":")
	absolute := filepath.IsAbs(source) || strings.HasPrefix(source, "/") ||
		(len(source) > 1 && source[1] == ':') // a Windows drive, on any OS
	return filepath.Base(source) == ".gitignore" && !absolute && !strings.HasPrefix(pattern, "!")
}
