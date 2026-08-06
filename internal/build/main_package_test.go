package build

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// writeGen writes a generated Go file at a gen-relative path, creating parents.
func writeGen(t *testing.T, b *Builder, rel, src string) {
	t.Helper()
	path := filepath.Join(b.workspace.GenDir, filepath.FromSlash(rel))
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(src), 0o644))
}

// newTestBuilder returns a Builder over an empty temp project with an isolated
// GALA_HOME, so the workspace it creates is scoped to the test.
func newTestBuilder(t *testing.T) *Builder {
	t.Helper()
	t.Setenv("GALA_HOME", t.TempDir())
	projectDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(projectDir, "gala.mod"),
		[]byte("module example.com/proj\n\ngala 0.74.1\n"), 0o644))
	b, err := NewBuilder(projectDir, "test", false)
	require.NoError(t, err)
	require.NoError(t, b.workspace.Ensure())
	return b
}

// A project root that holds no package of its own is the conventional
// cmd/<name> layout, not an error: the single main package below it is what the
// user means by `gala build`. Before this, the build fell through to
// `go build ./gen` on a directory with no Go files and surfaced the Go
// toolchain's "no Go files in <workspace hash>/gen".
func TestBuildTarget(t *testing.T) {
	t.Run("root is package main", func(t *testing.T) {
		b := newTestBuilder(t)
		writeGen(t, b, "main.gen.go", "package main\n\nfunc main() {}\n")
		writeGen(t, b, "internal/store/store.gen.go", "package store\n")

		target, err := b.buildTarget()
		require.NoError(t, err)
		require.Equal(t, "./gen", target)
	})

	t.Run("root is a library", func(t *testing.T) {
		b := newTestBuilder(t)
		writeGen(t, b, "lib.gen.go", "package proj\n")

		target, err := b.buildTarget()
		require.NoError(t, err)
		require.Empty(t, target, "libraries compile-check instead of building")
	})

	t.Run("empty root, one main below", func(t *testing.T) {
		b := newTestBuilder(t)
		writeGen(t, b, "cmd/galakv/main.gen.go", "package main\n\nfunc main() {}\n")
		writeGen(t, b, "internal/store/store.gen.go", "package store\n")

		target, err := b.buildTarget()
		require.NoError(t, err)
		require.Equal(t, "./gen/cmd/galakv", target)
	})

	t.Run("empty root, libraries only", func(t *testing.T) {
		b := newTestBuilder(t)
		writeGen(t, b, "internal/store/store.gen.go", "package store\n")

		target, err := b.buildTarget()
		require.NoError(t, err)
		require.Empty(t, target)
	})

	t.Run("empty root, several mains below", func(t *testing.T) {
		b := newTestBuilder(t)
		writeGen(t, b, "cmd/galakv/main.gen.go", "package main\n\nfunc main() {}\n")
		writeGen(t, b, "bench/shardindex/main.gen.go", "package main\n\nfunc main() {}\n")

		_, err := b.buildTarget()
		var multi *MultipleMainPackagesError
		require.ErrorAs(t, err, &multi)
		require.Equal(t, []string{"bench/shardindex", "cmd/galakv"}, multi.Candidates)
		require.Contains(t, multi.Error(), "./cmd/galakv")
	})

	t.Run("multi-package mode builds the synthesized consumer", func(t *testing.T) {
		b := newTestBuilder(t)
		b.SetSourceDir(filepath.Join(b.workspace.ProjectDir, "cmd", "app"))
		writeGen(t, b, "cmd/main/main.gen.go", "package main\n\nfunc main() {}\n")

		target, err := b.buildTarget()
		require.NoError(t, err)
		require.Equal(t, "./gen/cmd/main", target)
	})
}

// rootPackageName must not answer with a subpackage's name. A cmd/<name>
// layout has no root package, and claiming it is "main" because cmd/app
// sorted first made `gala test` write a root-level test runner referencing
// tests that live in a subpackage — "undefined: TestXxx".
func TestRootPackageName_EmptyWhenRootHasNoSource(t *testing.T) {
	projectDir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(projectDir, "cmd", "app"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(projectDir, "internal", "store"), 0o755))
	mainFile := filepath.Join(projectDir, "cmd", "app", "main.gala")
	storeFile := filepath.Join(projectDir, "internal", "store", "store.gala")
	require.NoError(t, os.WriteFile(mainFile, []byte("package main\n"), 0o644))
	require.NoError(t, os.WriteFile(storeFile, []byte("package store\n"), 0o644))

	require.Empty(t, rootPackageName([]string{mainFile, storeFile}, projectDir),
		"a subpackage's name must not stand in for the root's")

	// A root that does declare a package is still reported.
	rootFile := filepath.Join(projectDir, "app.gala")
	require.NoError(t, os.WriteFile(rootFile, []byte("package main\n"), 0o644))
	require.Equal(t, "main", rootPackageName([]string{mainFile, rootFile}, projectDir))
}

// A nested Go module's sources are copied into the workspace but resolve their
// dependencies through their own go.mod, so they are not buildable here. Naming
// one as a build target would hand the user a command that cannot work.
func TestMainPackageDirs_SkipsNestedGoModules(t *testing.T) {
	b := newTestBuilder(t)
	writeGen(t, b, "cmd/galakv/main.gen.go", "package main\n\nfunc main() {}\n")
	writeGen(t, b, "bench/report/main.go", "package main\n\nfunc main() {}\n")
	require.NoError(t, os.MkdirAll(filepath.Join(b.workspace.ProjectDir, "bench", "report"), 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(b.workspace.ProjectDir, "bench", "report", "go.mod"),
		[]byte("module example.com/report\n\ngo 1.22\n"), 0o644))

	mains, err := b.workspace.MainPackageDirs()
	require.NoError(t, err)
	require.Equal(t, []string{"cmd/galakv"}, mains)

	// With the nested module skipped there is exactly one candidate, so the
	// build resolves instead of asking the user to disambiguate.
	target, err := b.buildTarget()
	require.NoError(t, err)
	require.Equal(t, "./gen/cmd/galakv", target)
}

func TestPackageNameIn(t *testing.T) {
	dir := t.TempDir()
	require.Empty(t, PackageNameIn(dir), "no Go source")
	require.Empty(t, PackageNameIn(filepath.Join(dir, "missing")), "unreadable dir")

	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.go"),
		[]byte("// Code generated by GALA.\n\npackage store\n\nimport \"fmt\"\n"), 0o644))
	require.Equal(t, "store", PackageNameIn(dir), "the clause is found past comments")
}
