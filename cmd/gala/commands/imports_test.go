package commands

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeFixture writes content to a uniquely-named .gala file in dir and
// returns the full path.
func writeFixture(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
	return path
}

func TestImportsExtraction(t *testing.T) {
	tests := []struct {
		name     string
		content  string
		wantPkg  string
		wantImps []importInfo
	}{
		{
			name: "plain import",
			content: `package main

import "fmt"

func main() {
	Println("hi")
}
`,
			wantPkg:  "main",
			wantImps: []importInfo{{Path: "fmt", Alias: "", Dot: false}},
		},
		{
			name: "aliased import",
			content: `package main

import m "math"

func main() {
	val x = m.Sqrt(4.0)
}
`,
			wantPkg:  "main",
			wantImps: []importInfo{{Path: "math", Alias: "m", Dot: false}},
		},
		{
			name: "dot import",
			content: `package main

import . "martianoff/gala/collection_immutable"

func main() {
	val a = ArrayOf(1, 2, 3)
}
`,
			wantPkg:  "main",
			wantImps: []importInfo{{Path: "martianoff/gala/collection_immutable", Alias: "", Dot: true}},
		},
		{
			name: "grouped imports preserve order",
			content: `package main

import (
	"fmt"
	m "math"
	. "martianoff/gala/collection_immutable"
)

func main() {
	Println("hi")
}
`,
			wantPkg: "main",
			wantImps: []importInfo{
				{Path: "fmt", Alias: "", Dot: false},
				{Path: "math", Alias: "m", Dot: false},
				{Path: "martianoff/gala/collection_immutable", Alias: "", Dot: true},
			},
		},
		{
			name: "library package no imports",
			content: `package shapes

func area() int = 0
`,
			wantPkg:  "shapes",
			wantImps: []importInfo{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			file := writeFixture(t, dir, "input.gala", tt.content)

			cmd, out := newImportsTestCmd()
			cmd.SetArgs([]string{"--json", file})
			require.NoError(t, cmd.Execute())

			results := decodeResults(t, out.Bytes())
			require.Len(t, results, 1)
			assert.Equal(t, tt.wantPkg, results[0].Package)
			assert.Equal(t, file, results[0].File)
			assert.Empty(t, results[0].Error)
			assert.Equal(t, tt.wantImps, results[0].Imports)
		})
	}
}

func TestImportsMultipleFiles(t *testing.T) {
	dir := t.TempDir()
	a := writeFixture(t, dir, "a.gala", `package main

import "fmt"

func main() {
	Println("a")
}
`)
	b := writeFixture(t, dir, "b.gala", `package lib

import m "math"

func sq(x float64) float64 = m.Sqrt(x)
`)

	cmd, out := newImportsTestCmd()
	cmd.SetArgs([]string{"--json", a, b})
	require.NoError(t, cmd.Execute())

	results := decodeResults(t, out.Bytes())
	require.Len(t, results, 2)

	assert.Equal(t, a, results[0].File)
	assert.Equal(t, "main", results[0].Package)
	assert.Equal(t, []importInfo{{Path: "fmt"}}, results[0].Imports)

	assert.Equal(t, b, results[1].File)
	assert.Equal(t, "lib", results[1].Package)
	assert.Equal(t, []importInfo{{Path: "math", Alias: "m"}}, results[1].Imports)
}

func TestImportsParseErrorMixedWithGood(t *testing.T) {
	dir := t.TempDir()
	good := writeFixture(t, dir, "good.gala", `package main

import "fmt"

func main() {
	Println("ok")
}
`)
	// Missing the required empty line after the package clause and a broken
	// body — guaranteed to fail the parser.
	bad := writeFixture(t, dir, "bad.gala", `package main
func main( {
`)

	cmd, out := newImportsTestCmd()
	cmd.SetArgs([]string{"--json", good, bad})
	// Parse failures must NOT fail the command.
	require.NoError(t, cmd.Execute())

	results := decodeResults(t, out.Bytes())
	require.Len(t, results, 2)

	assert.Equal(t, "main", results[0].Package)
	assert.Empty(t, results[0].Error)
	assert.Equal(t, []importInfo{{Path: "fmt"}}, results[0].Imports)

	// The bad file reports an error, an empty imports array, and is still present.
	assert.Equal(t, bad, results[1].File)
	assert.NotEmpty(t, results[1].Error)
	assert.Equal(t, []importInfo{}, results[1].Imports)
}

func TestImportsUsageErrors(t *testing.T) {
	t.Run("no files", func(t *testing.T) {
		cmd, _ := newImportsTestCmd()
		cmd.SetArgs([]string{"--json"})
		assert.Error(t, cmd.Execute())
	})

	t.Run("missing --json flag", func(t *testing.T) {
		dir := t.TempDir()
		file := writeFixture(t, dir, "x.gala", "package main\n")
		cmd, _ := newImportsTestCmd()
		cmd.SetArgs([]string{file})
		assert.Error(t, cmd.Execute())
	})

	t.Run("file not found", func(t *testing.T) {
		cmd, _ := newImportsTestCmd()
		cmd.SetArgs([]string{"--json", filepath.Join(t.TempDir(), "nope.gala")})
		assert.Error(t, cmd.Execute())
	})
}

// newImportsTestCmd builds a fresh imports command with its own --json flag and
// captured stdout, isolated from package-global flag state.
func newImportsTestCmd() (*cobra.Command, *bytes.Buffer) {
	var jsonFlag bool
	out := &bytes.Buffer{}
	cmd := &cobra.Command{
		Use:           importsCmd.Use,
		Args:          importsCmd.Args,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(c *cobra.Command, args []string) error {
			importsJSON = jsonFlag
			return runImports(c, args)
		},
	}
	cmd.Flags().BoolVar(&jsonFlag, "json", false, "")
	cmd.SetOut(out)
	return cmd, out
}

func decodeResults(t *testing.T, data []byte) []fileImports {
	t.Helper()
	var results []fileImports
	require.NoError(t, json.Unmarshal(data, &results))
	return results
}
