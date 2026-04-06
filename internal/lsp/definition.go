package lsp

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/tliron/glsp"
	protocol "github.com/tliron/glsp/protocol_3_16"

	"martianoff/gala/internal/transpiler"
)

// TextDocumentDefinition handles Go to Definition requests.
// Looks up the symbol at the cursor and returns its definition location.
func (s *GalaServer) TextDocumentDefinition(ctx *glsp.Context, params *protocol.DefinitionParams) (any, error) {
	uri := params.TextDocument.URI
	line := int(params.Position.Line)
	char := int(params.Position.Character)

	s.mu.Lock()
	text, ok := s.documents[uri]
	richAST := s.richASTs[uri]
	s.mu.Unlock()

	if !ok || richAST == nil {
		return nil, nil
	}

	word := wordAtPosition(text, line, char)
	if word == "" {
		return nil, nil
	}

	// Try to find definition in type metadata
	loc := findDefinition(richAST, word, uri)
	if loc != nil {
		return loc, nil
	}

	// Try to find definition in the current file (local declarations)
	localLoc := findLocalDefinition(text, word, uri)
	if localLoc != nil {
		return localLoc, nil
	}

	return nil, nil
}

// findDefinition looks up a symbol in the RichAST metadata and returns its location.
func findDefinition(richAST *transpiler.RichAST, name string, currentURI string) *protocol.Location {
	// Check types — use DefinedIn field for cross-file navigation
	for key, typeMeta := range richAST.Types {
		typeName := key
		if idx := strings.LastIndex(key, "."); idx >= 0 {
			typeName = key[idx+1:]
		}
		if typeName == name && typeMeta.DefinedIn != "" {
			loc := fileToLocation(typeMeta.DefinedIn, name)
			if loc != nil {
				return loc
			}
		}
	}

	// Check functions
	for _, funcMeta := range richAST.Functions {
		if funcMeta.Name == name {
			// Functions in the current file — find the line
			return findSymbolInFile(currentURI, "func "+name)
		}
	}

	// Check methods on types
	for _, typeMeta := range richAST.Types {
		if method, ok := typeMeta.Methods[name]; ok {
			if method.DefinedIn != "" {
				loc := fileToLocation(method.DefinedIn, name)
				if loc != nil {
					return loc
				}
			}
		}
	}

	return nil
}

// findLocalDefinition scans the current file text for a declaration of the given name.
func findLocalDefinition(text, name, uri string) *protocol.Location {
	lines := strings.Split(text, "\n")
	patterns := []string{
		"val " + name,
		"var " + name,
		"func " + name,
		"type " + name,
		"sealed type " + name,
		"struct " + name,
		"case " + name,
	}

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		for _, pattern := range patterns {
			if strings.HasPrefix(trimmed, pattern) {
				col := strings.Index(line, name)
				if col < 0 {
					col = 0
				}
				return &protocol.Location{
					URI: uri,
					Range: protocol.Range{
						Start: protocol.Position{Line: uint32(i), Character: uint32(col)},
						End:   protocol.Position{Line: uint32(i), Character: uint32(col + len(name))},
					},
				}
			}
		}
	}
	return nil
}

// fileToLocation converts a file path and symbol name to an LSP Location.
func fileToLocation(filePath, name string) *protocol.Location {
	absPath, err := filepath.Abs(filePath)
	if err != nil {
		return nil
	}

	// Read the file to find the symbol's line
	data, err := os.ReadFile(absPath)
	if err != nil {
		return nil
	}

	loc := findSymbolInContent(string(data), name, pathToURI(absPath))
	return loc
}

// findSymbolInFile finds a symbol in a file identified by URI.
func findSymbolInFile(uri, pattern string) *protocol.Location {
	// For current file, we'd need the text — but we can return line 0 as fallback
	return &protocol.Location{
		URI:   uri,
		Range: zeroRange(),
	}
}

// findSymbolInContent scans file content for a declaration of the given name.
func findSymbolInContent(text, name, uri string) *protocol.Location {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		if strings.Contains(line, name) {
			col := strings.Index(line, name)
			return &protocol.Location{
				URI: uri,
				Range: protocol.Range{
					Start: protocol.Position{Line: uint32(i), Character: uint32(col)},
					End:   protocol.Position{Line: uint32(i), Character: uint32(col + len(name))},
				},
			}
		}
	}
	return nil
}

func pathToURI(path string) string {
	path = filepath.ToSlash(path)
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return "file://" + path
}
