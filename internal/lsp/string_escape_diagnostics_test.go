package lsp_test

import (
	"strings"
	"testing"
	"time"

	"github.com/owenrumney/go-lsp/lsp"
	"github.com/owenrumney/go-lsp/servertest"
)

// Invalid string escapes in the editor
// ------------------------------------
// The GALA-E0038 escape check runs in the TRANSFORMER, which the LSP invokes
// through TransformForLSP. That placement is what makes a hard error safe here,
// and it is worth stating why, because the same check placed one stage earlier
// would break the editor:
//
//   - The analyzer runs first, and an analyzer error makes the diagnostics path
//     return before `h.richASTs[uri]` is ever populated. Completion, hover and
//     go-to-definition all read that cache, so they would go dead while the
//     literal is malformed. That is why a check living in the analyzer has to be
//     disabled for the LSP.
//   - The transformer runs after the RichAST has already been cached, and
//     TransformForLSP deliberately returns partial results alongside its error
//     ("Always return collected var types, even on error").
//
// So a user typing a Windows path (`"C:\Users\…"`, where `\U` begins an
// eight-hex-digit escape) or a regular expression mid-edit now gets a real
// diagnostic where they previously got silence, and keeps a working editor.
// These tests pin both halves.

// escapeSource is a complete, PARSEABLE program whose only defect is the `\U`
// in a Windows path on line 13 (1-based). It must stay parseable: a syntax
// error short-circuits analyzeFile long before the transformer runs, so the
// escape check would never be reached and these tests would prove nothing.
//
//	 1: package main
//	 2:
//	 3: type Person struct {
//	 4:     val name string
//	 5:     val age int
//	 6: }
//	 7:
//	 8: func (p Person) FullInfo() string {
//	 9:     return "info"
//	10: }
//	11:
//	12: func main() {
//	13:     val home = "C:\Users\me"
//	14:     val p = Person(name = "Alice", age = 30)
//	15:     Println(home, p.FullInfo())
//	16: }
const escapeSource = "package main\n" +
	"\n" +
	"type Person struct {\n" +
	"    val name string\n" +
	"    val age int\n" +
	"}\n" +
	"\n" +
	"func (p Person) FullInfo() string {\n" +
	"    return \"info\"\n" +
	"}\n" +
	"\n" +
	"func main() {\n" +
	"    val home = \"C:\\Users\\me\"\n" +
	"    val p = Person(name = \"Alice\", age = 30)\n" +
	"    Println(home, p.FullInfo())\n" +
	"}\n"

// escapeFixedSource is escapeSource with the Windows path written as a backtick
// raw string — the fix the diagnostic recommends.
var escapeFixedSource = strings.Replace(
	escapeSource, `"C:\Users\me"`, "`C:\\Users\\me`", 1)

const (
	// escapeDiagLine is the 0-based LSP line of the bad literal (GALA line 13).
	escapeDiagLine = 12
	// personRefLine/personRefCol address the `Person` constructor call on GALA
	// line 14, used as the go-to-definition probe.
	personRefLine = 13
	personRefCol  = 12
	// personDeclLine is the 0-based line of `type Person struct {`.
	personDeclLine = 2
)

// awaitDiagnostics polls until the server publishes at least one diagnostic or
// the budget expires. Analysis is debounced and runs off the request thread, and
// how long it takes scales with the file, so a single fixed sleep is racy — a
// short one reads an empty set before analysis has landed.
func awaitDiagnostics(t *testing.T, h *servertest.Harness, uri lsp.DocumentURI) []lsp.Diagnostic {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	var diags []lsp.Diagnostic
	for time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
		diags = h.Diagnostics(uri)
		if len(diags) > 0 {
			return diags
		}
	}
	return diags
}

// hasEscapeDiagnostic reports whether any diagnostic is a GALA-E0038.
func hasEscapeDiagnostic(diags []lsp.Diagnostic) bool {
	for _, d := range diags {
		if strings.Contains(d.Message, "GALA-E0038") {
			return true
		}
	}
	return false
}

// awaitNoEscapeDiagnostic polls until the escape diagnostic has cleared, or the
// budget expires. Waiting for an ABSENCE must not be a fixed sleep: the sleep
// would have to outlast the slowest CI runner, and any run where it is too short
// still sees the stale pre-edit diagnostic and fails spuriously. Polling turns
// that race into a latency bound. It cannot pass vacuously either — until the
// debounce fires the previously published set is still the one carrying
// GALA-E0038, so the loop only exits once re-analysis has actually landed.
func awaitNoEscapeDiagnostic(t *testing.T, h *servertest.Harness, uri lsp.DocumentURI) []lsp.Diagnostic {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	var diags []lsp.Diagnostic
	for time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
		diags = h.Diagnostics(uri)
		if !hasEscapeDiagnostic(diags) {
			return diags
		}
	}
	return diags
}

