package build

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"martianoff/gala/galaerr"
)

// TestBuild_DefaultLexicalScope: a default value resolves its names where it
// is written. lib's default names the case OnlyB, which only main's dot
// import of kinds brings into scope; lib itself never imports kinds (the
// analyzer does not check a pattern's constructor where the default is
// declared, so lib builds). `gala build` reports it as undefined, at the
// pattern in lib, not at main's call. It used to compile through main's dot
// import. A default of lib's other file, which names its own file's dot
// import, still lowers in main.
func TestBuild_DefaultLexicalScope(t *testing.T) {
	files := func(main string) map[string]string {
		return map[string]string{
			"kinds/kinds.gala": `package kinds

sealed type Kind {
    case OnlyA()
    case OnlyB()
}
`,
			"lib/lib.gala": `package lib

val Thing any = 1

func Describe(s string = Thing match {
    case OnlyB() => "b"
    case _ => "other"
}) string = s
`,
			"lib/own.gala": `package lib

import . "example.com/lexical/kinds"

func Own(s string = OnlyA() match {
    case OnlyA() => "a"
    case _ => "other"
}) string = s
`,
			"main.gala": main,
		}
	}

	t.Run("a name only the call site's dot import resolves", func(t *testing.T) {
		projectDir := newForeignGenGoProject(t, "example.com/lexical", files(`package main

import (
    . "example.com/lexical/kinds"
    "example.com/lexical/lib"
)

func main() {
    Println(lib.Describe(), OnlyA())
}
`))
		chdirForTest(t, projectDir)
		b, err := NewBuilder(projectDir, "test", false)
		require.NoError(t, err)
		_, err = b.Build("")
		require.Error(t, err)
		diags := galaerr.Diagnostics(err)
		require.NotEmpty(t, diags, "%v", err)
		d := diags[0]
		assert.Equal(t, string(galaerr.CodeUndefinedVariable), d.Code, "%v", err)
		assert.Equal(t, "undefined: OnlyB", d.Message)
		assert.Equal(t, filepath.Join(projectDir, "lib", "lib.gala"), d.File)
		assert.Equal(t, 6, d.Line)
	})

	t.Run("its own file's dot import", func(t *testing.T) {
		projectDir := newForeignGenGoProject(t, "example.com/lexical", files(`package main

import "example.com/lexical/lib"

func main() {
    Println(lib.Own())
}
`))
		assert.Equal(t, "a", buildAndRun(t, projectDir))
	})
}
