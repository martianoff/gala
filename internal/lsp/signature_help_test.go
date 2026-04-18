package lsp_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/owenrumney/go-lsp/lsp"
	"github.com/owenrumney/go-lsp/servertest"
)

// requestSignatureHelp invokes textDocument/signatureHelp and decodes the
// result. Returns nil if the server returned no signature help (null
// response).
func requestSignatureHelp(t *testing.T, h *servertest.Harness, uri lsp.DocumentURI, line, char int) *lsp.SignatureHelp {
	t.Helper()
	raw, err := h.Call("textDocument/signatureHelp", map[string]interface{}{
		"textDocument": map[string]string{"uri": string(uri)},
		"position":     map[string]int{"line": line, "character": char},
	})
	if err != nil {
		t.Fatal(err)
	}
	if raw == nil || string(raw) == "null" {
		return nil
	}
	var sh lsp.SignatureHelp
	if err := json.Unmarshal(raw, &sh); err != nil {
		t.Fatalf("failed to unmarshal signature help: %v (raw: %s)", err, string(raw))
	}
	return &sh
}

// labelOf pulls the string label from a ParameterInformation. The LSP
// spec allows either a string or [int, int] range — we emit strings.
func labelOf(p lsp.ParameterInformation) string {
	switch v := p.Label.(type) {
	case string:
		return v
	default:
		return ""
	}
}

func activeParam(sh *lsp.SignatureHelp) int {
	if sh.ActiveParameter == nil {
		return -1
	}
	return *sh.ActiveParameter
}

// ---- Free function signatures ----

func TestSignatureHelp_FreeFunction_FirstArgument(t *testing.T) {
	h := newHarness(t)
	src := "package main\n" +
		"\n" +
		"func greet(name string, times int) string = name\n" +
		"\n" +
		"func main() {\n" +
		"    greet(\n" +
		"}\n"
	uri := openFileOnDisk(t, h, src)
	time.Sleep(200 * time.Millisecond)

	// Cursor right after `greet(`
	sh := requestSignatureHelp(t, h, uri, 5, 10)
	if sh == nil {
		t.Fatal("expected signature help, got nil")
	}
	if len(sh.Signatures) != 1 {
		t.Fatalf("expected 1 signature, got %d", len(sh.Signatures))
	}
	sig := sh.Signatures[0]
	if !strings.Contains(sig.Label, "greet") || !strings.Contains(sig.Label, "name string") {
		t.Errorf("unexpected label: %q", sig.Label)
	}
	if got, want := len(sig.Parameters), 2; got != want {
		t.Errorf("expected %d parameters, got %d", want, got)
	}
	if got := labelOf(sig.Parameters[0]); got != "name string" {
		t.Errorf("param[0] label: got %q want %q", got, "name string")
	}
	if got := labelOf(sig.Parameters[1]); got != "times int" {
		t.Errorf("param[1] label: got %q want %q", got, "times int")
	}
	if got, want := activeParam(sh), 0; got != want {
		t.Errorf("active parameter: got %d want %d", got, want)
	}
}

func TestSignatureHelp_FreeFunction_SecondArgument(t *testing.T) {
	h := newHarness(t)
	src := "package main\n" +
		"\n" +
		"func greet(name string, times int) string = name\n" +
		"\n" +
		"func main() {\n" +
		"    greet(\"world\", \n" +
		"}\n"
	uri := openFileOnDisk(t, h, src)
	time.Sleep(200 * time.Millisecond)

	sh := requestSignatureHelp(t, h, uri, 5, 19)
	if sh == nil {
		t.Fatal("expected signature help, got nil")
	}
	if got, want := activeParam(sh), 1; got != want {
		t.Errorf("active parameter after one comma: got %d want %d", got, want)
	}
}

