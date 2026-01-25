package transformer

import (
	"strings"
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
		entries:    make([]*ImportEntry, 0),
		byAlias:    make(map[string]*ImportEntry),
		byPath:     make(map[string]*ImportEntry),
		byPkgName:  make(map[string]*ImportEntry),
		dotImports: make([]*ImportEntry, 0),
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
