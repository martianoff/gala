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
	varTypeMap := h.varTypes[uri]
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

	// Check if it's a dot-accessed method/field: receiver.Method or receiver.Field
	if loc := h.dotMethodDefinition(text, word, uri, line, char, richAST, varTypeMap); loc != nil {
		return []lsp.Location{*loc}, nil
	}

	// Check sealed variants FIRST (Success, Failure, Some, None, etc.)
	// Must run before the type check because companion types (e.g., "Success")
	// would match the generic type handler and navigate to the wrong location.
	for _, typeMeta := range richAST.Types {
		if !typeMeta.IsSealed {
			continue
		}
		for _, v := range typeMeta.SealedVariants {
			if v.Name == word {
				if typeMeta.DefinedIn != "" {
					loc := fileLocationBroad(typeMeta.DefinedIn, word)
					if loc != nil {
						return []lsp.Location{*loc}, nil
					}
				}
				if typeMeta.Package != "" {
					for _, searchPath := range h.getSearchPaths(uriToPath(uri)) {
						pkgDir := filepath.Join(searchPath, typeMeta.Package)
						if loc := findDefinitionInDir(pkgDir, word); loc != nil {
							return []lsp.Location{*loc}, nil
						}
					}
				}
				currentDir := filepath.Dir(uriToPath(uri))
				if loc := findDefinitionInDir(currentDir, word); loc != nil {
					return []lsp.Location{*loc}, nil
				}
			}
		}
	}

	// Check type metadata for cross-file definitions
	for key, typeMeta := range richAST.Types {
		typeName := typeMeta.Name
		if typeName == "" {
			if idx := strings.LastIndex(key, "."); idx >= 0 {
				typeName = key[idx+1:]
			}
		}
		if typeName == word {
			if typeMeta.DefinedIn != "" {
				loc := fileLocationBroad(typeMeta.DefinedIn, word)
				if loc != nil {
					return []lsp.Location{*loc}, nil
				}
			}
			// Fallback: search current file's directory (same-package sibling)
			currentDir := filepath.Dir(uriToPath(uri))
			if loc := findDefinitionInDir(currentDir, word); loc != nil {
				return []lsp.Location{*loc}, nil
			}
			// Fallback: search package directory in search paths (std/imported types)
			if typeMeta.Package != "" {
				for _, searchPath := range h.getSearchPaths(uriToPath(uri)) {
					pkgDir := filepath.Join(searchPath, typeMeta.Package)
					loc := findDefinitionInDir(pkgDir, word)
					if loc != nil {
						return []lsp.Location{*loc}, nil
					}
				}
			}
		}
		// Check methods
		if method, ok := typeMeta.Methods[word]; ok {
			if method.DefinedIn != "" {
				loc := fileLocationBroad(method.DefinedIn, word)
				if loc != nil {
					return []lsp.Location{*loc}, nil
				}
			}
			// Fallback: search package directory for method definition
			if loc := h.searchPackageDirs(uri, typeMeta, word); loc != nil {
				return []lsp.Location{*loc}, nil
			}
		}
	}

	// Check functions for cross-file definitions
	for _, fm := range richAST.Functions {
		if fm.Name == word {
			if fm.DefinedIn != "" {
				loc := fileLocationBroad(fm.DefinedIn, word)
				if loc != nil {
					return []lsp.Location{*loc}, nil
				}
			}
			// Fallback: search current directory
			currentDir := filepath.Dir(uriToPath(uri))
			if loc := findDefinitionInDir(currentDir, word); loc != nil {
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
					loc := fileLocationBroad(typeMeta.DefinedIn, word)
					if loc != nil {
						return []lsp.Location{*loc}, nil
					}
				}
				// Fall back to finding it in current file
				loc := localDefinition(text, word, uri)
				if loc != nil {
					return []lsp.Location{*loc}, nil
				}
				// Fallback: search package directory (std types have empty DefinedIn)
				if loc := h.searchPackageDirs(uri, typeMeta, word); loc != nil {
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
						loc = fileLocationBroad(typeMeta.DefinedIn, v.Name)
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

// dotMethodDefinition resolves receiver.Method or receiver.Field to the correct definition.
func (h *GalaHandler) dotMethodDefinition(text, word, uri string, curLine, curChar int, richAST *transpiler.RichAST, varTypes map[string]string) *lsp.Location {
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

	// Resolve receiver type using the chain resolver (handles x.Method(), func().Method(), etc.)
	enclosingFunc := findEnclosingFunc(lines, curLine)
	receiverType := resolveChainTypeN(l[:wordStart-1], enclosingFunc, richAST, varTypes, 0)
	if receiverType == "" {
		return nil
	}

	// Find the type metadata for this receiver
	tm := findType(richAST, receiverType)
	if tm == nil {
		return nil
	}

	// Check methods first
	if method, ok := tm.Methods[word]; ok {
		if method.DefinedIn != "" {
			return fileLocationBroad(method.DefinedIn, word)
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

		// Fallback: search package directory for method definition (std/prelude types)
		if loc := h.searchPackageDirs(uri, tm, word); loc != nil {
			return loc
		}

		return nil
	}

	// Check fields (e.g., Tuple.V1, Tuple.V2)
	if _, ok := tm.Fields[word]; ok {
		// Fields are defined with the type — navigate to the field in the type definition file
		if tm.DefinedIn != "" {
			if loc := fileLocationBroad(tm.DefinedIn, word); loc != nil {
				return loc
			}
		}
		// Search current file
		if loc := localDefinition(text, word, uri); loc != nil {
			return loc
		}
		// Fallback: search package directory for the field
		if loc := h.searchPackageDirs(uri, tm, word); loc != nil {
			return loc
		}
	}

	return nil
}

// searchPackageDirs searches the type's package directory and search paths for a definition.
// Uses broad search because the transpiler already confirmed the method/field exists on this type.
func (h *GalaHandler) searchPackageDirs(uri string, tm *transpiler.TypeMetadata, word string) *lsp.Location {
	if tm.Package != "" {
		for _, searchPath := range h.getSearchPaths(uriToPath(uri)) {
			pkgDir := filepath.Join(searchPath, tm.Package)
			if loc := findDefinitionInDirBroad(pkgDir, word); loc != nil {
				return loc
			}
		}
	}
	// Also try current file's directory (same-package sibling)
	currentDir := filepath.Dir(uriToPath(uri))
	if loc := findDefinitionInDirBroad(currentDir, word); loc != nil {
		return loc
	}
	return nil
}

// findDefinitionInDirBroad searches .gala files in a directory using broad matching.
// Used when the transpiler has confirmed the definition exists in this package.
func findDefinitionInDirBroad(dir, name string) *lsp.Location {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".gala") {
			continue
		}
		absPath := filepath.Join(dir, e.Name())
		loc := fileLocationBroad(absPath, name)
		if loc != nil {
			return loc
		}
	}
	return nil
}

// findDefinitionInDir searches all .gala files in a directory for a type/func definition.
func findDefinitionInDir(dir, name string) *lsp.Location {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".gala") {
			continue
		}
		absPath := filepath.Join(dir, e.Name())
		loc := fileLocation(absPath, name)
		if loc != nil {
			return loc
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

// findWholeWord returns the column of the first whole-word occurrence of name in line,
// or -1 if not found. A whole word has no adjacent identifier characters.
func findWholeWord(line, name string) int {
	idx := 0
	for {
		pos := strings.Index(line[idx:], name)
		if pos < 0 {
			return -1
		}
		col := idx + pos
		before := col > 0 && isIdentChar(line[col-1])
		after := col+len(name) < len(line) && isIdentChar(line[col+len(name)])
		if !before && !after {
			return col
		}
		idx = col + len(name)
	}
}

// fileLocation searches a file for a declaration of name using keyword patterns.
// Used by findDefinitionInDir for directory scanning where multiple files are searched.
func fileLocation(filePath, name string) *lsp.Location {
	absPath, err := filepath.Abs(filePath)
	if err != nil {
		return nil
	}
	data, err := os.ReadFile(absPath)
	if err != nil {
		return nil
	}
	uri := pathToURI(absPath)
	return localDefinition(string(data), name, uri)
}

// fileLocationBroad searches a file for name using keyword patterns first,
// then falls back to whole-word search. Only use when the transpiler has already
// resolved the file path via DefinedIn — we know the file contains the definition.
func fileLocationBroad(filePath, name string) *lsp.Location {
	absPath, err := filepath.Abs(filePath)
	if err != nil {
		return nil
	}
	data, err := os.ReadFile(absPath)
	if err != nil {
		return nil
	}
	uri := pathToURI(absPath)
	text := string(data)

	if loc := localDefinition(text, name, uri); loc != nil {
		return loc
	}

	// Whole-word fallback: the transpiler already resolved this file as the
	// definition source — find the first whole-word occurrence of the name.
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		col := findWholeWord(line, name)
		if col >= 0 {
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

