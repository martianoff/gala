package analyzer_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestUndefinedSymbol_TransitiveImportDoesNotLeak covers names that reach the
// compilation only because an import imports their package. The GALA
// `strings` package imports the collection packages for its own use; a file
// importing only `strings` used to be able to call `ArrayOf(...)` unqualified,
// and the transformer bound it to whichever collection package it found
// first. A bare name must come from this file's package, its dot imports, or
// std.
func TestUndefinedSymbol_TransitiveImportDoesNotLeak(t *testing.T) {
	tests := []struct {
		name     string
		src      string
		symbol   string
		hint     string
		siblings map[string]string
	}{
		{
			name: "function from a transitively loaded package",
			src: `package main

import "martianoff/gala/strings"

func main() {
    Println(ArrayOf(1, 2, 3))
    Println(strings.ToUpper("x"))
}
`,
			symbol: "ArrayOf",
			hint:   "none of which this file imports",
		},
		{
			name: "function from a package imported by name",
			src: `package main

import "martianoff/gala/collection_immutable"

func main() {
    Println(ArrayOf(1, 2, 3))
}
`,
			symbol: "ArrayOf",
			hint:   "call it as `collection_immutable.ArrayOf`",
		},
		{
			name: "function from a package imported under an alias",
			src: `package main

import ci "martianoff/gala/collection_immutable"

func main() {
    Println(EmptyArray[int]())
    Println(ci.ArrayOf(1))
}
`,
			symbol: "EmptyArray",
			hint:   "call it as `ci.EmptyArray`",
		},
		{
			name: "type in a signature from a transitively loaded package",
			src: `package main

import "martianoff/gala/strings"

func total(xs Array[int]) int = xs.Size()

func main() {
    Println(strings.ToUpper("x"))
}
`,
			symbol: "Array",
			hint:   "none of which this file imports",
		},
		{
			name: "type in a val annotation from a transitively loaded package",
			src: `package main

import "martianoff/gala/strings"

func main() {
    val parts HashMap[string, int] = strings.Split("a", ",").GroupBy((s) => s).MapValues((v) => v.Size())
    Println(parts)
}
`,
			symbol: "HashMap",
			hint:   "none of which this file imports",
		},
		{
			name: "a sibling file's dot import does not reach this file",
			src: `package main

func main() {
    Println(ArrayOf(1))
}
`,
			siblings: map[string]string{
				"other.gala": `package main

import . "martianoff/gala/collection_immutable"

func seed() Array[int] = ArrayOf(1)
`,
			},
			symbol: "ArrayOf",
			hint:   "none of which this file imports",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := analyzeSources(t, tt.src, tt.siblings)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "GALA-E0023")
			assert.Contains(t, err.Error(), "undefined: "+tt.symbol)
			assert.Contains(t, err.Error(), tt.hint)
		})
	}
}

// TestUndefinedSymbol_InScopeNamesStillResolve is the other half: names this
// file can legitimately use unqualified keep compiling.
func TestUndefinedSymbol_InScopeNamesStillResolve(t *testing.T) {
	tests := []struct {
		name     string
		src      string
		siblings map[string]string
	}{
		{
			name: "dot import",
			src: `package main

import . "martianoff/gala/collection_immutable"

func total(xs Array[int]) int = xs.Size()

func main() {
    Println(total(ArrayOf(1, 2)))
}
`,
		},
		{
			name: "std prelude without any import",
			src: `package main

func main() {
    val o Option[int] = Some(1)
    Println(o.GetOrElse(0))
}
`,
		},
		{
			name: "declaration in a sibling file of the same package",
			src: `package main

func main() {
    Println(helper())
}
`,
			siblings: map[string]string{
				"other.gala": `package main

func helper() int = 1
`,
			},
		},
		{
			name: "type parameter sharing a name with a transitively loaded type",
			src: `package main

import "martianoff/gala/strings"

func first[Array any](xs Array) Array = xs

func main() {
    Println(first(strings.ToUpper("x")))
}
`,
		},
		{
			name: "local type sharing a name with a transitively loaded type",
			src: `package main

import "martianoff/gala/strings"

struct Array(Items string)

func wrap(s string) Array = Array(s)

func main() {
    Println(wrap(strings.ToUpper("x")).Items)
}
`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.NoError(t, analyzeSources(t, tt.src, tt.siblings))
		})
	}
}
