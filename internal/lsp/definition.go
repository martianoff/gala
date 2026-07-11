package lsp

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/owenrumney/go-lsp/lsp"

	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/analyzer"
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

	// Imported-package navigation. Parse imports from the document because
	// Go-only packages (e.g. go_interop, which ships only .go sources) never
	// appear in richAST.Packages — the analyzer records a package name only
	// when it finds GALA sources to attach it to.
	imports := parseGalaImports(text)

	// pkg.Symbol — clicking a member qualified by an imported package name
	// navigates to that symbol's definition in the package's source files.
	if loc := h.packageMemberDefinition(text, word, uri, line, char, imports); loc != nil {
		return []lsp.Location{*loc}, nil
	}

	// Clicking the imported package name itself navigates to the package.
	if importPath, ok := imports[word]; ok {
		if dir := h.resolveImportDir(uri, importPath); dir != "" {
			if loc := packageFileLocation(dir); loc != nil {
				return []lsp.Location{*loc}, nil
			}
		}
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
				if loc := locationAt(typeMeta.DefinedIn, v.Pos, word); loc != nil {
					return []lsp.Location{*loc}, nil
				}
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
			if loc := locationAt(typeMeta.DefinedIn, typeMeta.Pos, word); loc != nil {
				return []lsp.Location{*loc}, nil
			}
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
			if loc := locationAt(method.DefinedIn, method.Pos, word); loc != nil {
				return []lsp.Location{*loc}, nil
			}
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
			if loc := locationAt(fm.DefinedIn, fm.Pos, word); loc != nil {
				return []lsp.Location{*loc}, nil
			}
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
				if loc := fieldDefinitionLocation(typeMeta, word); loc != nil {
					return []lsp.Location{*loc}, nil
				}
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
					if loc := locationAt(typeMeta.DefinedIn, v.Pos, v.Name); loc != nil {
						return []lsp.Location{*loc}, nil
					}
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

	// Flatten multi-line chain expressions so the resolver can walk back
	// across newlines (same technique as typeAtDot).
	flat := flattenLogicalLine(lines, curLine, wordStart-1)
	enclosingFunc := findEnclosingFunc(lines, curLine)
	receiverType := resolveChainTypeN(flat, enclosingFunc, richAST, varTypes, 0)
	if receiverType == "" {
		return nil
	}

	// Find the type metadata for this receiver
	tm := findType(richAST, receiverType)
	if tm == nil {
		// The receiver may be a Go type (e.g. b : *bytes.Buffer). Try to
		// resolve the method in the Go type's package source.
		return h.goMethodDefinition(text, uri, receiverType, word, richAST)
	}

	// Check methods first
	if method, ok := tm.Methods[word]; ok {
		if loc := locationAt(method.DefinedIn, method.Pos, word); loc != nil {
			return loc
		}
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

	// Check fields (e.g., Tuple.V1, Tuple.V2, struct fields like SSEEvent.Data).
	// Prefer the exact position recorded by the analyzer over any text search,
	// which would mis-match identically-named tokens inside comments.
	if _, ok := tm.Fields[word]; ok {
		if loc := fieldDefinitionLocation(tm, word); loc != nil {
			return loc
		}
		if tm.DefinedIn != "" {
			if loc := fileLocationBroad(tm.DefinedIn, word); loc != nil {
				return loc
			}
		}
		if loc := localDefinition(text, word, uri); loc != nil {
			return loc
		}
		if loc := h.searchPackageDirs(uri, tm, word); loc != nil {
			return loc
		}
	}

	return nil
}

// parseGalaImports scans the document text for import declarations and returns
// a map of local package name -> import path. This captures Go-only packages
// (e.g. go_interop) that never surface in richAST.Packages because they have
// no .gala sources for the analyzer to attach a package name to. The local
// name is the alias when one is given (e.g. `im "path"`), otherwise the final
// path segment. Dot imports (`. "path"`) contribute no qualified name and are
// skipped.
func parseGalaImports(text string) map[string]string {
	imports := make(map[string]string)
	inBlock := false
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "//") {
			continue
		}
		if !inBlock {
			if strings.HasPrefix(line, "import (") {
				inBlock = true
				continue
			}
			if strings.HasPrefix(line, "import ") {
				addImportSpec(imports, strings.TrimSpace(strings.TrimPrefix(line, "import")))
			}
			continue
		}
		if strings.HasPrefix(line, ")") {
			inBlock = false
			continue
		}
		addImportSpec(imports, line)
	}
	return imports
}

