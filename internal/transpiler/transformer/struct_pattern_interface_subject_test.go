package transformer_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestStructPatternAssertsInterfaceSubject covers a struct extractor pattern
// on a subject whose static type is an interface other than `any`: `error`, a
// GALA interface, or a Go interface. The struct's fields are only reachable
// after the subject is asserted to the struct, and the arm must be gated on
// that assertion, at the top level and nested inside another pattern.
func TestStructPatternAssertsInterfaceSubject(t *testing.T) {
	trans := newDefaultsTranspiler()

	const decls = `
import "fmt"

type Shape interface {
    Area() float64
}

struct Circle(R float64)

func (c Circle) Area() float64 = c.R * c.R

func (c Circle) String() string = "circle"

struct NotFound(Key string)

func (e NotFound) Error() string = e.Key

struct Box[T any](V T)

func (b Box[T]) Error() string = "box"
`

	cases := []struct {
		name     string
		body     string
		contains []string
		absent   []string
	}{
		{
			name: "error subject",
			body: `func describe(err error) string = err match {
    case NotFound(k) => k
    case _ => "other"
}`,
			contains: []string{"std.As[NotFound](obj)", ".Key.Get()"},
			absent:   []string{"obj.Key"},
		},
		{
			name: "error subject with a guard",
			body: `func describe(err error) string = err match {
    case NotFound(k) if k == "x" => "x"
    case _ => "other"
}`,
			contains: []string{"std.As[NotFound](obj)"},
			absent:   []string{"obj.Key"},
		},
		{
			name: "generic struct names its type arguments",
			body: `func describe(err error) string = err match {
    case Box[int](v) => fmt.Sprint(v)
    case _ => "other"
}`,
			contains: []string{"std.As[Box[int]](obj)"},
			absent:   []string{"obj.V"},
		},
		{
			name: "GALA interface subject",
			body: `func describe(sh Shape) string = sh match {
    case Circle(r) => fmt.Sprint(r)
    case _ => "other"
}`,
			contains: []string{"std.As[Circle](obj)"},
			absent:   []string{"obj.R"},
		},
		{
			name: "Go interface subject",
			body: `func describe(st fmt.Stringer) string = st match {
    case Circle(r) => fmt.Sprint(r)
    case _ => "other"
}`,
			contains: []string{"std.As[Circle](obj)"},
			absent:   []string{"obj.R"},
		},
		{
			name: "nested in Some",
			body: `func describe(o Option[error]) string = o match {
    case Some(NotFound(k)) => k
    case _ => "other"
}`,
			contains: []string{"std.As[NotFound]("},
			absent:   []string{".Get().Key"},
		},
		{
			name: "type parameter subject is asserted through any",
			body: `func describe[T any](x T) string = x match {
    case Circle(r) => fmt.Sprint(r)
    case _ => "other"
}`,
			contains: []string{"std.As[Circle](any(obj))"},
			absent:   []string{"obj.R"},
		},
		{
			// A concrete subject keeps reading fields directly.
			name: "concrete subject reads fields directly",
			body: `func describe(c Circle) string = c match {
    case Circle(r) if r > 0 => fmt.Sprint(r)
    case _ => "other"
}`,
			contains: []string{"obj.R.Get()"},
			absent:   []string{"std.As[Circle]"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := "package main\n" + decls + "\n" + tc.body + "\n\nfunc main() {}\n"
			out, err := trans.Transpile(src, "struct_pattern_interface_subject_test.gala")
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

// TestStructPatternRejectsStructOutsideInterface covers a struct pattern whose
// struct an interface subject can never hold: one missing a method of the
// interface, or declaring it with a pointer receiver, which a value of the
// struct lacks. The arm could never match, so it is an error.
func TestStructPatternRejectsStructOutsideInterface(t *testing.T) {
	trans := newDefaultsTranspiler()

	cases := []struct {
		name    string
		src     string
		wantErr string
	}{
		{
			name: "missing method",
			src: `type Shape interface {
    Area() float64
}

struct Label(Text string)

func describe(sh Shape) string = sh match {
    case Label(t) => t
    case _ => "other"
}`,
			wantErr: "Label does not implement Shape (missing Area)",
		},
		{
			name: "pointer receiver",
			src: `struct NotFound(Key string)

func (e *NotFound) Error() string = e.Key

func describe(err error) string = err match {
    case NotFound(k) => k
    case _ => "other"
}`,
			wantErr: "NotFound does not implement error (Error has a pointer receiver)",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := "package main\n\n" + tc.src + "\n\nfunc main() {}\n"
			_, err := trans.Transpile(src, "struct_pattern_interface_subject_test.gala")
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.wantErr)
		})
	}
}
