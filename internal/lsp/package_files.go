package lsp

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

// sourcePackageName returns the package a GALA source declares, or "" when it
// has no package clause yet.
func sourcePackageName(src string) string {
	if m := packageClauseRe.FindStringSubmatch(src); m != nil {
		return m[1]
	}
	return ""
}

// packageFiles returns the other .gala files in filePath's directory that are
// compiled together with it, following the same rules as the analyzer's
// directory discovery: same package name, and `_test.gala` files only when
// filePath is itself a test file.
//
// A `main` package gets one more rule. The analyzer never discovers `main`
// siblings on its own, because a directory such as examples/ holds many
// independent programs. A directory with at most one `func main` is a single
// program split across files (what `gala new` creates), so all of its files
// belong together; with several programs nothing is returned and each file
// stands alone.
//
// The package name and `func main` are matched textually rather than parsed,
// so this costs one read per sibling instead of a full parse.
func (h *GalaHandler) packageFiles(filePath, text string) []string {
	pkgName := sourcePackageName(text)
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
	if mainFuncRe.MatchString(text) {
		mainFuncs++
	}
	var files []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || filepath.Ext(name) != ".gala" {
			continue
		}
		path := filepath.Join(dir, name)
		if sameFilePath(path, filePath) {
			continue
		}
		if strings.HasSuffix(name, "_test.gala") && !currentIsTest {
			continue
		}
		src, ok := h.fileText(path)
		if !ok || sourcePackageName(src) != pkgName {
			continue
		}
		if mainFuncRe.MatchString(src) {
			mainFuncs++
			if pkgName == "main" && mainFuncs > 1 {
				return nil // several programs: stop reading, the answer is fixed
			}
		}
		files = append(files, path)
	}
	return files
}

// fileText returns the text of path: the editor's copy when the file is open,
// so unsaved edits count, otherwise the file on disk.
func (h *GalaHandler) fileText(path string) (string, bool) {
	h.mu.Lock()
	for uri, text := range h.documents {
		if sameFilePath(uriToPath(uri), path) {
			h.mu.Unlock()
			return text, true
		}
	}
	h.mu.Unlock()
	return readSource(path)
}

// readSource reads a source file from disk the way an open document is held:
// without a leading byte order mark. Every position the server computes on a
// file — and every position the parser and analyzer record — is relative to
// the BOM-free text, and so is the document a client shows; reading the raw
// bytes puts anything on the first line three bytes out.
func readSource(path string) (string, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	return galaerr.StripBOM(string(b)), true
}

// sameFilePath reports whether two paths name the same file. Windows paths are
// compared case-insensitively, since a client may send a different drive-letter
// case than os.ReadDir returns.
func sameFilePath(a, b string) bool {
	a, b = filepath.Clean(a), filepath.Clean(b)
	if filepath.Separator == '\\' {
		return strings.EqualFold(a, b)
	}
	return a == b
}