func TestSignatureHelp_FreeFunction_ReturnTypeInLabel(t *testing.T) {
	h := newHarness(t)
	src := "package main\n" +
		"\n" +
		"func add(a int, b int) int = a + b\n" +
		"\n" +
		"func main() {\n" +
		"    add(\n" +
		"}\n"
	uri := openFileOnDisk(t, h, src)
	time.Sleep(200 * time.Millisecond)

	sh := requestSignatureHelp(t, h, uri, 5, 8)
	if sh == nil {
		t.Fatal("expected signature help, got nil")
	}
	if !strings.HasSuffix(sh.Signatures[0].Label, " int") {
		t.Errorf("expected signature label to end with return type ' int', got %q", sh.Signatures[0].Label)
	}
}

// ---- Method signatures ----

func TestSignatureHelp_MethodOnLocalVariable(t *testing.T) {
	h := newHarness(t)
	src := "package main\n" +
		"\n" +
		"type Box struct {\n" +
		"    val n int\n" +
		"}\n" +
		"\n" +
		"func (b Box) Add(x int, y int) Box = Box(n = b.n + x + y)\n" +
		"\n" +
		"func main() {\n" +
		"    val b = Box(n = 1)\n" +
		"    b.Add(\n" +
		"}\n"
	uri := openFileOnDisk(t, h, src)
	time.Sleep(200 * time.Millisecond)

	sh := requestSignatureHelp(t, h, uri, 10, 10)
	if sh == nil {
		t.Fatal("expected signature help on method, got nil")
	}
	sig := sh.Signatures[0]
	if !strings.HasPrefix(sig.Label, "Add(") {
		t.Errorf("expected label starting with Add(, got %q", sig.Label)
	}
	if got, want := len(sig.Parameters), 2; got != want {
		t.Fatalf("expected 2 params, got %d", got)
	}
	if got := labelOf(sig.Parameters[0]); got != "x int" {
		t.Errorf("param[0]: %q", got)
	}
	if got := labelOf(sig.Parameters[1]); got != "y int" {
		t.Errorf("param[1]: %q", got)
	}
}

func TestSignatureHelp_MethodInChain(t *testing.T) {
	h := newHarness(t)
	src := "package main\n" +
		"\n" +
		"type Server struct {\n" +
		"    val name string\n" +
		"}\n" +
		"\n" +
		"func (s Server) WithName(n string) Server = Server(name = n)\n" +
		"func (s Server) WithFilter(f string) Server = s\n" +
		"\n" +
		"func NewServer() Server = Server(name = \"default\")\n" +
		"\n" +
		"func main() {\n" +
		"    val x = NewServer().WithName(\"demo\").WithFilter(\n" +
		"}\n"
	uri := openFileOnDisk(t, h, src)
	time.Sleep(200 * time.Millisecond)

	// Cursor just after `WithFilter(`
	line := "    val x = NewServer().WithName(\"demo\").WithFilter("
	sh := requestSignatureHelp(t, h, uri, 12, len(line))
	if sh == nil {
		t.Fatal("expected signature help on chained method, got nil")
	}
	if !strings.HasPrefix(sh.Signatures[0].Label, "WithFilter(") {
		t.Errorf("expected WithFilter label, got %q", sh.Signatures[0].Label)
	}
	if got := labelOf(sh.Signatures[0].Parameters[0]); got != "f string" {
		t.Errorf("param[0]: %q", got)
	}
}

// ---- Nested call argIndex handling ----

func TestSignatureHelp_NestedCall_OuterArgIndex(t *testing.T) {
	h := newHarness(t)
	src := "package main\n" +
		"\n" +
		"func outer(a int, b int) int = a + b\n" +
		"func inner(x int, y int) int = x + y\n" +
		"\n" +
		"func main() {\n" +
		"    outer(inner(1, 2), \n" +
		"}\n"
	uri := openFileOnDisk(t, h, src)
	time.Sleep(200 * time.Millisecond)

	// Cursor on the outer call's 2nd arg, after the inner call + comma
	sh := requestSignatureHelp(t, h, uri, 6, 23)
	if sh == nil {
		t.Fatal("expected signature help, got nil")
	}
	if !strings.HasPrefix(sh.Signatures[0].Label, "func outer") {
		t.Errorf("expected outer func sig, got %q", sh.Signatures[0].Label)
	}
	if got, want := activeParam(sh), 1; got != want {
		t.Errorf("active parameter (skipping comma inside inner(1,2)): got %d want %d", got, want)
	}
}

