package lsp_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/owenrumney/go-lsp/lsp"
)

// errorDiags returns only the error-severity diagnostics.
func errorDiags(diags []lsp.Diagnostic) []lsp.Diagnostic {
	out := make([]lsp.Diagnostic, 0)
	for _, d := range diags {
		if d.Severity != nil && *d.Severity == lsp.SeverityError {
			out = append(out, d)
		}
	}
	return out
}

// TestDiagnostics_SizeSugarNoFalseError verifies that `.Size()` / `.ByteSize()`
// on Go primitive receivers (string, slice) raise NO diagnostic — the transpiler
// lowers them to len()/utf8.RuneCountInString(), so the LSP must not flag them as
// unresolved methods.
func TestDiagnostics_SizeSugarNoFalseError(t *testing.T) {
	_, diags := getDiags(t, `package main

func main() {
    val s = "hello"
    val n = s.Size()
    val b = s.ByteSize()
    val xs = SliceOf(1, 2, 3)
    val m = xs.Size()
    Println(n)
    Println(b)
    Println(m)
}
`)
	errs := errorDiags(diags)
	if len(errs) != 0 {
		for _, d := range errs {
			t.Errorf("unexpected error on .Size()/.ByteSize(): line=%d msg=%s", d.Range.Start.Line, d.Message)
		}
	}
}

// TestDiagnostics_ForbiddenBuiltin verifies bare `len(...)` raises GALA-E0035
// with its fix suggestion, in-editor.
func TestDiagnostics_ForbiddenBuiltin(t *testing.T) {
	_, diags := getDiags(t, `package main

func main() {
    val s = "hello"
    val n = len(s)
    Println(n)
}
`)
	if !hasErrorContaining(diags, "GALA-E0035") {
		t.Errorf("expected GALA-E0035 for bare len(...); diagnostics: %s", diagSummary(diags))
	}
	// The actionable suggestion (use .Size()/.ByteSize()) must reach the editor.
	if !hasErrorContaining(diags, ".Size()") {
		t.Errorf("expected the E0035 suggestion to mention .Size(); diagnostics: %s", diagSummary(diags))
	}
}

// TestDiagnostics_ForbiddenStatementKeyword verifies bare `defer ...` raises
// GALA-E0036 in-editor.
func TestDiagnostics_ForbiddenStatementKeyword(t *testing.T) {
	_, diags := getDiags(t, `package main

func main() {
    defer Println("x")
    Println("y")
}
`)
	if !hasErrorContaining(diags, "GALA-E0036") {
		t.Errorf("expected GALA-E0036 for bare defer; diagnostics: %s", diagSummary(diags))
	}
}

// TestDiagnostics_UseBindingNoError verifies a `use x = ...` scoped-resource
// binding and the bound name raise NO diagnostic.
func TestDiagnostics_UseBindingNoError(t *testing.T) {
	_, diags := getDiags(t, `package main

type Handle struct {
    name string
}

func (h Handle) Close() error {
    return nil
}

func open(name string) Handle {
    return Handle(name = name)
}

func work() {
    use a = open("a")
    Println(a.name)
}

func main() {
    work()
}
`)
	errs := errorDiags(diags)
	if len(errs) != 0 {
		for _, d := range errs {
			t.Errorf("unexpected error on `use` binding: line=%d msg=%s", d.Range.Start.Line, d.Message)
		}
	}
}

// TestDiagnostics_GoBuiltinsPanicResolves verifies the LSP resolves the
// go_builtins package and its Panic function — the sanctioned replacement for
// the forbidden bare `panic(...)` — without a false "unresolved" diagnostic.
func TestDiagnostics_GoBuiltinsPanicResolves(t *testing.T) {
	_, diags := getDiags(t, `package main

import . "martianoff/gala/go_builtins"

func main() {
    Panic("boom")
}
`)
	errs := errorDiags(diags)
	if len(errs) != 0 {
		for _, d := range errs {
			t.Errorf("unexpected error importing/using go_builtins.Panic: line=%d msg=%s", d.Range.Start.Line, d.Message)
		}
	}
}

