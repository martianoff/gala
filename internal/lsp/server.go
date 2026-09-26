// Package lsp implements the GALA Language Server Protocol (3.17) server.
package lsp

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/antlr4-go/antlr/v4"
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

// moduleRoot pairs a module path with the directory its packages live under.
// Imports name packages by their module-qualified path
// (github.com/you/proj/internal/resp) while the sources sit at internal/resp
// under the module's own root, so navigating to one means stripping the
// matching module's prefix and resolving the remainder under *that* module's
// directory — joining against an unrelated root is how a project package ends
// up pointing into a dependency's cache.
type moduleRoot struct {
	path string // module path as written in gala.mod, e.g. github.com/you/proj
	dir  string // directory the module's packages are rooted at
}

// GalaHandler implements all LSP handler interfaces for the GALA language.
//
// Diagnostic publishing follows the cancel-and-restart pattern (like gopls):
// - Each DidChange updates in-memory state and cancels any in-flight analysis
// - A short debounce (300ms) batches rapid keystrokes before starting analysis
// - Before publishing, the document version is checked to discard stale results
// - Old diagnostics remain visible until replaced (no eager clearing)
type GalaHandler struct {
	rootPath string

	// Version reported in the LSP Initialize response (ServerInfo.Version).
	// Set by SetVersion from cmd/gala/commands.Version, which is x_defs-stamped
	// at build time from STABLE_GALA_VERSION (see cmd/gala/BUILD.bazel). Falls
	// back to "dev" when the LSP is exercised through tests or invoked outside
	// the stamped binary.
	version string

	parser    transpiler.GalaParser
	generator transpiler.CodeGenerator

	extraSearchPaths []string          // additional search paths (for testing)
	goSrcDirs        map[string]string // Go module import-path prefix -> on-disk .go source dir (third-party deps)
	moduleRoots      []moduleRoot      // modules whose packages this project can import, from gala.mod
	client           *golsp.Client     // LSP client for sending notifications
	// snippetSupport is whether the client accepts snippet insert text in
	// completion items (tab stops such as `$1`), from Initialize.
	snippetSupport bool

	mu              sync.Mutex
	documents       map[string]string              // URI -> source text
	docVersions     map[string]int64               // URI -> monotonic edit version
	analysisCancels map[string]context.CancelFunc  // URI -> cancel func for in-flight analysis
	richASTs        map[string]*transpiler.RichAST // URI -> analyzed AST
	// parseTrees caches the raw ANTLR parse tree for the most recent
	// successful analysis. Features like signatureHelp walk the tree to
	// locate the call expression at a cursor position rather than doing
	// byte-level scans.
	parseTrees  map[string]antlr.Tree
	parseTexts  map[string]string                       // URI -> the source that produced parseTrees[URI] (may be surgically patched)
	varTypes    map[string]map[string]string            // URI -> (varName -> type) from transpiler
	lambdaHints map[string][]transpiler.LambdaParamHint // URI -> lambda param hints from transformer
}

// SetClient implements server.ClientHandler — receives the LSP client for notifications.
func (h *GalaHandler) SetClient(client *golsp.Client) {
	h.client = client
}

// SetSearchPaths adds additional search paths for the analyzer (for testing).
func (h *GalaHandler) SetSearchPaths(paths []string) {
	h.extraSearchPaths = paths
}

// SetGoSrcDirs wires the Go module import-path -> source-directory table used to
// resolve third-party Go types (for testing; in production it is populated from
// the project's gala.mod by loadProjectModule).
func (h *GalaHandler) SetGoSrcDirs(dirs map[string]string) {
	h.goSrcDirs = dirs
}

// SetVersion sets the version reported in the LSP Initialize response.
// Callers should pass the build-time stamped commands.Version so the IDE
// can surface the actual GALA release the user is running.
func (h *GalaHandler) SetVersion(v string) {
	h.version = v
}

// serverVersion returns the version to advertise in ServerInfo. Falls back
// to "dev" when SetVersion was not called (test harnesses, manual launches
// outside the stamped binary, etc.).
func (h *GalaHandler) serverVersion() string {
	if h.version == "" {
		return "dev"
	}
	return h.version
}

// DebugRichAST returns the cached RichAST for a given URI (for testing).
func (h *GalaHandler) DebugRichAST(uri string) *transpiler.RichAST {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.richASTs[uri]
}

