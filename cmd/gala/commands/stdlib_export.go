package commands

import (
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
    --proxy /tmp/goproxy --version v0.0.0-local.1`,
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
	dest, err := newExportDest(seOut, seProxy, seVersion, Version)
	if err != nil {
		return err
	}
	commit := seCommit
	if commit == "" {
		commit = GitCommit
	}
	files, err := goexport.Stdlib(goexport.StdlibOptions{ModulePath: seModule, GalaVersion: Version, Commit: commit})
	if err != nil {
		return err
	}
	return dest.write(cmd, seModule, files)
}
