package build

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"martianoff/gala/internal/depman/mod"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveGoModuleForImport(t *testing.T) {
	// A dependency (e.g. gala-tui) that declares golang.org/x/term but imports
	// the package golang.org/x/sys/windows (provided by module golang.org/x/sys,
	// which it does NOT declare).
	goReqs := []mod.Require{
		{Path: "golang.org/x/term", Version: "v0.25.0", Go: true},
		{Path: "golang.org/x/sys", Version: "v0.26.0", Go: true},
		{Path: "github.com/foo/bar", Version: "v1.2.3", Go: true},
	}

	tests := []struct {
		name        string
		importPath  string
		wantModule  string
		wantVersion string
		wantOK      bool
	}{
		{
			name:        "import path is the module root",
			importPath:  "golang.org/x/term",
			wantModule:  "golang.org/x/term",
			wantVersion: "v0.25.0",
			wantOK:      true,
		},
		{
			name:        "package path resolves to module prefix (BUG-1)",
			importPath:  "golang.org/x/sys/windows",
			wantModule:  "golang.org/x/sys",
			wantVersion: "v0.26.0",
			wantOK:      true,
		},
		{
			name:        "nested subpackage resolves to declared module",
			importPath:  "github.com/foo/bar/baz/qux",
			wantModule:  "github.com/foo/bar",
			wantVersion: "v1.2.3",
			wantOK:      true,
		},
		{
			name:       "no declared module prefix is omitted",
			importPath: "golang.org/x/sys/windows",
			wantOK:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reqs := goReqs
			if tt.name == "no declared module prefix is omitted" {
				// Drop the golang.org/x/sys declaration so the import has no
				// declared prefix and must be omitted (resolved by `go mod tidy`).
				reqs = []mod.Require{{Path: "golang.org/x/term", Version: "v0.25.0", Go: true}}
			}
			gotModule, gotVersion, gotOK := resolveGoModuleForImport(tt.importPath, reqs)
			assert.Equal(t, tt.wantOK, gotOK)
			if tt.wantOK {
				assert.Equal(t, tt.wantModule, gotModule)
				assert.Equal(t, tt.wantVersion, gotVersion)
			}
		})
	}
}

func TestResolveGoModuleForImport_PrefixBoundary(t *testing.T) {
	// A module path must match on a path boundary: golang.org/x/term must NOT
	// match the import golang.org/x/terminal.
	goReqs := []mod.Require{{Path: "golang.org/x/term", Version: "v0.25.0", Go: true}}
	_, _, ok := resolveGoModuleForImport("golang.org/x/terminal", goReqs)
	assert.False(t, ok, "golang.org/x/term must not match golang.org/x/terminal")
}

