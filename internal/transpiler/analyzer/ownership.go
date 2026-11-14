package analyzer

import (
	"path/filepath"

	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/ownership"
)

// ownershipSummaries records in pkgAST, the metadata of an imported package,
// the borrow summary of each of its functions and methods (ownership phase
// 1, docs/OWNERSHIP_DESIGN.MD). A caller in an importing package reads them
// when it lends a pointer to one. paths and entries are the package's files
// and their parses; a file that did not parse is left out.
func ownershipSummaries(pkgAST *transpiler.RichAST, paths []string, entries []*parsedFileEntry) {
	if pkgAST.PackageName == "" {
		return
	}
	files := make([]ownership.File, 0, len(entries))
	for i, e := range entries {
		if e != nil && e.nf.root.Ok() {
			files = append(files, ownership.File{Path: paths[i], Root: e.nf.root})
		}
	}
	if len(files) == 0 {
		return
	}
	opts := ownership.Options{HandWrittenGo: ownership.HasHandWrittenGo(filepath.Dir(files[0].Path))}
	ownership.AnalyzeAndApply(pkgAST.PackageName, files, pkgAST, opts)
}
