package analyzer

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/profiler"
)

// CacheVersion is incremented when the cache format or analysis semantics
// change. This ensures stale caches from older compiler versions are
// automatically invalidated.
//
// v2 (PR #318): switched from encoding/gob to a hand-rolled binary codec.
// Decode of the largest gala_team cache entry (~417 KB) dropped from
// 3.7 ms / 43k allocs (gob) to 1.16 ms / 24.5k allocs (binary) on a
// 14900KF — the wins compound across the ~26 cached packages an apex
// transpile rehydrates. The on-disk projection is unchanged; only the
// bytes-format differs. v1 caches sit under a separate version-keyed
// directory and are pruned by the existing stale-cache GC.
//
// v3: metadata carries doc comments (TypeMetadata.Doc/FieldDocs,
// MethodMetadata.Doc, FunctionMetadata.Doc, SealedVariant.Doc). The codec
// gained a string per declaration, so v2 payloads cannot be decoded by
// this reader.
const CacheVersion = "v3"

// CompilerVersion is set by the CLI to include the compiler version and git commit
// in the cache directory path. When the transpiler binary is upgraded, the cache path
// changes automatically, preventing stale entries from causing phantom errors.
// Format: "version-gitcommit" (e.g., "0.5.0-abc1234").
// If empty, only CacheVersion is used (backward compatible).
var CompilerVersion string

// binarySelfHash returns a short content hash of the running gala binary.
// It is used to invalidate the on-disk analysis cache automatically when the
// compiler binary changes — critical for unstamped "dev" builds (Version="dev",
// GitCommit="unknown") where CompilerVersion alone cannot distinguish between
// compiler revisions. Without this, recompiling the transpiler does not bust
// the cache, and cached metadata produced by an older analyzer can poison new
// builds (e.g., a per-package pkgAST that was serialized before transitive
// type-merge was introduced will still be loaded by a newer analyzer that
// expects the merged form, producing "no typeMeta for collection_immutable.Array"
// failures at the consumer side).
//
// Returns an empty string on any I/O failure; the caller treats the empty case
// as "no extra invalidation key", which preserves the previous behaviour.
var binarySelfHash = func() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	data, err := ioutil.ReadFile(exe)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])[:12]
}()

// CachedRichAST is the serializable subset of RichAST (no antlr.Tree).
//
// Only the package's OWN metadata is persisted on disk. Transitive
// imports are NOT inlined — instead, DirectImports lists the GALA import
// paths that the package directly references, and the analyzer is
// responsible for re-loading them (which is itself cache-served) and
// merging their metadata back into the returned RichAST after a cache
// hit. This avoids the O(N×M) blowup that occurred when every downstream
// pkgAST inlined the full transitive type closure of its imports — a
// pattern that produced multi-GB .gob files for the most-downstream
// package in a deep dependency graph.
type CachedRichAST struct {
	PackageName      string
	PackageDoc       string
	Types            map[string]*transpiler.TypeMetadata
	Functions        map[string]*transpiler.FunctionMetadata
	Packages         map[string]string
	CompanionObjects map[string]*transpiler.CompanionObjectMetadata
	GoExports        map[string][]string
	GoTypeInfo       *transpiler.GoTypeInfo
	TypeAliases      map[string]transpiler.Type
	ImportPathMap    map[string]string
	DepsHash         string   // hash of transitive dependency content (for invalidation)
	DirectImports    []string // GALA import paths this package directly imports (for re-merge on load)
}

// belongsToPkg reports whether a fully-qualified key like "pkgName.SymName"
// (or a bare "SymName" in main/test packages) belongs to the named package.
// Empty pkgName matches keys without a dot prefix (main/test packages).
func belongsToPkg(key, pkgName string) bool {
	dot := strings.IndexByte(key, '.')
	if dot < 0 {
		// Bare name — only main/test packages should keep these.
		return pkgName == "" || pkgName == "main" || pkgName == "test"
	}
	return key[:dot] == pkgName
}

