package transformer_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"martianoff/gala/galaerr"
)

// TestBareCaseThatCannotTestInterfaceSubject: on a subject of an interface or
// type-parameter type, a bare exported name of a sealed case that the
// pattern cannot test is GALA-E0039, not a binding that matches every value.
// A case with fields is spelled with its fields (`case Circle(_)`), as on a
// subject of its sealed type. A case of a generic sealed type (`case None`)
// cannot be tested without type arguments the pattern does not name.
func TestBareCaseThatCannotTestInterfaceSubject(t *testing.T) {
	const decls = `
sealed type Shape {
    case Circle(R int)
    case Square()
}

sealed type Wrapped {
    case Wrap(Inner any)
    case Empty()
}

sealed type Pair[A any, B any] {
    case Both(L A, R B)
    case Neither()
}
`
	cases := []struct {
		name string
		body string
		msg  string
		hint string
	}{
		{
			name: "case with fields, any subject",
			body: `func describe(v any) string = v match {
    case Circle => "circle"
    case _ => "other"
}`,
			msg:  "`Circle` is a variant of sealed type \"Shape\", so `case Circle` binds a new variable",
			hint: "write `case Circle(_)` to match the variant",
		},
		{
			name: "case with fields, the only arm",
			body: `func describe(v any) string = v match {
    case Circle => "circle"
}`,
			msg:  "`Circle` is a variant of sealed type \"Shape\"",
			hint: "write `case Circle(_)`",
		},
		{
			name: "case with fields, guarded",
			body: `func describe(v any, b bool) string = v match {
    case Circle if b => "circle"
    case _ => "other"
}`,
			msg:  "`Circle` is a variant of sealed type \"Shape\"",
			hint: "write `case Circle(_)`",
		},
		{
			name: "case with fields, nested in a field of type any",
			body: `func describe(w Wrapped) string = w match {
    case Wrap(Circle) => "wrapped circle"
    case _ => "other"
}`,
			msg:  "`Circle` is a variant of sealed type \"Shape\"",
			hint: "write `case Circle(_)`",
		},
		{
			name: "case with fields, type parameter subject",
			body: `func describe[T any](x T) string = x match {
    case Circle => "circle"
    case _ => "other"
}`,
			msg:  "`Circle` is a variant of sealed type \"Shape\"",
			hint: "write `case Circle(_)`",
		},
		{
			name: "generic case, any subject",
			body: `func describe(v any) string = v match {
    case None => "none"
    case _ => "other"
}`,
			msg:  "`None` is a variant of generic sealed type \"Option\", so `case None` binds a new variable and matches every value: a value of type any cannot be tested for None without Option's type arguments",
			hint: "write the type arguments (e.g. `case None[int]()`), match on a typed subject (e.g. `Option[int]`), or use a lowercase name to bind the value",
		},
		{
			// Spelling the fields alone would not test the value either: the
			// hint spells the type arguments too.
			name: "generic case with fields, any subject",
			body: `func describe(v any) string = v match {
    case Some => "some"
    case _ => "other"
}`,
			msg:  "`Some` is a variant of generic sealed type \"Option\"",
			hint: "`case Some[int](_)`",
		},
		{
			name: "generic case with fields, type parameter subject",
			body: `func describe[T any](x T) string = x match {
    case Both => "both"
    case _ => "other"
}`,
			msg:  "`Both` is a variant of generic sealed type \"Pair\"",
			hint: "`case Both[int, int](_, _)`",
		},
		{
			name: "generic case, the only arm",
			body: `func describe(v any) string = v match {
    case None => "none"
}`,
			msg:  "`None` is a variant of generic sealed type \"Option\"",
			hint: "`Option[int]`",
		},
		{
			name: "generic case, guarded, two type parameters",
			body: `func describe(v any, b bool) string = v match {
    case Neither if b => "neither"
    case _ => "other"
}`,
			msg:  "`Neither` is a variant of generic sealed type \"Pair\"",
			hint: "`Pair[int, int]`",
		},
		{
			name: "generic case, nested in a field of type any",
			body: `func describe(w Wrapped) string = w match {
    case Wrap(None) => "wrapped none"
    case _ => "other"
}`,
			msg:  "`None` is a variant of generic sealed type \"Option\"",
			hint: "`Option[int]`",
		},
		{
			name: "generic case, type parameter subject",
			body: `func describe[T any](x T) string = x match {
    case None => "none"
    case _ => "other"
}`,
			msg:  "`None` is a variant of generic sealed type \"Option\"",
			hint: "`Option[int]`",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := "package main\n" + decls + "\n" + tc.body + "\n\nfunc main() {}\n"
			_, err := newDefaultsTranspiler().Transpile(src, "bare_case_untestable_test.gala")
			requireBareCaseError(t, err, tc.msg, tc.hint)
		})
	}
}