// addImportSpec parses a single import spec ("path", `alias "path"`, or
// `. "path"`) and records the local-name -> path mapping.
func addImportSpec(imports map[string]string, spec string) {
	q := strings.Index(spec, "\"")
	if q < 0 {
		return
	}
	rest := spec[q+1:]
	end := strings.Index(rest, "\"")
	if end < 0 {
		return
	}
	path := rest[:end]
	if path == "" {
		return
	}
	alias := strings.TrimSpace(spec[:q])
	switch {
	case alias == ".":
		return // dot import: symbols are unqualified, no package name to click
	case alias != "":
		imports[alias] = path
	default:
		local := path
		if idx := strings.LastIndex(path, "/"); idx >= 0 {
			local = path[idx+1:]
		}
		imports[local] = path
	}
}

// resolveImportDir resolves an import path to a filesystem directory using the
// same search-path strategy the analyzer's resolver applies: internal
// martianoff/gala/* paths map to a directory relative to a search path with
// the module prefix stripped; everything else is tried as-is.
func (h *GalaHandler) resolveImportDir(uri, importPath string) string {
	relPath := strings.TrimPrefix(importPath, "martianoff/gala/")
	for _, sp := range h.getSearchPaths(uriToPath(uri)) {
		for _, cand := range []string{relPath, importPath} {
			dir := filepath.Join(sp, cand)
			if info, err := os.Stat(dir); err == nil && info.IsDir() {
				if abs, err := filepath.Abs(dir); err == nil {
					return abs
				}
				return dir
			}
		}
	}
	// Not on a GALA search path — fall back to the Go SDK source tree so
	// definitions in Go stdlib packages (bytes, crypto/sha256, ...) resolve.
	return analyzer.GoPackageSourceDir(importPath)
}

// packageMemberDefinition resolves a `pkg.Symbol` reference where `pkg` is an
// imported package name, navigating to Symbol's definition in the package's
// source files (.gala or .go). This is what makes go_interop.SliceAppend and
// similar Go-interop calls clickable.
func (h *GalaHandler) packageMemberDefinition(text, word, uri string, curLine, curChar int, imports map[string]string) *lsp.Location {
	lines := strings.Split(text, "\n")
	if curLine >= len(lines) {
		return nil
	}
	l := lines[curLine]

	// The word must be immediately preceded by a dot.
	start := curChar
	if start > len(l) {
		start = len(l)
	}
	for start > 0 && isIdentChar(l[start-1]) {
		start--
	}
	if start == 0 || l[start-1] != '.' {
		return nil
	}

	// The identifier before that dot is the (candidate) package name. It must
	// itself not be preceded by a dot — `x.pkg.Symbol` means `pkg` is a field,
	// not an imported package.
	pkgEnd := start - 1
	pkgStart := pkgEnd
	for pkgStart > 0 && isIdentChar(l[pkgStart-1]) {
		pkgStart--
	}
	if pkgStart == pkgEnd || (pkgStart > 0 && l[pkgStart-1] == '.') {
		return nil
	}
	importPath, ok := imports[l[pkgStart:pkgEnd]]
	if !ok {
		return nil
	}
	dir := h.resolveImportDir(uri, importPath)
	if dir == "" {
		return nil
	}
	return dirSymbolLocation(dir, word)
}

