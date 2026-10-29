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
		{
			// isBar() is generated for every case, so the field moves; a field
			// named like the Go predicate IsBar() keeps its name, and the
			// predicate is left out.
			name: "fields named like the generated case predicates",
			src: `package main

sealed type Flag {
    case Foo(IsBar bool, isBar bool)
    case Bar()
}

func main() {
    Println(Foo(true, false), Bar(), Foo(true, false).IsBar)
}
`,
			contains: []string{
				"\tIsBar    std.Immutable[bool]",
				"FooisBar std.Immutable[bool]",
				"func (s Flag) isBar() bool",
				"func (s Flag) IsFoo() bool",
			},
			absent: []string{"func (s Flag) IsBar() bool"},
		},
		{
			name: "an unexported type has no Go predicates to make room for",
			src: `package main

sealed type flag {
    case On(IsOff bool)
    case Off()
}

func main() {
    Println(On(true).IsOff)
}
`,
			contains: []string{"\tIsOff    std.Immutable[bool]"},
			absent:   []string{"OnIsOff"},
		},
		{
			name: "a case Go name never takes another field's name",
			src: `package main

sealed type T {
    case A(Target int)
    case B(ATarget string)
}

func (t T) Target() int = t match {
    case A(n) => n
    case B(_) => 0
}

func main() {
    Println(A(1).Target(), B("b").ATarget)
}
`,
			contains: []string{"ATarget_ std.Immutable[int]", "\tATarget  std.Immutable[string]"},
		},
		{
			name: "a case Go name never takes a Go predicate's name",
			src: `package main

sealed type Q {
    case Is(Off bool)
    case Off()
}

func (q Q) Off() bool = q match {
    case Is(o) => o
    case Off() => true
}

func main() {
    Println(Is(true).Off(), Off().Off())
}
`,
			contains: []string{"IsOff_   std.Immutable[bool]", "func (s Q) IsOff() bool"},
		},
		{
			name: "field named like the instance marker of a generic type",
			src: `package main

sealed type Box[T any] {
    case Full(Value T, IsBox bool)
    case Empty()
}

func main() {
    Println(Full(1, true))
}
`,
			contains: []string{"FullIsBox std.Immutable[bool]", "IsBox() bool"},
		},
		{
			name: "package-level value reading the method",
			src: "package main\n\n" + transitionDecl + "\n" + transitionTargetMethod + `
val target = Atom(2, 7).Target

func main() {
    Println(target())
}
`,
			contains: []string{"AtomTarget    std.Immutable[int]"},
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
		{
			// The sibling's package value is typed while the sibling is read,
			// before this file's method is registered.
			name:    "package value in the sibling, method in this file",
			sibling: "package main\n\n" + transitionDecl + "\nval target = Atom(2, 7).Target\n",
			main: "package main\n\n" + transitionTargetMethod + `
func main() {
    Println(target(), Epsilon(1).Target())
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

// TestSealedFieldNamedLikeGoSiblingMethod: a method a hand-written .go file of
// the package declares on the sealed type is part of its method set too.
func TestSealedFieldNamedLikeGoSiblingMethod(t *testing.T) {
	files, galaFile := samePackageModule(".",
		"package main\n\nfunc (t Transition) Target() int { return 0 }\n",
		"package main\n\n"+transitionDecl+"\nfunc main() {\n    Println(Atom(2, 7))\n}\n")
	out, err := transpileInModule(t, files, galaFile)
	require.NoError(t, err)
	assert.Contains(t, out, "AtomTarget", "generated:\n%s", out)
	assert.NotContains(t, out, "\tTarget ", "generated:\n%s", out)
}

// TestSealedCaseStorageFieldsAreNotGalaFields: a field stored under a Go name
// of its case is no field GALA code (or the editor) reads by that Go name.
func TestSealedCaseStorageFieldsAreNotGalaFields(t *testing.T) {
	p := transpiler.NewAntlrGalaParser()
	tree, _, err := transpiler.Parse(p, "package main\n\n"+transitionDecl+"\n"+transitionTargetMethod)
	require.NoError(t, err)
	richAST, err := analyzer.NewGalaAnalyzer(p, getStdSearchPath()).Analyze(tree, nil, "main.gala")
	require.NoError(t, err)
	meta := richAST.Types["Transition"]
	require.NotNil(t, meta)
	assert.Equal(t, []string{"EpsilonTarget"}, meta.SealedVariants[0].GoFieldNames)
	assert.Equal(t, []string{"AtomTarget", "Label"}, meta.SealedVariants[1].GoFieldNames)
	assert.True(t, meta.IsGalaField("Label"))
	for _, storage := range []string{"EpsilonTarget", "AtomTarget", "_variant"} {
		assert.False(t, meta.IsGalaField(storage), storage)
	}
}

// TestStructFieldNamedLikeMethod: a struct field is read by its name, so a
// method of the same name cannot be told apart from it and is GALA-E0081
// instead of a `go build` failure.
func TestStructFieldNamedLikeMethod(t *testing.T) {
	tests := []struct {
		name, src string
		field     string
		line, col int
	}{
		{
			name: "field named like the generated Copy",
			src: `package main

struct S(Copy int)

func main() {
    Println(S(1))
}
`,
			field: "Copy", line: 3, col: 9,
		},
		{
			name: "field named like the instance marker of a generic struct",
			src: `package main

struct Box[T any](Value T, IsBox bool)

func main() {
    Println(Box(1, true))
}
`,
			field: "IsBox", line: 3, col: 27,
		},
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
			field := tc.field
			if field == "" {
				field = "Name"
			}
			assert.Contains(t, se.Msg, "has both a field and a method named "+field)
			assert.Equal(t, tc.line, se.Line)
			assert.Equal(t, tc.col, se.Column)
		})
	}
}

// TestSealedCaseStorageFieldIsNotReadable: GALA code cannot read a case
// field by the Go name it is stored under; that field holds a value only for
// its own case.
func TestSealedCaseStorageFieldIsNotReadable(t *testing.T) {
	p := transpiler.NewAntlrGalaParser()
	a := analyzer.NewGalaAnalyzer(p, getStdSearchPath())
	_, err := newCheckedTranspiler(p, a, transformer.NewGalaASTTransformer(), generator.NewGoCodeGenerator()).
		Transpile("package main\n\n"+transitionDecl+"\n"+transitionTargetMethod+`
func main() {
    Println(Atom(2, 7).AtomTarget)
}
`, "main.gala")
	var se *galaerr.SemanticError
	require.ErrorAs(t, err, &se)
	assert.Equal(t, galaerr.CodeUnknownMethod, se.Code)
	assert.Equal(t, "Transition has no field or method AtomTarget", se.Msg)
	assert.Contains(t, se.Hint, "case Atom(...)")
}

// TestStructFieldNamedLikeUngeneratedMethod: a struct with an unexported field
// gets no generated Unapply, so a field named Unapply is no clash.
func TestStructFieldNamedLikeUngeneratedMethod(t *testing.T) {
	p := transpiler.NewAntlrGalaParser()
	a := analyzer.NewGalaAnalyzer(p, getStdSearchPath())
	out, err := newCheckedTranspiler(p, a, transformer.NewGalaASTTransformer(), generator.NewGoCodeGenerator()).
		Transpile(`package main

struct S(Unapply int, secret int)

func main() {
    Println(S(1, 2).Unapply)
}
`, "main.gala")
	require.NoError(t, err)
	checkGeneratedGo(t, out)
}

// TestStructFieldNamedLikeGoSiblingMethod: a method a hand-written .go file
// declares on a struct clashes with its field of the same name too.
func TestStructFieldNamedLikeGoSiblingMethod(t *testing.T) {
	files, galaFile := samePackageModule(".",
		"package main\n\nfunc (s S) Name() string { return \"x\" }\n",
		"package main\n\nstruct S(Name string)\n\nfunc main() {\n    Println(S(\"a\").Name)\n}\n")
	_, err := transpileInModule(t, files, galaFile)
	var se *galaerr.SemanticError
	require.ErrorAs(t, err, &se)
	assert.Equal(t, galaerr.CodeFieldNamedLikeMethod, se.Code)
	assert.Equal(t, 3, se.Line)
}
