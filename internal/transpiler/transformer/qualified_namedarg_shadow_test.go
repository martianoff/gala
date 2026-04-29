package transformer_test

import (
	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/analyzer"
	"martianoff/gala/internal/transpiler/generator"
	"martianoff/gala/internal/transpiler/transformer"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestQualifiedNamedArgConstructionShadowedByDotImport verifies that
// `pkg.Struct(Field = ...)` resolves to the qualified package's struct even
// when a dot-imported (or otherwise transitively in-scope) package exports a
// function (or different struct) with the same name.
//
// Before the fix, `getFunction("session.Snapshot")` would not find a function
// named "Snapshot" in the session package, then fall through to a simple-name
// search across dot imports — which matched the dot-imported `gala_tui.Snapshot`
// function. The named-args dispatcher then tried to use that function's
// parameter names, producing:
//
//	"unknown parameter \"ProjectName\" in call to Snapshot"
//
// The qualifier `session.` should anchor the lookup to the session package.
func TestQualifiedNamedArgConstructionShadowedByDotImport(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "qualified_namedarg_shadow_test")
	assert.NoError(t, err)
	defer os.RemoveAll(tempDir)

	err = os.WriteFile(filepath.Join(tempDir, "go.mod"), []byte("module testmod\n\ngo 1.22\n"), 0644)
	assert.NoError(t, err)

	// Package `session` exposes a struct named Snapshot using GALA positional
	// struct syntax — this is the form used in the original report.
	sessionDir := filepath.Join(tempDir, "session")
	err = os.MkdirAll(sessionDir, 0755)
	assert.NoError(t, err)
	err = os.WriteFile(filepath.Join(sessionDir, "session.gala"), []byte(`package session

struct Snapshot(ProjectName string, SavedAt int64)
`), 0644)
	assert.NoError(t, err)

	// Package `gala_tui` exposes a *function* named Snapshot. The signature
	// references its own Widget struct so the analyzer registers the function
	// with non-trivial parameter types.
	tuiDir := filepath.Join(tempDir, "gala_tui")
	err = os.MkdirAll(tuiDir, 0755)
	assert.NoError(t, err)
	err = os.WriteFile(filepath.Join(tuiDir, "tui.gala"), []byte(`package gala_tui

type Widget struct {
    Name string
}

func Snapshot(w Widget, width int, height int) string = "tui-snap"
`), 0644)
	assert.NoError(t, err)

	originalWd, err := os.Getwd()
	assert.NoError(t, err)
	defer os.Chdir(originalWd)
	err = os.Chdir(tempDir)
	assert.NoError(t, err)

	p := transpiler.NewAntlrGalaParser()
	// Pass nil search paths so the resolver derives the module root from the
	// current working directory (the temp module created above). Otherwise
	// the bazel runfiles' real gala.mod would shadow our temp module and the
	// session/gala_tui packages would never be discovered.
	a := analyzer.NewGalaAnalyzer(p, nil)
	tr := transformer.NewGalaASTTransformer()
	g := generator.NewGoCodeGenerator()
	trans := transpiler.NewGalaToGoTranspiler(p, a, tr, g)

	input := `package main

import (
    "testmod/session"
    . "testmod/gala_tui"
)

func makeSnap() session.Snapshot {
    return session.Snapshot(
        ProjectName = "demo",
        SavedAt     = int64(1234),
    )
}
`

	got, err := trans.Transpile(input, "")
	assert.NoError(t, err,
		"qualified named-arg struct construction should resolve to session.Snapshot,\nnot the shadowing dot-imported gala_tui.Snapshot function. got error:\n%v",
		err)
	if err != nil {
		return
	}
	t.Logf("transpiled output:\n%s", got)

	// Sanity: the output should contain a session.Snapshot composite literal
	// with the field names (rather than a Snapshot(...) function call).
	assert.Contains(t, got, "session.Snapshot",
		"expected session-qualified Snapshot reference, got:\n%s", got)
	assert.Contains(t, got, "ProjectName:",
		"expected struct literal with ProjectName field, got:\n%s", got)
}
