package analyzer

import (
	"path/filepath"
	"sync"

	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/ownership"
)

// ownershipSummaries records in pkgAST, the metadata of an imported package,
// the borrow summary of each of its functions and methods (ownership phase
// 1, docs/OWNERSHIP_DESIGN.MD). A caller in an importing package reads them
// when it lends a pointer to one. paths and entries are the package's files
// and their parses; a file that did not parse is left out.
func ownershipSummaries(pkgAST *transpiler.RichAST, paths []string, entries []*parsedFileEntry) {
	files := make([]ownership.File, 0, len(entries))
	for i, e := range entries {
		if e != nil && e.nf.root.Ok() {
			files = append(files, ownership.File{Path: paths[i], Root: e.nf.root})
		}
	}
	applyOwnershipSummaries(pkgAST, files)
}

// ownBorrowSummaries is RichAST.OwnBorrowSummaries for richAST, the
// metadata of the analyzed file at path, whose native tree is nf, and of its
// package: the same summaries ownershipSummaries records for an importer,
// over the file and its siblings, computed once on the first call.
func ownBorrowSummaries(richAST *transpiler.RichAST, path string, nf nativeFile, siblings []pkgFile) func() {
	var files []ownership.File
	if nf.root.Ok() {
		files = append(files, ownership.File{Path: path, Root: nf.root})
	}
	for _, sib := range siblings {
		if sib.parsedFileEntry != nil && sib.nf.root.Ok() {
			files = append(files, ownership.File{Path: sib.path, Root: sib.nf.root})
		}
	}
	return sync.OnceFunc(func() { applyOwnershipSummaries(richAST, files) })
}

// applyOwnershipSummaries records in pkgAST the borrow summaries of the
// functions and methods files, the package's, declare.
func applyOwnershipSummaries(pkgAST *transpiler.RichAST, files []ownership.File) {
	if pkgAST.PackageName == "" || len(files) == 0 {
		return
	}
	opts := ownership.Options{HandWrittenGo: ownership.HasHandWrittenGo(filepath.Dir(files[0].Path))}
	ownership.AnalyzeAndApply(pkgAST.PackageName, files, pkgAST, opts)
}
