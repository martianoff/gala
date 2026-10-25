package transformer_test

import (
	"os"
	"path/filepath"
	"testing"

	"martianoff/gala/galaerr"
	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/analyzer"
	"martianoff/gala/internal/transpiler/generator"
	"martianoff/gala/internal/transpiler/transformer"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const transitionDecl = `sealed type Transition {
    case Epsilon(Target int)
    case Atom(Target int, Label int)
}
`

const transitionTargetMethod = `func (t Transition) Target() int = t match {
    case Epsilon(target) => target
    case Atom(target, _) => target
}
`

// TestSealedFieldNamedLikeMethod: a case field may share its name with a
// method of its sealed type. The cases are flattened into one Go struct, where
// a field and a method of one name cannot coexist ("field and method with the
// same name"), so the field is stored under a Go name of its case, as a field
// whose type differs between cases already was. Every access path — Apply,
// Unapply, Copy, Equal, String — uses that name, and `t.Target()` calls the
// method.
func TestSealedFieldNamedLikeMethod(t *testing.T) {
	tests := []struct {
		name     string
		src      string
		contains []string
		absent   []string
	}{
		{
			name: "method named like a field the cases share",
			src: "package main\n\n" + transitionDecl + "\n" + transitionTargetMethod + `
func main() {
    Println(Atom(2, 7).Target(), Epsilon(1).Target(), Atom(2, 7))
}
`,
			contains: []string{
				"EpsilonTarget std.Immutable[int]",
				"AtomTarget    std.Immutable[int]",
				"EpsilonTarget: std.NewImmutable(Target)",
				"v.AtomTarget.Get()",
				"func (t Transition) Target() int",
			},
			absent: []string{"\tTarget  "},
		},
		{
			name: "method declared before the type",
			src: "package main\n\n" + transitionTargetMethod + "\n" + transitionDecl + `
func main() {
    Println(Atom(2, 7).Target())
}
`,
			contains: []string{"EpsilonTarget std.Immutable[int]"},
		},
		{
			name: "fields named like the generated String, Copy and Equal",
			src: `package main

sealed type Tagged[T any] {
    case Plain(Value T)
    case Labeled(Value T, String string, Copy int, Equal bool)
}

func (t Tagged[T]) Value() T = t match {
    case Plain(v) => v
    case Labeled(v, _, _, _) => v
}

func main() {
    val l = Labeled(5, "five", 1, true)
    Println(l, l.Value(), l.Copy() == l)
}
`,
			contains: []string{
				"PlainValue    std.Immutable[T]",
				"LabeledString std.Immutable[string]",
				"LabeledCopy   std.Immutable[int]",
				"LabeledEqual  std.Immutable[bool]",
			},
		},
		{
			name: "field read without a method of its name keeps its name",
			src: "package main\n\n" + transitionDecl + `
func main() {
    Println(Atom(2, 7).Target)
}
`,
			contains: []string{"\tTarget   std.Immutable[int]"},
			absent:   []string{"EpsilonTarget"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := transpiler.NewAntlrGalaParser()
			a := analyzer.NewGalaAnalyzer(p, getStdSearchPath())
			trans := newCheckedTranspiler(p, a, transformer.NewGalaASTTransformer(), generator.NewGoCodeGenerator())
			out, err := trans.Transpile(tc.src, "main.gala")
			require.NoError(t, err)
			checkGeneratedGo(t, out)
			for _, want := range tc.contains {
				assert.Contains(t, out, want, "generated:\n%s", out)
			}
			for _, unwanted := range tc.absent {
				assert.NotContains(t, out, unwanted, "generated:\n%s", out)
			}
		})
	}
}

// TestSealedFieldNamedLikeMethodAcrossFiles: the method and the type live in
// different files of the package, in either order. Each file names the field
// the same way, so the file holding the type and the file reading it agree.
func TestSealedFieldNamedLikeMethodAcrossFiles(t *testing.T) {
	tests := []struct {
		name, sibling, main string
	}{
		{
			name:    "method in the sibling",
			sibling: "package main\n\n" + transitionTargetMethod,
			main: "package main\n\n" + transitionDecl + `
func main() {
    Println(Atom(2, 7).Target())
}
`,
		},
		{
			name:    "type in the sibling",
			sibling: "package main\n\n" + transitionDecl,
			main: "package main\n\n" + transitionTargetMethod + `
func main() {
    val t = Atom(2, 7)
    Println(t.Target(), t)
}
`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			siblingPath := filepath.Join(dir, "sibling.gala")
			mainPath := filepath.Join(dir, "main.gala")
			require.NoError(t, os.WriteFile(siblingPath, []byte(tc.sibling), 0644))
			require.NoError(t, os.WriteFile(mainPath, []byte(tc.main), 0644))

			for _, f := range []struct{ path, src, other string }{
				{mainPath, tc.main, siblingPath},
				{siblingPath, tc.sibling, mainPath},
			} {
				p := transpiler.NewAntlrGalaParser()
				a := analyzer.NewGalaAnalyzerWithPackageFiles(p, getStdSearchPath(), []string{f.other})
				out, err := newCheckedTranspiler(p, a, transformer.NewGalaASTTransformer(), generator.NewGoCodeGenerator()).
					Transpile(f.src, f.path)
				require.NoError(t, err, "transpiling %s", filepath.Base(f.path))
				assert.NotContains(t, out, ".Target.Get()", "generated %s:\n%s", filepath.Base(f.path), out)
				if f.src == tc.main {
					assert.Contains(t, out, ".Target()", "generated %s:\n%s", filepath.Base(f.path), out)
				}
			}
		})
	}
}

