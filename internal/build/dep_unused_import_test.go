package build

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBuild_DependencyStrayImportIsNotChecked: dependencies are not checked
// for unused imports yet (GALA-E0120 applies to the module being built). A
// dependency module whose file imports what it never refers to still builds,
// and, as before that check, the import is dropped: the package it names is
// not initialized, so its package-level val prints nothing.
func TestBuild_DependencyStrayImportIsNotChecked(t *testing.T) {
	libDir := t.TempDir()
	writeFixtureFile(t, filepath.Join(libDir, "gala.mod"), "module example.com/straylib\n\ngala 0.0.0\n")
	writeFixtureFile(t, filepath.Join(libDir, "lib.gala"), `package straylib

import (
    "strings"
    "example.com/straylib/noisy"
)

func Hello() string = "hello"
`)
	writeFixtureFile(t, filepath.Join(libDir, "noisy", "noisy.gala"), `package noisy

func announce() string {
    Println("noisy: initialized")
    "noisy"
}

val Name = announce()
`)

	projectDir := newForeignGenGoProject(t, "example.com/strayapp", map[string]string{
		"main.gala": `package main

import "example.com/straylib"

func main() = Println(straylib.Hello())
`,
	})
	rel, err := filepath.Rel(projectDir, libDir)
	require.NoError(t, err)
	writeFixtureFile(t, filepath.Join(projectDir, "gala.mod"),
		"module example.com/strayapp\n\ngala 0.0.0\n\nrequire example.com/straylib v0.0.0\n\n"+
			"replace example.com/straylib => "+filepath.ToSlash(rel)+"\n")

	assert.Equal(t, "hello", buildAndRun(t, projectDir))
}
