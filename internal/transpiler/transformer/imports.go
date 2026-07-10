package transformer

import (
	"fmt"
	"go/ast"
	"go/token"
	"sort"
	"strings"

	"martianoff/gala/galaerr"
	"martianoff/gala/internal/transpiler"
)

// ImportManager provides a unified API for import tracking.
// It replaces the previous three separate maps (imports, importAliases, reverseImportAliases)
// and dotImports slice with a single coherent structure.
//
// Import resolution follows these rules:
//  1. If an alias is explicitly provided (e.g., `import libalias "pkg/lib"`), use that alias
//  2. If no alias, use the package name from the import path (last path component)
//  3. Dot imports (e.g., `import . "pkg/lib"`) make symbols directly accessible
type ImportManager struct {
	entries    []*ImportEntry          // All imports in declaration order
	byAlias    map[string]*ImportEntry // Lookup by user alias (how it appears in code)
	byPath     map[string]*ImportEntry // Lookup by full import path
	byPkgName  map[string]*ImportEntry // Lookup by actual package name
	dotImports []*ImportEntry          // Dot-imported packages

	// Usage tracking for dot imports (which packages were actually referenced)
	usedDotImports map[string]bool // package names of dot imports referenced during transformation

	// Transitive import tracking (imports needed by type inference that weren't
	// explicitly declared in the source file, e.g., when a lambda parameter type
	// references a package from a dependency's method signature)
	transitiveImports map[string]string // path -> alias
}

// ImportEntry represents a single import declaration.
type ImportEntry struct {
	Path    string // Full import path: "martianoff/gala/std"
	PkgName string // Actual package name: "std" (may differ from path's last component)
	Alias   string // User alias in code, or same as PkgName if no explicit alias
	IsDot   bool   // True for dot imports (import . "pkg")
}

// NewImportManager creates a new empty ImportManager.
func NewImportManager() *ImportManager {
	return &ImportManager{
		entries:           make([]*ImportEntry, 0),
		byAlias:           make(map[string]*ImportEntry),
		byPath:            make(map[string]*ImportEntry),
		byPkgName:         make(map[string]*ImportEntry),
		dotImports:        make([]*ImportEntry, 0),
		usedDotImports:    make(map[string]bool),
		transitiveImports: make(map[string]string),
	}
}

// Add registers an import. If actualPkgName is empty, it defaults to the last
// component of the path. If alias is empty, it defaults to actualPkgName.
// If an import with the same path already exists, it will be replaced.
// Returns the created ImportEntry.
func (m *ImportManager) Add(path, alias string, isDot bool, actualPkgName string) *ImportEntry {
	// Derive package name from path if not provided
	if actualPkgName == "" {
		parts := strings.Split(path, "/")
		actualPkgName = parts[len(parts)-1]
	}

	// Use package name as alias if no explicit alias
	effectiveAlias := alias
	if effectiveAlias == "" {
		effectiveAlias = actualPkgName
	}

	// Remove existing entry for this path if present
	if existing, ok := m.byPath[path]; ok {
		m.removeEntry(existing)
	}

	entry := &ImportEntry{
		Path:    path,
		PkgName: actualPkgName,
		Alias:   effectiveAlias,
		IsDot:   isDot,
	}

	m.entries = append(m.entries, entry)
	m.byPath[path] = entry

	if isDot {
		m.dotImports = append(m.dotImports, entry)
		// For dot imports, also index by package name for lookups
		m.byPkgName[actualPkgName] = entry
	} else {
		m.byAlias[effectiveAlias] = entry
		m.byPkgName[actualPkgName] = entry
	}

	return entry
}