// toCachedRichAST builds the on-disk projection of `r`. It strips the
// transitive type closure that Merge accumulated from imports, keeping
// only metadata that originated in this package. The resulting object
// is small and bounded by the package's own source size.
func toCachedRichAST(r *transpiler.RichAST, depsHash string, directImports []string) *CachedRichAST {
	if r == nil {
		return nil
	}
	pkg := r.PackageName

	// Filter Types to entries owned by this package.
	var ownTypes map[string]*transpiler.TypeMetadata
	if len(r.Types) > 0 {
		ownTypes = make(map[string]*transpiler.TypeMetadata)
		for k, v := range r.Types {
			if v == nil {
				continue
			}
			// Authoritative check: the metadata's own Package field.
			// Falls back to key-prefix when Package is unset (defensive;
			// some entries set Package="" for main/test packages).
			if v.Package == pkg {
				ownTypes[k] = v
			} else if v.Package == "" && belongsToPkg(k, pkg) {
				ownTypes[k] = v
			}
		}
	}

	// Filter Functions similarly.
	var ownFuncs map[string]*transpiler.FunctionMetadata
	if len(r.Functions) > 0 {
		ownFuncs = make(map[string]*transpiler.FunctionMetadata)
		for k, v := range r.Functions {
			if v == nil {
				continue
			}
			if v.Package == pkg {
				ownFuncs[k] = v
			} else if v.Package == "" && belongsToPkg(k, pkg) {
				ownFuncs[k] = v
			}
		}
	}

	// Filter CompanionObjects similarly.
	var ownCos map[string]*transpiler.CompanionObjectMetadata
	if len(r.CompanionObjects) > 0 {
		ownCos = make(map[string]*transpiler.CompanionObjectMetadata)
		for k, v := range r.CompanionObjects {
			if v == nil {
				continue
			}
			if v.Package == pkg {
				ownCos[k] = v
			} else if v.Package == "" && belongsToPkg(k, pkg) {
				ownCos[k] = v
			}
		}
	}

	// GoExports[pkg] is keyed by package name; keep only this package's row.
	// When pkg is "" (the analyzer's fallback uses relPath), we can't filter
	// reliably — but in that case the map is small enough to keep verbatim.
	var ownGoExports map[string][]string
	if len(r.GoExports) > 0 {
		if pkg != "" {
			if syms, ok := r.GoExports[pkg]; ok && len(syms) > 0 {
				ownGoExports = map[string][]string{pkg: syms}
			}
		} else {
			ownGoExports = r.GoExports
		}
	}

	// GoTypeInfo entries are keyed "pkgName.SymName" — keep only this package's.
	var ownGoTypeInfo *transpiler.GoTypeInfo
	if r.GoTypeInfo != nil {
		ownGoTypeInfo = filterGoTypeInfo(r.GoTypeInfo, pkg)
	}

	return &CachedRichAST{
		PackageName:      r.PackageName,
		PackageDoc:       r.PackageDoc,
		Types:            ownTypes,
		Functions:        ownFuncs,
		Packages:         nil, // reconstructed at load time from DirectImports
		CompanionObjects: ownCos,
		GoExports:        ownGoExports,
		GoTypeInfo:       ownGoTypeInfo,
		TypeAliases:      r.TypeAliases, // small; alias entries originate from this package's source
		ImportPathMap:    r.ImportPathMap,
		DepsHash:         depsHash,
		DirectImports:    directImports,
	}
}

// filterGoTypeInfo returns a new GoTypeInfo containing only entries whose
// "pkgName.SymName" qualified keys match the given package. Returns nil
// if no entries belong to this package.
func filterGoTypeInfo(g *transpiler.GoTypeInfo, pkg string) *transpiler.GoTypeInfo {
	if g == nil {
		return nil
	}
	prefix := pkg + "."
	out := transpiler.NewGoTypeInfo()
	any := false
	for k, v := range g.Functions {
		if strings.HasPrefix(k, prefix) {
			out.Functions[k] = v
			any = true
		}
	}
	for k, v := range g.Types {
		if strings.HasPrefix(k, prefix) {
			out.Types[k] = v
			any = true
		}
	}
	for k, v := range g.Variables {
		if strings.HasPrefix(k, prefix) {
			out.Variables[k] = v
			any = true
		}
	}
	for k, v := range g.Constants {
		if strings.HasPrefix(k, prefix) {
			out.Constants[k] = v
			any = true
		}
	}
	for k, v := range g.TypeAliases {
		if strings.HasPrefix(k, prefix) {
			out.TypeAliases[k] = v
			any = true
		}
	}
	if !any {
		return nil
	}
	return out
}

