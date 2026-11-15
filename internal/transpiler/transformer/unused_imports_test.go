package transformer_test

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"martianoff/gala/galaerr"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// importPathRE matches an import spec of a test source, capturing its path.
var importPathRE = regexp.MustCompile(`(?m)^(?:import\s+|\s+)(?:[\w.]+\s+)?"([^"]+)"\s*$`)

// An import the file writes and never refers to is GALA-E0120, whatever the
// form of the import; an import the file refers to in any position — value,
// call, type, pattern, interpolated string — is not. A blank import and the
// imports the transpiler adds itself are never reported.
func TestUnusedImports(t *testing.T) {
	tests := []struct {
		name string
		src  string
		// unused is the message of the expected GALA-E0120; empty when the
		// file compiles.
		unused string
		// line and col locate the expected error (col 0-based).
		line, col int
	}{
		{
			name: "plain import never referred to",
			src: `package main

import "strings"

func main() {
    Println("done")
}
`,
			unused: `unused import "strings": the file never refers to strings`,
			line:   3, col: 7,
		},
		{
			name: "aliased import never referred to",
			src: `package main

import gs "strings"

func main() {
    Println("done")
}
`,
			unused: `unused import gs "strings": the file never refers to gs`,
			line:   3, col: 7,
		},
		{
			name: "aliased import whose package name is written but not its alias",
			src: `package main

import gs "strings"

func main() {
    val strings = "a"
    Println(strings)
}
`,
			unused: `unused import gs "strings": the file never refers to gs`,
			line:   3, col: 7,
		},
		{
			name: "dot import none of whose names is used",
			src: `package main

import . "strings"

func main() {
    Println("done")
}
`,
			unused: `unused import . "strings": the file uses none of its names`,
			line:   3, col: 7,
		},
		{
			name: "the second import of a block is the unused one",
			src: `package main

import (
    "strings"
    "martianoff/gala/collection_immutable"
)

func main() {
    Println(strings.ToUpper("a"))
}
`,
			unused: `unused import "martianoff/gala/collection_immutable": the file never refers to collection_immutable`,
			line:   5, col: 4,
		},
		{
			name: "dot import of std nothing refers to",
			src: `package main

import . "martianoff/gala/std"

func test() int = 42

func main() = Println(test())
`,
			unused: `unused import . "martianoff/gala/std": the file uses none of its names`,
			line:   3, col: 7,
		},
		{
			// Use is read from the source: the std code the lowering emits,
			// and a method of a std value, do not use the import.
			name: "dot import of std used only by generated code and methods",
			src: `package main

import . "martianoff/gala/std"

struct Point(X int)

func main() {
    val p = Point(1)
    Println(p.Copy(X = 2).X)
}
`,
			unused: `unused import . "martianoff/gala/std": the file uses none of its names`,
			line:   3, col: 7,
		},
		{
			// Sendable is a std name the compiler knows without metadata.
			name: "dot import of std used only by a compiler-known std type",
			src: `package main

import . "martianoff/gala/std"

func run(f Sendable[func() int]) int = f()

func main() = Println(run(() => 1))
`,
		},
		{
			name: "blank import",
			src: `package main

import _ "strings"

func main() {
    Println("done")
}
`,
		},
		{
			name: "no import: std and fmt are added by the transpiler",
			src: `package main

func main() {
    Println(Some(1).GetOrElse(0))
}
`,
		},
		{
			name: "used in a call",
			src: `package main

import "strings"

func main() {
    Println(strings.ToUpper("a"))
}
`,
		},
		{
			name: "used through its alias",
			src: `package main

import gs "strings"

func main() {
    Println(gs.ToUpper("a"))
}
`,
		},
		{
			name: "used only in a parameter type",
			src: `package main

import "strings"

func size(b *strings.Builder) int = b.Len()

func main() = Println("done")
`,
		},
		{
			name: "used only in a type argument",
			src: `package main

import ci "martianoff/gala/collection_immutable"

func main() {
    val none = None[ci.Array[int]]()
    Println(none.IsEmpty())
}
`,
		},
		{
			name: "used only in an extractor pattern",
			src: `package main

import s "martianoff/gala/std"

func main() {
    val o = Some(1)
    val n = o match {
        case s.Some(v) => v
        case _ => 0
    }
    Println(n)
}
`,
		},
		{
			name: "used only in an interpolated string",
			src: `package main

import "strings"

func main() {
    Println(s"${strings.ToUpper("a")}")
}
`,
		},
		{
			name: "dot import used by a function",
			src: `package main

import . "strings"

func main() {
    Println(ToUpper("a"))
}
`,
		},
		{
			name: "dot import used only in a type",
			src: `package main

import . "martianoff/gala/collection_immutable"

func size(a Array[int]) int = a.Size()

func main() = Println("done")
`,
		},
		{
			name: "dot import used only in an interpolated string",
			src: `package main

import . "strings"

func main() {
    Println(s"${ToUpper("a")}")
}
`,
		},
	}

	trans := newBindTranspiler()
	imp := newOracleImporter()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := trans.Transpile(tt.src, "main.gala")
			if tt.unused == "" {
				require.NoError(t, err)
				// Every written import stays in the generated Go, as a blank
				// import when the Go does not refer to it, so that its
				// package is initialized; and the Go type-checks.
				for _, m := range importPathRE.FindAllStringSubmatch(tt.src, -1) {
					assert.Contains(t, got, `"`+m[1]+`"`, "the generated Go must import %s", m[1])
				}
				res := imp.TypeCheck("out.go", got)
				require.Empty(t, res.Skipped, "the generated file must be type-checked")
				assert.Empty(t, res.Errors, "generated Go:\n%s", got)
				assert.Zero(t, res.Soft, "generated Go has an unused import or variable:\n%s", got)
				return
			}
			require.Error(t, err)
			var se *galaerr.SemanticError
			require.True(t, errors.As(err, &se), "want a SemanticError, got %T: %v", err, err)
			assert.Equal(t, galaerr.CodeUnusedImport, se.Code, "%v", err)
			assert.Equal(t, tt.unused, se.Msg)
			assert.Equal(t, tt.line, se.Line)
			assert.Equal(t, tt.col, se.Column)
			assert.True(t, se.HasSpan(), "the error underlines the import spec")
		})
	}
}

// A dot import of another GALA package is used by any of its exported names,
// a type alias among them, which its metadata records apart from its types.
func TestUnusedImportsDotImportOfAGalaPackage(t *testing.T) {
	root := t.TempDir()
	write := func(rel, content string) {
		full := filepath.Join(root, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(full), 0755))
		require.NoError(t, os.WriteFile(full, []byte(content), 0644))
	}
	write("gala.mod", "module example.com/unusedx\n\ngala dev\n")
	write("lib/lib.gala", `package lib

type UserID int64

func Origin() int = 0
`)
	tests := []struct {
		name, body string
		unused     bool
	}{
		{"used only through a type alias", "func show(id UserID) int64 = int64(id)\n\nfunc main() = Println(show(1))\n", false},
		{"used through a function", "func main() = Println(Origin())\n", false},
		{"none of its names written", "func main() = Println(1)\n", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			src := "package main\n\nimport . \"example.com/unusedx/lib\"\n\n" + tt.body
			_, err := transpileCrossPkg(t, root, src)
			if !tt.unused {
				require.NoError(t, err)
				return
			}
			var se *galaerr.SemanticError
			require.True(t, errors.As(err, &se), "want GALA-E0120, got %v", err)
			assert.Equal(t, galaerr.CodeUnusedImport, se.Code, "%v", err)
		})
	}
}
