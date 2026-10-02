package lowering_test

import (
	"bytes"
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"

	"github.com/bazelbuild/rules_go/go/tools/bazel"
	"github.com/stretchr/testify/require"
)

// TestCrossModuleApplyLoweringWithRequireDirective verifies that the
// gala_transpile genrule lowers sealed-case and Go-style struct constructors
// correctly when the consumer ships its own gala.mod with a `require`
// directive (no `replace`) and the dep is supplied via Bazel's
// bazel_dep + local_path_override mechanism.
//
// This is the regression test for three cross-module lowering bugs
// (zero-field sealed cases, fielded sealed cases, and plain-struct lambda
// params) reported against gala-server. The earlier external_gala_consumer fixture passed
// because its consumer had no gala.mod at all, so the resolver never
// walked the require list and the bug never fired in that fixture. This
// fixture differs in exactly one way: gala.mod is present with a require
// entry pointing at the dep's module path. That's enough to drive the
// resolver into the `isGalaPackageInCache` branch — which under Bazel
// always misses (the cache at ~/.gala/cache is empty in the sandbox)
// and, before the fix, short-circuited IsGalaPackage to false.
//
// With IsGalaPackage falsely returning false the analyzer routed the
// dep through AnalyzeGoPackage and discarded its sealed-case Apply
// metadata and struct field types, producing all three broken
// lowerings on the consumer side: bare `Case[T]()` conversions for
// zero-field variants, named-field struct literals on zero-field
// variant structs (which Go reports as "unknown field" or "too many
// arguments in conversion"), and Go-style structs with raw field
// values plus lambda parameters typed `any` instead of the concrete
// element type.
func TestCrossModuleApplyLoweringWithRequireDirective(t *testing.T) {
	genFile, err := bazel.Runfile("bazel_test_fixtures/xmod_consumer_galamod/main.gen.go")
	require.NoError(t, err, "main.gen.go must be present in runfiles")

	data, err := os.ReadFile(genFile)
	require.NoError(t, err)
	got := withoutComments(t, data)

	// Zero-field sealed case must lower to {}.Apply().
	require.True(t, strings.Contains(got, "Halt[int]{}.Apply()"),
		"expected Halt[int]{}.Apply() in generated Go, got:\n%s", got)
	require.False(t, containsBareConversion(got, "Halt[int]"),
		"unexpected bare conversion Halt[int]() in generated Go, got:\n%s", got)

	// Fielded sealed case must lower to {}.Apply(arg), never
	// to a named-field struct literal against the zero-field variant struct.
	require.True(t, strings.Contains(got, "Yield[int]{}.Apply("),
		"expected Yield[int]{}.Apply(...) in generated Go, got:\n%s", got)
	require.False(t, strings.Contains(got, "Yield[int]{Val:"),
		"unexpected named-field struct literal Yield[int]{Val: ...} on zero-field variant struct, got:\n%s", got)

	// Plain struct must keep concrete-typed lambda parameters.
	// Cross-module struct field metadata must be loaded so the consumer
	// emits `func(x int) int` rather than `func(x any) any`.
	require.True(t, strings.Contains(got, "Container[int]{"),
		"expected Container[int]{...} in generated Go, got:\n%s", got)
	require.False(t, strings.Contains(got, "func(x any) any"),
		"lambda parameter must not collapse to func(any) any, got:\n%s", got)
	require.True(t, strings.Contains(got, "func(x int) int"),
		"expected concrete-typed lambda func(x int) int in generated Go, got:\n%s", got)
}

// containsBareConversion reports whether the source text contains a bare
// type-conversion call of the form `<typeExpr>()` (zero arguments) that
// would silently bypass the sealed-case Apply lowering. Excludes the
// `<typeExpr>{}.Apply()` form which is the correct lowering and shares a
// textual prefix.
func containsBareConversion(source, typeExpr string) bool {
	needle := typeExpr + "("
	idx := 0
	for {
		offset := strings.Index(source[idx:], needle)
		if offset < 0 {
			return false
		}
		hitEnd := idx + offset + len(needle)
		if hitEnd <= len(source) && source[hitEnd-1] == '(' && hitEnd < len(source) && source[hitEnd] == ')' {
			return true
		}
		idx = idx + offset + len(needle)
	}
}

// withoutComments returns the generated Go reprinted without its comments. The
// assertions here are about lowered code; the generated file also carries the
// fixture's doc comments, whose prose names the broken shapes being guarded
// against.
func withoutComments(t *testing.T, src []byte) string {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "", src, parser.SkipObjectResolution)
	require.NoError(t, err)
	var buf bytes.Buffer
	require.NoError(t, format.Node(&buf, fset, file))
	return buf.String()
}
