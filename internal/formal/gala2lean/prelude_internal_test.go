package gala2lean

import (
	"os"
	"regexp"
	"strings"
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

// TestHiddenNamesNeverResolve checks that a name hideLibNames renames can
// resolve to nothing: GALA rejects a declaration whose name starts with
// _gala_ (GALA-E0097), and the renamed name is no library name either.
func TestHiddenNamesNeverResolve(t *testing.T) {
	require.True(t, strings.HasPrefix(hiddenPrefix, "_gala_"))
	g := newGen()
	g.collections = true
	for x := range stdFuncs {
		require.False(t, g.isStdFunc(hiddenPrefix+x), x)
	}
	for x := range seqCtors {
		require.Empty(t, seqCtors[hiddenPrefix+x], x)
		require.False(t, g.libName(hiddenPrefix+x, true) && seqCtors[hiddenPrefix+x] != "", x)
	}
}
