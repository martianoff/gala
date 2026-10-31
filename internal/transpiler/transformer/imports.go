package transformer

import (
	"fmt"
	"go/ast"
	"go/token"
	"maps"
	"slices"
	"sort"
	"strconv"
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
//  2. If no alias, use the package's real name when known; otherwise every name the
//     path may bind (transpiler.ImportNames), below any surer binding
//  3. Dot imports (e.g., `import . "pkg/lib"`) make symbols directly accessible
type ImportManager struct {
	entries    []*ImportEntry          // All imports in declaration order (but see All)
	byAlias    map[string]*ImportEntry // Lookup by user alias (how it appears in code)
	aliasRank  map[string]int          // How sure each byAlias binding is (transpiler.Rank*)
	byPath     map[string]*ImportEntry // Lookup by full import path
	byPkgName  map[string]*ImportEntry // Lookup by actual package name
	dotImports []*ImportEntry          // Dot-imported packages

	// Usage tracking for dot imports (which packages were actually referenced)
	usedDotImports map[string]bool // package names of dot imports referenced during transformation

	// Transitive import tracking (imports needed by type inference that weren't
	// explicitly declared in the source file, e.g., when a lambda parameter type
	// references a package from a dependency's method signature)
	transitiveImports map[string]string // path -> alias

	// foreign holds the qualifiers of the file a default being lowered was
	// declared in (see WithForeignQualifiers); nil otherwise.
	foreign map[string]*ImportEntry

	// revision changes whenever the entry set, or a field of an existing
	// entry, changes. It is what lets a consumer that copies entries out of
	// this manager notice that its copy went stale: see Revision.
	revision uint64
}

// ImportEntry represents a single import declaration.
type ImportEntry struct {
	Path    string // Full import path: "martianoff/gala/std"
	PkgName string // Actual package name: "std" (may differ from path's last component)
	Alias   string // User alias in code, or same as PkgName if no explicit alias
	IsDot   bool   // True for dot imports (import . "pkg")

	// implicit marks an entry seeded by AddFromPackages rather than written in
	// the source. An explicit import of the same path replaces it; two
	// explicit imports of one path under different names both stay.
	implicit bool
	// named marks an import written with an alias.
	named bool
}

// Implicit reports whether the entry was seeded from the package's metadata
// (a GALA package this file knows only through a sibling) rather than written
// in this file.
func (e *ImportEntry) Implicit() bool { return e.implicit }

// QualifierFor is the name generated code uses for this import: its alias
// when one was written, otherwise pkgName — the package's real name, which an
// unaliased Go import binds — falling back to the name guessed from the path.
func (e *ImportEntry) QualifierFor(pkgName string) string {
	if e.named || pkgName == "" {
		return e.Alias
	}
	return pkgName
}

// NewImportManager creates a new empty ImportManager.
func NewImportManager() *ImportManager {
	return &ImportManager{
		entries:           make([]*ImportEntry, 0),
		byAlias:           make(map[string]*ImportEntry),
		aliasRank:         make(map[string]int),
		byPath:            make(map[string]*ImportEntry),
		byPkgName:         make(map[string]*ImportEntry),
		dotImports:        make([]*ImportEntry, 0),
		usedDotImports:    make(map[string]bool),
		transitiveImports: make(map[string]string),
	}
}

// Revision returns a token that changes whenever the imports this manager
// resolves against have changed: an entry added or removed, or an entry
// renamed. A consumer that caches a copy of the entries compares this before
// using its copy, and so stays correct without being told when they change.
//
// It is a change token rather than a count — Add removes and re-adds an entry
// when a path repeats, so one Add can move it twice. Only the non-zero matters.
func (m *ImportManager) Revision() uint64 { return m.revision }

// Add registers an import. actualPkgName is the package's real name, empty
// when unknown; the entry's PkgName then defaults to the name Go assumes from
// the path (transpiler.AssumedPackageName), and every name the import may
// bind resolves to it through GetByAlias, below any surer binding. If alias is
// empty, the entry's Alias is the package name.
//
// An existing entry for the same path is replaced when it was only implicit
// (see AddFromPackages) or binds the same name. A second explicit import of
// the path under a different name — Go allows `import "strings"` next to
// `import gostr "strings"` — is kept alongside the first, so both names
// resolve; GetByPath keeps returning the first.
// Returns the created ImportEntry.
func (m *ImportManager) Add(path, alias string, isDot bool, actualPkgName string) *ImportEntry {
	return m.add(path, alias, isDot, actualPkgName, false)
}

func (m *ImportManager) add(path, alias string, isDot bool, actualPkgName string, implicit bool) *ImportEntry {
	m.revision++

	// The names the import binds in source, ranked. An implicit entry binds
	// its package name below anything this file wrote.
	names := []transpiler.ImportName{{Name: alias, Rank: 0}}
	if !implicit {
		names = transpiler.ImportNames(path, alias, actualPkgName)
	}
	// Derive package name from path if not provided
	if actualPkgName == "" {
		actualPkgName = transpiler.AssumedPackageName(path)
	}

	// Use package name as alias if no explicit alias
	effectiveAlias := alias
	if effectiveAlias == "" {
		effectiveAlias = actualPkgName
	}

	existing, hasExisting := m.byPath[path]
	if hasExisting && (existing.implicit || (existing.Alias == effectiveAlias && existing.IsDot == isDot)) {
		m.removeEntry(existing)
		hasExisting = false
	}

	entry := &ImportEntry{
		Path:     path,
		PkgName:  actualPkgName,
		Alias:    effectiveAlias,
		IsDot:    isDot,
		implicit: implicit,
		named:    alias != "" && !implicit,
	}

	m.entries = append(m.entries, entry)
	if !hasExisting {
		m.byPath[path] = entry
	}

	if isDot {
		m.dotImports = append(m.dotImports, entry)
		// For dot imports, also index by package name for lookups
		m.byPkgName[actualPkgName] = entry
	} else {
		for _, n := range names {
			m.bindAlias(n.Name, entry, n.Rank)
		}
		m.byPkgName[actualPkgName] = entry
	}

	return entry
}

// bindAlias points name at entry unless a surer binding holds it; on a tie
// the later import wins.
func (m *ImportManager) bindAlias(name string, entry *ImportEntry, rank int) {
	if name == "" || name == "_" {
		return
	}
	if _, taken := m.byAlias[name]; taken && m.aliasRank[name] > rank {
		return
	}
	m.byAlias[name] = entry
	m.aliasRank[name] = rank
}

// removeEntry removes an entry from all indexes.
func (m *ImportManager) removeEntry(entry *ImportEntry) {
	m.revision++

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

	// Remove from byAlias — every name the entry was bound under
	for name, e := range m.byAlias {
		if e == entry {
			delete(m.byAlias, name)
			delete(m.aliasRank, name)
		}
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
		// A keyed package (see transpiler.PackageKey) is named by its key
		// in metadata, and by its own name in the generated Go.
		m.add(path, transpiler.PackageKeyName(pkgName, path), false, pkgName, true)
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
		m.revision++
		// Remove old entry if it points to this import
		if existing, ok := m.byPkgName[oldPkgName]; ok && existing == entry {
			delete(m.byPkgName, oldPkgName)
		}
		// Add new entry
		m.byPkgName[actualPkgName] = entry
	}
}

// ClaimGalaPackageNames settles which import owns each GALA package name in
// the package-name index that Qualifier reads.
//
// GALA ships packages whose names collide with Go stdlib ones (strings, io,
// json, path, fs, crypto, regex), and GALA metadata names a package by NAME
// ("strings.Str", "strings.Str_Fold"), so a lookup turns that name back into
// this file's qualifier. When a Go import shares the name — Go `strings`
// beside `gs "martianoff/gala/strings"` — whichever was declared last used to
// win, so a GALA symbol could be emitted as `strings.Str_Fold` against Go's
// package. Here the GALA import always wins: the last explicit non-dot import of the path, or,
// when this file does not import the package itself (it reached the file
// through a sibling's imports), the implicit entry. An implicit entry whose
// name is already bound by one of this file's imports is renamed to a free
// qualifier, so the transitive import added for it cannot collide.
//
// isGala reports whether an import path is a GALA package. Must run after the
// file's import declarations and UpdateActualPackageName calls.
func (m *ImportManager) ClaimGalaPackageNames(isGala map[string]bool) {
	owners := make(map[string]*ImportEntry)
	for _, e := range m.entries { // declaration order; implicit entries were seeded first
		if e.IsDot || !isGala[e.Path] {
			continue
		}
		if cur, ok := owners[e.PkgName]; ok && !cur.implicit && e.implicit {
			continue
		}
		owners[e.PkgName] = e
	}
	names := make([]string, 0, len(owners))
	for name := range owners {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		e := owners[name]
		if e.implicit {
			if other, taken := m.byAlias[e.Alias]; taken && other != e {
				m.renameAlias(e, m.freeAlias("gala_"+e.PkgName))
			}
		}
		if m.byPkgName[name] != e {
			m.byPkgName[name] = e
			m.revision++
		}
	}
}

// renameAlias moves entry to a new qualifier in the alias index.
func (m *ImportManager) renameAlias(entry *ImportEntry, alias string) {
	m.revision++
	if cur, ok := m.byAlias[entry.Alias]; ok && cur == entry {
		delete(m.byAlias, entry.Alias)
		delete(m.aliasRank, entry.Alias)
	}
	entry.Alias = alias
	entry.named = true
	m.bindAlias(alias, entry, transpiler.RankAlias)
}

// freeAlias returns base, or base with a numeric suffix, whichever neither an
// import of this file nor a transitive import binds yet.
func (m *ImportManager) freeAlias(base string) string {
	taken := func(alias string) bool {
		if _, ok := m.byAlias[alias]; ok {
			return true
		}
		for _, a := range m.transitiveImports {
			if a == alias {
				return true
			}
		}
		return false
	}
	alias := base
	for i := 2; taken(alias); i++ {
		alias = fmt.Sprintf("%s%d", base, i)
	}
	return alias
}

// TransitiveQualifier picks the qualifier for a package this file does not
// import but generated code references: the one already chosen for the path,
// else a free alias based on pkgName.
func (m *ImportManager) TransitiveQualifier(path, pkgName string) string {
	if alias, ok := m.transitiveImports[path]; ok {
		return alias
	}
	return m.freeAlias(pkgName)
}

// Qualifier returns the import through which this file refers to the package
// named pkgName (see ClaimGalaPackageNames): generated code qualifies with its
// Alias and needs its Path imported.
func (m *ImportManager) Qualifier(pkgName string) (*ImportEntry, bool) {
	entry, ok := m.byPkgName[pkgName]
	return entry, ok
}

// IsPackage checks if an identifier refers to an imported package.
// Returns true if the name is a known package alias.
func (m *ImportManager) IsPackage(name string) bool {
	_, ok := m.GetByAlias(name)
	return ok
}

// GetByAlias returns the import entry for a given alias. While a default
// declared in another file is lowered, the qualifiers that file binds come
// first (see WithForeignQualifiers).
func (m *ImportManager) GetByAlias(alias string) (*ImportEntry, bool) {
	if entry, ok := m.foreign[alias]; ok {
		return entry, true
	}
	entry, ok := m.byAlias[alias]
	return entry, ok
}

// WithForeignQualifiers puts the imports of the file a default was written in
// in scope until the returned function is called: each qualifier of imports
// (qualifier → import path) names its path, and each package of dots (import
// path → package name) is dot-imported ahead of this file's own dot imports.
// A default lowered here was written against its own file's imports, which
// this file may lack or bind otherwise; a bare name means there what that
// file's dot imports make it mean, so they are tried first even where this
// file dot-imports another package declaring the name too. Only how source
// names resolve is affected; the generated code refers to packages through
// this file's own names (see PathForQualifier), which the caller spells once
// the default is lowered.
func (m *ImportManager) WithForeignQualifiers(imports, dots map[string]string) (restore func()) {
	prev := m.foreign
	m.foreign = make(map[string]*ImportEntry, len(prev)+len(imports))
	for q, e := range prev {
		m.foreign[q] = e
	}
	for q, path := range imports {
		m.foreign[q] = &ImportEntry{Path: path, PkgName: q, Alias: q, named: true}
	}
	// front is the foreign file's dot imports, in a stable resolution order:
	// the entry this file already has for a path (which may sit behind
	// another of its dot imports), else a new one, added for the duration.
	var front, added []*ImportEntry
	for _, path := range slices.Sorted(maps.Keys(dots)) {
		if entry := m.dotImportByPath(path); entry != nil {
			front = append(front, entry)
			continue
		}
		pkgName := dots[path]
		entry := &ImportEntry{Path: path, PkgName: pkgName, Alias: pkgName, IsDot: true, implicit: true}
		front = append(front, entry)
		added = append(added, entry)
	}
	prevEntries, prevDots := m.entries, m.dotImports
	dotImports := slices.Concat(front, slices.DeleteFunc(slices.Clone(prevDots), func(e *ImportEntry) bool { return slices.Contains(front, e) }))
	if len(added) == 0 && slices.Equal(dotImports, prevDots) {
		// Already in that order: nothing to change, nor to rebuild from.
		return func() { m.foreign = prev }
	}
	// Only the entry set is revision-tracked; the named qualifiers live in
	// foreign, outside it.
	m.entries, m.dotImports = reorderDotEntries(prevEntries, prevDots, dotImports, added), dotImports
	m.revision++
	reordered := m.revision
	return func() {
		m.foreign = prev
		if m.revision == reordered {
			// No import was added or dropped meanwhile: the old slices,
			// which the reordering copied rather than changed, still hold.
			m.entries, m.dotImports = prevEntries, prevDots
		} else {
			m.entries = restoreImportOrder(m.entries, prevEntries, added)
			m.dotImports = restoreImportOrder(m.dotImports, prevDots, added)
		}
		m.revision++
	}
}

// reorderDotEntries returns a copy of entries, whose dot imports are dots,
// with them in the order of reordered instead: the entries of added (in
// reordered, not in dots) first, then entries with each slot a dot import
// held taken by the next of reordered's others. Every other entry keeps its
// place.
func reorderDotEntries(entries, dots, reordered, added []*ImportEntry) []*ImportEntry {
	next := slices.DeleteFunc(slices.Clone(reordered), func(e *ImportEntry) bool { return slices.Contains(added, e) })
	out := slices.Concat(added, entries)
	for i := len(added); i < len(out); i++ {
		if len(next) > 0 && slices.Contains(dots, out[i]) {
			out[i], next = next[0], next[1:]
		}
	}
	return out
}

// restoreImportOrder undoes a reordering of prev into cur, which an import
// may have been added to or dropped from since: the result holds
// the entries of cur not in drop, those that prev holds in prev's order, then
// those added since in cur's order.
func restoreImportOrder(cur, prev, drop []*ImportEntry) []*ImportEntry {
	kept := func(e *ImportEntry) bool { return slices.Contains(cur, e) && !slices.Contains(drop, e) }
	out := slices.DeleteFunc(slices.Clone(prev), func(e *ImportEntry) bool { return !kept(e) })
	for _, e := range cur {
		if !slices.Contains(prev, e) && !slices.Contains(drop, e) {
			out = append(out, e)
		}
	}
	return out
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
	entry, ok := m.GetByAlias(alias)
	if !ok {
		return "", false
	}
	return entry.PkgName, true
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

// IsDotImportedPath reports whether the package at path is dot-imported.
func (m *ImportManager) IsDotImportedPath(path string) bool {
	return m.dotImportByPath(path) != nil
}

// dotImportByPath returns the dot import of the package at path, nil when
// there is none.
func (m *ImportManager) dotImportByPath(path string) *ImportEntry {
	if i := slices.IndexFunc(m.dotImports, func(e *ImportEntry) bool { return e.Path == path }); i >= 0 {
		return m.dotImports[i]
	}
	return nil
}

// GetDotImports returns all dot-imported package names.
func (m *ImportManager) GetDotImports() []string {
	result := make([]string, len(m.dotImports))
	for i, entry := range m.dotImports {
		result[i] = entry.PkgName
	}
	return result
}

// PathForQualifier returns the import path behind a qualifier as it appears
// in generated code: one of this file's imports, or a transitive import
// discovered by type inference (e.g. io/fs, pulled in via os.ReadDir's
// []fs.DirEntry return), so a type round-tripping through the AST can recover
// its import path.
func (m *ImportManager) PathForQualifier(qualifier string) (string, bool) {
	if entry, ok := m.byAlias[qualifier]; ok {
		return entry.Path, true
	}
	for path, alias := range m.transitiveImports {
		if alias == qualifier {
			return path, true
		}
	}
	return "", false
}

// All returns all import entries in declaration order, except that while a
// default written in another file is lowered, the dot imports are in the
// order they resolve in: that file's first (see WithForeignQualifiers).
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

// snapshotUsage records which dot imports are marked used and which
// transitive imports are recorded, and returns a function that restores both.
// It lets a caller generate code it may throw away without leaving the file
// importing what the discarded code referenced.
func (m *ImportManager) snapshotUsage() func() {
	used := maps.Clone(m.usedDotImports)
	transitive := maps.Clone(m.transitiveImports)
	return func() {
		m.usedDotImports = used
		m.transitiveImports = transitive
	}
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
//
// It rewrites the generated file only. The manager keeps every entry it was
// given, including the ones just dropped from the output, so pruning leaves
// Revision — and anything cached against it — untouched.
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
				// Only keep dot imports that are actually used. Every package,
				// std included, is judged the same way: Go rejects an unused
				// dot import like any other.
				path := strings.Trim(importSpec.Path.Value, "\"")
				pkgName := ""
				if entry, ok := m.GetByPath(path); ok && entry != nil {
					pkgName = entry.PkgName
				}
				if pkgName == "" {
					parts := strings.Split(path, "/")
					pkgName = parts[len(parts)-1]
				}
				if m.usedDotImports[pkgName] || m.dotImportUsedInAST(file, path, pkgName, richAST) {
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
				if richAST != nil {
					if key := richAST.PackageKeys[path]; key != "" {
						localName = transpiler.PackageKeyName(key, path) // a key names the metadata, not the import
					}
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
//
// Only identifiers in a referring position count: the selected name of a
// qualified reference (`fmt.Println`), a struct field or a declared name is not
// a reference to the dot-imported package, even when it is spelled like one of
// its exports.
func (m *ImportManager) dotImportUsedInAST(file *ast.File, path, pkgName string, richAST *transpiler.RichAST) bool {
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
	for name := range richAST.ImportedVals[path] {
		exports[name] = true
	}
	// A Go package (the SDK's, or a module dependency's) is known through its
	// type info, keyed by import path and, when no GALA package of that name
	// exported anything, by package name: a GALA `strings` is not Go's.
	if gi := richAST.GoTypeInfo; gi != nil {
		prefixes := []string{path + "."}
		if len(exports) == 0 {
			prefixes = append(prefixes, pkgName+".")
		}
		for _, prefix := range prefixes {
			addGoTypeInfoNames(exports, prefix, gi.Functions)
			addGoTypeInfoNames(exports, prefix, gi.Types)
			addGoTypeInfoNames(exports, prefix, gi.Variables)
			addGoTypeInfoNames(exports, prefix, gi.Constants)
			addGoTypeInfoNames(exports, prefix, gi.TypeAliases)
		}
	}
	if len(exports) == 0 {
		return true // conservatively keep — can't determine if symbols are used
	}
	found := false
	inspectReferences(file, func(ident *ast.Ident) bool {
		// Only an exported name can refer to another package's declaration;
		// an unexported one spelled the same is a local.
		found = exports[ident.Name] && ast.IsExported(ident.Name)
		return !found
	})
	return found
}

// addGoTypeInfoNames adds to names the bare name of every key of entries that
// starts with prefix (`strings.ToUpper` -> ToUpper for prefix `strings.`).
func addGoTypeInfoNames[V any](names map[string]bool, prefix string, entries map[string]V) {
	for key := range entries {
		if name, ok := strings.CutPrefix(key, prefix); ok && name != "" && !strings.Contains(name, ".") {
			names[name] = true
		}
	}
}

// inspectReferences calls visit for every identifier of file that may refer
// to a package-level name of a dot import, until visit returns false. It
// skips the names that cannot: the package clause, import names, the selected
// name of a selector (`fmt.Println`, `x.Field`), struct field and method
// names, and the names a declaration introduces.
func inspectReferences(file *ast.File, visit func(*ast.Ident) bool) {
	done := false
	var walk func(n ast.Node) bool
	walk = func(n ast.Node) bool {
		if done || n == nil {
			return false
		}
		switch n := n.(type) {
		case *ast.Ident:
			done = !visit(n)
			return false
		case *ast.ImportSpec:
			return false
		case *ast.SelectorExpr:
			ast.Inspect(n.X, walk)
			return false
		case *ast.Field:
			// Struct fields, interface methods, parameters and results: only
			// the type refers to anything.
			ast.Inspect(n.Type, walk)
			return false
		case *ast.FuncDecl:
			if n.Recv != nil {
				ast.Inspect(n.Recv, walk)
			}
			ast.Inspect(n.Type, walk)
			if n.Body != nil {
				ast.Inspect(n.Body, walk)
			}
			return false
		case *ast.TypeSpec:
			if n.TypeParams != nil {
				ast.Inspect(n.TypeParams, walk)
			}
			ast.Inspect(n.Type, walk)
			return false
		case *ast.ValueSpec:
			if n.Type != nil {
				ast.Inspect(n.Type, walk)
			}
			for _, v := range n.Values {
				ast.Inspect(v, walk)
			}
			return false
		}
		return true
	}
	for _, decl := range file.Decls {
		ast.Inspect(decl, walk)
	}
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

// CheckImportPaths is the last gate on the imports a generated file declares:
// each must be a valid Go import path (transpiler.IsValidGoImportPath). An
// import path is recorded on types far from here — by the analyzer, from the
// Go packages it type-checks — and a filesystem path that slipped in would
// otherwise surface as unparseable Go on Windows (`"C:\Users\…"`) or as `go`
// rejecting "not a package path" elsewhere, neither naming the cause.
func CheckImportPaths(file *ast.File) error {
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
			path, err := strconv.Unquote(importSpec.Path.Value)
			if err == nil && transpiler.IsValidGoImportPath(path) {
				continue
			}
			return galaerr.NewCodedSemanticError(
				galaerr.CodeInternalTransformerPanic,
				0, 0,
				fmt.Sprintf("internal transpiler error: the generated Go would import %s, which is not a Go import path",
					importSpec.Path.Value),
				"not an error in your code: an internal transpiler defect recorded a path that is not an import path; "+
					"please file an issue at https://github.com/martianoff/gala/issues with the source that triggered it",
			)
		}
	}
	return nil
}

// AddTransitiveImportsToFile adds all transitive imports to the AST file,
// skipping any that are already present. This consolidates the logic that was
// previously inline in transformer.Transform.
//
// pathMap maps a GALA import path to its Go module path where they differ (see
// RichAST.ImportPathMap). An import whose qualifier is not the name Add would
// give it (transpiler.AssumedPackageName) is written with that qualifier as
// its name.
func (m *ImportManager) AddTransitiveImportsToFile(file *ast.File, pathMap map[string]string) {
	for galaPath, alias := range m.transitiveImports {
		path := galaPath
		if mapped, ok := pathMap[galaPath]; ok {
			path = mapped
		}
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
			if alias != "" && alias != transpiler.AssumedPackageName(path) {
				spec.Name = ast.NewIdent(alias)
			}
			importDecl := &ast.GenDecl{
				Tok:   token.IMPORT,
				Specs: []ast.Spec{spec},
			}
			file.Decls = append([]ast.Decl{importDecl}, file.Decls...)
		}
	}
}