// dirSymbolLocation searches the .gala and .go files in dir for a top-level
// definition of name. GALA files use the keyword-pattern search; Go files use
// goSymbolLocation.
func dirSymbolLocation(dir, name string) *lsp.Location {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var goFiles []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		n := e.Name()
		if strings.HasSuffix(n, ".gala") && !strings.HasSuffix(n, "_test.gala") {
			if loc := fileLocationBroad(filepath.Join(dir, n), name); loc != nil {
				return loc
			}
		} else if strings.HasSuffix(n, ".go") && !strings.HasSuffix(n, "_test.go") {
			goFiles = append(goFiles, filepath.Join(dir, n))
		}
	}
	for _, gf := range goFiles {
		if loc := goSymbolLocation(gf, name); loc != nil {
			return loc
		}
	}
	return nil
}

// goSymbolLocation searches a .go file for a top-level func/type/const/var
// declaration of name and returns the location of its identifier. Methods
// (func with a receiver) are skipped — a package-qualified reference resolves
// to a package-level symbol, not a method.
func goSymbolLocation(filePath, name string) *lsp.Location {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil
	}
	absPath, err := filepath.Abs(filePath)
	if err != nil {
		return nil
	}
	uri := pathToURI(absPath)
	keywords := []string{"func ", "type ", "const ", "var "}
	for i, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		for _, kw := range keywords {
			if !strings.HasPrefix(trimmed, kw) {
				continue
			}
			rest := trimmed[len(kw):]
			if strings.HasPrefix(rest, "(") {
				continue // func with receiver — a method, not a package symbol
			}
			end := 0
			for end < len(rest) && isIdentChar(rest[end]) {
				end++
			}
			if rest[:end] != name {
				continue
			}
			col := findWholeWord(line, name)
			if col < 0 {
				col = strings.Index(line, name)
			}
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

// packageFileLocation returns a location at the `package` declaration of a
// representative source file in dir — a .gala file when present, else a .go
// file. Used to navigate to a package from its imported name.
func packageFileLocation(dir string) *lsp.Location {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var goFallback string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		n := e.Name()
		if strings.HasSuffix(n, ".gala") && !strings.HasSuffix(n, "_test.gala") {
			return packageDeclLocation(filepath.Join(dir, n))
		}
		if goFallback == "" && strings.HasSuffix(n, ".go") && !strings.HasSuffix(n, "_test.go") {
			goFallback = filepath.Join(dir, n)
		}
	}
	if goFallback != "" {
		return packageDeclLocation(goFallback)
	}
	return nil
}

// packageDeclLocation points at the `package X` line of filePath, falling back
// to the file's start when no package clause is found.
func packageDeclLocation(filePath string) *lsp.Location {
	absPath, err := filepath.Abs(filePath)
	if err != nil {
		return nil
	}
	uri := lsp.DocumentURI(pathToURI(absPath))
	if data, err := os.ReadFile(absPath); err == nil {
		for i, line := range strings.Split(string(data), "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "package ") {
				return &lsp.Location{URI: uri, Range: lsp.Range{
					Start: lsp.Position{Line: i, Character: 0},
					End:   lsp.Position{Line: i, Character: 0},
				}}
			}
		}
	}
	return &lsp.Location{URI: uri, Range: zeroRange()}
}

// goMethodDefinition resolves a method call on a value whose type comes from a
// Go package (e.g. b.WriteString where b : *bytes.Buffer) by locating the
// method in the Go type's package source. Best-effort: the type's package must
// be one the document imports so its import path — hence source directory — is
// known, and the method must be declared as `func (recv Type) Method` (struct
// methods) or as an interface method in `type Type interface { ... }`.
func (h *GalaHandler) goMethodDefinition(text, uri, receiverType, method string, richAST *transpiler.RichAST) *lsp.Location {
	if richAST == nil || richAST.GoTypeInfo == nil {
		return nil
	}
	key := stripTypeParams(strings.TrimPrefix(strings.TrimSpace(receiverType), "*"))
	td := richAST.GoTypeInfo.Types[key]
	if td == nil {
		return nil
	}
	if _, ok := td.Methods[method]; !ok {
		return nil
	}
	dot := strings.LastIndex(key, ".")
	if dot < 0 {
		return nil
	}
	pkgName, typeName := key[:dot], key[dot+1:]
	importPath, ok := parseGalaImports(text)[pkgName]
	if !ok {
		return nil
	}
	dir := h.resolveImportDir(uri, importPath)
	if dir == "" {
		return nil
	}
	return goMethodInDir(dir, typeName, method)
}

