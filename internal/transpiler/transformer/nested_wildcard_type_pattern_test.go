package transformer_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNestedWildcardTypePattern covers `e: Tagged[_]` nested inside another
// pattern. Like the top-level form, it tests the value against the type's
// marker interface, so it matches every instantiation of Tagged; a
// `std.As[Tagged[any]]` would match only a Tagged[any]. It covers every place
// a sub-pattern sits: an extractor's result, a tuple element, a struct field
// and a sequence element. A typed pattern inside a tuple extractor is lowered
// rather than dropped.
func TestNestedWildcardTypePattern(t *testing.T) {
	trans := newDefaultsTranspiler()

	const decls = `
struct Tagged[T any](Tag T)

func (e Tagged[T]) Error() string = s"tag ${e.Tag}"

struct Holder[T any](V T)
struct Plain(Err error)
`

	cases := []struct {
		name        string
		imports     string
		body        string
		contains    []string
		notContains []string
	}{
		{
			name: "inside Failure",
			body: `val r = Failure[int](Tagged(Tag = "x")) match {
        case Failure(e: Tagged[_]) => s"tagged $e"
        case _ => "other"
    }
    Println(r)`,
			contains:    []string{"std.As[TaggedInstance](", ".IsTagged()"},
			notContains: []string{"Tagged[any]"},
		},
		{
			name: "inside Some of a concrete instantiation",
			body: `val r = Some(Tagged(Tag = 1)) match {
        case Some(e: Tagged[_]) => s"tagged ${e.Tag}"
        case _ => "other"
    }
    Println(r)`,
			contains:    []string{"std.As[TaggedInstance](", ".IsTagged()"},
			notContains: []string{"Tagged[any]"},
		},
		{
			name: "inside a tuple extractor",
			body: `val r = (Tagged(Tag = 1.5), 2) match {
        case Tuple(e: Tagged[_], n) => s"tuple ${e.Tag} $n"
        case _ => "other"
    }
    Println(r)`,
			contains:    []string{"std.As[TaggedInstance](", "e :="},
			notContains: []string{"Tagged[any]"},
		},
		{
			name: "inside a generic struct pattern",
			body: `val r = Holder(V = Tagged(Tag = 1)) match {
        case Holder(e: Tagged[_]) => s"holder ${e.Tag}"
        case _ => "other"
    }
    Println(r)`,
			contains:    []string{"std.As[TaggedInstance](", ".IsTagged()"},
			notContains: []string{"Tagged[any]"},
		},
		{
			name: "inside a struct pattern over an error field",
			body: `val r = Plain(Tagged(Tag = 2)) match {
        case Plain(e: Tagged[_]) => s"plain $e"
        case _ => "other"
    }
    Println(r)`,
			contains:    []string{"std.As[TaggedInstance](", ".IsTagged()"},
			notContains: []string{"Tagged[any]"},
		},
		{
			name:    "inside a sequence pattern",
			imports: `import . "martianoff/gala/collection_immutable"`,
			body: `val r = ArrayOf(Tagged(Tag = 2)) match {
        case Array(e: Tagged[_]) => s"array ${e.Tag}"
        case _ => "other"
    }
    Println(r)`,
			contains:    []string{"std.As[TaggedInstance](", ".IsTagged()"},
			notContains: []string{"Tagged[any]"},
		},
		{
			// A concrete type argument in a sequence pattern still asserts to
			// that instantiation.
			name:    "concrete type inside a sequence pattern",
			imports: `import . "martianoff/gala/collection_immutable"`,
			body: `val r = ArrayOf(Tagged(Tag = 2)) match {
        case Array(e: Tagged[int]) => s"array ${e.Tag}"
        case _ => "other"
    }
    Println(r)`,
			contains: []string{"std.As[Tagged[int]]("},
		},
		{
			// A concrete type argument still asserts to that instantiation.
			name: "concrete type inside a tuple extractor",
			body: `val r = (Tagged(Tag = 1.5), 2) match {
        case Tuple(e: Tagged[float64], n) => s"tuple ${e.Tag} $n"
        case _ => "other"
    }
    Println(r)`,
			contains: []string{"std.As[Tagged[float64]]("},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := "package main\n\n" + tc.imports + "\n" + decls + "\nfunc main() {\n    " + tc.body + "\n}\n"
			out, err := trans.Transpile(src, "nested_wildcard_type_pattern_test.gala")
			require.NoError(t, err)
			for _, want := range tc.contains {
				assert.Contains(t, out, want)
			}
			for _, unwanted := range tc.notContains {
				assert.NotContains(t, out, unwanted)
			}
		})
	}
}
