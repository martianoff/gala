package analyzer

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"martianoff/gala/internal/transpiler"
)

// TestDefaultSourceKeepsWhitespace pins that parameter and field defaults are
// recorded as written. They are re-parsed at every call or construction site
// that omits the argument; text taken from GetText() has its whitespace
// stripped, which re-parses `(a int) => a + 1` as a lambda whose one parameter
// is named `aint`.
func TestDefaultSourceKeepsWhitespace(t *testing.T) {
	rich := analyzeSrc(t, `package defpkg

struct Hooks(
    Name  string,
    OnOne func(int) int = (a int) => a + 1,
)

func greet(name string, f func(string) string = (s string) => s + "!") string = f(name)

type Box struct {
    val n int
}

func (b Box) Scale(k int, f func(int) int = (x int) =>
    x * 2) int = f(b.n)
`)

	hooks := rich.Types["defpkg.Hooks"]
	require.NotNil(t, hooks)
	assert.Equal(t, "(a int) => a + 1", hooks.FieldDefaults["OnOne"])
	assert.Equal(t, transpiler.SourcePos{Line: 5, Column: 26}, hooks.FieldDefaultPos["OnOne"])

	greet := rich.Functions["defpkg.greet"]
	require.NotNil(t, greet)
	assert.Equal(t, `(s string) => s + "!"`, greet.DefaultExprs[1])
	assert.Equal(t, transpiler.SourcePos{Line: 8, Column: 48}, greet.DefaultPos[1])

	box := rich.Types["defpkg.Box"]
	require.NotNil(t, box)
	scale := box.Methods["Scale"]
	require.NotNil(t, scale)
	assert.Equal(t, "(x int) =>\n    x * 2", scale.DefaultExprs[1])
	assert.Equal(t, transpiler.SourcePos{Line: 14, Column: 44}, scale.DefaultPos[1])
}
