package analyzer

import (
	"crypto/sha256"
	"encoding/gob"
	"encoding/hex"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/profiler"
)

func init() {
	// Register all concrete Type implementations for gob encoding
	gob.Register(transpiler.BasicType{})
	gob.Register(transpiler.NamedType{})
	gob.Register(transpiler.GenericType{})
	gob.Register(transpiler.ArrayType{})
	gob.Register(transpiler.MapType{})
	gob.Register(transpiler.PointerType{})
	gob.Register(transpiler.FuncType{})
	gob.Register(transpiler.NilType{})
	gob.Register(transpiler.VoidType{})
}

// CacheVersion is incremented when the cache format or analysis semantics change.
// This ensures stale caches from older compiler versions are automatically invalidated.
const CacheVersion = "v1"

// CachedRichAST is the serializable subset of RichAST (no antlr.Tree).
type CachedRichAST struct {
	PackageName      string
	Types            map[string]*transpiler.TypeMetadata
	Functions        map[string]*transpiler.FunctionMetadata
	Packages         map[string]string
	CompanionObjects map[string]*transpiler.CompanionObjectMetadata
	GoExports        map[string][]string
	GoTypeInfo       *transpiler.GoTypeInfo
	TypeAliases      map[string]transpiler.Type
	ImportPathMap    map[string]string
	DepsHash         string // hash of transitive dependency content (for invalidation)
}

func toCachedRichAST(r *transpiler.RichAST, depsHash string) *CachedRichAST {
	if r == nil {
		return nil
	}
	return &CachedRichAST{
		PackageName:      r.PackageName,
		Types:            r.Types,
		Functions:        r.Functions,
		Packages:         r.Packages,
		CompanionObjects: r.CompanionObjects,
		GoExports:        r.GoExports,
		GoTypeInfo:       r.GoTypeInfo,
		TypeAliases:      r.TypeAliases,
		ImportPathMap:    r.ImportPathMap,
		DepsHash:         depsHash,
	}
}

func fromCachedRichAST(c *CachedRichAST) *transpiler.RichAST {
	if c == nil {
		return nil
	}
	return &transpiler.RichAST{
		PackageName:      c.PackageName,
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
func newAnalysisCache(projectRoot string) *analysisCache {
	if projectRoot == "" {
		return &analysisCache{enabled: false}
	}
	dir := filepath.Join(projectRoot, ".gala", "cache", CacheVersion)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return &analysisCache{enabled: false}
	}
	return &analysisCache{dir: dir, enabled: true}
}

// Get retrieves a cached RichAST for the given package path and content hash.
// depsHash is the hash of transitive dependency content — must match for a valid hit.
func (c *analysisCache) Get(pkgPath string, contentHash string, depsHash string) *transpiler.RichAST {
	if !c.enabled {
		return nil
	}
	path := c.cachePath(pkgPath, contentHash)
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	var cached CachedRichAST
	if err := gob.NewDecoder(f).Decode(&cached); err != nil {
		os.Remove(path)
		return nil
	}

	// Validate transitive dependency hash
	if depsHash != "" && cached.DepsHash != depsHash {
		os.Remove(path)
		return nil
	}

	return fromCachedRichAST(&cached)
}

// Put stores a RichAST in the cache.
// Also removes stale entries for the same package (different content hashes).
func (c *analysisCache) Put(pkgPath string, contentHash string, depsHash string, richAST *transpiler.RichAST) {
	if !c.enabled || richAST == nil {
		return
	}
	// Clean up stale entries for this package before writing new one
	c.evictStale(pkgPath, contentHash)

	path := c.cachePath(pkgPath, contentHash)
	os.MkdirAll(filepath.Dir(path), 0755)

	f, err := os.Create(path)
	if err != nil {
		return
	}
	defer f.Close()

	cached := toCachedRichAST(richAST, depsHash)
	if err := gob.NewEncoder(f).Encode(cached); err != nil {
		f.Close()
		os.Remove(path)
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
	return filepath.Join(c.dir, c.cachePrefix(pkgPath)+contentHash[:12]+".gob")
}

// hashPackageDir computes a content hash for all .gala and .go files in a directory.
func hashPackageDir(dirPath string) string {
	files, err := ioutil.ReadDir(dirPath)
	if err != nil {
		return ""
	}

	h := sha256.New()
	var names []string
	for _, f := range files {
		if !f.IsDir() && (filepath.Ext(f.Name()) == ".gala" || filepath.Ext(f.Name()) == ".go") {
			if !strings.HasSuffix(f.Name(), "_test.gala") && !strings.HasSuffix(f.Name(), "_test.go") && !strings.HasSuffix(f.Name(), ".gen.go") {
				names = append(names, f.Name())
			}
		}
	}
	sort.Strings(names)

	for _, name := range names {
		content, err := ioutil.ReadFile(filepath.Join(dirPath, name))
		if err != nil {
			return ""
		}
		h.Write([]byte(name))
		h.Write(content)
	}

	return hex.EncodeToString(h.Sum(nil))
}

// hashImportPaths computes a hash of the import paths found in .gala files in a directory.
// This is used as a "dependency identity" — if imports change, the cache invalidates.
// Combined with the content hash of each resolved dependency, this ensures transitive
// invalidation when any dependency's source changes.
func hashImportPaths(dirPath string) string {
	files, err := ioutil.ReadDir(dirPath)
	if err != nil {
		return ""
	}

	h := sha256.New()
	for _, f := range files {
		if f.IsDir() || filepath.Ext(f.Name()) != ".gala" {
			continue
		}
		if strings.HasSuffix(f.Name(), "_test.gala") {
			continue
		}
		content, err := ioutil.ReadFile(filepath.Join(dirPath, f.Name()))
		if err != nil {
			continue
		}
		// Quick import extraction: look for lines matching 'import "..."' or '"..."'
		for _, line := range strings.Split(string(content), "\n") {
			trimmed := strings.TrimSpace(line)
			if idx := strings.Index(trimmed, "\""); idx >= 0 {
				if end := strings.Index(trimmed[idx+1:], "\""); end >= 0 {
					importPath := trimmed[idx+1 : idx+1+end]
					if strings.Contains(importPath, "/") {
						h.Write([]byte(importPath))
					}
				}
			}
		}
	}

	return hex.EncodeToString(h.Sum(nil))
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
