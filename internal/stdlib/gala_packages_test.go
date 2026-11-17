package stdlib

import (
	"slices"
	"strings"
	"testing"
)

// TestStdlibGalaPackagesAreMarked requires every stdlib package with .gala
// sources to be a GALA package to rules_gala, and no pure-Go one to be; see
// gala_packages.bzl. TestEmbeddedStdlibMatchesSourceTree checks that the
// package list behind bazelGalaPackages covers every registered package.
func TestStdlibGalaPackagesAreMarked(t *testing.T) {
	source := sourceTreeByPackage(t)
	for _, pkg := range sortedKeys(bazelGalaPackages) {
		hasGala := slices.ContainsFunc(sortedKeys(source[pkg]), func(file string) bool {
			return strings.HasSuffix(file, ".gala")
		})
		isGala := bazelGalaPackages[pkg]
		switch {
		case hasGala && !isGala:
			t.Errorf("stdlib package %q has .gala sources, but Bazel does not mark //%s as a GALA package, so transpiles that depend on it would read its types from its compiled Go instead of its .gala source. Its go_library is wired by hand: add `embed = [\":gala_package\"]` to it and call stdlib_gala_package() (from //internal/stdlib:shipped_sources.bzl) in %s/BUILD.bazel", pkg, pkg, pkg)
		case !hasGala && isGala:
			t.Errorf("stdlib package %q has no .gala sources, but Bazel marks //%s as a GALA package, so transpiles that depend on it would get none of its Go types: remove the gala_package marker from its go_library in %s/BUILD.bazel", pkg, pkg, pkg)
		}
	}
}
