// Package lsp implements the GALA Language Server Protocol server.
// It wraps the GALA transpiler's parser and analyzer to provide IDE features.
package lsp

import (
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	"github.com/tliron/glsp"
	protocol "github.com/tliron/glsp/protocol_3_16"
	"github.com/tliron/glsp/server"

	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/analyzer"
	"martianoff/gala/internal/transpiler/generator"
	"martianoff/gala/internal/transpiler/transformer"
)

// GalaServer holds the state for the GALA language server.
type GalaServer struct {
	server    *server.Server
	rootURI   string
	rootPath  string

	// Transpiler components
	parser      transpiler.GalaParser
	transformer transpiler.ASTTransformer
	generator   transpiler.CodeGenerator

	// Document state: URI -> content
	mu       sync.Mutex
	documents map[string]string   // URI -> source text
	richASTs  map[string]*transpiler.RichAST // URI -> analyzed AST
}

// NewGalaServer creates a new GALA language server.
func NewGalaServer() *GalaServer {
	return &GalaServer{
		documents: make(map[string]string),
		richASTs:  make(map[string]*transpiler.RichAST),
		parser:    transpiler.NewAntlrGalaParser(),
		transformer: transformer.NewGalaASTTransformer(),
		generator:   generator.NewGoCodeGenerator(),
	}
}

// SetServer stores the GLSP server reference for sending notifications.
func (s *GalaServer) SetServer(srv *server.Server) {
	s.server = srv
}

// Initialize handles the LSP initialize request.
func (s *GalaServer) Initialize(ctx *glsp.Context, params *protocol.InitializeParams) (any, error) {
	if params.RootURI != nil {
		s.rootURI = *params.RootURI
		s.rootPath = uriToPath(*params.RootURI)
	}

	syncKind := protocol.TextDocumentSyncKindFull
	completionTriggers := []string{"."}

	return protocol.InitializeResult{
		Capabilities: protocol.ServerCapabilities{
			TextDocumentSync: &protocol.TextDocumentSyncOptions{
				OpenClose: boolPtr(true),
				Change:    &syncKind,
				Save: &protocol.SaveOptions{
					IncludeText: boolPtr(true),
				},
			},
			HoverProvider:      true,
			DefinitionProvider: true,
			CompletionProvider: &protocol.CompletionOptions{
				TriggerCharacters: completionTriggers,
			},
			// Note: InlayHintProvider is LSP 3.17+ — registered via handler, not capabilities
		},
		ServerInfo: &protocol.InitializeResultServerInfo{
			Name:    "gala-lsp",
			Version: strPtr("0.1.0"),
		},
	}, nil
}

// Initialized handles the LSP initialized notification.
func (s *GalaServer) Initialized(ctx *glsp.Context, params *protocol.InitializedParams) error {
	return nil
}

// Shutdown handles the LSP shutdown request.
func (s *GalaServer) Shutdown(ctx *glsp.Context) error {
	return nil
}

// TextDocumentDidOpen handles file open — analyze and publish diagnostics.
func (s *GalaServer) TextDocumentDidOpen(ctx *glsp.Context, params *protocol.DidOpenTextDocumentParams) error {
	uri := params.TextDocument.URI
	text := params.TextDocument.Text

	s.mu.Lock()
	s.documents[uri] = text
	s.mu.Unlock()

	s.analyzeAndPublishDiagnostics(ctx, uri, text)
	return nil
}

// TextDocumentDidClose handles file close.
func (s *GalaServer) TextDocumentDidClose(ctx *glsp.Context, params *protocol.DidCloseTextDocumentParams) error {
	uri := params.TextDocument.URI

	s.mu.Lock()
	delete(s.documents, uri)
	delete(s.richASTs, uri)
	s.mu.Unlock()

	// Clear diagnostics
	go ctx.Notify(protocol.ServerTextDocumentPublishDiagnostics, protocol.PublishDiagnosticsParams{
		URI:         uri,
		Diagnostics: []protocol.Diagnostic{},
	})
	return nil
}

// TextDocumentDidChange handles file edits — re-analyze.
func (s *GalaServer) TextDocumentDidChange(ctx *glsp.Context, params *protocol.DidChangeTextDocumentParams) error {
	uri := params.TextDocument.URI
	if len(params.ContentChanges) == 0 {
		return nil
	}

	// Full sync: last change has the complete content
	text := params.ContentChanges[len(params.ContentChanges)-1].(protocol.TextDocumentContentChangeEventWhole).Text

	s.mu.Lock()
	s.documents[uri] = text
	s.mu.Unlock()

	s.analyzeAndPublishDiagnostics(ctx, uri, text)
	return nil
}

