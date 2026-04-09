package commands

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	golsp "github.com/owenrumney/go-lsp/server"
	"github.com/spf13/cobra"

	"martianoff/gala/internal/build"
	"martianoff/gala/internal/lsp"
	"martianoff/gala/internal/stdlib"
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

	// Auto-resolve stdlib and GALA dependency search paths so the LSP can
	// find standard packages (std, collection_immutable, etc.) from any project.
	handler.SetSearchPaths(autoResolveLSPSearchPaths())

	srv := golsp.NewServer(handler)
	if err := srv.Run(context.Background(), golsp.RunStdio()); err != nil {
		fmt.Fprintf(os.Stderr, "LSP server error: %v\n", err)
		os.Exit(1)
	}
}

// autoResolveLSPSearchPaths extracts the embedded stdlib and returns search paths.
func autoResolveLSPSearchPaths() []string {
	config := build.DefaultConfig()
	stdlibDir := config.StdlibVersionDir(Version)

	// Ensure stdlib is extracted (same as `gala build` does)
	markerPath := filepath.Join(stdlibDir, ".stdlib-extracted")
	if _, err := os.Stat(markerPath); os.IsNotExist(err) {
		os.MkdirAll(stdlibDir, 0755)
		if err := stdlib.ExtractTo(stdlibDir); err == nil {
			os.WriteFile(markerPath, []byte(Version), 0644)
		}
	}

	if info, err := os.Stat(stdlibDir); err == nil && info.IsDir() {
		return []string{stdlibDir}
	}
	return nil
}