func TestCopyNonGalaFiles(t *testing.T) {
	// Create source directory structure simulating a GALA module with Go subpackages
	srcDir := t.TempDir()
	dstDir := t.TempDir()

	// Create source files
	// Root .gala files (should be skipped - already transpiled)
	require.NoError(t, os.WriteFile(filepath.Join(srcDir, "server.gala"), []byte("package server"), 0644))
	// Root .go file (should be copied)
	require.NoError(t, os.WriteFile(filepath.Join(srcDir, "helpers.go"), []byte("package server"), 0644))
	// gala.mod (should be skipped)
	require.NoError(t, os.WriteFile(filepath.Join(srcDir, "gala.mod"), []byte("module test"), 0644))
	// go.mod (should be skipped - we generate our own)
	require.NoError(t, os.WriteFile(filepath.Join(srcDir, "go.mod"), []byte("module test"), 0644))

	// Go subpackage directory
	httpcore := filepath.Join(srcDir, "httpcore")
	require.NoError(t, os.MkdirAll(httpcore, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(httpcore, "httpcore.go"), []byte("package httpcore\n\nfunc Handler() {}"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(httpcore, "types.go"), []byte("package httpcore\n\ntype Request struct{}"), 0644))

	// Nested Go subpackage
	middleware := filepath.Join(srcDir, "httpcore", "middleware")
	require.NoError(t, os.MkdirAll(middleware, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(middleware, "auth.go"), []byte("package middleware"), 0644))

	// Hidden directory (should be skipped)
	gitDir := filepath.Join(srcDir, ".git")
	require.NoError(t, os.MkdirAll(gitDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(gitDir, "config"), []byte("git config"), 0644))

	// Pre-existing .gen.go in dst (should NOT be overwritten)
	require.NoError(t, os.WriteFile(filepath.Join(dstDir, "helpers.go"), []byte("GENERATED"), 0644))

	// Run copyNonGalaFiles
	err := copyNonGalaFiles(srcDir, dstDir, false)
	require.NoError(t, err)

	// Verify .gala files were NOT copied
	assert.NoFileExists(t, filepath.Join(dstDir, "server.gala"))

	// Verify gala.mod was NOT copied
	assert.NoFileExists(t, filepath.Join(dstDir, "gala.mod"))

	// Verify go.mod was NOT copied
	content, err := os.ReadFile(filepath.Join(dstDir, "go.mod"))
	assert.True(t, os.IsNotExist(err) || (err == nil && string(content) != "module test"),
		"go.mod from source should not overwrite destination")

	// Verify hidden dirs were NOT copied
	assert.NoDirExists(t, filepath.Join(dstDir, ".git"))

	// Verify Go subpackage files WERE copied
	data, err := os.ReadFile(filepath.Join(dstDir, "httpcore", "httpcore.go"))
	require.NoError(t, err)
	assert.Contains(t, string(data), "package httpcore")

	data, err = os.ReadFile(filepath.Join(dstDir, "httpcore", "types.go"))
	require.NoError(t, err)
	assert.Contains(t, string(data), "type Request struct{}")

	// Verify nested subpackage was copied
	data, err = os.ReadFile(filepath.Join(dstDir, "httpcore", "middleware", "auth.go"))
	require.NoError(t, err)
	assert.Contains(t, string(data), "package middleware")

	// Verify pre-existing file was NOT overwritten
	data, err = os.ReadFile(filepath.Join(dstDir, "helpers.go"))
	require.NoError(t, err)
	assert.Equal(t, "GENERATED", string(data))
}

func TestCopyNonGalaFiles_SkipsSymlinks(t *testing.T) {
	// Bazel creates symlinks (Linux) or junctions (Windows) that may point to
	// nonexistent targets. copyNonGalaFiles should skip all symlinks and still
	// copy regular files.
	srcDir := t.TempDir()
	dstDir := t.TempDir()

	// Valid Go subpackage
	sub := filepath.Join(srcDir, "pkg")
	require.NoError(t, os.MkdirAll(sub, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(sub, "pkg.go"), []byte("package pkg"), 0644))

	// Create a broken symlink (simulates bazel-bin junction)
	brokenLink := filepath.Join(srcDir, "bazel-broken")
	_ = os.Symlink(filepath.Join(srcDir, "nonexistent-target"), brokenLink)

	// Create a symlink to a valid file (should also be skipped — we only copy regular files)
	validTarget := filepath.Join(srcDir, "pkg", "pkg.go")
	validLink := filepath.Join(srcDir, "link-to-file")
	_ = os.Symlink(validTarget, validLink)

	err := copyNonGalaFiles(srcDir, dstDir, false)
	require.NoError(t, err, "copyNonGalaFiles should not fail on symlinks")

	// Valid file should still be copied
	data, err := os.ReadFile(filepath.Join(dstDir, "pkg", "pkg.go"))
	require.NoError(t, err)
	assert.Equal(t, "package pkg", string(data))
}

func TestRewritePackageToMain_ImportBlock(t *testing.T) {
	input := `package server

import (
	"fmt"
	"martianoff/gala/std"
)

func TestFoo() {
	fmt.Println("hello")
}`
	result := rewritePackageToMain(input)
	assert.True(t, strings.HasPrefix(result, "package main\n"))
	assert.Contains(t, result, `. "gala-build-workspace/gen"`)
	assert.Contains(t, result, `"fmt"`)
	assert.Contains(t, result, `"martianoff/gala/std"`)
}

func TestRewritePackageToMain_SingleImport(t *testing.T) {
	input := `package server

import "fmt"

func main() {}`
	result := rewritePackageToMain(input)
	assert.True(t, strings.HasPrefix(result, "package main\n"))
	assert.Contains(t, result, `. "gala-build-workspace/gen"`)
	assert.Contains(t, result, `"fmt"`)
}

func TestRewritePackageToMain_NoImport(t *testing.T) {
	input := `package server

func main() {}`
	result := rewritePackageToMain(input)
	assert.True(t, strings.HasPrefix(result, "package main\n"))
	assert.Contains(t, result, `import . "gala-build-workspace/gen"`)
}

func TestRewritePackageToMain_DotImport(t *testing.T) {
	input := `package server

import . "martianoff/gala/std"

func main() {}`
	result := rewritePackageToMain(input)
	assert.True(t, strings.HasPrefix(result, "package main\n"))
	assert.Contains(t, result, `. "gala-build-workspace/gen"`)
	assert.Contains(t, result, `. "martianoff/gala/std"`)
}

func TestRewriteTestFilesAsMain(t *testing.T) {
	dir := t.TempDir()

	// Write a test .gen.go file
	testCode := `package server

import "martianoff/gala/std"

func TestSomething() {
	_ = std.NewImmutable(42)
}
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "server_test.gen.go"), []byte(testCode), 0644))

	// Write a non-.gen.go file (should not be touched)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "helper.go"), []byte("package server"), 0644))

	err := rewriteTestFilesAsMain(dir)
	require.NoError(t, err)

	// Verify test file was rewritten
	data, err := os.ReadFile(filepath.Join(dir, "server_test.gen.go"))
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(string(data), "package main\n"))
	assert.Contains(t, string(data), `. "gala-build-workspace/gen"`)

	// Verify non-.gen.go was NOT touched
	data, err = os.ReadFile(filepath.Join(dir, "helper.go"))
	require.NoError(t, err)
	assert.Equal(t, "package server", string(data))
}

func TestRewriteImportsInSource(t *testing.T) {
	tests := []struct {
		name      string
		source    string
		oldModule string
		newModule string
		expected  string
	}{
		{
			name: "import block with subpackage",
			source: `package server

import (
	"fmt"
	. "github.com/user/project/httpcore"
	"martianoff/gala/std"
)

func main() {}`,
			oldModule: "github.com/user/project",
			newModule: "gala-build-workspace/gen",
			expected: `package server

import (
	"fmt"
	. "gala-build-workspace/gen/httpcore"
	"martianoff/gala/std"
)

func main() {}`,
		},
		{
			name: "single import with subpackage",
			source: `package server

import "github.com/user/project/httpcore"

func main() {}`,
			oldModule: "github.com/user/project",
			newModule: "gala-build-workspace/gen",
			expected: `package server

import "gala-build-workspace/gen/httpcore"

func main() {}`,
		},
		{
			name: "import of root module",
			source: `package httpcore

import "github.com/user/project"

func main() {}`,
			oldModule: "github.com/user/project",
			newModule: "gala-build-workspace/gen",
			expected: `package httpcore

import "gala-build-workspace/gen"

func main() {}`,
		},
		{
			name: "aliased import",
			source: `package server

import (
	h "github.com/user/project/httpcore"
)

func main() {}`,
			oldModule: "github.com/user/project",
			newModule: "gala-build-workspace/gen",
			expected: `package server

import (
	h "gala-build-workspace/gen/httpcore"
)

func main() {}`,
		},
		{
			name: "no matching imports unchanged",
			source: `package server

import (
	"fmt"
	"github.com/other/module"
)

func main() {}`,
			oldModule: "github.com/user/project",
			newModule: "gala-build-workspace/gen",
			expected: `package server

import (
	"fmt"
	"github.com/other/module"
)

func main() {}`,
		},
		{
			name: "multiple subpackages",
			source: `package server

import (
	"github.com/user/project/httpcore"
	"github.com/user/project/middleware"
)

func main() {}`,
			oldModule: "github.com/user/project",
			newModule: "gala-build-workspace/gen",
			expected: `package server

import (
	"gala-build-workspace/gen/httpcore"
	"gala-build-workspace/gen/middleware"
)

func main() {}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := rewriteImportsInSource(tt.source, tt.oldModule, tt.newModule)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestRewriteImportsInDir(t *testing.T) {
	dir := t.TempDir()

	// Write a .gen.go file with project module imports
	genCode := `package server

import (
	"fmt"
	. "github.com/user/project/httpcore"
)

func main() { fmt.Println("hello") }
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "server.gen.go"), []byte(genCode), 0644))

	// Write a Go subpackage file that references the project module
	sub := filepath.Join(dir, "httpcore")
	require.NoError(t, os.MkdirAll(sub, 0755))
	subCode := `package httpcore

import "github.com/user/project/httpcore/internal"

func Handler() { internal.Do() }
`
	require.NoError(t, os.WriteFile(filepath.Join(sub, "httpcore.go"), []byte(subCode), 0644))

	// Write a non-.go file (should be untouched)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "README.md"), []byte("github.com/user/project/httpcore"), 0644))

	err := rewriteImportsInDir(dir, "github.com/user/project", "gala-build-workspace/gen", false)
	require.NoError(t, err)

	// Verify .gen.go was rewritten
	data, err := os.ReadFile(filepath.Join(dir, "server.gen.go"))
	require.NoError(t, err)
	assert.Contains(t, string(data), `"gala-build-workspace/gen/httpcore"`)
	assert.NotContains(t, string(data), `"github.com/user/project/httpcore"`)

	// Verify subpackage .go file was rewritten
	data, err = os.ReadFile(filepath.Join(sub, "httpcore.go"))
	require.NoError(t, err)
	assert.Contains(t, string(data), `"gala-build-workspace/gen/httpcore/internal"`)

	// Verify non-.go file was NOT touched
	data, err = os.ReadFile(filepath.Join(dir, "README.md"))
	require.NoError(t, err)
	assert.Equal(t, "github.com/user/project/httpcore", string(data))
}

func TestParseGoModModulePath(t *testing.T) {
	tests := []struct {
		name     string
		content  string
		expected string
	}{
		{
			name:     "standard go.mod",
			content:  "module github.com/user/project\n\ngo 1.22\n",
			expected: "github.com/user/project",
		},
		{
			name:     "with comment",
			content:  "// generated\nmodule github.com/user/project\n",
			expected: "github.com/user/project",
		},
		{
			name:     "empty",
			content:  "",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, parseGoModModulePath(tt.content))
		})
	}
}