// projectOwnRichAST returns an in-memory own-only projection of `r`. It is
// the live counterpart of toCachedRichAST: produces a *RichAST (instead of
// the gob-serializable *CachedRichAST) so it can be stored in the analyzer's
// analyzedPkgs map without the transitive type closure that Merge accumulated
// from imports.
//
// Why this exists: the BatchAnalyzer keeps `analyzedPkgs[path]` for the entire
// run so std and shared deps are not re-analyzed per file. Without projection,
// each entry is the post-Merge "full closure" RichAST, which on a project
// like gala_team (16 first-party packages + ~80 transitive) blows past 16 GB
// of RSS because every package re-stores cloned (COW'd) copies of the same
// transitive types. Projection retains only the metadata that *originated* in
// this package; downstream consumers reconstruct the closure on demand by
// recursively merging direct imports' projections via mergeAnalyzedClosureAt.
//
// Tree, FilePath, SourceContent, AnalysisWarnings, EmbedDirectives, and
// ImportAliases are intentionally dropped — they belong to the per-file
// Analyze invocation, not to the package-level shared cache. PackageName,
// Types, Functions, CompanionObjects, GoExports, GoTypeInfo, TypeAliases,
// and ImportPathMap follow the same own-package filter as toCachedRichAST.
//
// Packages is set to nil; the recursive closure walker re-establishes
// path→pkgName mappings from analyzedPkgImports + analyzedPkgs[imp].PackageName.
func projectOwnRichAST(r *transpiler.RichAST) *transpiler.RichAST {
	if r == nil {
		return nil
	}
	pkg := r.PackageName

	var ownTypes map[string]*transpiler.TypeMetadata
	if len(r.Types) > 0 {
		ownTypes = make(map[string]*transpiler.TypeMetadata)
		for k, v := range r.Types {
			if v == nil {
				continue
			}
			if v.Package == pkg {
				ownTypes[k] = v
			} else if v.Package == "" && belongsToPkg(k, pkg) {
				ownTypes[k] = v
			}
		}
	}

	var ownFuncs map[string]*transpiler.FunctionMetadata
	if len(r.Functions) > 0 {
		ownFuncs = make(map[string]*transpiler.FunctionMetadata)
		for k, v := range r.Functions {
			if v == nil {
				continue
			}
			if v.Package == pkg {
				ownFuncs[k] = v
			} else if v.Package == "" && belongsToPkg(k, pkg) {
				ownFuncs[k] = v
			}
		}
	}

	var ownCos map[string]*transpiler.CompanionObjectMetadata
	if len(r.CompanionObjects) > 0 {
		ownCos = make(map[string]*transpiler.CompanionObjectMetadata)
		for k, v := range r.CompanionObjects {
			if v == nil {
				continue
			}
			if v.Package == pkg {
				ownCos[k] = v
			} else if v.Package == "" && belongsToPkg(k, pkg) {
				ownCos[k] = v
			}
		}
	}

	var ownGoExports map[string][]string
	if len(r.GoExports) > 0 {
		if pkg != "" {
			if syms, ok := r.GoExports[pkg]; ok && len(syms) > 0 {
				ownGoExports = map[string][]string{pkg: syms}
			}
		} else {
			ownGoExports = r.GoExports
		}
	}

	var ownGoTypeInfo *transpiler.GoTypeInfo
	if r.GoTypeInfo != nil {
		ownGoTypeInfo = filterGoTypeInfo(r.GoTypeInfo, pkg)
	}

	return &transpiler.RichAST{
		PackageName:      r.PackageName,
		PackageDoc:       r.PackageDoc,
		Types:            ownTypes,
		Functions:        ownFuncs,
		Packages:         nil, // re-established by mergeAnalyzedClosureAt walker
		CompanionObjects: ownCos,
		GoExports:        ownGoExports,
		GoTypeInfo:       ownGoTypeInfo,
		TypeAliases:      r.TypeAliases,
		ImportPathMap:    r.ImportPathMap,
	}
}

