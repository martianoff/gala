package module

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInternalPackageRoot(t *testing.T) {
	cases := []struct {
		name       string
		importPath string
		wantRoot   string
		wantIsInt  bool
	}{
		{
			name:       "no internal element",
			importPath: "example.com/mod/sub/pkg",
			wantIsInt:  false,
		},
		{
			name:       "internal is only a prefix of an element",
			importPath: "example.com/mod/internalize/pkg",
			wantIsInt:  false,
		},
		{
			name:       "internal is only a suffix of an element",
			importPath: "example.com/mod/nointernal/pkg",
			wantIsInt:  false,
		},
		{
			name:       "top-level internal in a module",
			importPath: "example.com/mod/internal/secret",
			wantRoot:   "example.com/mod",
			wantIsInt:  true,
		},
		{
			name:       "nested internal binds to its own parent",
			importPath: "example.com/mod/sub/internal/deep",
			wantRoot:   "example.com/mod/sub",
			wantIsInt:  true,
		},
		{
			name:       "internal as the final element",
			importPath: "example.com/mod/internal",
			wantRoot:   "example.com/mod",
			wantIsInt:  true,
		},
		{
			name:       "unqualified internal yields the empty root",
			importPath: "internal/secret",
			wantRoot:   "",
			wantIsInt:  true,
		},
		{
			// cmd/go's findInternal takes the LAST internal element, so the
			// binding root is the innermost — and narrowest — one.
			name:       "doubly nested internal takes the innermost",
			importPath: "a/internal/b/internal/c",
			wantRoot:   "a/internal/b",
			wantIsInt:  true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root, isInternal := InternalPackageRoot(tc.importPath)
			assert.Equal(t, tc.wantIsInt, isInternal, "isInternal for %q", tc.importPath)
			if tc.wantIsInt {
				assert.Equal(t, tc.wantRoot, root, "root for %q", tc.importPath)
			}
		})
	}
}

func TestAllowsInternalImport(t *testing.T) {
	cases := []struct {
		name       string
		importer   string
		importPath string
		want       bool
	}{
		{
			name:       "non-internal import is always allowed",
			importer:   "example.com/other",
			importPath: "example.com/mod/sub",
			want:       true,
		},
		{
			name:       "the internal parent itself may import it",
			importer:   "example.com/mod",
			importPath: "example.com/mod/internal/secret",
			want:       true,
		},
		{
			name:       "a package below the parent may import it",
			importer:   "example.com/mod/cmd/app",
			importPath: "example.com/mod/internal/secret",
			want:       true,
		},
		{
			name:       "the internal package's own sibling may import it",
			importer:   "example.com/mod/internal/other",
			importPath: "example.com/mod/internal/secret",
			want:       true,
		},
		{
			name:       "a different module may not import it",
			importer:   "example.com/consumer",
			importPath: "example.com/mod/internal/secret",
			want:       false,
		},
		{
			name:       "the parent's parent may not import a nested internal",
			importer:   "example.com/mod",
			importPath: "example.com/mod/sub/internal/deep",
			want:       false,
		},
		{
			name:       "a sibling subtree may not import a nested internal",
			importer:   "example.com/mod/other",
			importPath: "example.com/mod/sub/internal/deep",
			want:       false,
		},
		{
			// "example.com/modular" must not be treated as living inside
			// "example.com/mod" just because the string is a prefix.
			name:       "prefix match must respect element boundaries",
			importer:   "example.com/modular",
			importPath: "example.com/mod/internal/secret",
			want:       false,
		},
		{
			name:       "unknown importer fails open",
			importer:   "",
			importPath: "example.com/mod/internal/secret",
			want:       true,
		},
		{
			// std resolves through the same mechanism as any other GALA
			// library; there is no exemption for martianoff/gala/*.
			name:       "std internal is not exempt",
			importer:   "example.com/app",
			importPath: "martianoff/gala/internal/detail",
			want:       false,
		},
		{
			name:       "std package may import std internal",
			importer:   "martianoff/gala/collection_immutable",
			importPath: "martianoff/gala/internal/detail",
			want:       true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := AllowsInternalImport(tc.importer, tc.importPath)
			assert.Equal(t, tc.want, got,
				"AllowsInternalImport(%q, %q)", tc.importer, tc.importPath)
		})
	}
}

