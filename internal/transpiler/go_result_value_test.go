package transpiler

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// GoResultsOf undoes GoResultValueOf: the GALA value of n Go results spreads
// back over those n results.
func TestGoResultsOfInvertsGoResultValueOf(t *testing.T) {
	intT, strT, boolT := BasicType{Name: "int"}, BasicType{Name: "string"}, BasicType{Name: "bool"}
	errT := BasicType{Name: "error"}
	for _, results := range [][]Type{
		{intT, errT},
		{strT, intT, errT},
		{intT, intT},
		{strT, strT, boolT},
		{intT, intT, intT, intT, intT, intT, intT, intT, intT, intT},
	} {
		value, ok := GoResultValueOf(results)
		require.True(t, ok)
		got, ok := GoResultsOf(value.Type, len(results))
		require.True(t, ok, "%s", value.Type)
		assert.Equal(t, results, got, "%s", value.Type)
	}
}

// A value that cannot make n results has none.
func TestGoResultsOfRejects(t *testing.T) {
	intT := BasicType{Name: "int"}
	try := func(inner Type) Type {
		return GenericType{Base: NamedType{Package: "std", Name: TypeTry}, Params: []Type{inner}}
	}
	tuple := GenericType{Base: NamedType{Package: "std", Name: TypeTuple}, Params: []Type{intT, intT}}
	for _, tc := range []struct {
		name  string
		value Type
		n     int
	}{
		{"a plain value", intT, 2},
		{"a Try of a plain value for three results", try(intT), 3},
		{"a Tuple of the wrong arity", tuple, 3},
		{"a Try of a Tuple of the wrong arity", try(tuple), 4},
		{"a user type named Tuple", GenericType{Base: NamedType{Package: "main", Name: TypeTuple}, Params: []Type{intT, intT}}, 2},
		{"fewer than two results", try(intT), 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, ok := GoResultsOf(tc.value, tc.n)
			assert.False(t, ok)
		})
	}
}
