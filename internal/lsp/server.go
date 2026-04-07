// Package lsp implements the GALA Language Server Protocol (3.17) server.
package lsp

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	"github.com/owenrumney/go-lsp/lsp"
	golsp "github.com/owenrumney/go-lsp/server"

	"martianoff/gala/galaerr"
	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/analyzer"
	"martianoff/gala/internal/transpiler/generator"
	"martianoff/gala/internal/transpiler/transformer"
)

// GalaHandler implements all LSP handler interfaces for the GALA language.
type GalaHandler struct {
	rootPath string

	parser      transpiler.GalaParser
	transformer transpiler.ASTTransformer
	generator   transpiler.CodeGenerator

	extraSearchPaths []string          // additional search paths (for testing)
	client           *golsp.Client     // LSP client for sending notifications

	mu        sync.Mutex
	documents map[string]string              // URI -> source text
	richASTs  map[string]*transpiler.RichAST // URI -> analyzed AST
	varTypes  map[string]map[string]string   // URI -> (varName -> type) from transpiler
}

// SetClient implements server.ClientHandler — receives the LSP client for notifications.
func (h *GalaHandler) SetClient(client *golsp.Client) {
	h.client = client
}

// SetSearchPaths adds additional search paths for the analyzer (for testing).
func (h *GalaHandler) SetSearchPaths(paths []string) {
	h.extraSearchPaths = paths
}

// NewGalaHandler creates a new GALA LSP handler.
func NewGalaHandler() *GalaHandler {
	return &GalaHandler{
		documents:   make(map[string]string),
		richASTs:    make(map[string]*transpiler.RichAST),
		varTypes:    make(map[string]map[string]string),
		parser:      transpiler.NewAntlrGalaParser(),
		transformer: transformer.NewGalaASTTransformer(),
		generator:   generator.NewGoCodeGenerator(),
	}
}

// --- Lifecycle ---

func (h *GalaHandler) Initialize(ctx context.Context, params *lsp.InitializeParams) (*lsp.InitializeResult, error) {
	if params.RootURI != nil {
		h.rootPath = uriToPath(string(*params.RootURI))
	}

	openClose := true
	includeText := true

	return &lsp.InitializeResult{
		Capabilities: lsp.ServerCapabilities{
			TextDocumentSync: &lsp.TextDocumentSyncOptions{
				OpenClose: &openClose,
				Change:    lsp.SyncFull,
				Save:      &lsp.SaveOptions{IncludeText: &includeText},
			},
			HoverProvider:      boolPtr(true),
			DefinitionProvider: boolPtr(true),
			CompletionProvider: &lsp.CompletionOptions{
				TriggerCharacters: []string{".", "("},
			},
			InlayHintProvider:      &lsp.InlayHintOptions{},
			ReferencesProvider:     boolPtr(true),
			DocumentSymbolProvider: boolPtr(true),
		},
		ServerInfo: &lsp.ServerInfo{
			Name:    "gala-lsp",
			Version: "0.2.0",
		},
	}, nil
}

func (h *GalaHandler) Shutdown(ctx context.Context) error {
	return nil
}

// --- Document Sync ---

func (h *GalaHandler) DidOpen(ctx context.Context, params *lsp.DidOpenTextDocumentParams) error {
	uri := string(params.TextDocument.URI)
	h.mu.Lock()
	h.documents[uri] = params.TextDocument.Text
	h.mu.Unlock()
	h.publishDiagnostics(uri, params.TextDocument.Text)
	return nil
}

func (h *GalaHandler) DidChange(ctx context.Context, params *lsp.DidChangeTextDocumentParams) error {
	uri := string(params.TextDocument.URI)
	if len(params.ContentChanges) == 0 {
		return nil
	}
	text := params.ContentChanges[len(params.ContentChanges)-1].Text
	h.mu.Lock()
	h.documents[uri] = text
	h.mu.Unlock()
	h.publishDiagnostics(uri, text)
	return nil
}

func (h *GalaHandler) DidClose(ctx context.Context, params *lsp.DidCloseTextDocumentParams) error {
	uri := string(params.TextDocument.URI)
	h.mu.Lock()
	delete(h.documents, uri)
	delete(h.richASTs, uri)
	delete(h.varTypes, uri)
	h.mu.Unlock()
	// Clear diagnostics
	if h.client != nil {
		h.client.PublishDiagnostics(context.Background(), &lsp.PublishDiagnosticsParams{
			URI:         params.TextDocument.URI,
			Diagnostics: []lsp.Diagnostic{},
		})
	}
	return nil
}

func (h *GalaHandler) DidSave(ctx context.Context, params *lsp.DidSaveTextDocumentParams) error {
	if params.Text != nil {
		uri := string(params.TextDocument.URI)
		h.mu.Lock()
		h.documents[uri] = *params.Text
		h.mu.Unlock()
		h.publishDiagnostics(uri, *params.Text)
	}
	return nil
}

// --- Analysis ---

func (h *GalaHandler) publishDiagnostics(uri, text string) {
	filePath := uriToPath(uri)
	diagnostics := h.analyzeFile(uri, filePath, text)

	// Send diagnostics to the client
	if h.client != nil {
		h.client.PublishDiagnostics(context.Background(), &lsp.PublishDiagnosticsParams{
			URI:         lsp.DocumentURI(uri),
			Diagnostics: diagnostics,
		})
	}
}