// removeEntry removes an entry from all indexes.
func (m *ImportManager) removeEntry(entry *ImportEntry) {
	// Remove from entries slice
	for i, e := range m.entries {
		if e == entry {
			m.entries = append(m.entries[:i], m.entries[i+1:]...)
			break
		}
	}

	// Remove from byPath
	if e, ok := m.byPath[entry.Path]; ok && e == entry {
		delete(m.byPath, entry.Path)
	}

	// Remove from byAlias
	if e, ok := m.byAlias[entry.Alias]; ok && e == entry {
		delete(m.byAlias, entry.Alias)
	}

	// Remove from byPkgName
	if e, ok := m.byPkgName[entry.PkgName]; ok && e == entry {
		delete(m.byPkgName, entry.PkgName)
	}

	// Remove from dotImports
	if entry.IsDot {
		for i, e := range m.dotImports {
			if e == entry {
				m.dotImports = append(m.dotImports[:i], m.dotImports[i+1:]...)
				break
			}
		}
	}
}

// AddFromPackages populates imports from a richAST.Packages map (path -> pkgName).
// This is used for implicit imports like std.
func (m *ImportManager) AddFromPackages(packages map[string]string) {
	for path, pkgName := range packages {
		// Skip if already imported (explicit import takes precedence)
		if _, exists := m.byPath[path]; exists {
			continue
		}
		m.Add(path, pkgName, false, pkgName)
	}
}

// UpdateActualPackageName updates an import entry's actual package name.
// This is called when we learn the real package name from richAST.Packages
// after initially parsing the import declaration.
func (m *ImportManager) UpdateActualPackageName(path, actualPkgName string) {
	entry, ok := m.byPath[path]
	if !ok {
		return
	}

	oldPkgName := entry.PkgName
	entry.PkgName = actualPkgName

	// Update byPkgName index
	if oldPkgName != actualPkgName {
		// Remove old entry if it points to this import
		if existing, ok := m.byPkgName[oldPkgName]; ok && existing == entry {
			delete(m.byPkgName, oldPkgName)
		}
		// Add new entry
		m.byPkgName[actualPkgName] = entry
	}
}

// IsPackage checks if an identifier refers to an imported package.
// Returns true if the name is a known package alias.
func (m *ImportManager) IsPackage(name string) bool {
	_, ok := m.byAlias[name]
	return ok
}

// GetByAlias returns the import entry for a given alias.
func (m *ImportManager) GetByAlias(alias string) (*ImportEntry, bool) {
	entry, ok := m.byAlias[alias]
	return entry, ok
}

// GetByPath returns the import entry for a given import path.
func (m *ImportManager) GetByPath(path string) (*ImportEntry, bool) {
	entry, ok := m.byPath[path]
	return entry, ok
}

// GetByPkgName returns the import entry for a given actual package name.
func (m *ImportManager) GetByPkgName(pkgName string) (*ImportEntry, bool) {
	entry, ok := m.byPkgName[pkgName]
	return entry, ok
}

// ResolveAlias returns the actual package name for an alias.
// For example, if code has `import libalias "pkg/lib"`, then
// ResolveAlias("libalias") returns ("lib", true).
func (m *ImportManager) ResolveAlias(alias string) (string, bool) {
	entry, ok := m.byAlias[alias]
	if !ok {
		return "", false
	}
	return entry.PkgName, true
}

// GetAlias returns the alias to use for a package in generated code.
// For example, if code has `import libalias "pkg/lib"`, then
// GetAlias("lib") returns "libalias".
// Returns the package name itself if no explicit alias exists.
func (m *ImportManager) GetAlias(pkgName string) (string, bool) {
	entry, ok := m.byPkgName[pkgName]
	if !ok {
		return "", false
	}
	return entry.Alias, true
}

// IsDotImported checks if a package is dot-imported.
func (m *ImportManager) IsDotImported(pkgName string) bool {
	for _, entry := range m.dotImports {
		if entry.PkgName == pkgName {
			return true
		}
	}
	return false
}

// GetDotImports returns all dot-imported package names.
func (m *ImportManager) GetDotImports() []string {
	result := make([]string, len(m.dotImports))
	for i, entry := range m.dotImports {
		result[i] = entry.PkgName
	}
	return result
}

