package lsp_test

import (
	"slices"
	"strings"
	"testing"
)

// A Go function returning several results is one GALA value — `(T, error)` a
// Try[T], `(A, B, C)` a Tuple3 — and the language server shows that value:
// on the function, on a name bound to a call, and after the dot of a call.
const goResultsSrc = `package main

import (
    "os"
    "strings"
)

func Run() {
    val data = os.ReadFile("notes.txt")
    val parts = strings.Cut("a=b", "=")
    val raw, err = os.ReadFile("notes.txt")
    Println(data, parts, raw, err)
}
`

func TestHoverShowsTheGALAValueOfAGoCall(t *testing.T) {
	h := newHarness(t)
	uri := openFileOnDisk(t, h, goResultsSrc)
	settle(t, h, uri, goResultsSrc, "func Run()", "Run")

	for _, tt := range []struct {
		name          string
		anchor, word  string
		want, notWant []string
	}{
		{
			name:   "a (T, error) Go function shows its Try and the Go results",
			anchor: `val data = os.ReadFile("notes.txt")`, word: "ReadFile",
			want: []string{"func ReadFile(name string) Try[[]byte]", "Go returns `([]byte, error)`", "`val v, err = os.ReadFile(...)`"},
		},
		{
			name:   "a Go function returning three values shows its Tuple3",
			anchor: `val parts = strings.Cut`, word: "Cut",
			want: []string{"Tuple3[string, string, bool]"},
		},
		{
			name:   "a name bound to a (T, error) call is a Try",
			anchor: "    val data = ", word: "data",
			want: []string{"Try[[]byte]"},
		},
		{
			name:   "a name bound to a (A, B, C) call is a Tuple3",
			anchor: "    val parts = ", word: "parts",
			want: []string{"Tuple3[string, string, bool]"},
		},
		{
			name:   "a multi-name binding takes the raw Go results",
			anchor: "    val raw, err = ", word: "raw",
			want:    []string{"[]byte"},
			notWant: []string{"Try["},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			assertHover(t, hoverAt(t, h, uri, goResultsSrc, tt.anchor, tt.word), tt.want, tt.notWant)
		})
	}
}

// After the dot of a (T, error) Go call, the members are the Try's.
func TestCompletionAfterAGoCallOffersTryMembers(t *testing.T) {
	const src = "package main\n" +
		"\n" +
		"import \"strconv\"\n" +
		"\n" +
		"func main() {\n" +
		"    strconv.Atoi(\"1\").\n" +
		"}\n"
	h := newHarness(t)
	uri := openFileOnDisk(t, h, src)
	settle(t, h, uri, src, "func main()", "main")

	// Line 5, just past the dot that ends `    strconv.Atoi("1").`.
	list, err := h.Completion(uri, 5, len(`    strconv.Atoi("1").`))
	if err != nil {
		t.Fatalf("completion: %v", err)
	}
	labels := labelSlice(list)
	for _, want := range []string{"GetOrElse(", "Map("} {
		if !slices.ContainsFunc(labels, func(l string) bool { return strings.HasPrefix(l, want) }) {
			t.Errorf("completion missing %q, got %v", want, labels)
		}
	}
}