// TestSealedFieldNamedLikeMethodAcrossPackages: another package matches the
// cases and calls the method; it reads the type's metadata, which names the
// field as the declaring package's Go struct does.
func TestSealedFieldNamedLikeMethodAcrossPackages(t *testing.T) {
	root := t.TempDir()
	write := func(rel, content string) {
		t.Helper()
		full := filepath.Join(root, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(full), 0755))
		require.NoError(t, os.WriteFile(full, []byte(content), 0644))
	}
	write("gala.mod", "module example.com/fieldmethod\n\ngala dev\n")
	write("fsm/fsm.gala", "package fsm\n\n"+transitionDecl+"\n"+transitionTargetMethod)

	out, err := transpileCrossPkg(t, root, `package main

import "example.com/fieldmethod/fsm"

func describe(t fsm.Transition) string = t match {
    case fsm.Atom(target, label) => s"atom $label -> $target"
    case fsm.Epsilon(_) => s"epsilon -> ${t.Target()}"
}

func main() {
    Println(describe(fsm.Atom(2, 7)), fsm.Epsilon(1).Target())
}
`)
	require.NoError(t, err)
	assert.Contains(t, out, "t.Target()", "generated:\n%s", out)
	assert.NotContains(t, out, ".Target.Get()", "generated:\n%s", out)
}

// TestStructFieldNamedLikeMethod: a struct field is read by its name, so a
// method of the same name cannot be told apart from it and is GALA-E0081
// instead of a `go build` failure.
func TestStructFieldNamedLikeMethod(t *testing.T) {
	tests := []struct {
		name, src string
		line, col int
	}{
		{
			name: "shorthand struct",
			src: `package main

struct S(Name string)

func (s S) Name() string = "x"

func main() {
    Println(S("a").Name)
}
`,
			line: 5, col: 11,
		},
		{
			name: "block struct, method called first",
			src: `package main

func main() {
    Println(S(Name = "a").Name())
}

type S struct {
    Name string
}

func (s S) Name() string = "x"
`,
			line: 11, col: 11,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := transpiler.NewAntlrGalaParser()
			a := analyzer.NewGalaAnalyzer(p, getStdSearchPath())
			_, err := newCheckedTranspiler(p, a, transformer.NewGalaASTTransformer(), generator.NewGoCodeGenerator()).
				Transpile(tc.src, "main.gala")
			require.Error(t, err)
			var se *galaerr.SemanticError
			require.ErrorAs(t, err, &se)
			assert.Equal(t, galaerr.CodeFieldNamedLikeMethod, se.Code)
			assert.Equal(t, "type S has both a field and a method named Name", se.Msg)
			assert.Equal(t, tc.line, se.Line)
			assert.Equal(t, tc.col, se.Column)
		})
	}
}
