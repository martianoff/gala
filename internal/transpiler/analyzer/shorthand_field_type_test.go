package analyzer

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"martianoff/gala/internal/transpiler"
)

// TestShorthandImmutableFieldRecordedAsValueType pins the recorded type of a
// shorthand field declared Immutable[T]. With no keyword it is a val field
// stored as that Immutable[T], the same field as one declared T, so it is
// recorded as T. With `var`, or with an explicit `val` (which wraps it once
// more), it holds the Immutable[T] it names.
func TestShorthandImmutableFieldRecordedAsValueType(t *testing.T) {
	rich := analyzeSrc(t, `package shpkg

struct Box(A Immutable[int64], B int64, var C Immutable[int], val D Immutable[int])
`)

	box := rich.Types["shpkg.Box"]
	require.NotNil(t, box)
	assert.Equal(t, transpiler.BasicType{Name: "int64"}, box.Fields["A"])
	assert.Equal(t, box.Fields["B"], box.Fields["A"])
	assert.Equal(t, "std.Immutable[int]", box.Fields["C"].String())
	assert.Equal(t, "std.Immutable[int]", box.Fields["D"].String())
	assert.Equal(t, []bool{true, true, false, true}, box.ImmutFlags)
}