// TestBareCaseOnInterfaceSubjectStillTests: the spellings that test the
// value, or bind it, on an interface subject are unchanged.
func TestBareCaseOnInterfaceSubjectStillTests(t *testing.T) {
	const decls = `
import "fmt"

sealed type Shape {
    case Circle(R int)
    case Square()
}

sealed type Slot[T any] {
    case Filled(V T)
    case blank()
}
`
	cases := []struct {
		name     string
		body     string
		contains []string
	}{
		{
			name: "fields spelled",
			body: `func describe(v any) string = v match {
    case Circle(_) => "circle"
    case Square => "square"
    case _ => "other"
}`,
			contains: []string{"std.As[Shape](obj)", "Circle{}.Unapply(", "Square{}.Unapply("},
		},
		{
			name: "lowercase case of a generic sealed type binds",
			body: `func describe(v any) string = v match {
    case blank => fmt.Sprint(blank)
}`,
			contains: []string{"blank := obj"},
		},
		{
			name: "generic case with its type arguments",
			body: `func describe(v any) string = v match {
    case None[int]() => "none"
    case Some[int](n) => fmt.Sprint(n)
    case _ => "other"
}`,
			contains: []string{"std.As[std.Option[int]](obj)"},
		},
		{
			// A capitalized value in scope is compared, as before, even when
			// it is named like a case of a generic sealed type.
			name: "value named like a generic case",
			body: `func describe(v any) string {
    val Success = "ok"
    return v match {
        case Success => "success"
        case _ => "other"
    }
}`,
			contains: []string{"== Success"},
		},
		{
			name: "generic case on a typed subject",
			body: `func describe(o Option[int]) string = o match {
    case None => "none"
    case Some(_) => "some"
}`,
			contains: []string{"_unapply_std_None_"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := "package main\n" + decls + "\n" + tc.body + "\n\nfunc main() {}\n"
			out, err := newDefaultsTranspiler().Transpile(src, "bare_case_untestable_test.gala")
			require.NoError(t, err)
			for _, want := range tc.contains {
				assert.Contains(t, out, want, "generated:\n%s", out)
			}
		})
	}
}

// TestBareCaseThatCannotTestFromAnotherPackage: the cases of another package,
// named through a dot import, qualified, or through an import alias. A
// qualified case with fields (`case shapes.Circle`) is an error on any
// subject: it used to lower to a comparison with the case's type, which Go
// rejects.
func TestBareCaseThatCannotTestFromAnotherPackage(t *testing.T) {
	root := t.TempDir()
	write := func(rel, content string) {
		t.Helper()
		full := filepath.Join(root, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(full), 0755))
		require.NoError(t, os.WriteFile(full, []byte(content), 0644))
	}
	write("gala.mod", "module example.com/bare\n\ngala dev\n")
	write("shapes/shapes.gala", `package shapes

sealed type Shape {
    case Circle(R int)
    case Square()
}

sealed type Opt[T any] {
    case Has(V T)
    case Nope()
}
`)
	match := func(imports, subject, arm string) string {
		return "package main\n\n" + imports + "\n\nfunc describe(v " + subject + ") string = v match {\n    case " + arm +
			" => \"hit\"\n    case _ => \"other\"\n}\n\nfunc main() {}\n"
	}
	const (
		plain = `import "example.com/bare/shapes"`
		dot   = `import . "example.com/bare/shapes"`
		alias = `import sh "example.com/bare/shapes"`
	)
	tests := []struct {
		name string
		src  string
		msg  string
		hint string
	}{
		{
			name: "dot import, case with fields",
			src:  match(dot, "any", "Circle"),
			msg:  "`Circle` is a variant of sealed type \"Shape\", so `case Circle` binds a new variable",
			hint: "write `case Circle(_)`",
		},
		{
			name: "dot import, generic case",
			src:  match(dot, "any", "Nope"),
			msg:  "`Nope` is a variant of generic sealed type \"Opt\"",
			hint: "`Opt[int]`",
		},
		{
			name: "qualified case with fields, any subject",
			src:  match(plain, "any", "shapes.Circle"),
			msg:  "`case shapes.Circle` names variant Circle of sealed type \"Shape\" without its fields",
			hint: "write `case shapes.Circle(_)` to match the variant",
		},
		{
			name: "qualified case with fields, sealed subject",
			src:  match(plain, "shapes.Shape", "shapes.Circle"),
			msg:  "`case shapes.Circle` names variant Circle of sealed type \"Shape\" without its fields",
			hint: "write `case shapes.Circle(_)`",
		},
		{
			name: "aliased generic case, any subject",
			src:  match(alias, "any", "sh.Nope"),
			msg:  "`case sh.Nope` cannot test a value of type any: Nope is a variant of generic sealed type \"Opt\"",
			hint: "match on a typed subject (e.g. `sh.Opt[int]`)",
		},
		{
			name: "qualified std case, any subject",
			src:  match("", "any", "std.None"),
			msg:  "`case std.None` cannot test a value of type any",
			hint: "`std.Option[int]`",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := transpileCrossPkg(t, root, tt.src)
			requireBareCaseError(t, err, tt.msg, tt.hint)
		})
	}
}

// requireBareCaseError checks err is GALA-E0039 with msg in its message and
// hint in its hint.
func requireBareCaseError(t *testing.T, err error, msg, hint string) {
	t.Helper()
	require.Error(t, err)
	var se *galaerr.SemanticError
	require.ErrorAs(t, err, &se, "got: %v", err)
	assert.Equal(t, galaerr.CodeBareVariantBinding, se.Code, "got: %v", err)
	assert.Contains(t, se.Msg, msg)
	assert.Contains(t, se.Hint, hint)
}
