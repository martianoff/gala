package build

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBuild_InferredGalaTypeOfAnotherPackage builds programs whose GALA code
// reaches a GALA struct of another package only through the package's own
// hand-written Go — a function, a method, a struct field, a package variable —
// and consumes it with unannotated lambdas. Each lambda parameter is that
// struct, qualified, with its package imported; its fields have their types;
// no type parameter of the method or function it is passed to reaches the
// generated Go.
func TestBuild_InferredGalaTypeOfAnotherPackage(t *testing.T) {
	const helper = `package main

import "example.com/inferredforeign/sub"

type Reg struct{ Cmds map[string]sub.Command }

func (r Reg) Get() map[string]sub.Command { return r.Cmds }

func newReg() Reg { return Reg{Cmds: newMap()} }

func newMap() map[string]sub.Command {
	return map[string]sub.Command{"x": {Name: "run"}}
}

var typedMap map[string]sub.Command = newMap()

var literalMap = map[string]sub.Command{"x": {Name: "run"}}

func newSlice() []sub.Command { return []sub.Command{{Name: "r"}, {Name: "un"}} }
`
	cases := []struct {
		name string
		main string
	}{
		{"unexported function", `val m = newMap()
    go_interop.OptionFromMap(m, "x").ForEach((c) => Println(c.Name))`},
		{"method", `go_interop.OptionFromMap(newReg().Get(), "x").ForEach((c) => Println(c.Name))`},
		{"struct field", `go_interop.OptionFromMap(newReg().Cmds, "x").ForEach((c) => Println(c.Name))`},
		{"typed package variable", `go_interop.OptionFromMap(typedMap, "x").ForEach((c) => Println(c.Name))`},
		{"package variable from a literal", `go_interop.OptionFromMap(literalMap, "x").ForEach((c) => Println(c.Name))`},
		{"named argument", `go_interop.OptionFromMap(newMap(), "x").ForEach(f = (c) => Println(c.Name))`},
		{"generic function", `Println(apply(go_interop.OptionFromMap(newMap(), "x"), (c) => c.Name))`},
		{"field type through Map", `Println(go_interop.OptionFromMap(newMap(), "x").Map((c) => c.Name).GetOrElse("none") + "")`},
		{"array chain", `Println(ArrayFromSlice(newSlice()).Map((c) => c.Name).FoldLeft("", (acc, s) => acc + s))`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			projectDir := newForeignGenGoProject(t, "example.com/inferredforeign", map[string]string{
				"sub/sub.gala": "package sub\n\nstruct Command(var Name string)\n",
				"helper.go":    helper,
				"main.gala": `package main

import (
    . "martianoff/gala/collection_immutable"
    "martianoff/gala/go_interop"
)

func apply[T any](o Option[T], f func(T) string) string = o.Map(f).GetOrElse("none")

func main() {
    ` + tc.main + `
}
`,
			})
			chdirForTest(t, projectDir)
			b, err := NewBuilder(projectDir, "test", false)
			require.NoError(t, err)
			require.NoError(t, b.workspace.Ensure())
			require.NoError(t, b.ensureStdlib())
			require.NoError(t, b.transpileDeps())
			require.NoError(t, b.transpile())
			mainGen := readFileString(t, filepath.Join(b.workspace.GenDir, "main.gen.go"))
			assert.Contains(t, mainGen, "(c sub.Command)", "the lambda parameter must be the inferred struct:\n%s", mainGen)
			assert.NotContains(t, mainGen, "(c T)", "a type parameter must not reach the generated Go:\n%s", mainGen)
			assert.NotContains(t, mainGen, "sub.Command) any", "the field must keep its type:\n%s", mainGen)
			assert.Equal(t, "run", buildAndRun(t, projectDir))
		})
	}
}