// extractDirectGalaImports returns the GALA import paths declared by the
// package whose RichAST is `r`. We reuse `r.Packages` because it is built
// up at line ~478 of Analyze with one entry per direct GALA import scanned
// from the source file (transitive paths are also added there via Merge,
// but those entries' values are still resolved by the time we project, and
// the recursive closure walker is idempotent under repeated visits — over-
// reporting is harmless, missing edges would lose types). When projecting
// the post-Merge form for storage in analyzedPkgs, we therefore record
// every Packages key as a "direct" import for traversal purposes.
//
// This is deliberately broader than the on-disk DirectImports list (which
// uses extractDirectImportPaths to read source). The on-disk list is the
// authoritative direct-import set used for cache invalidation and disk
// rehydration; the in-memory list is just a closure traversal map and only
// needs to cover all paths reachable via Merge so the walker doesn't miss
// types.
func extractDirectGalaImports(r *transpiler.RichAST) []string {
	if r == nil || len(r.Packages) == 0 {
		return nil
	}
	out := make([]string, 0, len(r.Packages))
	for path := range r.Packages {
		out = append(out, path)
	}
	return out
}

func fromCachedRichAST(c *CachedRichAST) *transpiler.RichAST {
	if c == nil {
		return nil
	}
	return &transpiler.RichAST{
		PackageName:      c.PackageName,
		PackageDoc:       c.PackageDoc,
		Types:            c.Types,
		Functions:        c.Functions,
		Packages:         c.Packages,
		CompanionObjects: c.CompanionObjects,
		GoExports:        c.GoExports,
		GoTypeInfo:       c.GoTypeInfo,
		TypeAliases:      c.TypeAliases,
		ImportPathMap:    c.ImportPathMap,
	}
}

// analysisCache handles disk-based caching of analyzed package metadata.
type analysisCache struct {
	dir     string // .gala/cache directory path
	enabled bool
}

// newAnalysisCache creates a cache rooted at the given project directory.
// Returns a disabled cache if the directory can't be created.
//
// The cache directory path embeds:
//   - CacheVersion: bump on serialization-format changes.
//   - CompilerVersion: when stamping is active (Version + "-" + GitCommit).
//   - binarySelfHash: short hash of the gala binary itself, so unstamped "dev"
//     builds also invalidate when the binary changes. Without this, the
//     analyzer's serialized pkgAST from an older revision (e.g. before
//     transitive-type-merge in scanImports) could outlive the binary upgrade
//     and silently feed stale metadata to a newer transformer expecting the
//     merged shape — producing failures like
//     `firstD = NewImmutable(dirs.Get().Get(0))` typed as `Array[T]` instead
//     of `T` because `collection_immutable.Array` had been pruned from the
//     cached `directive` package's metadata.
func newAnalysisCache(projectRoot string) *analysisCache {
	if projectRoot == "" {
		return &analysisCache{enabled: false}
	}
	cacheDir := CacheVersion
	if CompilerVersion != "" {
		cacheDir = CacheVersion + "-" + CompilerVersion
	}
	if binarySelfHash != "" {
		cacheDir = cacheDir + "-" + binarySelfHash
	}
	parent := filepath.Join(projectRoot, ".gala", "cache")
	dir := filepath.Join(parent, cacheDir)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return &analysisCache{enabled: false}
	}
	// Asynchronously prune sibling cache directories whose binary hash
	// differs from the current binary's. Without this, every rebuild of
	// an unstamped dev binary leaves a fresh v1-dev-<hash>/ behind, and
	// the .gala/cache/ tree grows without bound. Pruning is gated by
	// GALA_KEEP_STALE_CACHES=1 for users who want to inspect prior
	// generations; failures are silent (best-effort cleanup).
	if os.Getenv("GALA_KEEP_STALE_CACHES") != "1" {
		go pruneStaleCaches(parent, cacheDir)
	}
	return &analysisCache{dir: dir, enabled: true}
}

// pruneStaleCaches removes sibling directories under parent that look like
// analysis caches from a previous binary (v1-* / v1-<ver>-* / v1-dev-*) but
// don't match keepName. Only directories older than staleCacheMaxAge are
// removed so that concurrent compiles using a slightly different binary
// (e.g. a rolling upgrade or two parallel invocations) don't trip on each
// other. The function is intentionally conservative: it only removes
// directories whose name starts with the CacheVersion prefix and looks
// like a cache directory.
func pruneStaleCaches(parent, keepName string) {
	// Best-effort cleanup — never crash the host. We do log the
	// recovered value to stderr so a misbehaving filesystem
	// (permission errors, OS-level corruption) leaves a breadcrumb
	// instead of being silently absorbed.
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintf(os.Stderr, "gala: pruneStaleCaches recovered from panic: %v\n", r)
		}
	}()

	entries, err := ioutil.ReadDir(parent)
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-staleCacheMaxAge)
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		if name == keepName {
			continue
		}
		// Only consider directories that look like analysis caches: a version
		// token, optionally suffixed (e.g. "v1", "v2-dev-abcdef"). Matching ANY
		// version rather than only the current one is what lets a CacheVersion
		// bump reclaim the directories it just orphaned; keying on the current
		// version left every previous generation on disk forever.
		if !looksLikeCacheDir(name) {
			continue
		}
		if e.ModTime().After(cutoff) {
			continue
		}
		_ = os.RemoveAll(filepath.Join(parent, name))
	}
}

