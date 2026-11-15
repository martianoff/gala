package analyzer

import (
	"fmt"
	"go/importer"
	"go/token"
	"go/types"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
)

// goExportFiles maps Go import paths to compiler export data a build system
// already produced, so the analyzer reads a package's types from it instead of
// type-checking the package and everything it imports from source. Under Bazel
// the worker sets it from each request's --go-export (the .x file rules_go
// writes for every go_library) and --go-stdlib-export (the directory of the
// standard library's compiled <importpath>.a archives).
//
// It holds one request's files: a request is analyzed with what it names and
// nothing an earlier request named, whatever the worker served before. Bazel
// sends a worker one request at a time.
type goExportFiles struct {
	mu    sync.Mutex
	files map[string]exportFile
	// stdlibDir is the standard library's archive directory, as a file: its
	// path, and its size and time, which a rebuild of it changes.
	stdlibDir exportFile
	// stdlib caches, per import path, its archive under stdlibDir, or "" when
	// stdlibDir has none: whether a path has export data is asked of every
	// import, and must not cost a file system lookup each time.
	stdlib map[string]string
	// generation moves on whenever a path is given another export file than
	// the one it had last, in any earlier table, so packages read from the old
	// file are not served again (see tableExportImporter).
	generation int
	// last is the export file each path had in the last table that named it,
	// and lastStdlib the last stdlib directory.
	last       map[string]exportFile
	lastStdlib exportFile
}

// exportFile is an export data file, and what it was when the table took it:
// a library rebuilt in place, as a worker that outlives builds sees, is a
// changed file.
type exportFile struct {
	path string
	size int64
	mod  int64
}

func newExportFile(path string) exportFile {
	f := exportFile{path: path}
	if info, err := os.Stat(path); err == nil {
		f.size, f.mod = info.Size(), info.ModTime().UnixNano()
	}
	return f
}

// source is how moduleDirs records a package read from f.
func (f exportFile) source() string {
	return fmt.Sprintf("%s%d:%d:%s", goExportSource, f.size, f.mod, f.path)
}

func newGoExportFiles() *goExportFiles {
	return &goExportFiles{files: map[string]exportFile{}, stdlib: map[string]string{}, last: map[string]exportFile{}}
}

// goExportTable is the table the shared importer reads (see getGoImporter).
var goExportTable = newGoExportFiles()

// SetGoExportFiles sets the table the analyzer reads Go package types from,
// before its usual importer, to files (import path -> export file) and the
// standard library's compiled archives in stdlibDir; empty arguments clear it.
// The Go type information cached for a path whose export file differs from the
// previous table's (added, removed or rewritten) is dropped.
func SetGoExportFiles(files map[string]string, stdlibDir string) {
	changed, stdlibChanged, rewritten := goExportTable.set(files, stdlibDir)
	if len(changed) > 0 || stdlibChanged {
		forgetGoPackages(changed, stdlibChanged)
	}
	if rewritten {
		// Packages type-checked against the replaced export data.
		forgetExportReadVariants()
	}
}

// set replaces the table. It returns the paths whose export file is not what
// it was, whether stdlibDir is not, which may change every standard-library
// path's, and whether a path got another file than it had before, which
// moves the generation on.
func (t *goExportFiles) set(files map[string]string, stdlibDir string) (changed []string, stdlibChanged, rewritten bool) {
	next := make(map[string]exportFile, len(files))
	for path, file := range files {
		if file != "" {
			next[path] = newExportFile(file)
		}
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	for path, f := range next {
		if old, ok := t.files[path]; !ok || old != f {
			changed = append(changed, path)
		}
		if last, ok := t.last[path]; ok && last != f {
			rewritten = true
		}
		t.last[path] = f
	}
	for path := range t.files {
		if _, ok := next[path]; !ok {
			changed = append(changed, path)
		}
	}
	t.files = next
	var dir exportFile
	if stdlibDir != "" {
		// Stamped by its runtime archive: a directory's own size and time do
		// not follow its files on every platform.
		dir = newExportFile(filepath.Join(stdlibDir, "runtime.a"))
		dir.path = stdlibDir
		if t.lastStdlib.path != "" && t.lastStdlib != dir {
			rewritten = true
		}
		t.lastStdlib = dir
	}
	if dir != t.stdlibDir {
		t.stdlibDir = dir
		clear(t.stdlib)
		stdlibChanged = true
	}
	if rewritten {
		t.generation++
	}
	slices.Sort(changed)
	return changed, stdlibChanged, rewritten
}

// lookup returns the export file the table gives path, and whether it gives
// one.
func (t *goExportFiles) lookup(path string) (exportFile, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if f, ok := t.files[path]; ok {
		return f, true
	}
	if t.stdlibDir.path == "" {
		return exportFile{}, false
	}
	archive, ok := t.stdlib[path]
	if !ok {
		archive = filepath.Join(t.stdlibDir.path, filepath.FromSlash(path)+".a")
		if _, err := os.Stat(archive); err != nil {
			archive = ""
		}
		t.stdlib[path] = archive
	}
	// The directory's size and time stand for the archive's: a rebuild
	// replaces the whole directory.
	return exportFile{path: archive, size: t.stdlibDir.size, mod: t.stdlibDir.mod}, archive != ""
}

// state returns whether the table is empty, and its generation.
func (t *goExportFiles) state() (empty bool, generation int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.files) == 0 && t.stdlibDir.path == "", t.generation
}

