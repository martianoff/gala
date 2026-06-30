package lspintegration

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/bazelbuild/rules_go/go/tools/bazel"
)

// findGalaBinary locates the `gala` binary to drive. Under Bazel it is provided
// as a runfiles data dependency; otherwise it falls back to `gala` on PATH so
// the test is still runnable via `go test`.
func findGalaBinary(t *testing.T) string {
	t.Helper()
	candidates := []string{
		"cmd/gala/gala_/gala",
		"cmd/gala/gala_/gala_/gala",
		"_main/cmd/gala/gala_/gala",
	}
	for _, c := range candidates {
		if p, err := bazel.Runfile(c); err == nil {
			if _, statErr := os.Stat(p); statErr == nil {
				return p
			}
		}
	}
	if p, err := exec.LookPath("gala"); err == nil {
		return p
	}
	t.Skip("gala binary not found in runfiles or PATH")
	return ""
}

// writeFixture writes a set of files into a fresh temp dir and returns it.
func writeFixture(t *testing.T, files map[string]string) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "gala-lsp-integ-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	for name, content := range files {
		full := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// errorDiagnostics returns the error-severity diagnostics for a file.
func (c *lspClient) errorDiagnostics(path string) []diagnostic {
	var errs []diagnostic
	for _, d := range c.diagnosticsFor(path) {
		if d.Severity == 1 { // 1 == Error
			errs = append(errs, d)
		}
	}
	return errs
}

// TestCrossFileSealedTypeNoFalsePositives reproduces the gala-mcp outage: a
// sealed type with a nullary variant (`case JNull`) defined in one file, used
// from sibling files via bare-constructor match and a method chain. With the
// real `gala lsp` and real-editor URIs, sibling discovery must resolve the
// whole package — exactly as `gala build` does — so NONE of these files report
// diagnostics. The bug (uriToPath dropping the POSIX leading slash) broke
// sibling discovery and produced bogus "unused variable" /
// "cannot infer type of matched expression" errors.
func TestCrossFileSealedTypeNoFalsePositives(t *testing.T) {
	galaBin := findGalaBinary(t)

	dir := writeFixture(t, map[string]string{
		"gala.mod": "module example.com/integ\n\ngala 1.0\n",
		"jsonvalue.gala": `package mcp

sealed type JsonValue {
    case JNull
    case JBool(Bool bool)
    case JStr(Str string)
}

func (v JsonValue) AsString() Option[string] = v match {
    case JStr(s) => Some(s)
    case _ => None[string]()
}

func (v JsonValue) GetString() Option[string] = v.AsString()
`,
		"render.gala": `package mcp

// Bare nullary-variant pattern (case JNull) must not be flagged as a binding.
func RenderJson(v JsonValue) string = v match {
    case JNull => "null"
    case JBool(b) => boolLit(b)
    case JStr(s) => s
}

func boolLit(b bool) string = if (b) "true" else "false"
`,
		"dispatch.gala": `package mcp

// Match subject produced by a cross-file method chain must infer as string,
// and every val must get an inferable type (inlay hints depend on it).
func dispatch(v JsonValue) string {
    val method = v.GetString().GetOrElse("")
    return method match {
        case "initialize" => "init"
        case _ => "other"
    }
}
`,
	})

	c, err := startLSP(galaBin)
	if err != nil {
		t.Fatalf("start gala lsp: %v", err)
	}
	defer c.close()

	if err := c.initialize(dir); err != nil {
		t.Fatalf("initialize: %v", err)
	}

	files := []string{"jsonvalue.gala", "render.gala", "dispatch.gala"}
	for _, f := range files {
		if err := c.openFile(filepath.Join(dir, f)); err != nil {
			t.Fatalf("open %s: %v", f, err)
		}
		time.Sleep(250 * time.Millisecond)
	}
	// Give the server time to publish diagnostics for all files.
	time.Sleep(1500 * time.Millisecond)

	for _, f := range files {
		path := filepath.Join(dir, f)
		if errs := c.errorDiagnostics(path); len(errs) > 0 {
			for _, d := range errs {
				t.Errorf("%s: unexpected diagnostic at line %d: %s", f, d.Range.Start.Line+1, d.Message)
			}
		}
	}
}

// TestPathWithSpacesResolvesSiblings guards the percent-decoding half of
// uriToPath end-to-end: when the project directory name contains a space the
// editor sends a percent-encoded URI ("file:///.../my%20proj/render.gala").
// uriToPath must decode it AND keep the leading slash so sibling discovery
// still finds the package's other files.
func TestPathWithSpacesResolvesSiblings(t *testing.T) {
	galaBin := findGalaBinary(t)

	base := writeFixture(t, nil)
	dir := filepath.Join(base, "my proj")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	fixtures := map[string]string{
		"gala.mod": "module example.com/spaced\n\ngala 1.0\n",
		"types.gala": `package mcp

sealed type Color {
    case Red
    case Green
}
`,
		"use.gala": `package mcp

func name(c Color) string = c match {
    case Red => "red"
    case Green => "green"
}
`,
	}
	for name, content := range fixtures {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	c, err := startLSP(galaBin)
	if err != nil {
		t.Fatalf("start gala lsp: %v", err)
	}
	defer c.close()

	if err := c.initialize(dir); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	for _, f := range []string{"types.gala", "use.gala"} {
		if err := c.openFile(filepath.Join(dir, f)); err != nil {
			t.Fatalf("open %s: %v", f, err)
		}
		time.Sleep(250 * time.Millisecond)
	}
	time.Sleep(1200 * time.Millisecond)

	usePath := filepath.Join(dir, "use.gala")
	if errs := c.errorDiagnostics(usePath); len(errs) > 0 {
		for _, d := range errs {
			t.Errorf("use.gala (spaced dir): unexpected diagnostic at line %d: %s", d.Range.Start.Line+1, d.Message)
		}
	}
}