// looksLikeCacheDir reports whether a directory name is one of this analyzer's
// version-keyed cache directories: "v" followed by digits, optionally followed
// by "-" and a build/hash suffix (e.g. "v2", "v3-dev-abcdef").
//
// Deliberately matches every generation, not just the current CacheVersion. The
// prune runs beside the directory currently in use, so a version bump is exactly
// the moment its predecessors become garbage — and those entries run to hundreds
// of megabytes per project.
func looksLikeCacheDir(name string) bool {
	rest, ok := strings.CutPrefix(name, "v")
	if !ok {
		return false
	}
	digits := 0
	for digits < len(rest) && rest[digits] >= '0' && rest[digits] <= '9' {
		digits++
	}
	if digits == 0 {
		return false
	}
	rest = rest[digits:]
	return rest == "" || rest[0] == '-'
}

// staleCacheMaxAge is the age threshold beyond which a non-matching
// sibling cache directory is eligible for removal. 7 days is long
// enough that an active developer's hot caches survive normal use,
// but short enough that abandoned dev-build directories from prior
// branches don't accumulate indefinitely.
const staleCacheMaxAge = 7 * 24 * time.Hour

// Get retrieves a cached RichAST for the given package path and content hash.
// depsHash is the hash of transitive dependency content — must match for a valid hit.
// Returns (nil, nil) on cache miss / corruption / stale deps. On a hit, returns
// the deserialized own-only RichAST and the package's direct GALA import paths;
// the caller is responsible for re-loading and merging those imports.
func (c *analysisCache) Get(pkgPath string, contentHash string, depsHash string) (*transpiler.RichAST, []string) {
	if !c.enabled {
		return nil, nil
	}
	path := c.cachePath(pkgPath, contentHash)
	data, err := ioutil.ReadFile(path)
	if err != nil {
		return nil, nil
	}

	cached, err := decodeCachedRichAST(data)
	if err != nil || cached == nil {
		os.Remove(path)
		return nil, nil
	}

	// Validate transitive dependency hash
	if depsHash != "" && cached.DepsHash != depsHash {
		os.Remove(path)
		return nil, nil
	}

	return fromCachedRichAST(cached), cached.DirectImports
}

// Put stores a RichAST in the cache using atomic write (temp file + rename).
// This prevents parallel Bazel genrules from reading partially-written files.
// Also removes stale entries for the same package (different content hashes).
//
// The serialized form is the package's OWN metadata only — transitive imports
// are recorded as a list of direct import paths and re-loaded at Get time. This
// avoids the multi-GB blowup that occurred when each pkgAST inlined the full
// type closure of every package it transitively depended on.
func (c *analysisCache) Put(pkgPath string, contentHash string, depsHash string, richAST *transpiler.RichAST, directImports []string) {
	if !c.enabled || richAST == nil {
		return
	}
	// Clean up stale entries for this package before writing new one
	c.evictStale(pkgPath, contentHash)

	path := c.cachePath(pkgPath, contentHash)
	os.MkdirAll(filepath.Dir(path), 0755)

	// Write to a temp file first, then rename atomically.
	// This prevents concurrent readers from seeing partial writes.
	tmpFile, err := ioutil.TempFile(c.dir, ".tmp-cache-*")
	if err != nil {
		return
	}
	tmpPath := tmpFile.Name()

	cached := toCachedRichAST(richAST, depsHash, directImports)
	encoded, err := encodeCachedRichAST(cached)
	if err != nil {
		tmpFile.Close()
		os.Remove(tmpPath)
		return
	}
	if _, err := tmpFile.Write(encoded); err != nil {
		tmpFile.Close()
		os.Remove(tmpPath)
		return
	}
	tmpFile.Close()

	// Atomic rename — readers see either the old file or the new complete file
	if err := os.Rename(tmpPath, path); err != nil {
		os.Remove(tmpPath)
	}
}

