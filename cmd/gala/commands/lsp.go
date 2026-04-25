package commands

import (
	"context"
	"fmt"
	"os"

	golsp "github.com/owenrumney/go-lsp/server"
	"github.com/spf13/cobra"

	"martianoff/gala/internal/build"
	"martianoff/gala/internal/lsp"
)

var lspCmd = &cobra.Command{
	Use:   "lsp",
	Short: "Start the GALA Language Server (LSP 3.17)",
	Long: `Start the GALA Language Server Protocol server.

The server communicates over stdin/stdout using JSON-RPC 2.0.
It provides IDE features including diagnostics, hover, go-to-definition,
completion, inlay hints, and more.

Usage with editors:
  GoLand/IntelliJ: Automatically started by the GALA plugin
  VS Code:         Configure as an LSP server with "gala lsp" command
  Neovim:          Use lspconfig with cmd = {"gala", "lsp"}`,
	Run: runLsp,
}

func init() {
	rootCmd.AddCommand(lspCmd)
}

func runLsp(cmd *cobra.Command, args []string) {
	handler := lsp.NewGalaHandler()

	// Stamp the LSP server version with the build-time GALA release.
	// `Version` is set via x_defs in cmd/gala/BUILD.bazel from
	// STABLE_GALA_VERSION; falls back to "dev" for unstamped builds.
	handler.SetVersion(Version)

	// Auto-resolve stdlib and GALA dependency search paths so the LSP can
	// find standard packages (std, collection_immutable, etc.) from any project.
	handler.SetSearchPaths(autoResolveLSPSearchPaths())

	srv := golsp.NewServer(handler)
	if err := srv.Run(context.Background(), golsp.RunStdio()); err != nil {
		fmt.Fprintf(os.Stderr, "LSP server error: %v\n", err)
		os.Exit(1)
	}
}

func autoResolveLSPSearchPaths() []string {
	stdlibDir := build.DefaultConfig().EnsureStdlib(Version)
	fmt.Fprintf(os.Stderr, "[gala-lsp] version=%s stdlib=%s\n", Version, stdlibDir)
	if stdlibDir != "" {
		return []string{stdlibDir}
	}
	return nil
}
