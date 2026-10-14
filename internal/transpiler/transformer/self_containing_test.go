package transformer_test

import (
	"testing"

	"martianoff/gala/galaerr"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSelfContainingType checks that a type holding itself by value is
// GALA-E0077, and that recursion through a reference is accepted.
func TestSelfContainingType(t *testing.T) {
	trans := newTranspiler()

	rejected := []struct {
		name  string
		input string
		msg   string
	}{
		{
			name:  "struct through Option",
			input: "package main\n\nstruct Node(value int, next Option[Node])\n",
			msg:   "struct Node holds itself by value: field next has type Option[Node], which contains a Node",
		},
		{
			name:  "struct through a tuple",
			input: "package main\n\nstruct Pair(id int, rest Tuple[int, Pair])\n",
			msg:   "struct Pair holds itself by value: field rest",
		},
		{
			name:  "struct through List",
			input: "package main\n\nimport . \"martianoff/gala/collection_immutable\"\n\nstruct Tree(value int, children List[Tree])\n",
			msg:   "struct Tree holds itself by value: field children",
		},
		{
			name:  "struct itself",
			input: "package main\n\ntype Loop struct {\n    next Loop\n}\n",
			msg:   "struct Loop holds itself by value: field next",
		},
		{
			name:  "mutual recursion",
			input: "package main\n\nstruct A(b B)\nstruct B(a Option[A])\n",
			msg:   "holds itself by value",
		},
		{
			name:  "sealed case field through Option",
			input: "package main\n\nsealed type Expr {\n    case Lit(value int)\n    case Neg(inner Option[Expr])\n}\n",
			msg:   "sealed type Expr holds itself by value: field inner",
		},
	}
	for _, tt := range rejected {
		t.Run("rejected/"+tt.name, func(t *testing.T) {
			_, err := trans.Transpile(tt.input, "")
			require.Error(t, err)
			var semErr *galaerr.SemanticError
			require.ErrorAs(t, err, &semErr)
			assert.Equal(t, galaerr.CodeSelfContainingType, semErr.Code)
			assert.Contains(t, semErr.Msg, tt.msg)
		})
	}

	accepted := []struct {
		name  string
		input string
	}{
		{"sealed case field of the type itself", "package main\n\nsealed type L {\n    case End()\n    case Cons(head int, tail L)\n}\n"},
		{"generic sealed case field", "package main\n\nsealed type Tree[T any] {\n    case Leaf()\n    case Branch(left Tree[T], value T, right Tree[T])\n}\n"},
		{"pointer field", "package main\n\nstruct Node(value int, next *Node)\n"},
		{"Option of a pointer", "package main\n\nstruct Node(value int, next Option[*Node])\n"},
		{"Array", "package main\n\nimport . \"martianoff/gala/collection_immutable\"\n\nstruct Tree(value int, children Array[Tree])\n"},
		{"HashMap", "package main\n\nimport . \"martianoff/gala/collection_immutable\"\n\nstruct Dir(name string, entries HashMap[string, Dir])\n"},
		{"function field", "package main\n\nstruct Lazy(get func() Lazy)\n"},
	}
	for _, tt := range accepted {
		t.Run("accepted/"+tt.name, func(t *testing.T) {
			_, err := trans.Transpile(tt.input, "")
			require.NoError(t, err)
		})
	}
}

// TestSelfContainingGenericTerminates checks that the walk ends on a generic
// type that holds itself by value, reached from another declaration.
func TestSelfContainingGenericTerminates(t *testing.T) {
	_, err := newTranspiler().Transpile("package main\n\nstruct Box[T any](v Option[Box[T]])\nstruct User(b Box[int])\n", "")
	require.Error(t, err)
	var semErr *galaerr.SemanticError
	require.ErrorAs(t, err, &semErr)
	assert.Equal(t, galaerr.CodeSelfContainingType, semErr.Code)
	assert.Contains(t, semErr.Msg, "Box")
}
