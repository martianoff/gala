package lsp_test

import "testing"

// TestDiagnostics_ValueCalledAsFunction verifies that calling a val whose type
// is not a function raises GALA-E0054 in-editor, on the call's line, and that
// calling a val that holds a function raises nothing.
func TestDiagnostics_ValueCalledAsFunction(t *testing.T) {
	const src = `package main

struct Color(N int)

val Red = Color(1)
val Inc = (x int) => x + 1

func main() {
    Println(Inc(1))
    Println(Red())
}
`
	h := newHarness(t)
	uri := openFileOnDisk(t, h, src)
	settle(t, h, uri, src, "val Inc", "Inc")
	diags := h.Diagnostics(uri)
	if !hasErrorContaining(diags, "GALA-E0054") || !hasErrorContaining(diags, "Red is a val of type Color") {
		t.Fatalf("expected GALA-E0054 for Red(); diagnostics: %s", diagSummary(diags))
	}
	for _, d := range errorDiags(diags) {
		if d.Range.Start.Line != 9 { // 0-based: `Println(Red())`
			t.Errorf("unexpected error at line %d: %s", d.Range.Start.Line, d.Message)
		}
	}
}
