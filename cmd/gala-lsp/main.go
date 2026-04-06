// Command gala-lsp implements a Language Server Protocol server for the GALA language.
// It wraps the GALA transpiler's parser and analyzer to provide IDE features:
//   - Diagnostics (transpilation errors shown in the editor)
//   - Hover (type info on hover)
//   - Go to Definition (cross-file navigation)
//   - Completion (types, functions, methods from analyzed packages)
//
// Usage:
//
//	gala-lsp --stdio
package main

import (
	"fmt"
	"os"

	"github.com/tliron/commonlog"
	_ "github.com/tliron/commonlog/simple"
	protocol "github.com/tliron/glsp/protocol_3_16"
	"github.com/tliron/glsp/server"

	"martianoff/gala/internal/lsp"
)

const serverName = "gala-lsp"
const serverVersion = "0.1.0"

func main() {
	commonlog.Configure(1, nil) // minimal logging

	s := lsp.NewGalaServer()
	handler := protocol.Handler{
		Initialize:             s.Initialize,
		Initialized:            s.Initialized,
		Shutdown:               s.Shutdown,
		TextDocumentDidOpen:    s.TextDocumentDidOpen,
		TextDocumentDidClose:   s.TextDocumentDidClose,
		TextDocumentDidChange:  s.TextDocumentDidChange,
		TextDocumentDidSave:    s.TextDocumentDidSave,
		TextDocumentHover:      s.TextDocumentHover,
		TextDocumentDefinition: s.TextDocumentDefinition,
		TextDocumentCompletion: s.TextDocumentCompletion,
	}

	glspServer := server.NewServer(&handler, serverName, false)
	s.SetServer(glspServer)

	// Run over stdio (standard for editor integration)
	if len(os.Args) > 1 && os.Args[1] == "--stdio" {
		glspServer.RunStdio()
	} else {
		fmt.Fprintf(os.Stderr, "Usage: %s --stdio\n", os.Args[0])
		os.Exit(1)
	}
}
