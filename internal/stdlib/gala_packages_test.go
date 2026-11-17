package stdlib

import (
	"strings"
	"testing"
)

// TestStdlibGalaPackagesAreMarked guards the GALA-package marker of the
// hand-wired stdlib packages. Under go_export_data the transpile rules read a
// Go library's types from its compiled export data unless rules_gala sees it
// as a GALA package; a GALA package's types must come from its .gala source.
// gala_library marks its library itself, but a package wired by hand
// (gala_bootstrap_transpile plus a plain go_library) is marked only if its
// go_library embeds :gala_package. Forgetting that compiles and mostly works,
// with the transpiler silently typing the package from its generated Go.
// bazelGalaPackages holds rules_gala's own verdict for every stdlib library
// (//internal/stdlib:gala_packages); this test holds it against the source
// tree.
func TestStdlibGalaPackagesAreMarked(t *testing.T) {
	source := sourceTreeByPackage(t)

	for _, pkg := range sortedKeys(PackageImportPaths) {
		if _, ok := bazelGalaPackages[pkg]; !ok {
			t.Errorf("stdlib package %q is not covered by the GALA-package marker check: list \"//%s\" in the libraries of the gala_packages target in internal/stdlib/BUILD.bazel", pkg, pkg)
		}
	}

	for _, pkg := range sortedKeys(bazelGalaPackages) {
		hasGala := false
		for file := range source[pkg] {
			if strings.HasSuffix(file, ".gala") {
				hasGala = true
				break
			}
		}
		isGala := bazelGalaPackages[pkg]
		switch {
		case hasGala && !isGala:
			t.Errorf("stdlib package %q has .gala sources, but Bazel does not mark //%s as a GALA package, so transpiles that depend on it would read its types from its compiled Go instead of its .gala source. Its go_library is wired by hand: add `embed = [\":gala_package\"]` to it and call stdlib_gala_package() (from //internal/stdlib:shipped_sources.bzl) in %s/BUILD.bazel", pkg, pkg, pkg)
		case !hasGala && isGala:
			t.Errorf("stdlib package %q has no .gala sources, but Bazel marks //%s as a GALA package, so transpiles that depend on it would get none of its Go types: remove the gala_package marker from its go_library in %s/BUILD.bazel", pkg, pkg, pkg)
		}
	}
}