// evictStale removes old cache entries for the same package with different content hashes.
func (c *analysisCache) evictStale(pkgPath string, keepHash string) {
	if !c.enabled {
		return
	}
	prefix := c.cachePrefix(pkgPath)
	keepPath := c.cachePath(pkgPath, keepHash)

	entries, err := ioutil.ReadDir(c.dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		full := filepath.Join(c.dir, entry.Name())
		if strings.HasPrefix(entry.Name(), prefix) && full != keepPath {
			os.Remove(full)
		}
	}
}

func (c *analysisCache) cachePrefix(pkgPath string) string {
	safe := strings.ReplaceAll(pkgPath, "/", "_")
	safe = strings.ReplaceAll(safe, "\\", "_")
	return safe + "_"
}

func (c *analysisCache) cachePath(pkgPath, contentHash string) string {
	// Extension is decorative — the directory's CacheVersion key is the
	// real format identifier. Kept as ".bin" for v2 to make the binary
	// codec switch visible at a glance when inspecting the cache tree.
	return filepath.Join(c.dir, c.cachePrefix(pkgPath)+contentHash[:12]+".bin")
}

// pkgFingerprint is the per-process memo of the three derived quantities
// the analyzer pulls out of a package directory on every analyzePackage call:
// the source content hash, the imports hash, and the sorted direct-import
// list. Computing each one independently — as the original code did — costs
// three full file reads of every .gala/.go file in the package, which on
// the apex transpile of a project like gala_team (~100 transitive packages,
// ~5 files each) explodes to 1500+ tiny reads. On ubuntu-latest's I/O-
// bound CI runners that round-trips through readahead/page-cache and slows
// the cmd/main.gala apex transpile to 600+ s, even after PR #316
// concentrated cold-start cost in a single process.
//
// We key by (dirPath, dirModTime, fileCount) so a write to the directory
// (touch / new / delete) invalidates the entry on the next access. Within
// a stable build that signature is invariant, so the heavy reads happen
// once per package per worker process instead of (3 × visits-during-walk).
type pkgFingerprint struct {
	contentHash   string
	depsHash      string
	directImports []string
}

type pkgFingerprintCache struct {
	mu      sync.Mutex
	entries map[string]*pkgFingerprintEntry
}

type pkgFingerprintEntry struct {
	dirSize int64       // st.Size() of the directory inode
	mtime   time.Time   // st.ModTime() of the directory inode
	count   int         // number of relevant non-test files
	fp      pkgFingerprint
}

var fpCache = &pkgFingerprintCache{
	entries: make(map[string]*pkgFingerprintEntry),
}

// pkgFingerprintForDir returns the (cached) content hash, deps hash, and
// direct-imports list for a package directory. On cache miss it does ONE
// pass over the directory, ONE read per file, and computes all three
// derived values from the same buffer — vs the previous three-pass shape
// that read every file three times. The result is memoized per process
// keyed by (dirPath, directory mtime + size + entry count) so a churn-
// free build pays the read cost once per package per worker process.
func pkgFingerprintForDir(dirPath string) pkgFingerprint {
	dirInfo, err := os.Stat(dirPath)
	if err != nil {
		return pkgFingerprint{}
	}
	files, err := ioutil.ReadDir(dirPath)
	if err != nil {
		return pkgFingerprint{}
	}
	// First pass: collect only the relevant filenames so the cache key
	// doesn't oscillate when irrelevant files are touched (Bazel sandbox
	// drops have a habit of scattering symlinks). Sort for hash stability.
	type fileEntry struct {
		name      string
		isGala    bool
	}
	var entries []fileEntry
	for _, f := range files {
		if f.IsDir() {
			continue
		}
		name := f.Name()
		ext := filepath.Ext(name)
		if ext != ".gala" && ext != ".go" {
			continue
		}
		if strings.HasSuffix(name, "_test.gala") || strings.HasSuffix(name, "_test.go") || strings.HasSuffix(name, ".gen.go") {
			continue
		}
		entries = append(entries, fileEntry{name: name, isGala: ext == ".gala"})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].name < entries[j].name })

	fpCache.mu.Lock()
	if e, ok := fpCache.entries[dirPath]; ok {
		if e.mtime.Equal(dirInfo.ModTime()) && e.dirSize == dirInfo.Size() && e.count == len(entries) {
			fp := e.fp
			fpCache.mu.Unlock()
			return fp
		}
	}
	fpCache.mu.Unlock()

	// Cache miss: do the heavy work once, then memoize. Read each file
	// exactly once; both the content hash and the import-line scan share
	// the same buffer.
	contentH := sha256.New()
	importSet := make(map[string]struct{})
	for _, e := range entries {
		content, err := ioutil.ReadFile(filepath.Join(dirPath, e.name))
		if err != nil {
			return pkgFingerprint{}
		}
		contentH.Write([]byte(e.name))
		contentH.Write(content)
		// Imports only come from .gala source — scan the same buffer.
		if e.isGala {
			scanImportsLineByLine(content, importSet)
		}
	}
	directImports := make([]string, 0, len(importSet))
	for p := range importSet {
		directImports = append(directImports, p)
	}
	sort.Strings(directImports)
	depsH := sha256.New()
	for _, p := range directImports {
		depsH.Write([]byte(p))
	}
	fp := pkgFingerprint{
		contentHash:   hex.EncodeToString(contentH.Sum(nil)),
		depsHash:      hex.EncodeToString(depsH.Sum(nil)),
		directImports: directImports,
	}
	fpCache.mu.Lock()
	fpCache.entries[dirPath] = &pkgFingerprintEntry{
		dirSize: dirInfo.Size(),
		mtime:   dirInfo.ModTime(),
		count:   len(entries),
		fp:      fp,
	}
	fpCache.mu.Unlock()
	return fp
}

