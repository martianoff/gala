package qualified_lowering_test

import (
	"os"
	"strings"
	"testing"

	"github.com/bazelbuild/rules_go/go/tools/bazel"
	"github.com/stretchr/testify/require"
)

// TestQualifiedCrossModuleApplyLowering verifies that gala_transpile lowers
// sealed-case constructors and named-arg struct constructors correctly when
// the dep is imported under a qualified (non-dot) name. The sibling
// external_gala_consumer fixture asserts the dot-import shape; this fixture
// asserts the SelectorExpr shape that arises from
// `import "martianoff/external_gala_module/effects"` (no dot).
//
// The hazards locked in by this test:
//
//  1. Zero-field sealed case must lower to {}.Apply(), never to a bare
//     type conversion `effects.Halt[int]()` that Go rejects on a struct
//     with no fields.
//  2. Fielded sealed case must lower to {}.Apply(arg), never to a raw
//     struct literal `effects.Yield[int]{Val: 42}` that bypasses the
//     variant invariants.
//  3. Named-arg struct constructor must lower to a struct literal whose
//     std.Immutable[T] fields are wrapped via std.NewImmutable, with a
//     concrete-typed lambda parameter (`func(x int) int`, not
//     `func(x any) any`).
//  4. The consumer's own sealed type used as a type argument to the
//     imported case (`effects.Halt[Msg]`) must propagate through the
//     dispatcher's type-arg inference even though `Msg` is defined in
//     this file, not in the imported module.
func TestQualifiedCrossModuleApplyLowering(t *testing.T) {
	genFile, err := bazel.Runfile("bazel_test_fixtures/xmod_qualified/main.gen.go")
	require.NoError(t, err, "main.gen.go must be present in runfiles")

	data, err := os.ReadFile(genFile)
	require.NoError(t, err)
	got := string(data)
	t.Logf("generated main.gen.go:\n%s", got)

	// (1) Zero-field sealed case across qualified import.
	require.True(t, strings.Contains(got, "effects.Halt[int]{}.Apply()"),
		"expected effects.Halt[int]{}.Apply() in generated Go, got:\n%s", got)
	require.False(t, containsBareConversion(got, "effects.Halt[int]"),
		"unexpected bare conversion effects.Halt[int]() in generated Go, got:\n%s", got)

	// (2) Fielded sealed case across qualified import.
	require.True(t, strings.Contains(got, "effects.Yield[int]{}.Apply("),
		"expected effects.Yield[int]{}.Apply(...) in generated Go, got:\n%s", got)
	require.False(t, strings.Contains(got, "effects.Yield[int]{Val:"),
		"unexpected raw struct literal effects.Yield[int]{Val: ...} in generated Go, got:\n%s", got)

	// (3) Named-arg struct constructor across qualified import.
	require.True(t, strings.Contains(got, "effects.Container[int]{"),
		"expected effects.Container[int]{...} in generated Go, got:\n%s", got)
	require.False(t, strings.Contains(got, "func(x any) any"),
		"lambda parameter must not collapse to func(any) any, got:\n%s", got)
	require.True(t, strings.Contains(got, "func(x int) int"),
		"expected concrete-typed lambda func(x int) int in generated Go, got:\n%s", got)
	require.True(t, strings.Contains(got, "Field: std.NewImmutable("),
		"expected Field: std.NewImmutable(...) wrapping in generated Go, got:\n%s", got)
	require.True(t, strings.Contains(got, "Cb: std.NewImmutable("),
		"expected Cb: std.NewImmutable(...) wrapping in generated Go, got:\n%s", got)

	// (4) Consumer-defined sealed type flowing into imported sealed-case
	// type arg. The match arm must lower `effects.Halt[Msg]()` to
	// `effects.Halt[Msg]{}.Apply()` (and likewise for the fielded variant).
	require.True(t, strings.Contains(got, "effects.Halt[Msg]{}.Apply()"),
		"expected effects.Halt[Msg]{}.Apply() in generated Go, got:\n%s", got)
	require.False(t, containsBareConversion(got, "effects.Halt[Msg]"),
		"unexpected bare conversion effects.Halt[Msg]() in generated Go, got:\n%s", got)
	require.True(t, strings.Contains(got, "effects.Yield[Msg]{}.Apply("),
		"expected effects.Yield[Msg]{}.Apply(...) in generated Go, got:\n%s", got)
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
