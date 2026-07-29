package transformer_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestSizeSugarOnGoFunctionReturn is the real, adversarially-sound guard for the
// go-FUNCTION-return path: the `.Size()` sugar must fire on the result of a
// *bare* (dot-imported) Go function, which resolves through
// inferCallIdentType's dot-import `getGoFuncReturnTypeForCall` loop —
// not the pre-existing qualified-call path in inferCallSelectorType.
//
// It is hermetic in the transformer_test sandbox: `strings` is Go stdlib, so
// its `Fields(...) []string` return resolves via goTypeInfo / the Go SDK
// (available on CI through .bazelrc --action_env=GOROOT) with NO GALA package
// wiring. Neutralizing the dot-import loop makes both cases keep a raw
// `.Size()` (which does not lower), so this test fails without the fix.
func TestSizeSugarOnGoFunctionReturn(t *testing.T) {
	trans := newForbiddenBuiltinTranspiler()

	cases := []struct {
		name  string
		input string
	}{
		{
			name:  "direct bare dot-imported Go func return",
			input: "package main\n\nimport . \"strings\"\n\nfunc f(s string) int = Fields(s).Size()",
		},
		{
			name:  "val-bound bare dot-imported Go func return",
			input: "package main\n\nimport . \"strings\"\n\nfunc f(s string) int {\n    val xs = Fields(s)\n    return xs.Size()\n}",
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			out, err := trans.Transpile(tc.input, "")
			require.NoError(t, err)
			// The sugar must have fired: a []string return lowers .Size() to len().
			require.Contains(t, out, "len(",
				".Size() on a bare dot-imported Go []string return should lower to len():\n%s", out)
			require.False(t, strings.Contains(out, ".Size()"),
				"no raw .Size() method call should survive on the Go slice return:\n%s", out)
		})
	}
}

// TestSizeSugarLowersSliceReceiverToLen covers a DISTINCT path from the
// go-function-return test above: the ArrayType-receiver → `len()` lowering when
// the receiver's slice type is already known locally (an explicitly-typed
// parameter). This path predates the go-function-return fix and is kept as its
// own guard so neither test is misleading about what it exercises.
func TestSizeSugarLowersSliceReceiverToLen(t *testing.T) {
	trans := newForbiddenBuiltinTranspiler()

	cases := []struct {
		name  string
		input string
	}{
		{
			name:  "[]rune param, direct .Size()",
			input: "package main\n\nfunc f(xs []rune) int = xs.Size()",
		},
		{
			name:  "[]int param, val-bound .Size()",
			input: "package main\n\nfunc f(xs []int) int {\n    val n = xs.Size()\n    return n\n}",
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			out, err := trans.Transpile(tc.input, "")
			require.NoError(t, err)
			require.Contains(t, out, "len(",
				".Size() on a Go slice receiver should lower to len():\n%s", out)
			require.False(t, strings.Contains(out, ".Size()"),
				"no raw .Size() method call should survive on the Go slice:\n%s", out)
		})
	}
}

// TestSizeResultTypeFlowsThroughCompositeExpr guards the interaction between the
// `.Size()` sugar and the emitted `len(...)` / `utf8.RuneCountInString(...)`:
// the sugar lowers to those calls, and downstream inference must type them as
// int so a surrounding if-expression resolves to int rather than being poisoned
// to `any`. Regression for the removed-then-restored `len` inference rule (and
// the added `utf8.RuneCountInString` rule). Hermetic: the receiver is a plain
// `string` parameter, no external package or Go SDK required.
func TestSizeResultTypeFlowsThroughCompositeExpr(t *testing.T) {
	trans := newForbiddenBuiltinTranspiler()

	input := "package main\n\n" +
		"func f(s string) int {\n" +
		"    val n = if (s.Size() > 0) s.Size() - 1 else s.Size()\n" +
		"    return n\n" +
		"}"

	out, err := trans.Transpile(input, "")
	require.NoError(t, err)
	// The if-expression IIFE must return int, not any.
	require.Contains(t, out, "func() int",
		"the if-expression over .Size() results should infer int, not any:\n%s", out)
	require.False(t, strings.Contains(out, "func() any"),
		"the if-expression result type must not collapse to any:\n%s", out)
}

