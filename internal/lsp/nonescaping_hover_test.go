package lsp_test

import (
	"testing"
)

// TestHoverShowsNonEscapingParameters covers ownership phase 3's
// non-escaping function parameters (docs/OWNERSHIP.MD#what-the-ide-and-gala-doc-show): hovering
// a function of an imported package names the function parameters it only
// calls during the call, as the ownership analysis inferred them.
func TestHoverShowsNonEscapingParameters(t *testing.T) {
	const src = `package main

import im "martianoff/gala/collection_immutable"

func main() {
    val arr = im.ArrayOf(1, 2, 3)
    arr.ForEach((x) => Println(x))
    Println(arr.Map((x) => x + 1).Size())
}
`
	h := newHarness(t)
	uri := openFileOnDisk(t, h, src)
	settle(t, h, uri, src, "arr.ForEach", "ForEach")

	assertHover(t, hoverAt(t, h, uri, src, "arr.ForEach", "ForEach"),
		[]string{"ForEach", "Non-escaping: `f` is only called during the call."}, nil)
	assertHover(t, hoverAt(t, h, uri, src, "arr.Map", "Map"),
		[]string{"Map", "Non-escaping: `f`"}, nil)
	// No function parameter, no note.
	assertHover(t, hoverAt(t, h, uri, src, ").Size()", "Size"),
		[]string{"Size"}, []string{"Non-escaping"})
}
