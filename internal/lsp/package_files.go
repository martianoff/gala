package lsp

import (
	"martianoff/gala/internal/transpiler/analyzer"
)

// sourcePackageName returns the package a GALA source declares, or "" when it
// has no package clause yet.
func sourcePackageName(src string) string {
	return analyzer.SourcePackageName(src)
}

// packageFiles returns the other .gala files in filePath's directory that are
// compiled together with it, as analyzer.PackageSiblings decides. An open
// sibling's editor text counts, so unsaved edits are seen.
func (h *GalaHandler) packageFiles(filePath, text string) []string {
	return analyzer.PackageSiblings(filePath, text, h.fileText)
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
	return analyzer.ReadSource(path)
}

// sameFilePath reports whether two paths name the same file.
func sameFilePath(a, b string) bool {
	return analyzer.SameFilePath(a, b)
}
