package analyzer

import (
	"bytes"
	"encoding/json"
	"fmt"
	"go/importer"
	"go/token"
	"go/types"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"

	"martianoff/gala/internal/transpiler/profiler"
)

// goExportData selects compiler export data over Go source for the types of
// imported Go packages (see UseGoExportData). Read once, when the shared
// importer is built.
var goExportData atomic.Bool

// UseGoExportData makes the analyzer read the types of imported Go packages
// from the export data `go list -export -deps` compiles for them into the Go
// build cache, instead of type-checking their source. The source importer runs
// one `go list` per import occurrence in module mode; this runs one per batch
// of imports.
//
// The CLI commands, which run in the user's Go environment, opt in. The Bazel
// persistent worker does not: its actions must not compile into a build cache
// outside Bazel's control. (rules-gala passes the transpile arguments in a
// param file, which only the worker reads, so Bazel never reaches the CLI
// commands.) Call it before the first analysis; later calls have no effect.
func UseGoExportData() { goExportData.Store(true) }

// goListedPackage is the part of `go list -json` output the export importer
// reads.
type goListedPackage struct {
	ImportPath string
	Export     string
}

// exportDataImporter imports Go packages from compiler export data, located
// with `go list -export -deps`. A package the listing gives no export file
// for — it does not build, or the listing failed — is imported from source
// instead, as before.
//
// Not safe for concurrent use; getGoImporter serializes it with the other
// importers.
type exportDataImporter struct {
	gc     types.Importer
	source types.Importer
	list   func(paths []string) []goListedPackage
	// exports maps every import path listed so far to its export file, ""
	// when it has none.
	exports map[string]string
}

var _ types.Importer = (*exportDataImporter)(nil)

// newExportDataImporter returns an export-data importer that lists packages
// with list and imports the ones without export data from source.
func newExportDataImporter(fset *token.FileSet, list func([]string) []goListedPackage, source types.Importer) *exportDataImporter {
	e := &exportDataImporter{source: source, list: list, exports: make(map[string]string)}
	e.gc = importer.ForCompiler(fset, "gc", e.lookup)
	return e
}

func (e *exportDataImporter) lookup(path string) (io.ReadCloser, error) {
	if !e.hasExportData(path) {
		return nil, fmt.Errorf("no export data for %q", path)
	}
	return os.Open(e.exports[path])
}

// Import implements types.Importer.
func (e *exportDataImporter) Import(path string) (*types.Package, error) {
	e.prefetch([]string{path})
	if e.hasExportData(path) {
		if pkg, err := e.importExportData(path); err == nil {
			return pkg, nil
		}
	}
	return e.source.Import(path)
}

// importExportData reads path's export data. The gc importer panics on export
// data it cannot decode — the format of a newer Go release than the one GALA
// was built with, say — and that is reported as an error, for the fallback.
func (e *exportDataImporter) importExportData(path string) (pkg *types.Package, err error) {
	defer func() {
		if r := recover(); r != nil {
			pkg, err = nil, fmt.Errorf("reading export data of %q: %v", path, r)
		}
	}()
	return e.gc.Import(path)
}

// goListBatch caps the import paths one `go list` is given, keeping its
// command line well under the platform limit.
const goListBatch = 200

// prefetch lists, in as few `go list` runs as it can, the paths not listed
// yet, together with everything they import. A path a run fails to report is
// recorded without export data, so it is never listed again.
func (e *exportDataImporter) prefetch(paths []string) {
	var missing []string
	for _, p := range paths {
		if _, ok := e.exports[p]; ok || p == "unsafe" || p == "C" {
			continue
		}
		e.exports[p] = "" // until the listing says otherwise; also dedups paths
		missing = append(missing, p)
	}
	for len(missing) > 0 {
		batch := missing[:min(len(missing), goListBatch)]
		missing = missing[len(batch):]
		for _, pkg := range e.list(batch) {
			// Never forget an export file a previous listing found.
			if pkg.Export != "" || !e.hasExportData(pkg.ImportPath) {
				e.exports[pkg.ImportPath] = pkg.Export
			}
		}
	}
}

// hasExportData reports whether path was listed with an export file.
func (e *exportDataImporter) hasExportData(path string) bool { return e.exports[path] != "" }

// goListCmd is the go command of the SDK at goroot, run with args in the
// current directory — the one the source importer resolves imports from. It
// reads packages with the analyzer's cgo setting, and is pinned to that SDK
// (GOTOOLCHAIN=local), so its export data is of the release whose source the
// analyzer would otherwise read.
func goListCmd(goroot string, cgo bool, args ...string) *exec.Cmd {
	cgoEnabled := "0"
	if cgo {
		cgoEnabled = "1"
	}
	cmd := exec.Command(filepath.Join(goroot, "bin", "go"), args...)
	cmd.Env = append(os.Environ(), "GOROOT="+goroot, "CGO_ENABLED="+cgoEnabled, "GOTOOLCHAIN=local")
	return cmd
}

// goListExports returns a lister that runs `go list -e -export -deps` (see
// goListCmd).
func goListExports(goroot string, cgo bool) func([]string) []goListedPackage {
	return func(paths []string) []goListedPackage {
		args := append([]string{"list", "-e", "-export", "-deps", "-json=ImportPath,Export", "--"}, paths...)
		// A package that does not build is listed without an export file, and
		// a failed run (exit status) still prints the packages it could list.
		cmd := goListCmd(goroot, cgo, args...)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		out, err := cmd.Output()
		if err != nil && profiler.Enabled {
			fmt.Fprintf(os.Stderr, "  [go-types] go list -export: %v\n%s", err, stderr.Bytes())
		}
		return decodeGoList(out)
	}
}

// decodeGoList decodes the stream of JSON objects `go list -json` prints,
// keeping the ones before any malformed one.
func decodeGoList(out []byte) []goListedPackage {
	var pkgs []goListedPackage
	dec := json.NewDecoder(bytes.NewReader(out))
	for dec.More() {
		var pkg goListedPackage
		if dec.Decode(&pkg) != nil {
			break
		}
		pkgs = append(pkgs, pkg)
	}
	return pkgs
}
