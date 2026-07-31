package build

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBuild_ProjectSubpackageImportsRewrittenAtAnyDepth builds the *same*
// two-package program at several directory depths and requires every one of
// them to work. The layouts differ only in where the imported package sits:
//
//	greet/greet.gala           imported as example.com/layout/greet
//	internal/greet/greet.gala  imported as example.com/layout/internal/greet
//	lib/a/b/greet.gala         imported as example.com/layout/lib/a/b
//
// Why this shape
// --------------
// A package's own module path is not something the Go toolchain can fetch, so
// every import naming it has to be redirected to the workspace gen/ tree before
// `go mod tidy` and `go build` run. That redirection used to be skipped unless
// an *immediate* child of the project directory contained .go or .gala files.
// The nested layouts above fail that check — `internal/` and `lib/` hold only
// more directories — so the imports were left naming the project module and the
// build died trying to resolve example.com/layout/internal/greet over the
// network. The flat layout passed the check and worked, which is why the gap
// went unnoticed: depth was the only variable.
//
// The nested cases are the regression; the flat case is the control that proves
// the harness itself builds a working program. `internal/` is spelled out
// because it is the layout a Go developer reaches for first, but nothing about
// the fix keys on that name — `lib/a/b` covers the general case and would fail
// identically under the old gate.
//
// Two layers of proof
// -------------------
//  1. Import rewriting (always runs, needs no Go toolchain): after transpiling,
//     the generated main.gen.go must import the workspace path and must not
//     mention the project module. This is the assertion that actually pins the
//     fix, and it is hermetic — no network, no `go` binary.
//  2. End-to-end (when a usable Go toolchain is present): Builder.Build — the
//     exact entry point `gala build` drives — produces a binary that runs and
//     prints the expected greeting. Toolchain problems skip rather than fail,
//     matching the convention already used for source-mapped stack traces.
//
// The build is offline in both layers: the generated workspace go.mod resolves
// the GALA standard library through local `replace` directives into the
// extracted stdlib cache, and the project's own packages through gen/.
func TestBuild_ProjectSubpackageImportsRewrittenAtAnyDepth(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not on PATH")
	}

	const moduleName = "example.com/layout"

	tests := []struct {
		name string
		// pkgDir is the package's location relative to the project root,
		// in slash form.
		pkgDir string
		// pkgName is the GALA package clause, which must match the last
		// path segment so the import qualifier below resolves.
		pkgName string
	}{
		{
			name:    "flat sibling directory (control)",
			pkgDir:  "greet",
			pkgName: "greet",
		},
		{
			name:    "below a source-free internal directory",
			pkgDir:  "internal/greet",
			pkgName: "greet",
		},
		{
			name:    "three levels below a source-free root",
			pkgDir:  "lib/a/b",
			pkgName: "b",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			projectDir := t.TempDir()
			importPath := moduleName + "/" + tc.pkgDir

			require.NoError(t, os.WriteFile(filepath.Join(projectDir, "gala.mod"),
				[]byte("module "+moduleName+"\n\ngala 0.0.0\n"), 0o644))
			// A real go.mod alongside gala.mod is what a Go developer writes,
			// and it is the authoritative source for projectGoModulePath.
			require.NoError(t, os.WriteFile(filepath.Join(projectDir, "go.mod"),
				[]byte("module "+moduleName+"\n\ngo 1.22\n"), 0o644))

			pkgPath := filepath.Join(projectDir, filepath.FromSlash(tc.pkgDir))
			require.NoError(t, os.MkdirAll(pkgPath, 0o755))
			require.NoError(t, os.WriteFile(filepath.Join(pkgPath, "greet.gala"),
				[]byte("package "+tc.pkgName+"\n\nfunc Hello(name string) string = s\"hello $name\"\n"), 0o644))

			require.NoError(t, os.WriteFile(filepath.Join(projectDir, "main.gala"),
				[]byte("package main\n\nimport \""+importPath+"\"\n\nfunc main() {\n    Println("+tc.pkgName+".Hello(\"world\"))\n}\n"), 0o644))

			isolateUserState(t)
			// Isolating HOME/USERPROFILE removes the default GOCACHE location,
			// so point it at a throwaway dir — the `go build` inside
			// Builder.Build needs a writable build cache.
			setEnvForTest(t, "GOCACHE", filepath.Join(t.TempDir(), "gocache"))
			alignGorootWithPathGo(t)
			chdirForTest(t, projectDir)

			b, err := NewBuilder(projectDir, "test", false)
			require.NoError(t, err)
			require.NoError(t, b.workspace.Ensure())

			// --- Layer 1: the import rewrite itself. ---
			require.NoError(t, b.ensureStdlib())
			require.NoError(t, b.transpileDeps())
			require.NoError(t, b.transpile())

			// The subpackage must land in gen/ mirroring its source layout,
			// otherwise the rewritten import would point at nothing.
			genPkg := filepath.Join(b.workspace.GenDir, filepath.FromSlash(tc.pkgDir), "greet.gen.go")
			require.FileExists(t, genPkg,
				"the subpackage must be generated at gen/%s so the rewritten import resolves", tc.pkgDir)

			mainGen := readFileString(t, filepath.Join(b.workspace.GenDir, "main.gen.go"))
			assert.Contains(t, mainGen, "\"gala-build-workspace/gen/"+tc.pkgDir+"\"",
				"the subpackage import must be redirected into the workspace gen/ tree:\n%s", mainGen)
			assert.NotContains(t, mainGen, "\""+importPath+"\"",
				"no import may still name the project module — the Go toolchain would try to fetch it:\n%s", mainGen)

			// --- Layer 2: the whole `gala build` pipeline. ---
			binPath, buildErr := b.Build("")
			if buildErr != nil {
				if isToolchainEnvError(buildErr.Error()) {
					t.Skipf("skipping end-to-end check: Go toolchain unavailable/mismatched in this environment: %v", buildErr)
				}
				t.Fatalf("gala build failed for layout %s: %v", tc.pkgDir, buildErr)
			}
			require.NotEmpty(t, binPath)

			out, runErr := runBuiltBinary(binPath)
			require.NoError(t, runErr, "built binary failed to run; output:\n%s", out)
			assert.Equal(t, "hello world", strings.TrimSpace(out))
		})
	}
}