// DebugVarTypes returns the cached variable types for a given URI (for testing).
func (h *GalaHandler) DebugVarTypes(uri string) map[string]string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.varTypes[uri]
}

// DebugParseTree returns the cached parse tree and the source text it was
// parsed from (may be surgically patched for signatureHelp / completion).
// For tests.
func (h *GalaHandler) DebugParseTree(uri string) (antlr.Tree, string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.parseTrees[uri], h.parseTexts[uri]
}

// NewGalaHandler creates a new GALA LSP handler.
func NewGalaHandler() *GalaHandler {
	return &GalaHandler{
		documents:       make(map[string]string),
		richASTs:        make(map[string]*transpiler.RichAST),
		parseTrees:      make(map[string]antlr.Tree),
		parseTexts:      make(map[string]string),
		varTypes:        make(map[string]map[string]string),
		lambdaHints:     make(map[string][]transpiler.LambdaParamHint),
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
	if td := params.Capabilities.TextDocument; td != nil && td.Completion != nil &&
		td.Completion.CompletionItem != nil && td.Completion.CompletionItem.SnippetSupport != nil {
		h.mu.Lock()
		h.snippetSupport = *td.Completion.CompletionItem.SnippetSupport
		h.mu.Unlock()
	}

	fmt.Fprintf(os.Stderr, "[gala-lsp] Initialize rootPath=%s extraSearchPaths=%v\n", h.rootPath, h.extraSearchPaths)

	// Auto-resolve gala.mod dependencies from project root
	if h.rootPath != "" {
		h.loadProjectModule(h.rootPath)
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
				// `.` only. `(` opens an argument list, which signature help
				// answers; as a completion trigger it ran a popup after every
				// call's opening paren that almost always came back empty, and
				// IntelliJ parks an empty auto-popup in
				// CompletionPhase.EmptyAutoPopup, where the next scheduled
				// auto-popup — typically the `.` for the following link of a
				// builder chain — is skipped.
				TriggerCharacters: []string{"."},
				// Documentation is attached per item on demand rather than for the
				// whole list — see completion_resolve.go.
				ResolveProvider: boolPtr(true),
			},
			SignatureHelpProvider: &lsp.SignatureHelpOptions{
				TriggerCharacters:   []string{"(", ","},
				RetriggerCharacters: []string{","},
			},
			InlayHintProvider:       &lsp.InlayHintOptions{},
			ReferencesProvider:      boolPtr(true),
			DocumentSymbolProvider:  &lsp.DocumentSymbolOptions{},
			WorkspaceSymbolProvider: &lsp.WorkspaceSymbolOptions{},
		},
		ServerInfo: &lsp.ServerInfo{
			Name:    "gala-lsp",
			Version: h.serverVersion(),
		},
	}, nil
}

func (h *GalaHandler) Shutdown(ctx context.Context) error {
	return nil
}

// --- Document Sync ---
//
// Every entry point normalizes the incoming text with galaerr.StripBOM before
// caching it. The parser strips a leading BOM too, so anything that correlates
// a cached document with a parse-tree token index (signatureHelp patches the
// text and walks the tree by offset) would otherwise be three bytes out on a
// document a BOM-preserving client sent us verbatim.

func (h *GalaHandler) DidOpen(ctx context.Context, params *lsp.DidOpenTextDocumentParams) error {
	uri := string(params.TextDocument.URI)
	text := galaerr.StripBOM(params.TextDocument.Text)
	h.mu.Lock()
	h.documents[uri] = text
	h.docVersions[uri]++
	// Cancel any in-flight analysis from a previous DidChange
	if cancel, ok := h.analysisCancels[uri]; ok {
		cancel()
	}
	h.mu.Unlock()
	h.publishDiagnostics(uri, text)
	return nil
}