func TestSignatureHelp_NestedCall_InnerCallActive(t *testing.T) {
	h := newHarness(t)
	src := "package main\n" +
		"\n" +
		"func outer(a int, b int) int = a + b\n" +
		"func inner(x int, y int) int = x + y\n" +
		"\n" +
		"func main() {\n" +
		"    outer(inner(1, \n" +
		"}\n"
	uri := openFileOnDisk(t, h, src)
	time.Sleep(200 * time.Millisecond)

	// Cursor inside the inner call's 2nd arg
	sh := requestSignatureHelp(t, h, uri, 6, 19)
	if sh == nil {
		t.Fatal("expected signature help, got nil")
	}
	if !strings.HasPrefix(sh.Signatures[0].Label, "func inner") {
		t.Errorf("expected inner func sig, got %q", sh.Signatures[0].Label)
	}
	if got, want := activeParam(sh), 1; got != want {
		t.Errorf("active parameter: got %d want %d", got, want)
	}
}

// ---- String literals must not confuse paren counting ----

func TestSignatureHelp_StringWithParensAndCommas(t *testing.T) {
	h := newHarness(t)
	src := "package main\n" +
		"\n" +
		"func fmt3(a string, b int) string = a\n" +
		"\n" +
		"func main() {\n" +
		"    fmt3(\"a(b,c)d\", \n" +
		"}\n"
	uri := openFileOnDisk(t, h, src)
	time.Sleep(200 * time.Millisecond)

	sh := requestSignatureHelp(t, h, uri, 5, 21)
	if sh == nil {
		t.Fatal("expected signature help, got nil")
	}
	if got, want := activeParam(sh), 1; got != want {
		t.Errorf("parens/commas inside string literal must not affect count: got %d want %d", got, want)
	}
}

func TestSignatureHelp_EscapedQuoteInString(t *testing.T) {
	h := newHarness(t)
	src := "package main\n" +
		"\n" +
		"func fmt3(a string, b int) string = a\n" +
		"\n" +
		"func main() {\n" +
		"    fmt3(\"quote\\\"inside,still\", \n" +
		"}\n"
	uri := openFileOnDisk(t, h, src)
	time.Sleep(200 * time.Millisecond)

	// Cursor past the escaped-quote string + comma → arg index 1
	line := "    fmt3(\"quote\\\"inside,still\", "
	sh := requestSignatureHelp(t, h, uri, 5, len(line))
	if sh == nil {
		t.Fatal("expected signature help, got nil")
	}
	if got, want := activeParam(sh), 1; got != want {
		t.Errorf("escaped quote must not end string: got arg %d want %d", got, want)
	}
}

// ---- No call / outside call → nil ----

func TestSignatureHelp_OutsideCall_ReturnsNil(t *testing.T) {
	h := newHarness(t)
	src := "package main\n" +
		"\n" +
		"func main() {\n" +
		"    val x = 1\n" +
		"}\n"
	uri := openFileOnDisk(t, h, src)
	time.Sleep(200 * time.Millisecond)

	sh := requestSignatureHelp(t, h, uri, 3, 12)
	if sh != nil {
		t.Fatalf("expected nil outside a call, got %+v", sh)
	}
}

func TestSignatureHelp_ParenGroupingNotCall(t *testing.T) {
	h := newHarness(t)
	// `(1 + 2)` is a grouping, not a call — no signature help expected
	src := "package main\n" +
		"\n" +
		"func main() {\n" +
		"    val x = (1 + \n" +
		"}\n"
	uri := openFileOnDisk(t, h, src)
	time.Sleep(200 * time.Millisecond)

	sh := requestSignatureHelp(t, h, uri, 3, 17)
	if sh != nil {
		t.Fatalf("expected nil for paren grouping, got %+v", sh)
	}
}

// ---- Unknown function → nil ----