// scanImportsLineByLine extracts quoted "path/with/slashes" import strings
// from a .gala source buffer. Mirrors the original line-based scan; we
// pull it into a helper so the fingerprint loop and the test path can
// share the implementation without re-reading the file.
func scanImportsLineByLine(content []byte, into map[string]struct{}) {
	for _, line := range strings.Split(string(content), "\n") {
		trimmed := strings.TrimSpace(line)
		if idx := strings.Index(trimmed, "\""); idx >= 0 {
			if end := strings.Index(trimmed[idx+1:], "\""); end >= 0 {
				importPath := trimmed[idx+1 : idx+1+end]
				if strings.Contains(importPath, "/") {
					into[importPath] = struct{}{}
				}
			}
		}
	}
}

// hashPackageDir computes a content hash for all .gala and .go files in a directory.
// Memoized by pkgFingerprintForDir; see that function for cache semantics.
func hashPackageDir(dirPath string) string {
	return pkgFingerprintForDir(dirPath).contentHash
}

// hashImportPaths computes a hash of the import paths found in .gala files in a directory.
// This is used as a "dependency identity" — if imports change, the cache invalidates.
// Combined with the content hash of each resolved dependency, this ensures transitive
// invalidation when any dependency's source changes.
func hashImportPaths(dirPath string) string {
	return pkgFingerprintForDir(dirPath).depsHash
}

// extractDirectImportPaths reads all non-test .gala files in dirPath and
// returns the set of import paths that appear in their import declarations.
// Memoized by pkgFingerprintForDir.
//
// The list is sorted and deduplicated so it can be persisted in a cache
// entry and replayed deterministically.
//
// This is a quick line-based scan (no full ANTLR parse) that mirrors the
// scan used by hashImportPaths. It treats any quoted string containing a
// "/" on a non-comment line as a possible import. The analyzer further
// filters this list — entries that don't resolve to GALA packages are
// silently ignored at re-merge time.
func extractDirectImportPaths(dirPath string) []string {
	src := pkgFingerprintForDir(dirPath).directImports
	if src == nil {
		return nil
	}
	// Defensive copy so callers that append/sort don't mutate the cached slice.
	out := make([]string, len(src))
	copy(out, src)
	return out
}

// findProjectRoot walks up from startDir looking for go.mod or gala.mod.
func findProjectRoot(startDir string) string {
	dir := startDir
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		if _, err := os.Stat(filepath.Join(dir, "gala.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// logCache prints a cache hit/miss message if profiling is enabled.
func logCache(hit bool, pkgPath string, elapsed time.Duration) {
	if !profiler.Enabled {
		return
	}
	status := "MISS"
	if hit {
		status = "HIT"
	}
	fmt.Fprintf(os.Stderr, "    [cache] %-5s %-30s %s\n", status, pkgPath, elapsed.Round(time.Millisecond))
}