// TestDiagnostics_ResourcePackageResolves verifies the LSP resolves the
// resource package (Closeable / Using / Bracket / WithLock) without false
// errors. Mirrors examples/resource_combinators.gala.
func TestDiagnostics_ResourcePackageResolves(t *testing.T) {
	_, diags := getDiags(t, `package main

import . "martianoff/gala/resource"
import . "martianoff/gala/go_interop"

type Handle struct {
    name string
}

func (h Handle) Close() error {
    return nil
}

func main() {
    val n = Using(Handle(name = "log.txt"), (h) => h.name.Size())
    val doubled = Bracket(21, (r) => Println(r), (r) => r * 2)
    val mu = NewMutex()
    val guarded = WithLock(mu, () => 7 + 1)
    Println(n)
    Println(doubled)
    Println(guarded)
}
`)
	errs := errorDiags(diags)
	if len(errs) != 0 {
		for _, d := range errs {
			t.Errorf("unexpected error importing/using resource combinators: line=%d msg=%s", d.Range.Start.Line, d.Message)
		}
	}
}

// TestCompletion_SizeSugarOnString verifies dot completion on a string receiver
// offers Size() and ByteSize().
func TestCompletion_SizeSugarOnString(t *testing.T) {
	h := newHarness(t)
	// GALA requires a blank line after the package clause. Cursor is right after
	// "s." in `    val n = s.` on line 4 (0-indexed), col 14.
	uri := openFileOnDisk(t, h, "package main\n\nfunc main() {\n    val s = \"hello\"\n    val n = s.\n    Println(n)\n}\n")
	list, err := h.Completion(uri, 4, 14)
	if err != nil {
		t.Fatal(err)
	}
	if list == nil {
		t.Fatal("completion returned nil for string receiver")
	}
	labels := collectLabels(list)
	if labels["val"] {
		t.Fatalf("got keyword fallback — analyzer did not produce type-aware completion; items: %v", labelSlice(list))
	}
	if !labels["Size()"] {
		t.Errorf("string dot completion missing Size(); got: %v", labelSlice(list))
	}
	if !labels["ByteSize()"] {
		t.Errorf("string dot completion missing ByteSize(); got: %v", labelSlice(list))
	}
}

// TestCompletion_SizeSugarOnSlice verifies dot completion on a Go slice receiver
// offers Size() but NOT ByteSize().
func TestCompletion_SizeSugarOnSlice(t *testing.T) {
	h := newHarness(t)
	// Blank line after package; cursor right after "xs." in `    val n = xs.`
	// on line 4 (0-indexed), col 15.
	uri := openFileOnDisk(t, h, "package main\n\nfunc main() {\n    val xs = SliceOf(1, 2, 3)\n    val n = xs.\n    Println(n)\n}\n")
	list, err := h.Completion(uri, 4, 15)
	if err != nil {
		t.Fatal(err)
	}
	if list == nil {
		t.Fatal("completion returned nil for slice receiver")
	}
	labels := collectLabels(list)
	if labels["val"] {
		t.Fatalf("got keyword fallback — analyzer did not produce type-aware completion; items: %v", labelSlice(list))
	}
	if !labels["Size()"] {
		t.Errorf("slice dot completion missing Size(); got: %v", labelSlice(list))
	}
	if labels["ByteSize()"] {
		t.Errorf("slice dot completion should NOT offer ByteSize(); got: %v", labelSlice(list))
	}
}

// hasErrorContaining reports whether any error-severity diagnostic message
// contains substr.
func hasErrorContaining(diags []lsp.Diagnostic, substr string) bool {
	for _, d := range diags {
		if d.Severity == nil || *d.Severity != lsp.SeverityError {
			continue
		}
		if strings.Contains(d.Message, substr) {
			return true
		}
	}
	return false
}

func diagSummary(diags []lsp.Diagnostic) string {
	if len(diags) == 0 {
		return "(none)"
	}
	var b strings.Builder
	for _, d := range diags {
		fmt.Fprintf(&b, "\n  line=%d msg=%s", d.Range.Start.Line, d.Message)
	}
	return b.String()
}
