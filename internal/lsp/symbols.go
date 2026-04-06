package lsp

import (
	"context"
	"strings"

	"github.com/owenrumney/go-lsp/lsp"
)

// DocumentSymbol returns the symbol tree for a GALA file.
func (h *GalaHandler) DocumentSymbol(ctx context.Context, params *lsp.DocumentSymbolParams) ([]lsp.DocumentSymbol, error) {
	uri := string(params.TextDocument.URI)

	h.mu.Lock()
	text := h.documents[uri]
	richAST := h.richASTs[uri]
	h.mu.Unlock()

	if text == "" || richAST == nil {
		return nil, nil
	}

	var symbols []lsp.DocumentSymbol

	// Types
	for key, tm := range richAST.Types {
		name := tm.Name
		if name == "" {
			if idx := strings.LastIndex(key, "."); idx >= 0 {
				name = key[idx+1:]
			}
		}
		if !isExported(name) || tm.Package != richAST.PackageName {
			continue
		}

		kind := lsp.SymbolKindStruct
		if tm.IsSealed {
			kind = lsp.SymbolKindEnum
		}

		sym := lsp.DocumentSymbol{
			Name:           name,
			Kind:           kind,
			Range:          findSymbolRange(text, name),
			SelectionRange: findSymbolRange(text, name),
		}

		// Add sealed variants as children
		for _, v := range tm.SealedVariants {
			sym.Children = append(sym.Children, lsp.DocumentSymbol{
				Name:           v.Name,
				Kind:           lsp.SymbolKindEnumMember,
				Range:          findSymbolRange(text, v.Name),
				SelectionRange: findSymbolRange(text, v.Name),
			})
		}

		// Add methods as children
		for mName := range tm.Methods {
			sym.Children = append(sym.Children, lsp.DocumentSymbol{
				Name:           mName,
				Kind:           lsp.SymbolKindMethod,
				Range:          findSymbolRange(text, mName),
				SelectionRange: findSymbolRange(text, mName),
			})
		}

		symbols = append(symbols, sym)
	}

	// Functions
	for _, fm := range richAST.Functions {
		if fm.Package != richAST.PackageName {
			continue
		}
		symbols = append(symbols, lsp.DocumentSymbol{
			Name:           fm.Name,
			Kind:           lsp.SymbolKindFunction,
			Range:          findSymbolRange(text, fm.Name),
			SelectionRange: findSymbolRange(text, fm.Name),
		})
	}

	return symbols, nil
}

func findSymbolRange(text, name string) lsp.Range {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		col := strings.Index(line, name)
		if col >= 0 {
			return lsp.Range{
				Start: lsp.Position{Line: i, Character: col},
				End:   lsp.Position{Line: i, Character: col + len(name)},
			}
		}
	}
	return zeroRange()
}
