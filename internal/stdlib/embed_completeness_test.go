package stdlib

import (
	"path"
	"sort"
	"strings"
	"testing"
)

// embedGenrule is where a missing file has to be added; named in every failure.
const embedGenrule = "the generate_embedded srcs in internal/stdlib/BUILD.bazel"

// sourceTreeByPackage groups sourceTreeFiles (generated from each stdlib
// package's :shipped_sources glob) into package name -> set of file names.
func sourceTreeByPackage(t *testing.T) map[string]map[string]bool {
	t.Helper()
	if len(sourceTreeFiles) == 0 {
		t.Fatal("sourceTreeFiles is empty — the //internal/stdlib:source_tree_files genrule produced no paths")
	}
	byPkg := make(map[string]map[string]bool)
	for _, p := range sourceTreeFiles {
		p = strings.ReplaceAll(p, "\\", "/")
		pkg := path.Base(path.Dir(p))
		if byPkg[pkg] == nil {
			byPkg[pkg] = make(map[string]bool)
		}
		byPkg[pkg][path.Base(p)] = true
	}
	return byPkg
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// TestEmbeddedStdlibMatchesSourceTree is the general completeness guard for the
// CLI's embedded stdlib snapshot. `bazel test` builds everything against the
// repo's stdlib sources, so a stdlib file that exists in the repo but is missing
// from generate_embedded is invisible there — yet the released CLI (`gala build`,
// `gala run`) only sees the embedded snapshot, and reports the file's symbols as
// undefined. Instead of a hand-maintained list of expected files, this test
// derives the expectation from the source tree itself: every non-test `.gala`
// file must be embedded as source (for the analyzer) and as its transpiled
// `.gen.go` (for compilation), and every hand-written `.go` file as-is.
func TestEmbeddedStdlibMatchesSourceTree(t *testing.T) {
	source := sourceTreeByPackage(t)

	// Every package the CLI knows about must be covered by the source-tree
	// list, or its files would silently escape this check.
	for _, pkg := range sortedKeys(PackageImportPaths) {
		if _, ok := source[pkg]; !ok {
			t.Errorf("stdlib package %q is not covered by the completeness check: add a :shipped_sources filegroup to %s/BUILD.bazel and list \"//%s:shipped_sources\" in the source_tree_files genrule in internal/stdlib/BUILD.bazel", pkg, pkg, pkg)
		}
	}

	for _, pkg := range sortedKeys(source) {
		if _, ok := PackageImportPaths[pkg]; !ok {
			t.Errorf("stdlib package %q has sources but no import path: register it in PackageImportPaths (cmd/stdlib_gen/main.go)", pkg)
		}
		embedded, ok := EmbeddedPackages[pkg]
		if !ok {
			t.Errorf("stdlib package %q is not embedded at all: add its sources to %s", pkg, embedGenrule)
			continue
		}
		for _, file := range sortedKeys(source[pkg]) {
			if strings.HasSuffix(file, ".gala") {
				base := strings.TrimSuffix(file, ".gala")
				if _, ok := embedded[file]; !ok {
					t.Errorf("%s/%s is missing from the CLI's embedded stdlib: add \"//%s:%s\" to %s (and to exports_files in %s/BUILD.bazel)", pkg, file, pkg, file, embedGenrule, pkg)
				}
				if _, ok := embedded[base+".gen.go"]; !ok {
					t.Errorf("%s/%s has no transpiled Go in the CLI's embedded stdlib: add \"//%s:%s_go\" (its gala_bootstrap_transpile target) to %s", pkg, file, pkg, base, embedGenrule)
				}
				continue
			}
			if _, ok := embedded[file]; !ok {
				t.Errorf("%s/%s is missing from the CLI's embedded stdlib: add \"//%s:%s\" to %s (and to exports_files in %s/BUILD.bazel)", pkg, file, pkg, file, embedGenrule, pkg)
			}
		}
	}
}

// TestEmbeddedStdlibHasNoStaleFiles is the reverse direction: every embedded
// file must correspond to a source file that still exists, so a renamed or
// deleted stdlib file cannot linger in the CLI's snapshot.
func TestEmbeddedStdlibHasNoStaleFiles(t *testing.T) {
	source := sourceTreeByPackage(t)
	for _, pkg := range sortedKeys(EmbeddedPackages) {
		for _, file := range sortedKeys(EmbeddedPackages[pkg]) {
			origin := file
			if strings.HasSuffix(file, ".gen.go") {
				origin = strings.TrimSuffix(file, ".gen.go") + ".gala"
			}
			if !source[pkg][origin] {
				t.Errorf("embedded %s/%s has no source file %s/%s in the repo: remove it from %s", pkg, file, pkg, origin, embedGenrule)
			}
		}
	}
}
