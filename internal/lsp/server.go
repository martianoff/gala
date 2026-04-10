// Package lsp implements the GALA Language Server Protocol (3.17) server.
package lsp

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/owenrumney/go-lsp/lsp"
	golsp "github.com/owenrumney/go-lsp/server"

	"martianoff/gala/galaerr"
	"martianoff/gala/internal/build"
	"martianoff/gala/internal/depman/mod"
	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/analyzer"
	"martianoff/gala/internal/transpiler/generator"
	"martianoff/gala/internal/transpiler/transformer"
)

// GalaHandler implements all LSP handler interfaces for the GALA language.
//
// Diagnostic publishing follows the cancel-and-restart pattern (like gopls):
// - Each DidChange updates in-memory state and cancels any in-flight analysis
// - A short debounce (300ms) batches rapid keystrokes before starting analysis
// - Before publishing, the document version is checked to discard stale results
// - Old diagnostics remain visible until replaced (no eager clearing)
type GalaHandler struct {
	rootPath string

	parser    transpiler.GalaParser
	generator transpiler.CodeGenerator

	extraSearchPaths []string          // additional search paths (for testing)
	client           *golsp.Client     // LSP client for sending notifications

	mu            sync.Mutex
	documents     map[string]string              // URI -> source text
	docVersions   map[string]int64               // URI -> monotonic edit version
	analysisCancels map[string]context.CancelFunc // URI -> cancel func for in-flight analysis
	richASTs      map[string]*transpiler.RichAST // URI -> analyzed AST
	varTypes      map[string]map[string]string   // URI -> (varName -> type) from transpiler
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
		documents:       make(map[string]string),
		richASTs:        make(map[string]*transpiler.RichAST),
		varTypes:        make(map[string]map[string]string),
		docVersions:     make(map[string]int64),
		analysisCancels: make(map[string]context.CancelFunc),
		parser:          transpiler.NewAntlrGalaParser(),
		generator:       generator.NewGoCodeGenerator(),
	}
}

// --- Lifecycle ---

func (h *GalaHandler) Initialize(ctx context.Context, params *lsp.InitializeParams) (*lsp.InitializeResult, error) {
	if params.RootURI != nil {
		h.rootPath = uriToPath(string(*params.RootURI))
	}

	// Auto-resolve gala.mod dependencies from project root
	if h.rootPath != "" {
		h.resolveProjectDeps(h.rootPath)
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
	h.docVersions[uri]++
	// Cancel any in-flight analysis from a previous DidChange
	if cancel, ok := h.analysisCancels[uri]; ok {
		cancel()
	}
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
	h.docVersions[uri]++
	version := h.docVersions[uri]

	// Cancel any in-flight analysis for this URI
	if cancel, ok := h.analysisCancels[uri]; ok {
		cancel()
	}
	analysisCtx, cancel := context.WithCancel(context.Background())
	h.analysisCancels[uri] = cancel
	h.mu.Unlock()

	go func() {
		select {
		case <-time.After(300 * time.Millisecond):
		case <-analysisCtx.Done():
			return // cancelled by newer edit
		}

		// Read current text (might differ from what triggered this if edits came during debounce)
		h.mu.Lock()
		if h.docVersions[uri] != version {
			h.mu.Unlock()
			return // stale — newer edit arrived
		}
		currentText := h.documents[uri]
		h.mu.Unlock()

		filePath := uriToPath(uri)
		diagnostics := h.analyzeFile(uri, filePath, currentText)

		// Check cancellation and version after analysis
		select {
		case <-analysisCtx.Done():
			return // cancelled during analysis
		default:
		}
		h.mu.Lock()
		if h.docVersions[uri] != version {
			h.mu.Unlock()
			return // stale
		}
		h.mu.Unlock()

		if h.client != nil {
			h.client.PublishDiagnostics(context.Background(), &lsp.PublishDiagnosticsParams{
				URI:         lsp.DocumentURI(uri),
				Diagnostics: diagnostics,
			})
		}
	}()

	return nil
}

func (h *GalaHandler) DidClose(ctx context.Context, params *lsp.DidCloseTextDocumentParams) error {
	uri := string(params.TextDocument.URI)
	h.mu.Lock()
	if cancel, ok := h.analysisCancels[uri]; ok {
		cancel()
		delete(h.analysisCancels, uri)
	}
	delete(h.documents, uri)
	delete(h.docVersions, uri)
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
		h.docVersions[uri]++
		if cancel, ok := h.analysisCancels[uri]; ok {
			cancel()
		}
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
		diagnostics = append(diagnostics, errorsToDiagnostics(err)...)
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

	// Use a fresh transformer per analysis to avoid race conditions between
	// concurrent debounce timers (the transformer has mutable internal state).
	// Run transformer for type inference and diagnostic reporting.
	xformer := transformer.NewGalaASTTransformer()
	result, transformErr := xformer.TransformForLSP(richAST)
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

// resolveProjectDeps scans for gala.mod in the project root and adds
// dependency paths to the search paths so imported packages resolve correctly.
func (h *GalaHandler) resolveProjectDeps(rootPath string) {
	galaModPath := filepath.Join(rootPath, "gala.mod")
	galaMod, err := mod.ParseFile(galaModPath)
	if err != nil {
		return
	}
	config := build.DefaultConfig()
	for _, req := range galaMod.GalaRequires() {
		depDir := config.GalaModulePath(req.Path, req.Version)
		if info, err := os.Stat(depDir); err == nil && info.IsDir() {
			h.extraSearchPaths = append(h.extraSearchPaths, depDir)
		}
	}
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
		var l, c int
		// Try "line N:N" format (ANTLR syntax errors)
		n, _ := fmt.Sscanf(msg[idx:], "line %d:%d", &l, &c)
		if n >= 1 && l > 0 {
			line = l - 1
			if n >= 2 {
				char = c
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
