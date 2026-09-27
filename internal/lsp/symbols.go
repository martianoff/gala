package lsp

import (
	"context"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"

	"github.com/owenrumney/go-lsp/lsp"
)

// maxWorkspaceSymbols caps a workspace/symbol answer; clients re-query as the
// user types, so a short query does not need every match in a large tree.
const maxWorkspaceSymbols = 500

// DocumentSymbol returns the outline of a GALA file: its types (sealed
// variants and same-file methods as children), functions, methods of types
// declared elsewhere, and package-level vals and vars.
//
// It reads the document's own text, so it lists exactly this file's
// declarations at their real positions, whatever the analysis merged in from
// sibling files, and it keeps working while the file does not parse.
func (h *GalaHandler) DocumentSymbol(ctx context.Context, params *lsp.DocumentSymbolParams) ([]lsp.DocumentSymbol, error) {
	uri := string(params.TextDocument.URI)
	h.mu.Lock()
	text := h.documents[uri]
	h.mu.Unlock()
	if text == "" {
		return nil, nil
	}

	lines := splitLines(text)
	decls := scanDeclarations(text)

	typeIndex := make(map[string]int) // type name -> index in symbols
	symbols := make([]lsp.DocumentSymbol, 0, len(decls))
	for _, d := range decls {
		if d.kind == lsp.SymbolKindMethod {
			continue
		}
		sym := documentSymbol(d, lines)
		for _, v := range d.variants {
			sym.Children = append(sym.Children, documentSymbol(v, lines))
		}
		if isTypeKind(d.kind) {
			typeIndex[d.name] = len(symbols)
		}
		symbols = append(symbols, sym)
	}

	// Methods: under their receiver type when it is declared in this file,
	// which widens the type's range to cover them; otherwise top level.
	for _, d := range decls {
		if d.kind != lsp.SymbolKindMethod {
			continue
		}
		method := documentSymbol(d, lines)
		i, ok := typeIndex[d.container]
		if !ok {
			method.Name = d.container + "." + d.name
			symbols = append(symbols, method)
			continue
		}
		parent := &symbols[i]
		parent.Children = append(parent.Children, method)
		if method.Range.End.Line > parent.Range.End.Line {
			parent.Range.End = method.Range.End
		}
	}

	sort.SliceStable(symbols, func(a, b int) bool {
		return symbols[a].SelectionRange.Start.Line < symbols[b].SelectionRange.Start.Line
	})
	symbolsToWire(h.index(text), symbols)
	return symbols, nil
}

// symbolsToWire converts the byte-column ranges of an outline, children
// included, to wire columns.
func symbolsToWire(x lineIndex, symbols []lsp.DocumentSymbol) {
	for i := range symbols {
		symbols[i].Range = x.rangeToWire(symbols[i].Range)
		symbols[i].SelectionRange = x.rangeToWire(symbols[i].SelectionRange)
		symbolsToWire(x, symbols[i].Children)
	}
}

// WorkspaceSymbol finds declarations whose name contains the query, ignoring
// case, across the .gala files of the workspace: the project root, plus the
// directory of any open document outside it. Hidden directories (build caches
// such as .gala, VCS metadata) and Bazel output trees are skipped.
func (h *GalaHandler) WorkspaceSymbol(ctx context.Context, params *lsp.WorkspaceSymbolParams) ([]lsp.SymbolInformation, error) {
	query := strings.ToLower(params.Query)
	enc := h.positionEncoding()
	var symbols []lsp.SymbolInformation
	for _, path := range h.workspaceFiles() {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		text, ok := h.fileText(path)
		if !ok {
			continue
		}
		pkgName := sourcePackageName(text)
		uri := lsp.DocumentURI(pathToURI(path))
		x := newLineIndex(text, enc)
		add := func(d declaration) {
			if !strings.Contains(strings.ToLower(d.name), query) {
				return
			}
			container := d.container
			if container == "" {
				container = pkgName
			}
			symbols = append(symbols, lsp.SymbolInformation{
				Name:          d.name,
				Kind:          d.kind,
				Location:      lsp.Location{URI: uri, Range: x.rangeToWire(d.selectionRange())},
				ContainerName: container,
			})
		}
		for _, d := range scanDeclarations(text) {
			add(d)
			for _, v := range d.variants {
				add(v)
			}
		}
	}

	// Exact names first, then shorter names, so a truncated answer keeps the
	// matches a user most likely wants.
	sort.SliceStable(symbols, func(a, b int) bool {
		ea, eb := strings.EqualFold(symbols[a].Name, query), strings.EqualFold(symbols[b].Name, query)
		if ea != eb {
			return ea
		}
		return len(symbols[a].Name) < len(symbols[b].Name)
	})
	if len(symbols) > maxWorkspaceSymbols {
		symbols = symbols[:maxWorkspaceSymbols]
	}
	return symbols, nil
}

// workspaceFiles lists the .gala files under the workspace roots, each once.
func (h *GalaHandler) workspaceFiles() []string {
	h.mu.Lock()
	roots := []string{}
	if h.rootPath != "" {
		roots = append(roots, h.rootPath)
	}
	for uri := range h.documents {
		dir := filepath.Dir(uriToPath(uri))
		if h.rootPath == "" || !withinDir(h.rootPath, dir) {
			roots = append(roots, dir)
		}
	}
	h.mu.Unlock()

	seen := make(map[string]bool)
	var files []string
	for _, root := range roots {
		_ = filepath.WalkDir(root, func(path string, e fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			name := e.Name()
			if e.IsDir() {
				if path != root && (strings.HasPrefix(name, ".") || strings.HasPrefix(name, "bazel-") || name == "node_modules") {
					return filepath.SkipDir
				}
				return nil
			}
			key := filepath.Clean(path)
			if filepath.Separator == '\\' {
				key = strings.ToLower(key) // Windows paths are case-insensitive
			}
			if filepath.Ext(name) == ".gala" && !seen[key] {
				seen[key] = true
				files = append(files, path)
			}
			return nil
		})
	}
	return files
}

// withinDir reports whether path is dir or lies below it.
func withinDir(dir, path string) bool {
	rel, err := filepath.Rel(dir, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func documentSymbol(d declaration, lines []string) lsp.DocumentSymbol {
	return lsp.DocumentSymbol{
		Name:           d.name,
		Kind:           d.kind,
		Range:          d.fullRange(lines),
		SelectionRange: d.selectionRange(),
	}
}

func isTypeKind(k lsp.SymbolKind) bool {
	switch k {
	case lsp.SymbolKindStruct, lsp.SymbolKindInterface, lsp.SymbolKindEnum, lsp.SymbolKindClass:
		return true
	}
	return false
}

// splitLines splits text into lines without their line terminators, the same
// way scanDeclarations numbers them.
func splitLines(text string) []string {
	lines := strings.Split(text, "\n")
	for i := range lines {
		lines[i] = strings.TrimRight(lines[i], "\r")
	}
	return lines
}