func TestDirContainsInternalViolation(t *testing.T) {
	root := t.TempDir()
	j := func(parts ...string) string { return filepath.Join(append([]string{root}, parts...)...) }

	cases := []struct {
		name        string
		importerDir string
		importedDir string
		want        bool
	}{
		{
			name:        "importer above the internal parent is a violation",
			importerDir: j("proj"),
			importedDir: j("proj", "sub", "internal", "deep"),
			want:        true,
		},
		{
			name:        "importer in a sibling subtree is a violation",
			importerDir: j("proj", "other"),
			importedDir: j("proj", "sub", "internal", "deep"),
			want:        true,
		},
		{
			name:        "the internal parent itself is allowed",
			importerDir: j("proj", "sub"),
			importedDir: j("proj", "sub", "internal", "deep"),
			want:        false,
		},
		{
			name:        "a package below the internal parent is allowed",
			importerDir: j("proj", "sub", "nested", "pkg"),
			importedDir: j("proj", "sub", "internal", "deep"),
			want:        false,
		},
		{
			name:        "no internal directory on disk means nothing to enforce",
			importerDir: j("proj"),
			importedDir: j("proj", "sub", "public"),
			want:        false,
		},
		{
			name:        "unknown importer directory reports nothing",
			importerDir: "",
			importedDir: j("proj", "sub", "internal", "deep"),
			want:        false,
		},
		{
			name:        "unknown imported directory reports nothing",
			importerDir: j("proj"),
			importedDir: "",
			want:        false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := DirContainsInternalViolation(tc.importerDir, tc.importedDir)
			assert.Equal(t, tc.want, got,
				"DirContainsInternalViolation(%q, %q)", tc.importerDir, tc.importedDir)
		})
	}
}

// The Bazel case that motivated the two-signal design: a gala_library declares
// importpath "martianoff/gala/greeting" for sources that live under
// examples/internal_package/greeting/. The import path alone says "violation";
// the layout says otherwise, and the layout is right.
func TestDirContainsInternalViolation_DeclaredImportPathMayNotMirrorLayout(t *testing.T) {
	root := t.TempDir()
	greetingDir := filepath.Join(root, "examples", "internal_package", "greeting")
	formatDir := filepath.Join(greetingDir, "internal", "format")

	// Signal 1 (import paths) would reject this...
	assert.False(t,
		AllowsInternalImport("martianoff/gala/examples/internal_package/greeting",
			"martianoff/gala/greeting/internal/format"),
		"derived importer path disagrees with the declared importpath")

	// ...but signal 2 (layout) shows the importer is the internal parent.
	assert.False(t, DirContainsInternalViolation(greetingDir, formatDir),
		"a package importing its own internal/ subtree must never be reported")
}

func TestPackageImportPath(t *testing.T) {
	moduleRoot := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(moduleRoot, "gala.mod"),
		[]byte("module example.com/mod\n\ngala 1.0\n"), 0644))

	subDir := filepath.Join(moduleRoot, "sub", "internal", "deep")
	require.NoError(t, os.MkdirAll(subDir, 0755))

	r := &Resolver{moduleRoot: moduleRoot, moduleName: "example.com/mod"}

	t.Run("file at the module root", func(t *testing.T) {
		got := r.PackageImportPath(filepath.Join(moduleRoot, "main.gala"))
		assert.Equal(t, "example.com/mod", got)
	})

	t.Run("file in a nested package", func(t *testing.T) {
		got := r.PackageImportPath(filepath.Join(subDir, "deep.gala"))
		assert.Equal(t, "example.com/mod/sub/internal/deep", got)
	})

	t.Run("path need not exist on disk", func(t *testing.T) {
		// PackageImportPath is pure path arithmetic — it must not stat.
		got := r.PackageImportPath(filepath.Join(moduleRoot, "not", "created", "yet.gala"))
		assert.Equal(t, "example.com/mod/not/created", got)
	})

	t.Run("file outside the module root yields unknown", func(t *testing.T) {
		outside := filepath.Join(t.TempDir(), "stray.gala")
		assert.Equal(t, "", r.PackageImportPath(outside))
	})

	t.Run("unknown module yields unknown", func(t *testing.T) {
		bare := &Resolver{}
		assert.Equal(t, "", bare.PackageImportPath(filepath.Join(moduleRoot, "main.gala")))
	})

	t.Run("empty path yields unknown", func(t *testing.T) {
		assert.Equal(t, "", r.PackageImportPath(""))
	})
}
