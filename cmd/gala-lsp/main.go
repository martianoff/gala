// Command gala-lsp implements a Language Server Protocol (3.17) server for GALA.
//
// Usage:
//
//	gala-lsp --stdio
package main

import (
	"context"
	"fmt"
	"os"

	golsp "github.com/owenrumney/go-lsp/server"

	"martianoff/gala/internal/lsp"
)

func main() {
	if len(os.Args) < 2 || os.Args[1] != "--stdio" {
		fmt.Fprintf(os.Stderr, "Usage: %s --stdio\n", os.Args[0])
		os.Exit(1)
	}

	handler := lsp.NewGalaHandler()
	srv := golsp.NewServer(handler)
	if err := srv.Run(context.Background(), golsp.RunStdio()); err != nil {
		fmt.Fprintf(os.Stderr, "server error: %v\n", err)
		os.Exit(1)
	}
}
