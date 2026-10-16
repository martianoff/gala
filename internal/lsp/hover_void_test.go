package lsp_test

import "testing"

// A function or method declared with no result shows no result in hover: not
// `void`, and not a formatting artifact of a missing type.
const hoverVoidSrc = `package main

struct Counter(n int)

func (c Counter) Show() {
    Println(c.n)
}

func (c Counter) Next() Counter = Counter(n = c.n + 1)

func log(s string) {
    Println(s)
}

func Run() {
    val c = Counter(n = 1)
    c.Show()
    log("a")
}
`

func TestHoverOfAVoidDeclarationShowsNoResult(t *testing.T) {
	h := newHarness(t)
	uri := openFileOnDisk(t, h, hoverVoidSrc)
	settle(t, h, uri, hoverVoidSrc, "func Run()", "Run")

	for _, tt := range []struct {
		name          string
		anchor, word  string
		want, notWant []string
	}{
		{
			name:   "a void function",
			anchor: `    log("a")`, word: "log",
			want:    []string{"func log(s string)"},
			notWant: []string{"void", "%!"},
		},
		{
			name:   "a void method",
			anchor: "    c.Show()", word: "Show",
			want:    []string{"Show()"},
			notWant: []string{"void", "%!"},
		},
		{
			name:   "a type's method list",
			anchor: "struct Counter(", word: "Counter",
			want:    []string{"- `Show()`", "- `Next() Counter`"},
			notWant: []string{"void", "%!"},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			assertHover(t, hoverAt(t, h, uri, hoverVoidSrc, tt.anchor, tt.word), tt.want, tt.notWant)
		})
	}
}
