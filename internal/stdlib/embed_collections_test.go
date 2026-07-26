package stdlib

import (
	"strings"
	"testing"
)

// TestCollectionFilesEmbedded guards against a stdlib source file existing in the
// repo but being forgotten in the embed srcs list (internal/stdlib/BUILD.bazel's
// generate_embedded genrule). That drift is invisible to `bazel test` — which
// builds examples against the repo's stdlib tree — but breaks the released CLI,
// whose analyzer/build only sees the embedded snapshot.
//
// Concretely: `collection_mutable`'s treemap was present in the repo but missing
// from the embed, so `gala build` reported `undefined: TreeMapOf` (the analyzer
// couldn't resolve the symbol, dropped the import, and Go compilation failed)
// even though the bazel example suite passed.
func TestCollectionFilesEmbedded(t *testing.T) {
	// The immutable and mutable collection packages both provide the full set of
	// collection types; each must be embedded as GALA source (for the analyzer)
	// and transpiled Go (for compilation).
	collections := []string{"array", "list", "hashmap", "hashset", "treeset", "treemap"}
	for _, pkg := range []string{"collection_immutable", "collection_mutable"} {
		files, ok := EmbeddedPackages[pkg]
		if !ok {
			t.Fatalf("package %q is not embedded at all", pkg)
		}
		for _, c := range collections {
			for _, name := range []string{c + ".gala", c + ".gen.go"} {
				if _, ok := files[name]; !ok {
					t.Errorf("%s embed is missing %q — add it to the generate_embedded srcs in internal/stdlib/BUILD.bazel", pkg, name)
				}
			}
		}
	}
}

// TestSubprocessAsyncEmbedded guards the same drift for the subprocess package's
// async surface (async.gala): its methods live in a SEPARATE source file from
// subprocess.gala, so it is easy to embed the base package while forgetting the
// async file. When that happens the released CLI's analyzer never sees
// ReadLineAsync / WriteLineAsync / KillAfter and `gala build` reports them as
// undefined — even though `bazel test` (repo-source builds) passes.
func TestSubprocessAsyncEmbedded(t *testing.T) {
	files, ok := EmbeddedPackages["subprocess"]
	if !ok {
		t.Fatalf("package \"subprocess\" is not embedded at all")
	}
	for _, name := range []string{"async.gala", "async.gen.go"} {
		if _, ok := files[name]; !ok {
			t.Errorf("subprocess embed is missing %q — add it to the generate_embedded srcs in internal/stdlib/BUILD.bazel", name)
		}
	}
}

// TestSubprocessAsyncMethodsEmbedded is a symbol-level check: the embedded async
// GALA source actually carries its public method surface, so a content
// regression (not just a missing filename) is caught too.
func TestSubprocessAsyncMethodsEmbedded(t *testing.T) {
	content, ok := GetPackageContent("subprocess", "async.gala")
	if !ok {
		t.Fatalf("subprocess: async.gala not embedded")
	}
	for _, sym := range []string{"ReadLineAsync", "WriteLineAsync", "CloseStdinAsync", "KillAfter"} {
		if !strings.Contains(content, sym) {
			t.Errorf("subprocess async.gala embed does not define %s", sym)
		}
	}
}

// TestCollectionConstructorsEmbedded is a symbol-level sanity check: the embedded
// GALA source for treemap actually carries its public constructor, so a content
// regression (not just a missing filename) is caught too.
func TestCollectionConstructorsEmbedded(t *testing.T) {
	for _, pkg := range []string{"collection_immutable", "collection_mutable"} {
		content, ok := GetPackageContent(pkg, "treemap.gala")
		if !ok {
			t.Fatalf("%s: treemap.gala not embedded", pkg)
		}
		if !strings.Contains(content, "func TreeMapOf") {
			t.Errorf("%s treemap.gala embed does not define func TreeMapOf", pkg)
		}
	}
}