// GetPath returns the import path for a package alias or name.
func (m *ImportManager) GetPath(aliasOrPkgName string) (string, bool) {
	// Try alias first
	if entry, ok := m.byAlias[aliasOrPkgName]; ok {
		return entry.Path, true
	}
	// Then try package name
	if entry, ok := m.byPkgName[aliasOrPkgName]; ok {
		return entry.Path, true
	}
	// Finally, transitive imports discovered by type inference (path -> alias).
	// These aren't explicitly declared in source but are needed when generated
	// code references a dependency's types (e.g. io/fs, pulled in via os.ReadDir's
	// []fs.DirEntry return). Reverse-match on the alias/package name so a type
	// round-tripping through the AST can recover its import path.
	for path, alias := range m.transitiveImports {
		if alias == aliasOrPkgName {
			return path, true
		}
	}
	return "", false
}

// All returns all import entries in declaration order.
func (m *ImportManager) All() []*ImportEntry {
	return m.entries
}

// ForEachImport iterates over all non-dot imports, calling fn with (alias, actualPkgName).
func (m *ImportManager) ForEachImport(fn func(alias, actualPkgName string)) {
	for _, entry := range m.entries {
		if !entry.IsDot {
			fn(entry.Alias, entry.PkgName)
		}
	}
}

// ForEachDotImport iterates over all dot imports, calling fn with the package name.
func (m *ImportManager) ForEachDotImport(fn func(pkgName string)) {
	for _, entry := range m.dotImports {
		fn(entry.PkgName)
	}
}

// MarkDotImportUsed records that a symbol from a dot-imported package was referenced
// during transformation. This is used by PruneUnused to decide which dot imports to keep.
func (m *ImportManager) MarkDotImportUsed(pkgName string) {
	m.usedDotImports[pkgName] = true
}

// AddTransitive records a transitive import needed by type inference.
// These are imports not explicitly declared in the source file but required because
// generated code references types/packages from dependencies.
func (m *ImportManager) AddTransitive(path, alias string) {
	m.transitiveImports[path] = alias
}

// GetTransitiveImports returns all transitive imports (path -> alias).
func (m *ImportManager) GetTransitiveImports() map[string]string {
	return m.transitiveImports
}

// PruneUnused walks the AST file and removes any import that is not referenced by
// a SelectorExpr (qualified reference), a used dot import, or a blank import.
// Dot imports are only kept if their package was marked as used via MarkDotImportUsed
// or if the AST contains identifiers matching the package's exported symbols.
// The std dot import is always kept.
func (m *ImportManager) PruneUnused(file *ast.File, richAST *transpiler.RichAST) {
	usedPkgs := make(map[string]bool)
	ast.Inspect(file, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if ident, ok := sel.X.(*ast.Ident); ok {
			usedPkgs[ident.Name] = true
		}
		return true
	})

	for _, decl := range file.Decls {
		genDecl, ok := decl.(*ast.GenDecl)
		if !ok || genDecl.Tok != token.IMPORT {
			continue
		}
		var kept []ast.Spec
		for _, spec := range genDecl.Specs {
			importSpec, ok := spec.(*ast.ImportSpec)
			if !ok {
				kept = append(kept, spec)
				continue
			}
			if importSpec.Name != nil && importSpec.Name.Name == "_" {
				kept = append(kept, spec)
				continue
			}
			if importSpec.Name != nil && importSpec.Name.Name == "." {
				// Only keep dot imports that are actually used.
				// Always keep std (implicitly used everywhere).
				path := strings.Trim(importSpec.Path.Value, "\"")
				pkgName := ""
				if entry, ok := m.GetByPath(path); ok && entry != nil {
					pkgName = entry.PkgName
				}
				if pkgName == "" {
					parts := strings.Split(path, "/")
					pkgName = parts[len(parts)-1]
				}
				if pkgName == "std" || m.usedDotImports[pkgName] || m.dotImportUsedInAST(file, pkgName, richAST) {
					kept = append(kept, spec)
				}
				continue
			}
			// Determine the local name used in code for this import
			localName := ""
			if importSpec.Name != nil {
				localName = importSpec.Name.Name
			} else {
				path := strings.Trim(importSpec.Path.Value, "\"")
				// Check ImportManager for the actual package name (may differ from path)
				if entry, ok := m.GetByPath(path); ok && entry != nil {
					localName = entry.PkgName
				}
				if localName == "" {
					parts := strings.Split(path, "/")
					localName = parts[len(parts)-1]
				}
			}
			if usedPkgs[localName] {
				kept = append(kept, spec)
			}
		}
		genDecl.Specs = kept
	}

	var keptDecls []ast.Decl
	for _, decl := range file.Decls {
		genDecl, ok := decl.(*ast.GenDecl)
		if ok && genDecl.Tok == token.IMPORT && len(genDecl.Specs) == 0 {
			continue
		}
		keptDecls = append(keptDecls, decl)
	}
	file.Decls = keptDecls
}

