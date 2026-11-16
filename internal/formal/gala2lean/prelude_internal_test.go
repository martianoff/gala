package gala2lean

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"martianoff/gala/internal/formal/coreexport"
	"martianoff/gala/internal/formal/coreexport/analyzerhints"
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
// resolve to nothing. A name resolves to a declaration of the program, and
// GALA rejects one whose name starts with _gala_ (GALA-E0097), or to an
// entry of a library table, and no table holds a renamed name.
func TestHiddenNamesNeverResolve(t *testing.T) {
	for _, pre := range []string{otherDotPrefix, noCollPrefix} {
		require.True(t, strings.HasPrefix(pre, "_gala_"))
		for _, x := range []string{"ArrayOf", "ListOf", "ArrayTabulate", "When", "Tuple", "int64",
			"Array", "List", "HashMap", "HashSetOf"} {
			fn, seq := libNames(x)
			require.True(t, fn || seq, x)
			h := pre + x
			fn, seq = libNames(h)
			require.False(t, fn || seq, h)
			_, std := stdFuncs[h]
			_, gen := stdGeneric[h]
			require.False(t, std || gen || seqCtors[h] != "" || stdCtors[h] != "" || hashTypes[h], h)
			require.Equal(t, x, unhide(h))
		}
	}
}

// TestHideLibNamesKeepsDeclared checks that a library name the program
// declares itself is not renamed, in any file: a package-level declaration
// is seen in every file of its package.
func TestHideLibNamesKeepsDeclared(t *testing.T) {
	p, err := coreexport.Export([]coreexport.Source{
		{Path: "a.gala", Text: "package main\n\nfunc ListOf(n int) int = n\n"},
		{Path: "b.gala", Text: "package main\n\nfunc main() { Println(ListOf(1), Some(2)) }\n"},
	}, coreexport.Options{LiteralType: analyzerhints.LiteralType})
	require.NoError(t, err)
	var names []string
	var walk func(n coreexport.Node)
	walk = func(n coreexport.Node) {
		if nm, ok := n.(*coreexport.Name); ok {
			names = append(names, nm.Name)
		}
		walkChildren(n, walk)
	}
	for _, f := range hideLibNames(p).Files {
		walk(f)
	}
	// ListOf is declared; Some is std's and not a collections name.
	require.Contains(t, names, "ListOf")
	require.Contains(t, names, "Some")
}
