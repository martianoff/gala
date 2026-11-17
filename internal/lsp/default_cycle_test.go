package lsp_test

import (
	"testing"
	"time"

	"github.com/owenrumney/go-lsp/lsp"
)

// TestDiagnostics_DefaultCycle verifies that a default which depends on
// itself shows GALA-E0135 in-editor. Expanding it used to overflow the stack,
// which no recover catches: it took the language server down.
func TestDiagnostics_DefaultCycle(t *testing.T) {
	h := newHarness(t)
	uri := openFileOnDisk(t, h, `package main

func f(a int, b int = f(1)) int = a + b

val y = f(0)

func main() = Println(y)
`)
	// Analysis runs in the background: wait for it, not a fixed time.
	var diags []lsp.Diagnostic
	for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		if diags = h.Diagnostics(uri); hasErrorContaining(diags, "GALA-E0135") {
			break
		}
	}
	if !hasErrorContaining(diags, "GALA-E0135") || !hasErrorContaining(diags, "f.b → f.b") {
		t.Fatalf("expected GALA-E0135 (f.b → f.b); diagnostics: %s", diagSummary(diags))
	}
}
