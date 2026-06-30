package lsp_test

import (
	"strings"
	"testing"
	"time"
)

// These reproduce cross-file false positives observed in a real project: code
// that compiles cleanly with `gala build` but produced bogus diagnostics in the
// editor. Root cause: uriToPath dropped the leading slash of POSIX absolute
// paths, so sibling-directory discovery resolved nothing and the package's
// sealed-type companions / method signatures were invisible to the analyzer.
//
// These run through the in-process handler. They only catch the regression
// because the test harness now builds real-editor URIs (file:///abs/path); see
// fileURIForPath. The end-to-end variant lives in lspintegration, which drives
// the actual `gala lsp` binary.

// jsonValueSrc mirrors gala-mcp's jsonvalue.gala (trimmed).
const jsonValueSrc = `package mcp

sealed type JsonValue {
    case JNull
    case JBool(Bool bool)
    case JNum(Num float64)
    case JStr(Str string)
}

func (v JsonValue) AsString() Option[string] = v match {
    case JStr(s) => Some(s)
    case _ => None[string]()
}

func (v JsonValue) GetString(key string) Option[string] =
    v.AsString()
`

// Issue 1: a nullary sealed variant declared without parens (`case JNull`) and
// matched without parens (`case JNull =>`) must NOT be flagged as an unused
// pattern variable. It is a constructor pattern.
func TestRepro_NullaryVariantBareCase_NoUnusedVar(t *testing.T) {
	h := newHarness(t)
	dir := createTestProject(t, []testProjectFile{
		{Name: "jsonvalue.gala", Src: `package mcp

sealed type JsonValue {
    case JNull
    case JBool(Bool bool)
    case JStr(Str string)
}`},
		{Name: "render.gala", Src: `package mcp

func RenderJson(v JsonValue) string = v match {
    case JNull => "null"
    case JBool(b) => boolLit(b)
    case JStr(s) => s
}

func boolLit(b bool) string = if (b) "true" else "false"
`},
	})
	openProjectFile(t, h, dir, "jsonvalue.gala")
	uri := openProjectFile(t, h, dir, "render.gala")
	time.Sleep(300 * time.Millisecond)
	for _, d := range h.Diagnostics(uri) {
		if strings.Contains(d.Message, "unused variable 'JNull'") {
			t.Fatalf("false-positive unused variable for nullary variant JNull: %q at line %d", d.Message, d.Range.Start.Line)
		}
	}
}

// Issue 2: matching on a string-typed value produced by a method chain
// (`request.GetString("method").GetOrElse("")`) must infer the subject type.
func TestRepro_MatchOnChainGetOrElse_InfersType(t *testing.T) {
	h := newHarness(t)
	dir := createTestProject(t, []testProjectFile{
		{Name: "jsonvalue.gala", Src: jsonValueSrc},
		{Name: "dispatch.gala", Src: `package mcp

func dispatch(request JsonValue) string {
    val method = request.GetString("method").GetOrElse("")
    return method match {
        case "initialize" => "init"
        case _ => "other"
    }
}
`},
	})
	openProjectFile(t, h, dir, "jsonvalue.gala")
	uri := openProjectFile(t, h, dir, "dispatch.gala")
	time.Sleep(300 * time.Millisecond)
	for _, d := range h.Diagnostics(uri) {
		if strings.Contains(d.Message, "cannot infer type of matched expression") {
			t.Fatalf("false inference failure on chain GetOrElse: %q at line %d", d.Message, d.Range.Start.Line)
		}
	}
}

// Issue 3: inlay type hints should appear on every inferable val, not only the first.
func TestRepro_InlayHintsOnEveryVal(t *testing.T) {
	h := newHarness(t)
	dir := createTestProject(t, []testProjectFile{
		{Name: "jsonvalue.gala", Src: jsonValueSrc},
		{Name: "dispatch.gala", Src: `package mcp

func dispatch(request JsonValue) string {
    val a = request.GetString("a").GetOrElse("")
    val b = request.GetString("b").GetOrElse("")
    val c = request.GetString("c").GetOrElse("")
    return a + b + c
}
`},
	})
	openProjectFile(t, h, dir, "jsonvalue.gala")
	uri := openProjectFile(t, h, dir, "dispatch.gala")
	time.Sleep(300 * time.Millisecond)
	hints := requestInlayHints(t, h, uri, 0, 20)
	count := 0
	for _, hint := range hints {
		if strings.Contains(string(hint.Label), "string") {
			count++
		}
	}
	if count < 3 {
		t.Fatalf("expected string hints on all 3 vals, got %d: %v", count, hints)
	}
}
