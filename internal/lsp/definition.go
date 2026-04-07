package lsp

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/owenrumney/go-lsp/lsp"

	"martianoff/gala/internal/transpiler"
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
	line := int(params.Position.Line)
	char := int(params.Position.Character)

	// Check if cursor is on a pattern binding (case Xxx(b, h) =>)
	if loc := patternBindingDefinition(text, word, uri, line, char); loc != nil {
		return []lsp.Location{*loc}, nil
	}

	// Check if it's a dot-accessed method: receiver.Method
	// Resolve receiver type to find the correct method definition
	if loc := h.dotMethodDefinition(text, word, uri, line, char, richAST); loc != nil {
		return []lsp.Location{*loc}, nil
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

// patternBindingDefinition checks if the word at the cursor is a pattern binding
// variable (inside case Xxx(b, h) =>). If so, returns its own position as the definition.
// Also searches backwards for the case line if the cursor is in the body after =>.
func patternBindingDefinition(text, word, uri string, curLine, curChar int) *lsp.Location {
	lines := strings.Split(text, "\n")

	// Search backwards from cursor to find the nearest case pattern containing this binding
	for i := curLine; i >= 0; i-- {
		trimmed := strings.TrimSpace(lines[i])
		if !strings.HasPrefix(trimmed, "case ") {
			// Stop if we hit the match opening or a non-case line that's not blank/comment
			if trimmed == "}" || (trimmed != "" && !strings.HasPrefix(trimmed, "//") && i < curLine) {
				// Only stop if we've gone past the current line
				if i < curLine-1 {
					break
				}
			}
			continue
		}

		// Found a case line: case Constructor(a, b, c) =>
		parenOpen := strings.Index(trimmed, "(")
		parenClose := strings.Index(trimmed, ")")
		if parenOpen < 0 || parenClose < 0 || parenClose <= parenOpen {
			continue
		}

		bindings := trimmed[parenOpen+1 : parenClose]
		for _, binding := range strings.Split(bindings, ",") {
			binding = strings.TrimSpace(binding)
			// Strip type annotation if present: "x int" → "x"
			parts := strings.Fields(binding)
			if len(parts) == 0 {
				continue
			}
			varName := parts[0]
			if varName == word {
				// Found the binding — return its position on the case line
				col := strings.Index(lines[i][parenOpen:], word)
				if col >= 0 {
					col += parenOpen
					// Find the absolute position accounting for any offset
					absCol := strings.Index(lines[i], lines[i][col:col+len(word)])
					if absCol < 0 {
						absCol = col
					}
					return &lsp.Location{
						URI: lsp.DocumentURI(uri),
						Range: lsp.Range{
							Start: lsp.Position{Line: i, Character: absCol},
							End:   lsp.Position{Line: i, Character: absCol + len(word)},
						},
					}
				}
			}
		}
	}
	return nil
}

// dotMethodDefinition resolves receiver.Method to the correct method definition.
func (h *GalaHandler) dotMethodDefinition(text, word, uri string, curLine, curChar int, richAST *transpiler.RichAST) *lsp.Location {
	if richAST == nil {
		return nil
	}
	lines := strings.Split(text, "\n")
	if curLine >= len(lines) {
		return nil
	}
	l := lines[curLine]

	// Check if there's a dot before the word
	wordStart := curChar
	for wordStart > 0 && isIdentChar(l[wordStart-1]) {
		wordStart--
	}
	if wordStart <= 0 || l[wordStart-1] != '.' {
		return nil
	}

	// Get the receiver name before the dot
	dotPos := wordStart - 1
	recvEnd := dotPos
	recvStart := recvEnd - 1
	for recvStart >= 0 && isIdentChar(l[recvStart]) {
		recvStart--
	}
	recvStart++
	if recvStart >= recvEnd {
		return nil
	}
	receiverName := l[recvStart:recvEnd]

	// Resolve receiver type from transpiler's VarTypes
	var receiverType string
	if h.varTypes != nil {
		if vtMap, ok := h.varTypes[uri]; ok {
			if t, ok := vtMap[receiverName]; ok {
				receiverType = t
				if idx := strings.Index(receiverType, "["); idx > 0 {
					receiverType = receiverType[:idx]
				}
			}
		}
	}
	if receiverType == "" {
		return nil
	}

	// Find the method on this type
	tm := findType(richAST, receiverType)
	if tm == nil {
		return nil
	}
	method, ok := tm.Methods[word]
	if !ok {
		return nil
	}

	// Navigate to the method's definition
	if method.DefinedIn != "" {
		return fileLocation(method.DefinedIn, word)
	}

	// Search in current file for "func (recv Type) MethodName"
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "func ") && strings.Contains(trimmed, receiverType) && strings.Contains(trimmed, word) {
			col := strings.Index(line, word)
			if col >= 0 {
				return &lsp.Location{
					URI: lsp.DocumentURI(uri),
					Range: lsp.Range{
						Start: lsp.Position{Line: i, Character: col},
						End:   lsp.Position{Line: i, Character: col + len(word)},
					},
				}
			}
		}
	}

	return nil
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
			if !strings.HasPrefix(trimmed, p) {
				continue
			}
			// Ensure word boundary: next char after match must not be alphanumeric
			if len(trimmed) > len(p) && isIdentChar(trimmed[len(p)]) {
				continue // "func perimeter" should not match pattern "func p"
			}
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