// dotImportUsedInAST checks if the generated AST contains unqualified identifiers
// matching exported symbols from the given dot-imported package. This catches
// function calls and other references not tracked by MarkDotImportUsed.
func (m *ImportManager) dotImportUsedInAST(file *ast.File, pkgName string, richAST *transpiler.RichAST) bool {
	if richAST == nil {
		return true // be conservative if no metadata available
	}
	// Collect known exports from this package
	exports := make(map[string]bool)
	for _, meta := range richAST.Types {
		if meta.Package == pkgName {
			exports[meta.Name] = true
		}
	}
	for _, meta := range richAST.Functions {
		if meta.Package == pkgName {
			exports[meta.Name] = true
		}
	}
	for _, meta := range richAST.CompanionObjects {
		if meta.Package == pkgName {
			exports[meta.Name] = true
		}
	}
	if goExports, ok := richAST.GoExports[pkgName]; ok {
		for _, sym := range goExports {
			exports[sym] = true
		}
	}
	if len(exports) == 0 {
		return true // conservatively keep — can't determine if symbols are used
	}
	// Scan AST for matching unqualified identifiers. Walk through generic
	// instantiation wrappers (IndexExpr for `Foo[A]`, IndexListExpr for
	// `Foo[A, B]`) so that files referencing only generic types from the
	// dot-imported package still anchor the import.
	found := false
	ast.Inspect(file, func(n ast.Node) bool {
		if found {
			return false
		}
		expr, ok := n.(ast.Expr)
		if !ok {
			return true
		}
		if ident := unwrapToBaseIdent(expr); ident != nil && exports[ident.Name] {
			found = true
		}
		return !found
	})
	return found
}

