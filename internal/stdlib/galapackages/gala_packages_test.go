// Package galapackages checks that Bazel treats every stdlib package with
// .gala sources as a GALA package; see gala_packages.bzl.
package galapackages

import "testing"

type stdlibPackage struct {
	dir     string
	hasGala bool
	isGala  bool
}

// TestStdlibGalaPackagesAreMarked requires every stdlib package with .gala
// sources to be a GALA package to rules_gala, and no pure-Go one to be.
// //internal/stdlib's TestEmbeddedStdlibMatchesSourceTree checks that
// STDLIB_PACKAGES, the list behind stdlibPackages, covers every registered
// package.
func TestStdlibGalaPackagesAreMarked(t *testing.T) {
	if len(stdlibPackages) == 0 {
		t.Fatal("stdlibPackages is empty: //internal/stdlib/galapackages:gala_packages checked no package")
	}
	for _, p := range stdlibPackages {
		switch {
		case p.hasGala && !p.isGala:
			t.Errorf("stdlib package %q has .gala sources, but Bazel does not mark //%s as a GALA package, so transpiles that depend on it would read its types from its compiled Go instead of its .gala source. Its go_library is wired by hand: add `embed = [\":gala_package\"]` to it and call stdlib_gala_package() (from //internal/stdlib:shipped_sources.bzl) in %s/BUILD.bazel", p.dir, p.dir, p.dir)
		case !p.hasGala && p.isGala:
			t.Errorf("stdlib package %q has no .gala sources, but Bazel marks //%s as a GALA package, so transpiles that depend on it would get none of its Go types: remove the gala_package marker from its go_library in %s/BUILD.bazel", p.dir, p.dir, p.dir)
		}
	}
}