// TestDiagnostics_InvalidStringEscapeIsReported pins that an unrecognised escape
// surfaces as an editor diagnostic, on the right line, rather than being
// silently transpiled into Go that does not compile.
func TestDiagnostics_InvalidStringEscapeIsReported(t *testing.T) {
	h := newHarness(t)
	uri := openFileOnDisk(t, h, escapeSource)

	diags := awaitDiagnostics(t, h, uri)
	t.Logf("diagnostics: %d", len(diags))

	var found *lsp.Diagnostic
	for i, d := range diags {
		t.Logf("  line=%d msg=%s", d.Range.Start.Line, d.Message)
		if strings.Contains(d.Message, "GALA-E0038") {
			found = &diags[i]
		}
	}

	if found == nil {
		t.Fatalf("expected a GALA-E0038 diagnostic for the invalid \\U escape, got %d diagnostics", len(diags))
	}
	if !strings.Contains(found.Message, `invalid escape sequence "\U`) {
		t.Errorf("diagnostic should name the offending escape, got: %s", found.Message)
	}
	if found.Range.Start.Line != escapeDiagLine {
		t.Errorf("expected the diagnostic on 0-based line %d, got %d",
			escapeDiagLine, found.Range.Start.Line)
	}
	if found.Severity == nil || *found.Severity != lsp.SeverityError {
		t.Errorf("an invalid escape is an error, not a warning/hint")
	}
}

// TestDefinition_SurvivesInvalidStringEscape is the important half: the hard
// error must NOT cost the user their editor. Go-to-definition reads the cached
// RichAST, so if this regresses the check has drifted into the analyzer (or the
// RichAST caching moved after the transform) and the editor goes dead whenever a
// Windows path or a regex is mid-edit.
func TestDefinition_SurvivesInvalidStringEscape(t *testing.T) {
	h := newHarness(t)
	uri := openFileOnDisk(t, h, escapeSource)

	// Precondition: the file really is in the broken state.
	if !hasEscapeDiagnostic(awaitDiagnostics(t, h, uri)) {
		t.Fatal("precondition failed: expected the file to report GALA-E0038")
	}

	locs, err := h.Definition(uri, personRefLine, personRefCol)
	if err != nil {
		t.Fatalf("go-to-definition errored while an escape was invalid: %v", err)
	}
	if len(locs) == 0 {
		t.Fatal("go-to-definition returned nothing while an escape was invalid — " +
			"the RichAST was dropped when the escape check failed the transform")
	}
	if locs[0].Range.Start.Line != personDeclLine {
		t.Errorf("expected the Person declaration on 0-based line %d, got %d",
			personDeclLine, locs[0].Range.Start.Line)
	}
}

// TestDefinition_UnaffectedWhenEscapeIsFixed pins the other direction: doubling
// the backslash clears the diagnostic and leaves the editor exactly as it was,
// so the check leaves no lasting state behind.
func TestDefinition_UnaffectedWhenEscapeIsFixed(t *testing.T) {
	if escapeFixedSource == escapeSource {
		t.Fatal("test setup: the source substitution did not apply")
	}

	h := newHarness(t)
	uri := openFileOnDisk(t, h, escapeSource)
	awaitDiagnostics(t, h, uri)

	if err := h.DidChange(uri, 1, escapeFixedSource); err != nil {
		t.Fatal(err)
	}

	for _, d := range awaitNoEscapeDiagnostic(t, h, uri) {
		if strings.Contains(d.Message, "GALA-E0038") {
			t.Errorf("GALA-E0038 persisted after the escape was fixed: %s", d.Message)
		}
	}

	locs, err := h.Definition(uri, personRefLine, personRefCol)
	if err != nil {
		t.Fatal(err)
	}
	if len(locs) == 0 {
		t.Fatal("go-to-definition returned nothing after the escape was fixed")
	}
	if locs[0].Range.Start.Line != personDeclLine {
		t.Errorf("expected the Person declaration on 0-based line %d, got %d",
			personDeclLine, locs[0].Range.Start.Line)
	}
}