// goMethodInDir searches the non-test .go files in dir for a declaration of
// method on typeName.
func goMethodInDir(dir, typeName, method string) *lsp.Location {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		if loc := goMethodInFile(filepath.Join(dir, e.Name()), typeName, method); loc != nil {
			return loc
		}
	}
	return nil
}

// goMethodInFile looks for `func (recv [*]typeName[...]) method(` in a .go file
// and returns the location of the method identifier.
func goMethodInFile(filePath, typeName, method string) *lsp.Location {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil
	}
	absPath, err := filepath.Abs(filePath)
	if err != nil {
		return nil
	}
	uri := pathToURI(absPath)
	for i, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "func (") {
			continue
		}
		closeParen := strings.Index(trimmed, ")")
		if closeParen < 0 {
			continue
		}
		if !receiverMatchesType(trimmed[len("func ("):closeParen], typeName) {
			continue
		}
		rest := strings.TrimSpace(trimmed[closeParen+1:])
		end := 0
		for end < len(rest) && isIdentChar(rest[end]) {
			end++
		}
		if rest[:end] != method {
			continue
		}
		col := findWholeWord(line, method)
		if col < 0 {
			col = 0
		}
		return &lsp.Location{
			URI: lsp.DocumentURI(uri),
			Range: lsp.Range{
				Start: lsp.Position{Line: i, Character: col},
				End:   lsp.Position{Line: i, Character: col + len(method)},
			},
		}
	}
	return nil
}

// receiverMatchesType reports whether a Go method receiver clause (the text
// between the parens in `func (b *Buffer) ...`) is a receiver for typeName,
// tolerating a pointer star and generic type parameters.
func receiverMatchesType(recv, typeName string) bool {
	parts := strings.Fields(strings.TrimSpace(recv))
	if len(parts) == 0 {
		return false
	}
	typ := strings.TrimPrefix(parts[len(parts)-1], "*")
	if idx := strings.IndexByte(typ, '['); idx >= 0 {
		typ = typ[:idx]
	}
	return typ == typeName
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
		"val " + name, "var " + name, "bind " + name, "also " + name, "func " + name,
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

// locationAt converts a (file, SourcePos, identifier) triple captured by the
// analyzer into an LSP Location. Prefer this over any text search: the
// analyzer records the exact identifier position at parse time, so the result
// is immune to identically-named tokens inside comments, strings, or aliases.
func locationAt(definedIn string, pos transpiler.SourcePos, name string) *lsp.Location {
	if definedIn == "" || pos.Line == 0 {
		return nil
	}
	absPath, err := filepath.Abs(definedIn)
	if err != nil {
		return nil
	}
	uri := pathToURI(absPath)
	line := pos.Line - 1 // analyzer is 1-based, LSP is 0-based
	return &lsp.Location{
		URI: lsp.DocumentURI(uri),
		Range: lsp.Range{
			Start: lsp.Position{Line: line, Character: pos.Column},
			End:   lsp.Position{Line: line, Character: pos.Column + len(name)},
		},
	}
}

func fieldDefinitionLocation(tm *transpiler.TypeMetadata, fieldName string) *lsp.Location {
	if tm == nil {
		return nil
	}
	pos, ok := tm.FieldPositions[fieldName]
	if !ok {
		return nil
	}
	return locationAt(tm.DefinedIn, pos, fieldName)
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

