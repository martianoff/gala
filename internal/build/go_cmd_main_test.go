package build

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBuild_CmdDirMainInGo builds `gala build ./cmd/<name>` where the main
// package imports the module's GALA package and is written in Go alone, or in
// GALA and Go together. A Go-only main used to fail with "no .gala files found
// in <module>/cmd/<name>", and the GALA half of a mixed main was generated
// apart from its .go files, so neither half saw the other's declarations.
func TestBuild_CmdDirMainInGo(t *testing.T) {
	projectDir := newForeignGenGoProject(t, "example.com/gocmdmain", map[string]string{
		"textstats/textstats.gala": `package textstats

import "strings"

func WordCount(text string) int = strings.Fields(text).Size()
`,
		"cmd/gomain/main.go": `package main

import (
	"fmt"

	"example.com/gocmdmain/textstats"
)

func main() { fmt.Printf("gomain: %d words\n", textstats.WordCount("a Go main over GALA")) }
`,
		// An external test package, which must not be taken for the
		// directory's package.
		"cmd/gomain/main_ext_test.go": "package main_test\n",
		"cmd/mixed/main.gala": `package main

import "example.com/gocmdmain/textstats"

func main() {
    Println(s"${banner()}: ${textstats.WordCount("GALA and Go together")} words")
}
`,
		"cmd/mixed/banner.go": "package main\n\nfunc banner() string { return \"mixed\" }\n",
		// Under testdata/, which the project-wide copy into gen/ skips.
		"testdata/tool/main.go": "package main\n\nimport \"fmt\"\n\nfunc main() { fmt.Println(\"tool\") }\n",
	})
	cmdDir := func(name string) string { return filepath.Join(projectDir, "cmd", name) }

	t.Run("Go-only main", func(t *testing.T) {
		assert.Equal(t, "gomain:|5|words", buildAndRunFrom(t, projectDir, cmdDir("gomain")))
	})
	t.Run("GALA and Go main", func(t *testing.T) {
		assert.Equal(t, "mixed:|4|words", buildAndRunFrom(t, projectDir, cmdDir("mixed")))
	})
	t.Run("Go-only main under testdata", func(t *testing.T) {
		assert.Equal(t, "tool", buildAndRunFrom(t, projectDir, filepath.Join(projectDir, "testdata", "tool")))
	})
	t.Run("a directory with neither .gala nor .go files is an error", func(t *testing.T) {
		require.NoError(t, os.MkdirAll(cmdDir("empty"), 0o755))
		b, err := NewBuilder(projectDir, "test", false)
		require.NoError(t, err)
		b.SetSourceDir(cmdDir("empty"))
		_, err = b.Build("")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no .gala or .go files found in")
	})
}
