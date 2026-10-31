package transformer_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBareCaseTestsInterfaceSubject covers a bare zero-field sealed case
// (`case Square =>`) on a subject of an interface or type-parameter type. It
// tests for the case, exactly as `case Square() =>` does and as it does on a
// subject of the sealed type: the subject is asserted to the sealed type
// first, and a value of another type falls through. It used to bind a new
// variable named Square that matched every value.
func TestBareCaseTestsInterfaceSubject(t *testing.T) {
	trans := newDefaultsTranspiler()

	const decls = `
import "fmt"

sealed type Shape {
    case Circle(R int)
    case Square()
}

sealed type Wrapped {
    case Wrap(Inner any)
    case Empty()
}

sealed type Mark {
    case dot()
}
`

	cases := []struct {
		name     string
		body     string
		contains []string
		absent   []string
	}{
		{
			name: "any subject",
			body: `func describe(v any) string = v match {
    case Square => "square"
    case _ => "other"
}`,
			contains: []string{"std.As[Shape](obj)", "Square{}.Unapply("},
			absent:   []string{"Square := obj"},
		},
		{
			name: "guarded, beside a case of another sealed type",
			body: `func describe(v any, b bool) string = v match {
    case Square if b => "square"
    case Empty => "empty"
    case _ => "other"
}`,
			contains: []string{"std.As[Shape](obj)", "std.As[Wrapped](obj)", "Empty{}.Unapply("},
		},
		{
			name: "nested in a field of type any",
			body: `func describe(w Wrapped) string = w match {
    case Wrap(Square) => "wrapped square"
    case _ => "other"
}`,
			contains: []string{"std.As[Shape](", "Square{}.Unapply("},
		},
		{
			name: "type parameter subject",
			body: `func describe[T any](x T) string = x match {
    case Square => "square"
    case _ => "other"
}`,
			contains: []string{"std.As[Shape](any(obj))"},
		},
		{
			// An unexported case name binds on a subject that is not its own
			// sealed type.
			name: "lowercase name binds",
			body: `func describe(v any) string = v match {
    case dot => fmt.Sprint(dot)
}`,
			contains: []string{"dot := obj"},
			absent:   []string{"std.As[Mark]"},
		},
		{
			// On its own sealed type the bare name already tested the case,
			// and is not asserted.
			name: "sealed subject is not asserted",
			body: `func describe(s Shape) string = s match {
    case Square => "square"
    case Circle(_) => "circle"
}`,
			contains: []string{"Square{}.Unapply(obj)"},
			absent:   []string{"std.As[Shape]"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := "package main\n" + decls + "\n" + tc.body + "\n\nfunc main() {}\n"
			out, err := trans.Transpile(src, "bare_case_interface_subject_test.gala")
			require.NoError(t, err)
			for _, want := range tc.contains {
				assert.Contains(t, out, want)
			}
			for _, bad := range tc.absent {
				assert.NotContains(t, out, bad)
			}
		})
	}
}

// TestBareCaseRejectsTypeOutsideInterface: a bare case whose sealed type an
// interface subject can never hold never matches, so it is an error, as
// `case Square()` is.
func TestBareCaseRejectsTypeOutsideInterface(t *testing.T) {
	src := `package main

sealed type Shape {
    case Circle(R int)
    case Square()
}

func describe(err error) string = err match {
    case Square => "square"
    case _ => "other"
}

func main() {}
`
	_, err := newDefaultsTranspiler().Transpile(src, "bare_case_interface_subject_test.gala")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Shape does not implement error (missing Error)")
}

// TestBareCaseFromAnotherPackage: a zero-field case of another package tests
// the value whether it is named through a dot import or qualified (by the
// package's name or an alias), on an interface subject and on a subject of
// its sealed type. The qualified spelling used to compare the subject with
// the case's type, which Go rejects.
func TestBareCaseFromAnotherPackage(t *testing.T) {
	root := t.TempDir()
	write := func(rel, content string) {
		t.Helper()
		full := filepath.Join(root, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(full), 0755))
		require.NoError(t, os.WriteFile(full, []byte(content), 0644))
	}
	write("gala.mod", "module example.com/bare\n\ngala dev\n")
	write("shapes/shapes.gala", "package shapes\n\nsealed type Shape {\n    case Circle(R int)\n    case Square()\n}\n")

	tests := []struct {
		name     string
		src      string
		contains []string
		absent   []string
	}{
		{
			name: "dot import, any subject",
			src: `package main

import . "example.com/bare/shapes"

func describe(v any) string = v match {
    case Square => "square"
    case _ => "other"
}

func main() {}
`,
			contains: []string{"std.As[shapes.Shape](obj)", "shapes.Square{}.Unapply("},
			absent:   []string{"Square := obj"},
		},
		{
			name: "qualified and aliased, any subject",
			src: `package main

import (
    "example.com/bare/shapes"
    sh "example.com/bare/shapes"
)

sealed type Box {
    case Wrap(Inner any)
}

func describe(v any) string = v match {
    case shapes.Square => "square"
    case Wrap(sh.Square) => "wrapped square"
    case _ => "other"
}

func main() {}
`,
			contains: []string{"std.As[shapes.Shape](obj)", "shapes.Square{}.Unapply("},
			absent:   []string{"== shapes.Square"},
		},
		{
			name: "qualified, sealed subject, exhaustive",
			src: `package main

import "example.com/bare/shapes"

func describe(s shapes.Shape) string = s match {
    case shapes.Square => "square"
    case shapes.Circle(_) => "circle"
}

func main() {}
`,
			contains: []string{"shapes.Square{}.Unapply(obj)"},
			absent:   []string{"std.As[shapes.Shape]", "== shapes.Square"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := transpileCrossPkg(t, root, tt.src)
			require.NoError(t, err)
			for _, want := range tt.contains {
				assert.Contains(t, out, want, "generated:\n%s", out)
			}
			for _, bad := range tt.absent {
				assert.NotContains(t, out, bad, "generated:\n%s", out)
			}
		})
	}
}
