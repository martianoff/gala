package lsp_test

import (
	"strings"
	"testing"
)

const opaqueHoverSrc = `package main

opaque type UserID int64

func (u UserID) Next() UserID = u + 1

func main() {
    val id = UserID(1)
    Println(id.Hash())
}
`

// An opaque type renders as its declaration, `opaque type UserID int64`, with
// its declared methods and the Hash/Compare the transpiler synthesizes marked
// as such; a synthesized method selected on a value hovers as a method of the
// type.
func TestHover_OpaqueType(t *testing.T) {
	h := newHarness(t)
	uri := openFileOnDisk(t, h, opaqueHoverSrc)
	settle(t, h, uri, opaqueHoverSrc, "Println(id.Hash", "id")

	for _, tt := range []struct {
		name         string
		anchor, word string
		want         []string
	}{
		{
			name:   "declaration",
			anchor: "opaque type UserID", word: "UserID",
			want: []string{
				"opaque type UserID int64",
				"- `Next() UserID`\n",
				"- `Hash() uint32` *(synthesized)*",
				"- `Compare(other UserID) int` *(synthesized)*",
			},
		},
		{
			name:   "synthesized method on a value",
			anchor: "id.Hash()", word: "Hash",
			want:   []string{"func (UserID) Hash() uint32", "Synthesized"},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			line, col := locate(t, opaqueHoverSrc, tt.anchor, tt.word)
			hover, err := h.Hover(uri, line, col)
			if err != nil {
				t.Fatal(err)
			}
			if hover == nil {
				t.Fatal("no hover")
			}
			got := hover.Contents.Value()
			for _, want := range tt.want {
				if !strings.Contains(got, want) {
					t.Errorf("hover is missing %q\n--- got ---\n%s", want, got)
				}
			}
		})
	}

	t.Run("dot completion offers the synthesized methods", func(t *testing.T) {
		line, col := locate(t, opaqueHoverSrc, "id.Hash()", "Hash")
		list, err := h.Completion(uri, line, col-1)
		if err != nil {
			t.Fatal(err)
		}
		labels := collectLabels(list)
		for _, want := range []string{"Hash() uint32", "Compare(other UserID) int", "Next() UserID"} {
			if !labels[want] {
				t.Errorf("completion is missing %q; got %v", want, labels)
			}
		}
	})
}
