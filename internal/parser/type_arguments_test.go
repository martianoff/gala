package parser

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// Explicit type arguments accept the full `type` production. Slice, map and
// function types have no expression spelling, so they used to be rejected
// between `[` and `]` even though the same types are legal in every other type
// position (parameters, return types, struct fields, `val` annotations).
//
// The index/subscript cases are here for the other half of the contract: `[`
// after a primary still parses as an index when its contents are expressions,
// so widening type arguments must not steal ordinary subscripting.
func TestTypeArgumentsAcceptFullTypeGrammar(t *testing.T) {
	p := NewAntlrGalaParser()

	tests := []struct {
		name string
		body string
	}{
		{
			name: "slice type argument on a generic function",
			body: `val x = zero[[]byte]()`,
		},
		{
			name: "map type argument on a generic function",
			body: `val x = zero[map[string]int]()`,
		},
		{
			name: "function type argument",
			body: `val x = zero[func(int) string]()`,
		},
		{
			name: "pointer type argument still parses",
			body: `val x = zero[*Node]()`,
		},
		{
			name: "named type argument still parses",
			body: `val x = zero[Bytes]()`,
		},
		{
			name: "slice among several type arguments",
			body: `val x = EmptyHashMap[string, []byte]()`,
		},
		{
			name: "map among several type arguments",
			body: `val x = EmptyHashMap[string, map[string]int]()`,
		},
		{
			name: "slice of slices",
			body: `val x = EmptyArray[[][]byte]()`,
		},
		{
			name: "slice type argument on a package-qualified generic",
			body: `val x = collection_immutable.EmptyHashMap[string, []byte]()`,
		},
		{
			name: "slice type argument on a generic struct constructor",
			body: `val x = Box[[]byte](Value = payload)`,
		},
		{
			name: "slice type argument on a generic type instantiation",
			body: `val x Box[[]byte] = boxed`,
		},
		{
			name: "index with an identifier subscript",
			body: `val x = arr[i]`,
		},
		{
			name: "index with a computed subscript",
			body: `val x = arr[i + 1]`,
		},
		{
			name: "index with a string subscript",
			body: `val x = m["key"]`,
		},
		{
			name: "index with a pointer-deref subscript",
			body: `val x = arr[*p]`,
		},
		{
			name: "index on a call result",
			body: `val x = items()[0]`,
		},
		{
			name: "chained index",
			body: `val x = grid[r][c]`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			src := "package main\n\nfunc main() {\n    " + tt.body + "\n}\n"
			_, err := p.Parse(src)
			assert.NoError(t, err, "unexpected parse error for: %s", tt.body)
		})
	}
}
