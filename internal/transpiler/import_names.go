package transpiler

import (
	"path"
	"strconv"
	"strings"
	"unicode"
)

// AssumedPackageName is the package name an unaliased import is expected to
// bind, derived from its path by Go's naming conventions: the trailing segment,
// skipping a `/vN` major-version segment, without a `go-` prefix, and cut at
// the first character that cannot appear in an identifier. So
// `gopkg.in/yaml.v3` is `yaml`, `github.com/x/foo/v2` is `foo` and
// `github.com/mattn/go-sqlite3` is `sqlite3` — the same guess goimports makes.
func AssumedPackageName(importPath string) string {
	base := path.Base(importPath)
	if len(base) > 1 && base[0] == 'v' {
		if _, err := strconv.Atoi(base[1:]); err == nil {
			if dir := path.Dir(importPath); dir != "." {
				base = path.Base(dir)
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

// PackageNameCandidates is every name an unaliased import of importPath may
// bind: AssumedPackageName plus, when it differs, the raw last path segment. A
// trailing `/vN` is ambiguous — `math/rand/v2` is package `rand`, while
// `k8s.io/api/core/v1` is package `v1` — so both readings are returned.
func PackageNameCandidates(importPath string) []string {
	assumed, last := AssumedPackageName(importPath), path.Base(importPath)
	if assumed == last {
		return []string{assumed}
	}
	return []string{assumed, last}
}