// TextDocumentDidSave handles file save — re-analyze with saved content.
func (s *GalaServer) TextDocumentDidSave(ctx *glsp.Context, params *protocol.DidSaveTextDocumentParams) error {
	uri := params.TextDocument.URI
	if params.Text != nil {
		s.mu.Lock()
		s.documents[uri] = *params.Text
		s.mu.Unlock()
		s.analyzeAndPublishDiagnostics(ctx, uri, *params.Text)
	}
	return nil
}

// analyzeAndPublishDiagnostics runs the GALA transpiler on the file and publishes errors as diagnostics.
func (s *GalaServer) analyzeAndPublishDiagnostics(ctx *glsp.Context, uri string, text string) {
	filePath := uriToPath(uri)
	diagnostics := s.analyzeFile(uri, filePath, text)

	go ctx.Notify(protocol.ServerTextDocumentPublishDiagnostics, protocol.PublishDiagnosticsParams{
		URI:         uri,
		Diagnostics: diagnostics,
	})
}

// analyzeFile parses and analyzes a GALA file, returning diagnostics and caching the RichAST.
func (s *GalaServer) analyzeFile(uri, filePath, text string) []protocol.Diagnostic {
	var diagnostics []protocol.Diagnostic

	// Parse
	tree, err := s.parser.Parse(text)
	if err != nil {
		diagnostics = append(diagnostics, protocol.Diagnostic{
			Range:    zeroRange(),
			Severity: severityPtr(protocol.DiagnosticSeverityError),
			Source:   strPtr("gala"),
			Message:  fmt.Sprintf("Parse error: %s", err),
		})
		return diagnostics
	}

	// Analyze
	searchPaths := s.getSearchPaths(filePath)
	a := analyzer.NewGalaAnalyzer(s.parser, searchPaths)
	richAST, err := a.Analyze(tree, filePath)
	if err != nil {
		diag := errorToDiagnostic(err)
		diagnostics = append(diagnostics, diag)
		return diagnostics
	}

	// Cache the RichAST for hover/definition
	s.mu.Lock()
	s.richASTs[uri] = richAST
	s.mu.Unlock()

	// Try full transpilation to catch code generation errors
	_, _, err = s.transformer.Transform(richAST)
	if err != nil {
		diag := errorToDiagnostic(err)
		diagnostics = append(diagnostics, diag)
	}

	// Check match exhaustiveness
	diagnostics = append(diagnostics, checkMatchExhaustiveness(text, richAST)...)

	// Add analysis warnings as info diagnostics
	for _, warning := range richAST.AnalysisWarnings {
		diagnostics = append(diagnostics, protocol.Diagnostic{
			Range:    zeroRange(),
			Severity: severityPtr(protocol.DiagnosticSeverityWarning),
			Source:   strPtr("gala"),
			Message:  warning,
		})
	}

	return diagnostics
}

// getSearchPaths returns search paths for the analyzer.
func (s *GalaServer) getSearchPaths(filePath string) []string {
	paths := []string{"."}
	if s.rootPath != "" {
		paths = append(paths, s.rootPath)
	}
	dir := filepath.Dir(filePath)
	if dir != "" && dir != "." {
		paths = append(paths, dir)
	}
	return paths
}

// --- Helpers ---

func uriToPath(uri string) string {
	// file:///C:/path/to/file.gala -> C:/path/to/file.gala
	path := uri
	path = strings.TrimPrefix(path, "file:///")
	path = strings.TrimPrefix(path, "file://")
	path = strings.ReplaceAll(path, "%20", " ")
	return filepath.FromSlash(path)
}

func boolPtr(b bool) *bool          { return &b }
func strPtr(s string) *string       { return &s }

func severityPtr(s protocol.DiagnosticSeverity) *protocol.DiagnosticSeverity { return &s }

func zeroRange() protocol.Range {
	return protocol.Range{
		Start: protocol.Position{Line: 0, Character: 0},
		End:   protocol.Position{Line: 0, Character: 0},
	}
}

func errorToDiagnostic(err error) protocol.Diagnostic {
	msg := err.Error()
	line := uint32(0)
	char := uint32(0)

	// Try to extract line number from error messages like "[line 5:10] ..."
	if idx := strings.Index(msg, "[line "); idx >= 0 {
		var l, c int
		_, scanErr := fmt.Sscanf(msg[idx:], "[line %d:%d]", &l, &c)
		if scanErr == nil && l > 0 {
			line = uint32(l - 1) // LSP is 0-indexed
			char = uint32(c)
		}
	}

	// Try "[SemanticError]" style
	if idx := strings.Index(msg, "line "); idx >= 0 {
		var l int
		fmt.Sscanf(msg[idx:], "line %d", &l)
		if l > 0 {
			line = uint32(l - 1)
		}
	}

	return protocol.Diagnostic{
		Range: protocol.Range{
			Start: protocol.Position{Line: line, Character: char},
			End:   protocol.Position{Line: line, Character: char + 1},
		},
		Severity: severityPtr(protocol.DiagnosticSeverityError),
		Source:   strPtr("gala"),
		Message:  msg,
	}
}