func (h *GalaHandler) DidChange(ctx context.Context, params *lsp.DidChangeTextDocumentParams) error {
	uri := string(params.TextDocument.URI)
	if len(params.ContentChanges) == 0 {
		return nil
	}
	text := galaerr.StripBOM(params.ContentChanges[len(params.ContentChanges)-1].Text)

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
		filePath := uriToPath(uri)
		diagnostics := h.analyzeFile(uri, filePath, text)

		// Discard if cancelled or stale
		select {
		case <-analysisCtx.Done():
			return
		default:
		}
		h.mu.Lock()
		if h.docVersions[uri] != version {
			h.mu.Unlock()
			return
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
	delete(h.lambdaHints, uri)
	h.mu.Unlock()
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
		text := galaerr.StripBOM(*params.Text)
		h.mu.Lock()
		h.documents[uri] = text
		h.docVersions[uri]++
		if cancel, ok := h.analysisCancels[uri]; ok {
			cancel()
		}
		h.mu.Unlock()
		h.publishDiagnostics(uri, text)
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
	diagnostics := make([]lsp.Diagnostic, 0) // must be [] not null in JSON

	tree, docs, err := h.parser.Parse(text)
	if err != nil {
		diagnostics = append(diagnostics, errorsToDiagnostics(err)...)
		// Primary parse failed — try ANTLR's error-recovered partial tree.
		// If the analyzer can extract type metadata from it, cache the
		// richAST so completion/hover/definition work while mid-edit.
		partialTree, partialDocs, _ := h.parser.ParseLenient(text)
		if partialTree != nil {
			h.mu.Lock()
			h.parseTrees[uri] = partialTree
			h.parseTexts[uri] = text
			h.mu.Unlock()
		}
		h.tryAnalyzePartial(uri, filePath, text, partialTree, partialDocs)
		return diagnostics
	}

	richAST, err := h.newAnalyzer(filePath, text).Analyze(tree, docs, filePath)
	if err != nil {
		diagnostics = append(diagnostics, errorsToDiagnostics(err)...)
		return diagnostics
	}

	// Transform BEFORE publishing richAST, not after.
	//
	// The transformer aliases the RichAST's Types map rather than copying it
	// (`t.typeMetas = richAST.Types` in transformer.go), and it writes to that
	// map while it runs — registerEmbeddedFSMetadata is one such write. So
	// publishing richAST first left every hover and completion arriving during
	// the transform iterating a map the transformer was still assigning into,
	// which Go turns into a `concurrent map read and map write` fatal: the
	// language server dies and takes the editor's GALA features with it.
	//
	// The fresh transformer per analysis was already there to keep
	// concurrent analyses off each other's internal state; it does nothing for
	// the RichAST they all hand it, which is the map that actually escapes.
	//
	// Publishing after costs nothing a reader wants: before the transform the
	// metadata is incomplete anyway. Publication is unconditional so a failed
	// transform still leaves the analyzer's results available, which is what
	// the old ordering was really buying.
	xformer := transformer.NewGalaASTTransformer()
	result, transformErr := xformer.TransformForLSP(richAST)

	h.mu.Lock()
	h.richASTs[uri] = richAST
	h.parseTrees[uri] = tree
	h.parseTexts[uri] = text
	h.mu.Unlock()

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
		h.lambdaHints[uri] = result.LambdaParamHints
		h.mu.Unlock()
	}

	// Match exhaustiveness is handled by the transpiler (SemanticError with line numbers).
	// No separate text-based check needed.

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

// newAnalyzer builds the analyzer for one document: search paths, Go source
// directories, and the files of a multi-file `main` package, which the
// analyzer does not discover on its own (see packageFiles).
func (h *GalaHandler) newAnalyzer(filePath, text string) transpiler.Analyzer {
	a := analyzer.NewGalaAnalyzerForLSP(h.parser, h.getSearchPaths(filePath))
	if len(h.goSrcDirs) > 0 {
		if s, ok := a.(interface{ SetGoSrcDirs(map[string]string) }); ok {
			s.SetGoSrcDirs(h.goSrcDirs)
		}
	}
	if sourcePackageName(text) == "main" {
		if files := h.packageFiles(filePath, text); len(files) > 0 {
			if s, ok := a.(interface{ SetPackageFiles([]string) }); ok {
				s.SetPackageFiles(files)
			}
		}
	}
	return a
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

// findSiblingGalaFiles returns all .gala files in the same directory as filePath,
// excluding the file itself and test files. Used so the analyzer sees the full package.
func findSiblingGalaFiles(filePath string) []string {
	dir := filepath.Dir(filePath)
	base := filepath.Base(filePath)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var siblings []string
	for _, e := range entries {
		name := e.Name()
		if name == base || e.IsDir() {
			continue
		}
		if strings.HasSuffix(name, ".gala") && !strings.HasSuffix(name, "_test.gala") {
			siblings = append(siblings, filepath.Join(dir, name))
		}
	}
	return siblings
}

// loadProjectModule parses the project's gala.mod and wires everything derived
// from it: the module roots imports resolve against, the dependency search
// paths, and the Go source directories.
func (h *GalaHandler) loadProjectModule(rootPath string) {
	galaModPath := filepath.Join(rootPath, "gala.mod")
	galaMod, err := mod.ParseFile(galaModPath)
	if err != nil {
		return
	}
	h.moduleRoots = []moduleRoot{{path: galaMod.Module.Path, dir: rootPath}}
	config := build.DefaultConfig()
	for _, req := range galaMod.GalaRequires() {
		depDir := config.GalaModulePath(req.Path, req.Version)
		if info, err := os.Stat(depDir); err == nil && info.IsDir() {
			h.extraSearchPaths = append(h.extraSearchPaths, depDir)
			h.moduleRoots = append(h.moduleRoots, moduleRoot{path: req.Path, dir: depDir})
		}
	}
	// Wire third-party Go module source directories so the analyzer can resolve
	// their concrete types (completion) and definition can navigate into them.
	if dirs := build.GoModuleSrcDirs(galaMod, config); len(dirs) > 0 {
		h.goSrcDirs = dirs
	}
}

// tryAnalyzePartial attempts to analyze an error-recovered ANTLR tree.
// The tree may contain nil or error nodes, so this is wrapped in a panic
// recovery — a crash just means we skip caching rather than losing the
// LSP connection. When it succeeds, the cached richAST/varTypes keep
// completion and go-to-def working while the user is mid-edit.
// tryAnalyzePartial runs the analyzer on ANTLR's error-recovered tree.
// The tree may have nil/error nodes, so this is wrapped in panic recovery.
func (h *GalaHandler) tryAnalyzePartial(uri, filePath, text string, tree antlr.Tree, docs map[int]string) {
	if tree == nil {
		return
	}
	defer func() { recover() }()

	richAST, err := h.newAnalyzer(filePath, text).Analyze(tree, docs, filePath)
	if err != nil || richAST == nil {
		return
	}

	h.mu.Lock()
	h.richASTs[uri] = richAST
	h.mu.Unlock()
}

// ensureAnalysis is called by the Completion handler when no cached richAST
// exists. It uses the "IntelliJ trick" (also used by rust-analyzer): remove
// the trigger character at the cursor position to produce a syntactically
// valid document, parse + analyze it, and cache the result. This is surgical
// — it touches only the character at the known cursor position, not the
// entire file.
func (h *GalaHandler) ensureAnalysis(uri string, line, char int) {
	h.mu.Lock()
	hasVarTypes := hasResolvedVarType(h.varTypes[uri])
	if h.richASTs[uri] != nil && hasVarTypes {
		h.mu.Unlock()
		return
	}
	text := h.documents[uri]
	h.mu.Unlock()

	if text == "" {
		return
	}

	dot := memberDotOffset(text, line, char)
	if dot < 0 {
		return
	}
	// Remove just the dot, producing e.g. "Text(\"hello\")" from "Text(\"hello\")."
	h.analyzeAndCache(uri, text[:dot]+text[dot+1:], "ensureAnalysis")
}

// memberDotOffset returns the offset of the dot selecting the member being
// typed at the cursor, or -1 when there is none.
//
// The dot may end an earlier line: a builder chain is written one call per
// line with the dot trailing the previous one, possibly followed by a comment,
// and with comment lines between links — the continuation flattenLogicalLine
// follows.
func memberDotOffset(text string, line, char int) int {
	lines := strings.Split(text, "\n")
	if line < 0 || line >= len(lines) {
		return -1
	}
	char = min(max(char, 0), len(lines[line]))
	for char > 0 && isIdentChar(lines[line][char-1]) {
		char--
	}
	before := strings.TrimRight(lines[line][:char], " \t")
	for before == "" {
		line--
		if line < 0 || strings.TrimSpace(lines[line]) == "" {
			return -1
		}
		// A comment-only line trims to nothing and the walk goes on upwards.
		before = strings.TrimRight(stripLineComment(lines[line]), " \t\r")
	}
	if !strings.HasSuffix(before, ".") {
		return -1
	}
	return lineCharToOffset(text, line, len(before)-1)
}

// ensureAnalysisForSignature is the analog of ensureAnalysis for
// textDocument/signatureHelp. The user's cursor is inside a call that
// may not be closed yet (`foo(` or `foo(a,`), in which case the raw
// document fails to parse; patchTextForSignature closes it at the cursor,
// and the normal parse + analyze pipeline runs on the result, which is cached.
func (h *GalaHandler) ensureAnalysisForSignature(uri string, line, char int) {
	h.mu.Lock()
	hasVarTypes := hasResolvedVarType(h.varTypes[uri])
	if h.richASTs[uri] != nil && hasVarTypes {
		h.mu.Unlock()
		return
	}
	text := h.documents[uri]
	h.mu.Unlock()

	if text == "" {
		return
	}
	// The same text the call is then looked up in, so the analysis and the
	// tree walk agree on the document.
	if cleanText, offset := patchTextForSignature(text, line, char); offset >= 0 {
		h.analyzeAndCache(uri, cleanText, "ensureAnalysisForSignature")
	}
}

// analyzeAndCache parses + analyzes + runs the transformer on the given
// (possibly surgically patched) document text, caching the resulting
// richAST / varTypes / lambdaHints under the URI. A nil or failing
// analysis is logged and silently skipped; LSP features then fall back
// to their normal "no data" behavior.
func (h *GalaHandler) analyzeAndCache(uri, cleanText, caller string) {
	tree, docs, err := h.parser.Parse(cleanText)
	if err != nil {
		return
	}

	filePath := uriToPath(uri)
	richAST, aerr := h.newAnalyzer(filePath, cleanText).Analyze(tree, docs, filePath)
	if aerr != nil {
		fmt.Fprintf(os.Stderr, "[%s] analyze failed: %v\n", caller, aerr)
		return
	}
	if richAST == nil {
		fmt.Fprintf(os.Stderr, "[%s] richAST nil\n", caller)
		return
	}

	// Transform before publishing, for the reason given in analyzeFile: the
	// transformer writes the RichAST's Types map in place, so a reader that
	// sees richAST early can be iterating it mid-assignment.
	xformer := transformer.NewGalaASTTransformer()
	result, _ := xformer.TransformForLSP(richAST)

	h.mu.Lock()
	h.richASTs[uri] = richAST
	h.parseTrees[uri] = tree
	h.parseTexts[uri] = cleanText
	h.mu.Unlock()

	if result != nil && result.VarTypes != nil {
		typeMap := make(map[string]string, len(result.VarTypes))
		for name, typ := range result.VarTypes {
			typeMap[name] = cleanGoTypeForDisplay(typ.String())
		}
		h.mu.Lock()
		h.varTypes[uri] = typeMap
		h.lambdaHints[uri] = result.LambdaParamHints
		h.mu.Unlock()
	}
}

// --- Helpers ---

// uriToPath converts a "file://" URI to a native filesystem path. It must work
// cross-platform:
//
//	POSIX:   "file:///tmp/x"          -> "/tmp/x"
//	Windows: "file:///C:/Users/x"     -> "C:\\Users\\x"
//	Windows: "file:///c%3A/Users/x"   -> "c:\\Users\\x"  (percent-encoded ':')
//
// Only the "file://" scheme is stripped. For a POSIX absolute path the URI is
// "file://" + "/abs/path" = "file:///abs/path", so the third slash is the
// leading slash of the path and MUST be preserved. The previous implementation
// stripped "file:///" wholesale, which dropped that slash and produced a
// relative path — breaking sibling-directory discovery for every analyzed file
// and so silently disabling cross-file type resolution in the LSP.
func uriToPath(uri string) string {
	path := strings.TrimPrefix(uri, "file://")
	// Decode percent-encoded characters (e.g., %3A → :, %20 → space) before
	// drive-letter detection so an encoded colon ("/c%3A/...") is recognized.
	if decoded, err := url.PathUnescape(path); err == nil {
		path = decoded
	}
	// Windows drive paths arrive as "/C:/Users/..."; drop the leading slash so
	// the result is the valid "C:/Users/..." form. POSIX paths keep their
	// leading slash.
	if len(path) >= 3 && path[0] == '/' && isASCIILetter(path[1]) && path[2] == ':' {
		path = path[1:]
	}
	return filepath.FromSlash(path)
}

func isASCIILetter(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
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
		diags := make([]lsp.Diagnostic, 0)
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
