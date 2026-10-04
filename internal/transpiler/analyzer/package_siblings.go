package analyzer

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"martianoff/gala/galaerr"
)

var (
	packageClauseRe = regexp.MustCompile(`(?m)^[ \t]*package[ \t]+([A-Za-z_][A-Za-z0-9_]*)`)
	mainFuncRe      = regexp.MustCompile(`(?m)^[ \t]*func[ \t]+main[ \t]*\(`)
)

// SourcePackageName returns the package a GALA source declares, or "" when it
// has no package clause yet.
func SourcePackageName(src string) string {
	if m := packageClauseRe.FindStringSubmatch(src); m != nil {
		return m[1]
	}
	return ""
}

// PackageSiblings returns the other .gala files in filePath's directory that
// are compiled together with it (src is filePath's text): same package name,
// and `_test.gala` files only when filePath is itself a test file. read
// supplies a sibling's text; ReadSource reads it from disk.
//
// Callers pass the result to SetPackageFiles rather than relying on the
// analyzer's own directory discovery, which skips packages named main or
// test. A library package may be called test, as the shipped one is, and its
// files must still see each other's declarations.
//
// A `main` package gets one more rule. A directory such as examples/ holds
// many independent programs, so with several `func main` in the directory
// nothing is returned and each file stands alone; with at most one it is a
// single program split across files (what `gala new` creates).
//
// The package name and `func main` are matched textually rather than parsed,
// so this costs one read per sibling instead of a full parse.
func PackageSiblings(filePath, src string, read func(path string) (string, bool)) []string {
	src = galaerr.StripBOM(src)
	pkgName := SourcePackageName(src)
	if pkgName == "" {
		return nil
	}
	dir := filepath.Dir(filePath)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	currentIsTest := strings.HasSuffix(filePath, "_test.gala")
	mainFuncs := 0
	if mainFuncRe.MatchString(src) {
		mainFuncs++
	}
	var files []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || filepath.Ext(name) != ".gala" {
			continue
		}
		path := filepath.Join(dir, name)
		if SameFilePath(path, filePath) {
			continue
		}
		if strings.HasSuffix(name, "_test.gala") && !currentIsTest {
			continue
		}
		text, ok := read(path)
		if !ok || SourcePackageName(text) != pkgName {
			continue
		}
		if mainFuncRe.MatchString(text) {
			mainFuncs++
			if pkgName == "main" && mainFuncs > 1 {
				return nil // several programs: stop reading, the answer is fixed
			}
		}
		files = append(files, path)
	}
	return files
}

// ReadSource reads a source file from disk without a leading byte order mark,
// the way the parser and analyzer see it.
func ReadSource(path string) (string, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	return galaerr.StripBOM(string(b)), true
}

// SameFilePath reports whether two paths name the same file. Windows paths are
// compared case-insensitively, since a caller may spell the drive letter in a
// different case than os.ReadDir returns.
func SameFilePath(a, b string) bool {
	a, b = filepath.Clean(a), filepath.Clean(b)
	if filepath.Separator == '\\' {
		return strings.EqualFold(a, b)
	}
	return a == b
}