func TestSignatureHelp_UnknownFunction_ReturnsNil(t *testing.T) {
	h := newHarness(t)
	src := "package main\n" +
		"\n" +
		"func main() {\n" +
		"    definitelyNotDefined(\n" +
		"}\n"
	uri := openFileOnDisk(t, h, src)
	time.Sleep(200 * time.Millisecond)

	sh := requestSignatureHelp(t, h, uri, 3, 25)
	if sh != nil {
		t.Fatalf("expected nil for unknown function, got %+v", sh)
	}
}

// ---- Type constructor → signature from fields ----

func TestSignatureHelp_TypeConstructor(t *testing.T) {
	h := newHarness(t)
	src := "package main\n" +
		"\n" +
		"type Person struct {\n" +
		"    val name string\n" +
		"    val age int\n" +
		"}\n" +
		"\n" +
		"func main() {\n" +
		"    val p = Person(\n" +
		"}\n"
	uri := openFileOnDisk(t, h, src)
	time.Sleep(200 * time.Millisecond)

	sh := requestSignatureHelp(t, h, uri, 8, 19)
	if sh == nil {
		t.Fatal("expected signature help for type constructor, got nil")
	}
	sig := sh.Signatures[0]
	if !strings.HasPrefix(sig.Label, "Person(") {
		t.Errorf("label: got %q", sig.Label)
	}
	if got, want := len(sig.Parameters), 2; got != want {
		t.Fatalf("expected 2 params (fields), got %d", got)
	}
	if got := labelOf(sig.Parameters[0]); got != "name string" {
		t.Errorf("param[0]: %q", got)
	}
}

// ---- Multi-line call ----

func TestSignatureHelp_MultiLineCall(t *testing.T) {
	h := newHarness(t)
	src := "package main\n" +
		"\n" +
		"func make4(a int, b int, c int, d int) int = a\n" +
		"\n" +
		"func main() {\n" +
		"    val x = make4(\n" +
		"        1,\n" +
		"        2,\n" +
		"        \n" +
		"}\n"
	uri := openFileOnDisk(t, h, src)
	time.Sleep(200 * time.Millisecond)

	// Cursor on the blank line inside the call — third arg
	sh := requestSignatureHelp(t, h, uri, 8, 8)
	if sh == nil {
		t.Fatal("expected signature help inside multi-line call, got nil")
	}
	if got, want := activeParam(sh), 2; got != want {
		t.Errorf("active parameter across lines: got %d want %d", got, want)
	}
}

// ---- Active param clamped when beyond parameter count (variadic-style overflow) ----

func TestSignatureHelp_ActiveParamClamped(t *testing.T) {
	h := newHarness(t)
	src := "package main\n" +
		"\n" +
		"func two(a int, b int) int = a + b\n" +
		"\n" +
		"func main() {\n" +
		"    two(1, 2, 3, \n" +
		"}\n"
	uri := openFileOnDisk(t, h, src)
	time.Sleep(200 * time.Millisecond)

	// Cursor at "4th" arg — exceeds the 2-param signature; must clamp
	// to the last param rather than returning out-of-range index.
	sh := requestSignatureHelp(t, h, uri, 5, 17)
	if sh == nil {
		t.Fatal("expected signature help, got nil")
	}
	if got, want := activeParam(sh), 1; got != want {
		t.Errorf("active parameter clamped to last: got %d want %d", got, want)
	}
}

// ---- Zero-parameter function ----

func TestSignatureHelp_NoParams(t *testing.T) {
	h := newHarness(t)
	src := "package main\n" +
		"\n" +
		"func nullary() int = 42\n" +
		"\n" +
		"func main() {\n" +
		"    nullary(\n" +
		"}\n"
	uri := openFileOnDisk(t, h, src)
	time.Sleep(200 * time.Millisecond)

	sh := requestSignatureHelp(t, h, uri, 5, 12)
	if sh == nil {
		t.Fatal("expected signature help, got nil")
	}
	sig := sh.Signatures[0]
	if len(sig.Parameters) != 0 {
		t.Errorf("expected 0 params, got %d", len(sig.Parameters))
	}
	if !strings.Contains(sig.Label, "nullary()") {
		t.Errorf("label: %q", sig.Label)
	}
}

