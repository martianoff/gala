package galaerr_test

import (
	"encoding/json"
	"testing"

	"martianoff/gala/galaerr"

	"github.com/stretchr/testify/require"
)

// TestRenderJSONCarriesEverythingTheFrameShows pins A1: a consumer must be able
// to get the code, message, hint, position and docs link without parsing the
// framed ASCII output.
func TestRenderJSONCarriesEverythingTheFrameShows(t *testing.T) {
	err := galaerr.NewCodedSemanticError(
		galaerr.CodeBareLambdaParam, 7, 19,
		"lambda parameters must be parenthesized",
		"use `(x) => ...`",
	).WithSpan(20)
	err.FilePath = "pkg/main.gala"

	out, jsonErr := galaerr.RenderJSON(error(err))
	require.NoError(t, jsonErr)

	var got struct {
		Diagnostics []galaerr.Diagnostic `json:"diagnostics"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &got))
	require.Len(t, got.Diagnostics, 1)

	d := got.Diagnostics[0]
	require.Equal(t, "error", d.Severity)
	require.Equal(t, "GALA-E0042", d.Code)
	require.Equal(t, "lambda parameters must be parenthesized", d.Message)
	require.Equal(t, "use `(x) => ...`", d.Hint)
	require.Equal(t, "pkg/main.gala", d.File)
	require.Equal(t, 7, d.Line)
	// Columns are 1-based in JSON, matching the `-->` locus, while the error
	// stores them 0-based.
	require.Equal(t, 20, d.Column)
	require.Equal(t, 21, d.EndColumn)
	require.Equal(t, "https://gala.fyi/docs/errors/gala-e0042/", d.DocsURL)
}

// TestRenderJSONFlattensMultiError verifies every contained diagnostic survives.
func TestRenderJSONFlattensMultiError(t *testing.T) {
	multi := &galaerr.MultiError{Errors: []error{
		galaerr.NewCodedSemanticError(galaerr.CodeUnknownMethod, 3, 4, "a has no method X", "did you mean `Y`?"),
		galaerr.NewSyntaxError(9, 2, "no viable alternative"),
	}}

	diags := galaerr.Diagnostics(multi)
	require.Len(t, diags, 2)
	require.Equal(t, "GALA-E0044", diags[0].Code)
	require.Equal(t, 3, diags[0].Line)
	// A syntax error is uncoded, so it carries no code and no docs link.
	require.Empty(t, diags[1].Code)
	require.Empty(t, diags[1].DocsURL)
	require.Equal(t, 9, diags[1].Line)
}

// TestRenderJSONAlwaysValid guards the contract that a consumer can always
// json.Unmarshal the output: a non-GALA error (a wrapped `go build` failure)
// still produces one well-formed entry rather than an empty list.
func TestRenderJSONAlwaysValid(t *testing.T) {
	out, jsonErr := galaerr.RenderJSON(errorsNew("go build: exit status 1"))
	require.NoError(t, jsonErr)

	var got struct {
		Diagnostics []galaerr.Diagnostic `json:"diagnostics"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &got))
	require.Len(t, got.Diagnostics, 1)
	require.Equal(t, "go build: exit status 1", got.Diagnostics[0].Message)

	// A nil error is still valid JSON with an empty list, never "null".
	empty, jsonErr := galaerr.RenderJSON(nil)
	require.NoError(t, jsonErr)
	require.NoError(t, json.Unmarshal([]byte(empty), &got))
	require.Empty(t, got.Diagnostics)
}

// errorsNew is a local helper so the test does not import "errors" solely for
// one call.
func errorsNew(msg string) error { return &plainError{msg} }

type plainError struct{ msg string }

func (e *plainError) Error() string { return e.msg }

// TestRenderJSONWithHintFillsTheFirstEmptyHint pins the review finding that
// `--json` dropped the ambiguous-main-package remediation. That advice lives in
// the CLI, not in the error, so it has to be injected for machine consumers —
// otherwise the one failure whose whole value is the suggestion loses it.
func TestRenderJSONWithHintFillsTheFirstEmptyHint(t *testing.T) {
	out, err := galaerr.RenderJSONWithHint(
		errorsNew("multiple main packages"),
		"name the one you want, for example: gala build ./cmd/app",
	)
	require.NoError(t, err)
	require.Contains(t, out, "gala build ./cmd/app")

	// A diagnostic that already carries a hint keeps its own.
	coded := galaerr.NewCodedSemanticError(galaerr.CodeUnknownMethod, 1, 0, "m", "original hint")
	out, err = galaerr.RenderJSONWithHint(error(coded), "injected")
	require.NoError(t, err)
	require.Contains(t, out, "original hint")
	require.NotContains(t, out, "injected")
}
