package lsp_test

import "testing"

// TestDiagnostics_GoKeywordAsName verifies that a name spelled like a Go
// keyword raises GALA-E0055 in-editor, at the declaration, rather than the
// internal GALA-E0017 the unparseable generated Go used to produce.
func TestDiagnostics_GoKeywordAsName(t *testing.T) {
	_, diags := getDiags(t, `package main

func main() {
    Println(go())
}

func go() int = 1
`)
	if !hasErrorContaining(diags, "GALA-E0055") || !hasErrorContaining(diags, `"go" is a Go keyword`) {
		t.Fatalf("expected GALA-E0055 for func go; diagnostics: %s", diagSummary(diags))
	}
	for _, d := range errorDiags(diags) {
		if d.Range.Start.Line != 6 { // 0-based: `func go() int = 1`
			t.Errorf("unexpected error at line %d: %s", d.Range.Start.Line, d.Message)
		}
	}
}
