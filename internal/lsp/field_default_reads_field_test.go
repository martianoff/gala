package lsp_test

import (
	"testing"
	"time"

	"github.com/owenrumney/go-lsp/lsp"
)

// TestDiagnostics_FieldDefaultReadsField verifies that GALA-E0098 shows
// in-editor, for a field read and for a package val spelled like a field,
// and that the LSP analyzer still leaves undefined names (GALA-E0023) alone.
func TestDiagnostics_FieldDefaultReadsField(t *testing.T) {
	for _, tc := range []struct {
		name, src, want string
	}{
		{"field read", `package main

struct Rect(W int, H int = W)

func main() = Println(Rect(2).H)
`, "reads field W of Rect"},
		{"package val spelled like a field", `package main

val W = 7

struct Rect(W int, H int = W)

func main() = Println(Rect(2).H)
`, "reads package-level val W"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			uri := openFileOnDisk(t, h, tc.src)
			// Analysis runs in the background: wait for it, not a fixed time.
			var diags []lsp.Diagnostic
			for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
				if diags = h.Diagnostics(uri); hasErrorContaining(diags, "GALA-E0098") {
					break
				}
			}
			if !hasErrorContaining(diags, "GALA-E0098") || !hasErrorContaining(diags, tc.want) {
				t.Fatalf("expected GALA-E0098 (%s); diagnostics: %s", tc.want, diagSummary(diags))
			}
		})
	}
	// The LSP analyzer reports GALA-E0098 alone: an undefined name, in a
	// field default or anywhere else, is no error there (it may be mid-typing).
	_, undefDiags := getDiags(t, `package main

struct S(A int = notYetTyped)

func main() = Println(alsoNotYet)
`)
	if hasErrorContaining(undefDiags, "GALA-E0023") || hasErrorContaining(undefDiags, "GALA-E0098") {
		t.Fatalf("expected no GALA-E0023 or GALA-E0098; diagnostics: %s", diagSummary(undefDiags))
	}
	// Types, functions and imports spelled like fields are no field reads.
	_, diags := getDiags(t, `package main

import "strings"

struct Retry(N int)

func span() int = 5

struct Policy(Retry Retry = Retry(3), span int = span(), strings string = strings.ToUpper("ok"))

func main() = Println(Policy().span)
`)
	if errs := errorDiags(diags); len(errs) > 0 {
		t.Fatalf("expected no errors; diagnostics: %s", diagSummary(diags))
	}
}
