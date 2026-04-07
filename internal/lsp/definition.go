package lsp

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/owenrumney/go-lsp/lsp"
)

func (h *GalaHandler) Definition(ctx context.Context, params *lsp.DefinitionParams) ([]lsp.Location, error) {
	uri := string(params.TextDocument.URI)

	h.mu.Lock()
	text := h.documents[uri]
	richAST := h.richASTs[uri]
	h.mu.Unlock()

	if text == "" || richAST == nil {
		return nil, nil
	}

	word := wordAtPosition(text, int(params.Position.Line), int(params.Position.Character))
	if word == "" {
		return nil, nil
	}

	// Check type metadata for cross-file definitions
	for key, typeMeta := range richAST.Types {
		typeName := typeMeta.Name
		if typeName == "" {
			if idx := strings.LastIndex(key, "."); idx >= 0 {
				typeName = key[idx+1:]
			}
		}
		if typeName == word && typeMeta.DefinedIn != "" {
			loc := fileLocation(typeMeta.DefinedIn, word)
			if loc != nil {
				return []lsp.Location{*loc}, nil
			}
		}
		// Check methods
		if method, ok := typeMeta.Methods[word]; ok && method.DefinedIn != "" {
			loc := fileLocation(method.DefinedIn, word)
			if loc != nil {
				return []lsp.Location{*loc}, nil
			}
		}
	}

	// Check struct/sealed fields — named arg field names like Circle(radius = ...)
	for _, typeMeta := range richAST.Types {
		// Check regular struct fields
		for _, fn := range typeMeta.FieldNames {
			if fn == word {
				if typeMeta.DefinedIn != "" {
					loc := fileLocation(typeMeta.DefinedIn, word)
					if loc != nil {
						return []lsp.Location{*loc}, nil
					}
				}
				// Fall back to finding it in current file
				loc := localDefinition(text, word, uri)
				if loc != nil {
					return []lsp.Location{*loc}, nil
				}
			}
		}
		// Check sealed variant fields
		for _, v := range typeMeta.SealedVariants {
			for _, fn := range v.FieldNames {
				if fn == word {
					// Navigate to the sealed case declaration
					loc := localDefinition(text, v.Name, uri)
					if loc == nil && typeMeta.DefinedIn != "" {
						loc = fileLocation(typeMeta.DefinedIn, v.Name)
					}
					if loc != nil {
						return []lsp.Location{*loc}, nil
					}
				}
			}
		}
	}

	// Check import paths — clicking a package name navigates to its directory
	for path, pkgName := range richAST.Packages {
		if pkgName == word || word == path {
			// Try to find the package directory
			for _, searchPath := range h.getSearchPaths(uriToPath(uri)) {
				pkgDir := searchPath + "/" + path
				loc := findFirstGalaFile(pkgDir, word)
				if loc != nil {
					return []lsp.Location{*loc}, nil
				}
			}
		}
	}

	// Local definition search
	loc := localDefinition(text, word, uri)
	if loc != nil {
		return []lsp.Location{*loc}, nil
	}

	return nil, nil
}

func findFirstGalaFile(dir, name string) *lsp.Location {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".gala") {
			absPath := dir + "/" + e.Name()
			return &lsp.Location{
				URI:   lsp.DocumentURI(pathToURI(absPath)),
				Range: zeroRange(),
			}
		}
	}
	return nil
}

func (h *GalaHandler) References(ctx context.Context, params *lsp.ReferenceParams) ([]lsp.Location, error) {
	uri := string(params.TextDocument.URI)

	h.mu.Lock()
	text := h.documents[uri]
	h.mu.Unlock()

	if text == "" {
		return nil, nil
	}

	word := wordAtPosition(text, int(params.Position.Line), int(params.Position.Character))
	if word == "" {
		return nil, nil
	}

	// Find all occurrences of the word in the current file
	var locs []lsp.Location
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		idx := 0
		for {
			pos := strings.Index(line[idx:], word)
			if pos < 0 {
				break
			}
			col := idx + pos
			// Verify it's a whole word
			before := col > 0 && isIdentChar(line[col-1])
			after := col+len(word) < len(line) && isIdentChar(line[col+len(word)])
			if !before && !after {
				locs = append(locs, lsp.Location{
					URI: lsp.DocumentURI(uri),
					Range: lsp.Range{
						Start: lsp.Position{Line: i, Character: col},
						End:   lsp.Position{Line: i, Character: col + len(word)},
					},
				})
			}
			idx = col + len(word)
		}
	}

	return locs, nil
}

func localDefinition(text, name, uri string) *lsp.Location {
	lines := strings.Split(text, "\n")
	patterns := []string{
		"val " + name, "var " + name, "func " + name,
		"type " + name, "sealed type " + name, "struct " + name, "case " + name,
	}
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		for _, p := range patterns {
			if strings.HasPrefix(trimmed, p) {
				col := strings.Index(line, name)
				if col < 0 {
					col = 0
				}
				return &lsp.Location{
					URI: lsp.DocumentURI(uri),
					Range: lsp.Range{
						Start: lsp.Position{Line: i, Character: col},
						End:   lsp.Position{Line: i, Character: col + len(name)},
					},
				}
			}
		}
	}
	return nil
}

func fileLocation(filePath, name string) *lsp.Location {
	absPath, err := filepath.Abs(filePath)
	if err != nil {
		return nil
	}
	data, err := os.ReadFile(absPath)
	if err != nil {
		return nil
	}
	lines := strings.Split(string(data), "\n")
	for i, line := range lines {
		if strings.Contains(line, name) {
			col := strings.Index(line, name)
			return &lsp.Location{
				URI: lsp.DocumentURI(pathToURI(absPath)),
				Range: lsp.Range{
					Start: lsp.Position{Line: i, Character: col},
					End:   lsp.Position{Line: i, Character: col + len(name)},
				},
			}
		}
	}
	return nil
}
