package testcorpus

import (
	"os"
	"strings"
	"testing"
)

func TestExtract(t *testing.T) {
	c := Extract(t)
	files := Files()
	gala := c.GalaFiles()
	if len(gala) < 600 {
		t.Fatalf("the corpus has %d .gala files", len(gala))
	}
	for _, name := range []string{"std/option.gala", "go_interop/types.go", "docs/errors/GALA-E0011.md", "gala.mod"} {
		if _, ok := files[name]; !ok {
			t.Errorf("the corpus has no %s", name)
		}
	}
	for _, path := range gala {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(data) != files[c.Rel(path)] {
			t.Errorf("%s: extracted text differs", c.Rel(path))
		}
		if strings.Contains(string(data), "\r") {
			t.Errorf("%s: has a CR", c.Rel(path))
		}
	}
}

func TestNormalizer(t *testing.T) {
	n := NewNormalizer(map[string]string{"/r/x": "$ROOT", "/r/x/tmp": "$TMP"})
	for in, want := range map[string]string{
		"/r/x/a/b.gala:3:1: bad":     "$ROOT/a/b.gala:3:1: bad",
		"/r/x/tmp/m.gala":            "$TMP/m.gala",
		`at /r/x\a\b.gala, then`:     "at $ROOT/a/b.gala, then",
		`{"file":"/r/x\\a\\b.gala"}`: `{"file":"$ROOT/a/b.gala"}`,
		`a\tb outside any path`:      `a\tb outside any path`,
	} {
		if got := n.Replace(in); got != want {
			t.Errorf("Replace(%q) = %q, want %q", in, got, want)
		}
	}
}
