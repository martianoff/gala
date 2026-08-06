package lsp_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/owenrumney/go-lsp/lsp"

	lspserver "martianoff/gala/internal/lsp"
)

// The signature under test in every case below. Line 4 of the dispatch file.
const (
	dispatchSigLine = 4
	dispatchSig     = "func Reply() resp.RespValue = resp.NullBulk()"
)

const respValueSrc = "package resp\n" +
	"\n" +
	"sealed type RespValue {\n" +
	"    case SimpleString(Text string)\n" +
	"    case NullBulk\n" +
	"}\n"

// dispatchSrc imports respPath and returns a value from it.
func dispatchSrc(respPath string) string {
	return "package command\n" +
		"\n" +
		"import \"" + respPath + "\"\n" +
		"\n" +
		dispatchSig + "\n"
}

// Packages are imported by their module-qualified path
// (github.com/you/proj/internal/resp) while living at internal/resp under the
// owning module's root. Definition has to strip the prefix of the module that
// owns the path — the project's own, or a dependency's, both read from
// gala.mod — before the import names anything on disk. Without that step the
// package name and every pkg.Symbol reference answered "cannot find
// declaration".
func TestDefinition_ModuleQualifiedImport(t *testing.T) {
	// Cursor one character inside each identifier of interest.
	cases := []struct {
		what string
		col  int
	}{
		{"package qualifier", strings.Index(dispatchSig, "resp.") + 1},
		{"qualified type", strings.Index(dispatchSig, "RespValue") + 1},
		{"qualified sealed-case constructor", strings.Index(dispatchSig, "NullBulk") + 1},
	}

	// The project's own packages, and a GALA dependency's packages, resolve by
	// the same mechanism against different module roots.
	for _, project := range []struct {
		name     string
		setup    func(t *testing.T) (root string, wantDir string)
		respPath string
	}{
		{
			name:     "project's own package",
			respPath: "github.com/example/kv/internal/resp",
			setup: func(t *testing.T) (string, string) {
				root := createTestProject(t, []testProjectFile{
					{Name: "gala.mod", Src: "module github.com/example/kv\n\ngala 0.74.1\n"},
					{Name: "internal/resp/value.gala", Src: respValueSrc},
					{Name: "internal/command/dispatch.gala", Src: dispatchSrc("github.com/example/kv/internal/resp")},
				})
				return root, filepath.Join(root, "internal", "resp")
			},
		},
		{
			name:     "dependency's package",
			respPath: "github.com/example/proto/internal/resp",
			setup: func(t *testing.T) (string, string) {
				// A GALA dependency's sources live in the version-keyed module
				// cache under GALA_HOME, which the handler stats when reading
				// gala.mod's require list.
				galaHome := t.TempDir()
				t.Setenv("GALA_HOME", galaHome)
				depDir := filepath.Join(galaHome, "pkg", "mod", "github.com", "example", "proto@v1.2.0", "internal", "resp")
				if err := os.MkdirAll(depDir, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(depDir, "value.gala"), []byte(respValueSrc), 0o644); err != nil {
					t.Fatal(err)
				}
				root := createTestProject(t, []testProjectFile{
					{Name: "gala.mod", Src: "module github.com/example/kv\n\ngala 0.74.1\n\nrequire github.com/example/proto v1.2.0\n"},
					{Name: "internal/command/dispatch.gala", Src: dispatchSrc("github.com/example/proto/internal/resp")},
				})
				return root, depDir
			},
		},
	} {
		t.Run(project.name, func(t *testing.T) {
			root, wantDir := project.setup(t)
			h, handler := newHarnessWithHandler(t)
			initializeAtRoot(t, handler, root)
			uri := openProjectFile(t, h, root, "internal/command/dispatch.gala")

			for _, tc := range cases {
				t.Run(tc.what, func(t *testing.T) {
					locs, err := h.Definition(uri, dispatchSigLine, tc.col)
					if err != nil {
						t.Fatal(err)
					}
					if len(locs) == 0 {
						t.Fatalf("no definition found for %s", tc.what)
					}
					want := strings.ToLower(filepath.ToSlash(filepath.Join(wantDir, "value.gala")))
					got := strings.ToLower(filepath.ToSlash(string(locs[0].URI)))
					if !strings.HasSuffix(got, want) {
						t.Fatalf("%s: expected a location in %s, got %s", tc.what, want, locs[0].URI)
					}
				})
			}
		})
	}
}

// The GALA compiler repo is itself a module (`module martianoff/gala`) whose
// stdlib packages sit at the repo root, so when a gala.mod names it, stdlib
// imports resolve through the same module-root mechanism as anyone else's —
// no prefix literal involved. (The literal survives only for the case with no
// owning module at all, covered by TestDefinition_GoInteropPackageAndFunc.)
func TestDefinition_StdlibImportUsesTheSameModuleMechanism(t *testing.T) {
	root := createTestProject(t, []testProjectFile{
		{Name: "gala.mod", Src: "module martianoff/gala\n\ngala 0.74.1\n"},
		{Name: "collection_immutable/array.gala", Src: "package collection_immutable\n\ntype Marker struct {}\n"},
		{Name: "app/main.gala", Src: "package main\n\nimport \"martianoff/gala/collection_immutable\"\n\nfunc Run() = Println(collection_immutable.Marker())\n"},
	})

	h, handler := newHarnessWithHandler(t)
	initializeAtRoot(t, handler, root)
	uri := openProjectFile(t, h, root, "app/main.gala")

	// Line 4, cursor inside the "collection_immutable" qualifier.
	locs, err := h.Definition(uri, 4, strings.Index("func Run() = Println(collection_immutable.Marker())", "collection_immutable")+1)
	if err != nil {
		t.Fatal(err)
	}
	if len(locs) == 0 {
		t.Fatal("no definition found for the stdlib package qualifier")
	}
	// Assert the full temp-project path: the real GALA repo is on the test
	// harness's search paths and has a collection_immutable/array.gala of its
	// own, which a suffix-only check would happily accept.
	want := strings.ToLower(filepath.ToSlash(filepath.Join(root, "collection_immutable", "array.gala")))
	if got := strings.ToLower(filepath.ToSlash(string(locs[0].URI))); !strings.HasSuffix(got, want) {
		t.Fatalf("expected %s, got %s", want, locs[0].URI)
	}
}

// initializeAtRoot drives the Initialize handshake with a project root, the way
// an IDE does — this is what makes the handler read the project's gala.mod.
// The harness's own handshake (servertest.New) sends no root; go-lsp v0.1.4
// has no option to supply one.
func initializeAtRoot(t *testing.T, handler *lspserver.GalaHandler, root string) {
	t.Helper()
	rootURI := fileURIForPath(root)
	if _, err := handler.Initialize(context.Background(), &lsp.InitializeParams{RootURI: &rootURI}); err != nil {
		t.Fatal(err)
	}
}
