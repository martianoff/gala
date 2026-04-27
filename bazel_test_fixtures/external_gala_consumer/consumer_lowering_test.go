package consumer_lowering_test

import (
	"os"
	"strings"
	"testing"

	"github.com/bazelbuild/rules_go/go/tools/bazel"
	"github.com/stretchr/testify/require"
)

// TestCrossModuleApplyLoweringForExternalRepoDep verifies that
// gala_transpile lowers sealed-type and struct constructors correctly
// when the dep is an external Bazel module (rather than an in-repo
// gala_library). The fixture under bazel_test_fixtures/external_gala_module
// is wired into the workspace via bazel_dep + local_path_override so the
// dep's source files are materialised under bazel-out/.../external/
// external_gala_module+/..., faithfully exercising the cross-repository
// path through Bazel's external-repository machinery.
//
// On the broken macro the gala CLI received --search entries derived
// from the raw label string (token "@external_gala_module"), the
// transpiler's resolver could not walk that as a filesystem path, the
// dep was misclassified as a Go package, and the consumer regressed to
// emitting Halt[int]() / Yield[int]{Val: 42} / Container[int]{... func(any, any) ...}.
// The fixed macro hands the CLI the actual on-disk parent directories
// of the dep's source files, so the resolver finds the GALA package and
// preserves its sealed/struct metadata.
func TestCrossModuleApplyLoweringForExternalRepoDep(t *testing.T) {
	genFile, err := bazel.Runfile("bazel_test_fixtures/external_gala_consumer/consumer_0.gen.go")
	require.NoError(t, err, "consumer_0.gen.go must be present in runfiles")

	data, err := os.ReadFile(genFile)
	require.NoError(t, err)
	got := string(data)
	t.Logf("generated consumer_0.gen.go:\n%s", got)

	// Zero-field sealed case must lower to {}.Apply(), never to a bare
	// type conversion that Go rejects on a struct with no fields.
	require.True(t, strings.Contains(got, "Halt[int]{}.Apply()"),
		"expected Halt[int]{}.Apply() in generated Go, got:\n%s", got)
	require.False(t, containsBareConversion(got, "Halt[int]"),
		"unexpected bare conversion Halt[int]() in generated Go, got:\n%s", got)

	// Fielded sealed case must lower to {}.Apply(arg), never to a bare
	// struct literal Yield[int]{Val: 42} which would still compile but
	// breaks the variant's invariants (skips the Apply lowering pass).
	require.True(t, strings.Contains(got, "Yield[int]{}.Apply("),
		"expected Yield[int]{}.Apply(...) in generated Go, got:\n%s", got)
	require.False(t, strings.Contains(got, "Yield[int]{Val:"),
		"unexpected bare struct literal Yield[int]{Val: ...} in generated Go, got:\n%s", got)

	// Plain struct must keep concrete-typed lambda parameters. The bug
	// surface for cross-module deps was that the resolver lost the
	// struct's field-type metadata and the lambda regressed to func(any) any.
	require.True(t, strings.Contains(got, "Container[int]{"),
		"expected Container[int]{...} in generated Go, got:\n%s", got)
	require.False(t, strings.Contains(got, "func(x any) any"),
		"lambda parameter must not collapse to func(any) any, got:\n%s", got)
	require.True(t, strings.Contains(got, "func(x int) int"),
		"expected concrete-typed lambda func(x int) int in generated Go, got:\n%s", got)

	// Top-level cross-module function call: the analyzer must load the
	// imported package's function metadata so the lambda's first parameter
	// gets the declared `int` type and the result type stays string. If the
	// signature is dropped, the lambda regresses to `func(i any) any` and
	// the body's string-interpolation produces the wrong return type.
	require.False(t, strings.Contains(got, "func(i any) any"),
		"top-level cross-module lambda must not collapse to func(i any) any, got:\n%s", got)
	require.False(t, strings.Contains(got, "func(i any) string"),
		"top-level cross-module lambda parameter must not be typed any, got:\n%s", got)
	require.True(t, strings.Contains(got, "func(i int) string"),
		"expected concrete-typed lambda func(i int) string for TabulateInts call, got:\n%s", got)
}

// containsBareConversion reports whether the source text contains a bare
// type-conversion call of the form `<typeExpr>()` (zero arguments) that
// would silently bypass the sealed-case Apply lowering. The check
// excludes the `<typeExpr>{}.Apply()` form, which is the correct
// lowering and shares a textual prefix with the broken shape.
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
