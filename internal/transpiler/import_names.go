package transpiler

import (
	"strconv"
	"strings"
	"unicode"
)

// IsValidGoImportPath reports whether p can be written in a Go import
// declaration: non-empty slash-separated elements of ASCII letters, digits and
// `-._~+`, none of them `.` or `..`, and no leading or trailing slash. That
// is the character set golang.org/x/mod/module.CheckImportPath allows, so a
// filesystem path — a drive letter (`C:`), a backslash, a leading `/` — is
// never valid.
func IsValidGoImportPath(p string) bool {
	if p == "" {
		return false
	}
	for elem := range strings.SplitSeq(p, "/") {
		if elem == "" || elem == "." || elem == ".." {
			return false
		}
		for i := 0; i < len(elem); i++ {
			c := elem[i]
			switch {
			case 'a' <= c && c <= 'z', 'A' <= c && c <= 'Z', '0' <= c && c <= '9':
			case strings.IndexByte("-._~+", c) >= 0:
			default:
				return false
			}
		}
	}
	return true
}

// LastPathSegment is the part of an import path after its last slash.
func LastPathSegment(importPath string) string {
	return importPath[strings.LastIndex(importPath, "/")+1:]
}

// AssumedPackageName is the package name an unaliased import is guessed to
// bind when its real name is unknown, derived from its path by Go's naming
// conventions: the trailing segment, skipping a `/vN` major-version segment,
// without a `go-` prefix, and cut at the first character that cannot appear
// in an identifier. So `gopkg.in/yaml.v3` is `yaml`, `github.com/x/foo/v2` is
// `foo` and `github.com/mattn/go-sqlite3` is `sqlite3` — the same guess
// goimports makes. It is only a guess: `k8s.io/api/core/v1` is package `v1`.
func AssumedPackageName(importPath string) string {
	base := LastPathSegment(importPath)
	if len(base) > 1 && base[0] == 'v' {
		if _, err := strconv.Atoi(base[1:]); err == nil {
			if dir := strings.TrimSuffix(importPath, "/"+base); dir != importPath {
				base = LastPathSegment(dir)
			}
		}
	}
	base = strings.TrimPrefix(base, "go-")
	if i := strings.IndexFunc(base, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_'
	}); i >= 0 {
		base = base[:i]
	}
	return base
}

// Import name ranks. When two imports of one file claim the same name, the
// surer binding wins: a written alias, then the package's real name, then the
// path's last segment, then a name derived from it by AssumedPackageName.
const (
	RankDerived = iota + 1
	RankLastSegment
	RankPackageName
	RankAlias
)

// ImportName is one name an import may bind, with how sure that is.
type ImportName struct {
	Name string
	Rank int
}

// ImportNames lists the names an import of importPath binds. An alias is the
// only one. Otherwise pkgName, the package's real name, when known. Otherwise
// every plausible name — the last path segment and the name derived from it —
// because a `/vN` suffix does not settle it: `math/rand/v2` is package `rand`,
// `k8s.io/api/core/v1` is package `v1`.
func ImportNames(importPath, alias, pkgName string) []ImportName {
	switch {
	case alias != "":
		return []ImportName{{alias, RankAlias}}
	case pkgName != "":
		return []ImportName{{pkgName, RankPackageName}}
	}
	last, derived := LastPathSegment(importPath), AssumedPackageName(importPath)
	if derived == last {
		return []ImportName{{last, RankLastSegment}}
	}
	return []ImportName{{last, RankLastSegment}, {derived, RankDerived}}
}

// RankedNames maps names to the import that binds them, keeping for each name
// the binding with the highest rank (the first on a tie).
type RankedNames[V any] struct {
	values map[string]V
	ranks  map[string]int
}

// NewRankedNames returns an empty RankedNames.
func NewRankedNames[V any]() *RankedNames[V] {
	return &RankedNames[V]{values: make(map[string]V), ranks: make(map[string]int)}
}

// Bind records v under every name ImportNames lists for the import.
func (r *RankedNames[V]) Bind(importPath, alias, pkgName string, v V) {
	for _, n := range ImportNames(importPath, alias, pkgName) {
		if n.Name != "" && n.Name != "_" && n.Rank > r.ranks[n.Name] {
			r.values[n.Name], r.ranks[n.Name] = v, n.Rank
		}
	}
}

// Get returns what name is bound to.
func (r *RankedNames[V]) Get(name string) (V, bool) {
	v, ok := r.values[name]
	return v, ok
}

// Map returns every binding, by name.
func (r *RankedNames[V]) Map() map[string]V { return r.values }
