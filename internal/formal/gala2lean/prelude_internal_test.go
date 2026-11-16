package gala2lean

import (
	"os"
	"regexp"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestPreludeNamesReserved checks that every definition of the prelude,
// which every translated program opens, is a reserved name (leanReserved),
// so that a GALA declaration of the same name is renamed rather than
// colliding with it.
func TestPreludeNamesReserved(t *testing.T) {
	text := readRunfiles(t, "PRELUDE_LEAN")[os.Getenv("PRELUDE_LEAN")]
	defs := regexp.MustCompile(`(?m)^(?:def|abbrev|inductive|structure|class) ([A-Za-z_][A-Za-z0-9_]*)`).FindAllStringSubmatch(text, -1)
	require.NotEmpty(t, defs)
	for _, d := range defs {
		require.True(t, leanReserved[d[1]], "prelude definition %s is not in leanReserved", d[1])
	}
}