// TestSizeSugarOnQualifiedGoCallReturn guards the `.Size()`/`.ByteSize()` sugar
// on the return of a QUALIFIED Go-stdlib function call (SelectorExpr `pkg.Fn(...)`)
// — distinct from the bare dot-imported case above. `strings.SplitN(s, "\n", 2)`
// returns Go `[]string`; the sugar must classify that receiver as a slice and
// lower `.Size()` to `len(...)`. Before the fix the receiver type didn't resolve
// through goTypeInfo for a qualified call, so `.Size()` was emitted verbatim and
// `go build` failed ("[]string has no field or method Size").
//
// Hermetic: `strings` is Go stdlib, resolved via the Go SDK.
func TestSizeSugarOnQualifiedGoCallReturn(t *testing.T) {
	trans := newForbiddenBuiltinTranspiler()

	cases := []struct {
		name  string
		input string
	}{
		{
			name:  "direct qualified Go call return",
			input: "package main\n\nimport \"strings\"\n\nfunc f(s string) int = strings.SplitN(s, \",\", 2).Size()",
		},
		{
			name:  "val-bound qualified Go call return",
			input: "package main\n\nimport \"strings\"\n\nfunc f(s string) int {\n    val parts = strings.SplitN(s, \",\", 2)\n    return parts.Size()\n}",
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			out, err := trans.Transpile(tc.input, "")
			require.NoError(t, err)
			require.Contains(t, out, "len(",
				".Size() on a qualified Go []string return should lower to len():\n%s", out)
			require.False(t, strings.Contains(out, ".Size()"),
				"no raw .Size() should survive on the Go slice return:\n%s", out)
		})
	}
}

// TestSizeSugarOnGoSliceIndex guards the sugar when the receiver is a Go-slice
// INDEX expression: `strings.SplitN(s, "\n", 2)[0]` is a Go `string`, so
// `.Size()` must lower to `utf8.RuneCountInString(...)` (character count). Before
// the fix the IndexExpr on a Go slice didn't resolve to its element type, so
// `.Size()` was emitted verbatim ("string has no field or method Size").
func TestSizeSugarOnGoSliceIndex(t *testing.T) {
	trans := newForbiddenBuiltinTranspiler()

	cases := []struct {
		name  string
		input string
	}{
		{
			name:  "direct Go slice index",
			input: "package main\n\nimport \"strings\"\n\nfunc f(s string) int = strings.SplitN(s, \",\", 2)[0].Size()",
		},
		{
			name:  "val-bound Go slice index",
			input: "package main\n\nimport \"strings\"\n\nfunc f(s string) int {\n    val first = strings.SplitN(s, \",\", 2)[0]\n    return first.Size()\n}",
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			out, err := trans.Transpile(tc.input, "")
			require.NoError(t, err)
			require.Contains(t, out, "utf8.RuneCountInString",
				".Size() on a Go string (slice element) should lower to utf8.RuneCountInString:\n%s", out)
			require.False(t, strings.Contains(out, ".Size()"),
				"no raw .Size() should survive on the Go string:\n%s", out)
		})
	}
}

// TestSizeSugarOnNamedGoCollectionType guards the sugar on a NAMED Go type whose
// underlying is a slice or map — `sort.StringSlice` (`type StringSlice []string`)
// and `url.Values` (`type Values map[string][]string`). The receiver resolves to
// a NamedType, not a bare ArrayType/MapType, so the sugar must consult goTypeInfo
// for the underlying and classify by it. Before the fix `.Size()` was emitted
// verbatim ("sort.StringSlice / url.Values has no field or method Size").
//
// Hermetic: sort/net/url are Go stdlib, resolved via the Go SDK.
func TestSizeSugarOnNamedGoCollectionType(t *testing.T) {
	trans := newForbiddenBuiltinTranspiler()

	cases := []struct {
		name  string
		input string
	}{
		{
			name:  "named slice type param (sort.StringSlice)",
			input: "package main\n\nimport \"sort\"\n\nfunc f(ss sort.StringSlice) int = ss.Size()",
		},
		{
			name:  "named map type param (http.Header)",
			input: "package main\n\nimport \"net/http\"\n\nfunc f(h http.Header) int = h.Size()",
		},
		{
			name:  "named map type composite literal (url.Values)",
			input: "package main\n\nimport \"net/url\"\n\nfunc f() int {\n    val v = url.Values{}\n    return v.Size()\n}",
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			out, err := trans.Transpile(tc.input, "")
			require.NoError(t, err)
			require.Contains(t, out, "len(",
				".Size() on a named Go slice/map type should lower to len():\n%s", out)
			require.False(t, strings.Contains(out, ".Size()"),
				"no raw .Size() should survive on the named Go collection type:\n%s", out)
		})
	}
}