func (h *GalaHandler) analyzeFile(uri, filePath, text string) []lsp.Diagnostic {
	var diagnostics []lsp.Diagnostic

	tree, err := h.parser.Parse(text)
	if err != nil {
		diagnostics = append(diagnostics, lsp.Diagnostic{
			Range:    zeroRange(),
			Severity: sevPtr(lsp.SeverityError),
			Source:   "gala",
			Message:  fmt.Sprintf("Parse error: %s", err),
		})
		return diagnostics
	}

	searchPaths := h.getSearchPaths(filePath)
	a := analyzer.NewGalaAnalyzer(h.parser, searchPaths)
	richAST, err := a.Analyze(tree, filePath)
	if err != nil {
		diagnostics = append(diagnostics, errorsToDiagnostics(err)...)
		return diagnostics
	}

	h.mu.Lock()
	h.richASTs[uri] = richAST
	h.mu.Unlock()

	// Use TransformForLSP to get resolved variable types directly from the transpiler
	result, transformErr := h.transformer.TransformForLSP(richAST)
	if transformErr != nil {
		diagnostics = append(diagnostics, errorsToDiagnostics(transformErr)...)
	}

	// Cache the transpiler's resolved variable types
	if result != nil && result.VarTypes != nil {
		typeMap := make(map[string]string, len(result.VarTypes))
		for name, typ := range result.VarTypes {
			typeMap[name] = cleanGoTypeForDisplay(typ.String())
		}
		h.mu.Lock()
		h.varTypes[uri] = typeMap
		h.mu.Unlock()
	}

	diagnostics = append(diagnostics, checkMatchExhaustiveness(text, richAST)...)

	for _, warning := range richAST.AnalysisWarnings {
		diagnostics = append(diagnostics, lsp.Diagnostic{
			Range:    zeroRange(),
			Severity: sevPtr(lsp.SeverityWarning),
			Source:   "gala",
			Message:  warning,
		})
	}

	return diagnostics
}

func (h *GalaHandler) getSearchPaths(filePath string) []string {
	paths := []string{"."}
	if h.rootPath != "" {
		paths = append(paths, h.rootPath)
	}
	dir := filepath.Dir(filePath)
	if dir != "" && dir != "." {
		paths = append(paths, dir)
	}
	paths = append(paths, h.extraSearchPaths...)
	return paths
}

// --- Helpers ---

func uriToPath(uri string) string {
	path := uri
	path = strings.TrimPrefix(path, "file:///")
	path = strings.TrimPrefix(path, "file://")
	path = strings.ReplaceAll(path, "%20", " ")
	return filepath.FromSlash(path)
}

func pathToURI(path string) string {
	path = filepath.ToSlash(path)
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return "file://" + path
}

func boolPtr(b bool) *bool                                    { return &b }
func sevPtr(s lsp.DiagnosticSeverity) *lsp.DiagnosticSeverity { return &s }

func zeroRange() lsp.Range {
	return lsp.Range{
		Start: lsp.Position{Line: 0, Character: 0},
		End:   lsp.Position{Line: 0, Character: 0},
	}
}

// errorsToDiagnostics converts an error (possibly MultiError) into diagnostics.
func errorsToDiagnostics(err error) []lsp.Diagnostic {
	var multiErr *galaerr.MultiError
	if errors.As(err, &multiErr) {
		var diags []lsp.Diagnostic
		for _, subErr := range multiErr.Errors {
			diags = append(diags, errorToDiagnostic(subErr))
		}
		return diags
	}
	return []lsp.Diagnostic{errorToDiagnostic(err)}
}

func errorToDiagnostic(err error) lsp.Diagnostic {
	msg := err.Error()
	line := 0
	char := 0

	// Try to extract line info from typed errors
	var semErr *galaerr.SemanticError
	if errors.As(err, &semErr) && semErr.Line > 0 {
		line = semErr.Line - 1 // LSP is 0-indexed
		char = semErr.Column
	} else if idx := strings.Index(msg, "[line "); idx >= 0 {
		// Fallback: parse from error message text
		var l, c int
		fmt.Sscanf(msg[idx:], "[line %d:%d]", &l, &c)
		if l > 0 {
			line = l - 1
			char = c
		}
	} else if idx := strings.Index(msg, "line "); idx >= 0 {
		var l int
		fmt.Sscanf(msg[idx:], "line %d", &l)
		if l > 0 {
			line = l - 1
		}
	}

	// Also handle MultiError (multiple errors from parser/analyzer)
	// by extracting the first line number from any sub-error
	var multiErr *galaerr.MultiError
	if errors.As(err, &multiErr) {
		for _, subErr := range multiErr.Errors {
			d := errorToDiagnostic(subErr)
			if d.Range.Start.Line > 0 {
				line = d.Range.Start.Line
				char = d.Range.Start.Character
				break
			}
		}
	}

	return lsp.Diagnostic{
		Range: lsp.Range{
			Start: lsp.Position{Line: line, Character: char},
			End:   lsp.Position{Line: line, Character: char + 1},
		},
		Severity: sevPtr(lsp.SeverityError),
		Source:   "gala",
		Message:  msg,
	}
}