// tableExportImporter imports a package from the export file its table gives
// it, and any other package, or one whose export data cannot be read, with
// fallback. With an empty table it is fallback. Not safe for concurrent use;
// the shared importer's mutex serializes it.
type tableExportImporter struct {
	fset     *token.FileSet
	table    *goExportFiles
	fallback types.Importer

	// gc reads export data and keeps every package it read, for the table
	// generation it was made for.
	gc           types.Importer
	gcGeneration int
	// unreadable holds the paths gc could not read in this generation, so
	// they go to fallback without another attempt.
	unreadable map[string]bool
}

var _ types.Importer = (*tableExportImporter)(nil)

func newTableExportImporter(fset *token.FileSet, table *goExportFiles, fallback types.Importer) *tableExportImporter {
	return &tableExportImporter{fset: fset, table: table, fallback: fallback, gcGeneration: -1}
}

// Import implements types.Importer.
func (t *tableExportImporter) Import(path string) (*types.Package, error) {
	empty, generation := t.table.state()
	if empty {
		return t.fallback.Import(path)
	}
	if _, ok := t.table.lookup(path); !ok {
		return t.fallback.Import(path)
	}
	if generation != t.gcGeneration {
		t.gc = importer.ForCompiler(t.fset, "gc", t.table.open)
		t.gcGeneration = generation
		t.unreadable = map[string]bool{}
	}
	if !t.unreadable[path] {
		if pkg, err := importExportData(t.gc, path); err == nil {
			return pkg, nil
		}
		t.unreadable[path] = true
	}
	return t.fallback.Import(path)
}

// open opens the export file the table gives path, for a gc importer.
func (t *goExportFiles) open(path string) (io.ReadCloser, error) {
	f, ok := t.lookup(path)
	if !ok {
		return nil, fmt.Errorf("no export data for %q", path)
	}
	return os.Open(f.path)
}

// readsGoExportData reports whether table reads fmt, and so the standard
// library, from export data this build of GALA can decode.
func readsGoExportData(table *goExportFiles) bool {
	if _, ok := table.lookup("fmt"); !ok {
		return false
	}
	gc := importer.ForCompiler(token.NewFileSet(), "gc", table.open)
	_, err := importExportData(gc, "fmt")
	return err == nil
}

// goExportSource is how moduleDirs records an import read from export data.
const goExportSource = "\x00export:"

// goTypesSource returns where the types of the package at path are read from
// for goSrcDirs, as moduleDirs records it, and whether that is export data:
// the export file the build system gave (see exportFile.source), the source
// directory goSrcDirs gives, or "" for the shared importer.
func goTypesSource(goSrcDirs map[string]string, path string) (source string, fromExport bool) {
	if f, ok := goExportTable.lookup(path); ok {
		return f.source(), true
	}
	dir, _ := ResolveGoSrcDir(goSrcDirs, path)
	return dir, false
}

// forgetExportReadVariants drops the cached type checks of Go files that read
// a package from export data. They hold that data's packages, which a new
// generation's are not identical to.
func forgetExportReadVariants() {
	readExport := func(d moduleDirs) bool {
		for _, source := range d {
			if strings.HasPrefix(source, goExportSource) {
				return true
			}
		}
		return false
	}
	goFilesCache.mu.Lock()
	for key, results := range goFilesCache.cache {
		goFilesCache.cache[key] = slices.DeleteFunc(results, func(r goFilesResult) bool { return readExport(r.dirsUsed) })
	}
	goFilesCache.mu.Unlock()
	moduleSourcePackages.forget(func(p sourcePackage) bool { return readExport(p.used) })
}