// ---- Generic function preserves type params in label ----

func TestSignatureHelp_GenericFunction_LabelShowsTypeParams(t *testing.T) {
	h := newHarness(t)
	src := "package main\n" +
		"\n" +
		"func identity[T](x T) T = x\n" +
		"\n" +
		"func main() {\n" +
		"    identity(\n" +
		"}\n"
	uri := openFileOnDisk(t, h, src)
	time.Sleep(200 * time.Millisecond)

	sh := requestSignatureHelp(t, h, uri, 5, 13)
	if sh == nil {
		t.Fatal("expected signature help for generic fn, got nil")
	}
	if !strings.Contains(sh.Signatures[0].Label, "[T]") {
		t.Errorf("expected [T] in generic signature label, got %q", sh.Signatures[0].Label)
	}
}

// ---- Chain completion + signature help integration: the user's
//      long-builder scenario still resolves the signature properly.

func TestSignatureHelp_LongBuilderChain(t *testing.T) {
	h := newHarness(t)
	src := "package main\n" +
		"\n" +
		"type Server struct {\n" +
		"    val name string\n" +
		"}\n" +
		"\n" +
		"func (s Server) WithName(n string) Server = Server(name = n)\n" +
		"func (s Server) WithPort(p int) Server = s\n" +
		"func (s Server) WithFilter(f string) Server = s\n" +
		"\n" +
		"func NewServer() Server = Server(name = \"d\")\n" +
		"\n" +
		"func main() {\n" +
		"    val x = NewServer().\n" +
		"        WithName(\"a\").\n" +
		"        WithPort(1).\n" +
		"        WithFilter(\"b\").\n" +
		"        WithFilter(\"c\").\n" +
		"        WithFilter(\"d\").\n" +
		"        WithFilter(\"e\").\n" +
		"        WithFilter(\"f\").\n" +
		"        WithFilter(\"g\").\n" +
		"        WithFilter(\"h\").\n" +
		"        WithFilter(\"i\").\n" +
		"        WithFilter(\n" +
		"}\n"
	uri := openFileOnDisk(t, h, src)
	time.Sleep(200 * time.Millisecond)

	// Cursor right after the last `WithFilter(` — long chain.
	lines := strings.Split(src, "\n")
	target := -1
	for i, l := range lines {
		if strings.HasSuffix(l, "WithFilter(") {
			target = i
		}
	}
	if target < 0 {
		t.Fatal("could not locate trailing WithFilter(")
	}
	sh := requestSignatureHelp(t, h, uri, target, len(lines[target]))
	if sh == nil {
		t.Fatal("expected signature help after deep chain, got nil")
	}
	if !strings.HasPrefix(sh.Signatures[0].Label, "WithFilter(") {
		t.Errorf("expected WithFilter label after deep chain, got %q", sh.Signatures[0].Label)
	}
}

// ---- String containing escaped dollar sign and braces (interpolated
//      string-like content) — parser sees a normal string, paren counter
//      must treat it as such.

func TestSignatureHelp_InterpolatedStringLiteral(t *testing.T) {
	h := newHarness(t)
	src := "package main\n" +
		"\n" +
		"func tag(label string, n int) string = label\n" +
		"\n" +
		"func main() {\n" +
		"    tag(s\"hello, ${name}(!)\", \n" +
		"}\n"
	uri := openFileOnDisk(t, h, src)
	time.Sleep(200 * time.Millisecond)

	// Cursor after the interpolated string + comma
	line := "    tag(s\"hello, ${name}(!)\", "
	sh := requestSignatureHelp(t, h, uri, 5, len(line))
	if sh == nil {
		t.Fatal("expected signature help, got nil")
	}
	if got, want := activeParam(sh), 1; got != want {
		t.Errorf("parens inside interpolation must not leak out: got %d want %d", got, want)
	}
}
