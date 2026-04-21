package commands

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"martianoff/gala/internal/depman/mod"
)

// newModulePath optionally overrides the auto-generated module path.
var newModulePath string

var newCmd = &cobra.Command{
	Use:   "new <name>",
	Short: "Scaffold a new GALA project",
	Long: `Create a new directory <name> pre-populated with a minimal
runnable GALA project:

  <name>/
    gala.mod      module example.com/<name>  (override with --module)
    main.gala     "Hello, GALA!" program
    .gitignore    standard GALA build-output excludes

Examples:
  gala new myapp                                   # module example.com/myapp
  gala new myapp --module github.com/you/myapp     # explicit module path`,
	Args: cobra.ExactArgs(1),
	Run:  runNew,
}

func init() {
	newCmd.Flags().StringVar(&newModulePath, "module", "", "Module path (default: example.com/<name>)")
}

func runNew(cmd *cobra.Command, args []string) {
	name := args[0]

	if err := validateProjectName(name); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	// Fail if the target directory already exists.
	if _, err := os.Stat(name); err == nil {
		fmt.Fprintf(os.Stderr, "Error: %q already exists\n", name)
		os.Exit(1)
	} else if !os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "Error: failed to stat %q: %v\n", name, err)
		os.Exit(1)
	}

	modulePath := newModulePath
	if modulePath == "" {
		modulePath = "example.com/" + filepath.Base(name)
	}

	if err := scaffoldProject(name, modulePath); err != nil {
		// Best-effort cleanup on failure so the user can re-run.
		_ = os.RemoveAll(name)
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Created new GALA project %q (module %s)\n", name, modulePath)
	fmt.Println()
	fmt.Printf("  cd %s\n", name)
	fmt.Println("  gala run main.gala")
}

// validateProjectName rejects empty names, absolute paths, parent traversal,
// and path separators. The name must be usable as a plain directory name.
func validateProjectName(name string) error {
	if name == "" {
		return fmt.Errorf("project name cannot be empty")
	}
	if filepath.IsAbs(name) {
		return fmt.Errorf("project name must be a relative directory name, not an absolute path")
	}
	if name == "." || name == ".." {
		return fmt.Errorf("invalid project name %q", name)
	}
	if strings.ContainsAny(name, `/\`) {
		return fmt.Errorf("project name %q must not contain path separators", name)
	}
	return nil
}

// scaffoldProject creates <dir>/ with gala.mod, main.gala, and .gitignore.
func scaffoldProject(dir, modulePath string) error {
	if err := os.Mkdir(dir, 0755); err != nil {
		return fmt.Errorf("failed to create directory %q: %w", dir, err)
	}

	// gala.mod — reuse the same writer used by `gala mod init` for consistency.
	galaMod := mod.NewFile(modulePath)
	galaMod.Gala = Version
	if err := mod.WriteFile(galaMod, filepath.Join(dir, "gala.mod")); err != nil {
		return fmt.Errorf("failed to write gala.mod: %w", err)
	}

	// main.gala — minimal runnable program.
	mainContent := `package main

func main() {
    Println("Hello, GALA!")
}
`
	if err := os.WriteFile(filepath.Join(dir, "main.gala"), []byte(mainContent), 0644); err != nil {
		return fmt.Errorf("failed to write main.gala: %w", err)
	}

	// .gitignore — GALA build-output and cache excludes.
	// Mirrors the top-level repo .gitignore entries relevant to a user project.
	gitignoreContent := `# GALA build workspaces and cache
.gala/

# Bazel output (if the user adopts Bazel)
/bazel-*

# Editor / IDE
.idea/
.vscode/
*.iml

# OS cruft
.DS_Store
Thumbs.db
`
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte(gitignoreContent), 0644); err != nil {
		return fmt.Errorf("failed to write .gitignore: %w", err)
	}

	return nil
}