// ValidateDotImports detects when multiple dot-imported packages export symbols with the
// same name, which would cause Go compilation errors ("redeclared in this block").
// line and col provide the source position of the import block for error reporting.
// Returns a SemanticError listing all clashing symbols, or nil if no clashes found.
func (m *ImportManager) ValidateDotImports(richAST *transpiler.RichAST, line, col int) error {
	dotPkgs := m.GetDotImports()
	if len(dotPkgs) < 2 {
		return nil // need at least 2 dot imports for a clash
	}

	dotPkgSet := make(map[string]bool, len(dotPkgs))
	for _, pkg := range dotPkgs {
		dotPkgSet[pkg] = true
	}

	// Collect symbol -> set of source packages
	symbolSources := make(map[string]map[string]bool) // symbol name -> {pkg1, pkg2, ...}

	// Check GALA-analyzed metadata (Types, Functions, CompanionObjects)
	for _, meta := range richAST.Types {
		if meta.Package != "" && dotPkgSet[meta.Package] {
			if symbolSources[meta.Name] == nil {
				symbolSources[meta.Name] = make(map[string]bool)
			}
			symbolSources[meta.Name][meta.Package] = true
		}
	}

	for _, meta := range richAST.Functions {
		if meta.Package != "" && dotPkgSet[meta.Package] {
			if symbolSources[meta.Name] == nil {
				symbolSources[meta.Name] = make(map[string]bool)
			}
			symbolSources[meta.Name][meta.Package] = true
		}
	}

	for _, meta := range richAST.CompanionObjects {
		if meta.Package != "" && dotPkgSet[meta.Package] {
			if symbolSources[meta.Name] == nil {
				symbolSources[meta.Name] = make(map[string]bool)
			}
			symbolSources[meta.Name][meta.Package] = true
		}
	}

	// Check Go-only package exports (from GoExports field)
	for pkg, symbols := range richAST.GoExports {
		if !dotPkgSet[pkg] {
			continue
		}
		for _, sym := range symbols {
			if symbolSources[sym] == nil {
				symbolSources[sym] = make(map[string]bool)
			}
			symbolSources[sym][pkg] = true
		}
	}

	// Collect clashes
	var clashes []string
	// Sort symbol names for deterministic output
	symbolNames := make([]string, 0, len(symbolSources))
	for symbol := range symbolSources {
		symbolNames = append(symbolNames, symbol)
	}
	sort.Strings(symbolNames)

	for _, symbol := range symbolNames {
		sources := symbolSources[symbol]
		if len(sources) > 1 {
			pkgs := make([]string, 0, len(sources))
			for pkg := range sources {
				pkgs = append(pkgs, pkg)
			}
			sort.Strings(pkgs)
			clashes = append(clashes, fmt.Sprintf("  - symbol %q is exported by multiple dot-imported packages: %s", symbol, strings.Join(pkgs, ", ")))
		}
	}

	if len(clashes) > 0 {
		msg := "dot-import symbol collision(s) detected:\n" + strings.Join(clashes, "\n") +
			"\nUse a qualified or aliased import for one of the packages to resolve the conflict." +
			"\n(Some stdlib packages intentionally re-export names from another package as a convenience facade —" +
			" e.g. `concurrent` re-exports `go_interop`'s execution-context helpers — so dot-importing both is never" +
			" meaningful. Pick the facade you want.)"
		return galaerr.NewCodedSemanticError(
			galaerr.CodeDotImportCollision,
			line, col, msg,
			"qualify or alias one of the dot-imports to disambiguate",
		)
	}
	return nil
}

// AddTransitiveImportsToFile adds all transitive imports to the AST file,
// skipping any that are already present. This consolidates the logic that was
// previously inline in transformer.Transform.
func (m *ImportManager) AddTransitiveImportsToFile(file *ast.File) {
	for path, alias := range m.transitiveImports {
		// Skip if already imported explicitly
		alreadyImported := false
		for _, decl := range file.Decls {
			genDecl, ok := decl.(*ast.GenDecl)
			if !ok || genDecl.Tok != token.IMPORT {
				continue
			}
			for _, spec := range genDecl.Specs {
				importSpec, ok := spec.(*ast.ImportSpec)
				if !ok {
					continue
				}
				importPath := strings.Trim(importSpec.Path.Value, "\"")
				if importPath == path {
					alreadyImported = true
					break
				}
				// Also check by alias — if the package is imported under a different path
				if importSpec.Name != nil && importSpec.Name.Name == alias {
					alreadyImported = true
					break
				}
			}
			if alreadyImported {
				break
			}
		}
		if !alreadyImported {
			spec := &ast.ImportSpec{
				Path: &ast.BasicLit{
					Kind:  token.STRING,
					Value: fmt.Sprintf("\"%s\"", path),
				},
			}
			importDecl := &ast.GenDecl{
				Tok:   token.IMPORT,
				Specs: []ast.Spec{spec},
			}
			file.Decls = append([]ast.Decl{importDecl}, file.Decls...)
		}
	}
}
