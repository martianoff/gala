package commands

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"martianoff/gala/internal/goexport"
)

var (
	seModule  string
	seOut     string
	seProxy   string
	seVersion string
	seCommit  string
)

var stdlibCmd = &cobra.Command{
	Use:   "stdlib",
	Short: "Work with the GALA standard library bundled in this binary",
}

var stdlibExportCmd = &cobra.Command{
	Use:   "export",
	Short: "Write the standard library as one plain Go module for Go consumers",
	Long: `Write the standard library bundled in this binary as a single Go module that a
plain Go program can depend on without GALA.

GALA code imports the standard library as martianoff/gala/..., which the go
command cannot download. The export rewrites those imports to --go-module and
writes every package under one go.mod, so Go consumers need no replace
directive. GALA builds are unaffected: they keep using martianoff/gala/...

--out writes the module tree. --proxy also writes it as module version
--version in the layout GOPROXY=file://<dir> serves, for testing a Go consumer
offline before the version is published.

Example:
  gala stdlib export --go-module go.gala.fyi/stdlib --out /tmp/stdlib
  gala stdlib export --go-module go.gala.fyi/stdlib --out /tmp/stdlib \
    --proxy /tmp/goproxy --version v0.87.0-dev.1`,
	Args: cobra.NoArgs,
	RunE: runStdlibExport,
}

func init() {
	f := stdlibExportCmd.Flags()
	f.StringVar(&seModule, "go-module", "", "Go module path to publish the standard library under (required)")
	f.StringVar(&seOut, "out", "", "Directory to write the module to; must be empty and outside any Bazel workspace (required)")
	f.StringVar(&seProxy, "proxy", "", "Also write the module into this GOPROXY file layout")
	f.StringVar(&seVersion, "version", "", "Module version for --proxy (default: this binary's GALA version, v-prefixed)")
	f.StringVar(&seCommit, "commit", "", "Compiler commit recorded in VERSION (default: this binary's commit)")
	_ = stdlibExportCmd.MarkFlagRequired("go-module")
	_ = stdlibExportCmd.MarkFlagRequired("out")
	stdlibCmd.AddCommand(stdlibExportCmd)
}

func runStdlibExport(cmd *cobra.Command, args []string) error {
	out, err := filepath.Abs(seOut)
	if err != nil {
		return err
	}
	// Gazelle would pick up the rewritten imports of an export inside a
	// Bazel workspace and generate targets for them.
	if ws, ok := enclosingBazelWorkspace(out); ok {
		return fmt.Errorf("refusing to export into %s: it is inside the Bazel workspace %s; choose a directory outside it", out, ws)
	}
	if seVersion != "" && seProxy == "" {
		return fmt.Errorf("--version only applies with --proxy")
	}
	var modVersion string
	if seProxy != "" {
		version := seVersion
		if version == "" {
			version = Version
		}
		if modVersion, err = goexport.ModuleVersion(version); err != nil {
			return fmt.Errorf("%w; pass --version for a dev build", err)
		}
	}
	commit := seCommit
	if commit == "" {
		commit = GitCommit
	}

	files, err := goexport.Stdlib(goexport.StdlibOptions{ModulePath: seModule, GalaVersion: Version, Commit: commit})
	if err != nil {
		return err
	}
	if err := goexport.WriteDir(out, files); err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Exported %s (%d files) to %s\n", seModule, len(files), out)
	if seProxy == "" {
		return nil
	}
	if err := goexport.WriteProxy(seProxy, seModule, modVersion, files); err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Wrote %s@%s to proxy %s\n", seModule, modVersion, seProxy)
	return nil
}

// enclosingBazelWorkspace reports the nearest ancestor of dir (or dir itself)
// that is a Bazel workspace root.
func enclosingBazelWorkspace(dir string) (string, bool) {
	for d := dir; ; d = filepath.Dir(d) {
		for _, marker := range []string{"MODULE.bazel", "WORKSPACE", "WORKSPACE.bazel", "REPO.bazel"} {
			if _, err := os.Stat(filepath.Join(d, marker)); err == nil {
				return d, true
			}
		}
		if filepath.Dir(d) == d {
			return "", false
		}
	}
}
