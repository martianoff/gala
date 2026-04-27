package transformer_test

import (
	"strings"
	"testing"

	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/analyzer"
	"martianoff/gala/internal/transpiler/generator"
	"martianoff/gala/internal/transpiler/transformer"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestExpectedArgStackInvariant_NestedSealedConstructors stress-tests the
// B1 expectedArgTypeStack contract via the public transpiler API.
//
// Before B1, downward-inference state was a single transformer field
// whose save/restore bookkeeping was scattered across 6+ sites. A nested
// constructor that consumed the hint left the outer push site's deferred
// restore in a stale state — so any unbalanced sequence silently leaked
// state across siblings, demoting inferred types to `any`.
//
// This test compiles a source with a nested sealed-variant constructor
// inside another sealed-variant constructor's argument. Every push must
// pair with its unwind cleanly, and the dispatcher's `consume` of an
// outer hint must not corrupt the deferred pop. If imbalance returns,
// either:
//
//   - The transpile fails (pop-of-empty-stack panic surfaces as
//     GALA-E0017 via the B4 wrap), or
//   - A type silently widens to `any` in the generated Go.
func TestExpectedArgStackInvariant_NestedSealedConstructors(t *testing.T) {
	p := transpiler.NewAntlrGalaParser()
	a := analyzer.NewGalaAnalyzer(p, getStdSearchPath())
	tr := transformer.NewGalaASTTransformer()
	g := generator.NewGoCodeGenerator()
	trans := transpiler.NewGalaToGoTranspiler(p, a, tr, g)

	// Nested generic sealed-variant constructor: outer slot expects
	// Cmd[Option[Int]], so the inner Some(7) needs Option[Int] from
	// the outer hint, threaded through RunCmd's parameter inference.
	src := `package main

sealed type Cmd[T any] {
    case NoCmd()
    case RunCmd(arg T)
}

func main() {
    val nested Cmd[Option[Int]] = RunCmd(Some(7))
    Println(nested)
}`
	out, err := trans.Transpile(src, "")
	require.NoError(t, err,
		"nested sealed-variant ctor must transpile cleanly — "+
			"a stack imbalance surfaces here as GALA-E0017 or a type widening")

	// The annotation `Cmd[Option[Int]]` should round-trip into the
	// generated Go with both type params resolved. No `any` should
	// appear in any Cmd or Option ctor.
	assert.True(t, strings.Contains(out, "Cmd[std.Option[Int]]") ||
		strings.Contains(out, "Cmd[Option[Int]]"),
		"Cmd[Option[Int]] should round-trip into generated Go\n--- generated ---\n%s", out)
	assert.False(t, strings.Contains(out, "Cmd[any]"),
		"Cmd[any] indicates downward inference leaked\n--- generated ---\n%s", out)
}

// TestExpectedArgStackInvariant_SiblingValDecls verifies that consecutive
// val declarations do not contaminate each other's expected-type frames.
// This is the "between top-level statements" depth-zero invariant: even
// without a depth() accessor, a sibling that pushes the wrong type would
// flow into the next sibling's RHS and produce visibly-wrong Go.
func TestExpectedArgStackInvariant_SiblingValDecls(t *testing.T) {
	p := transpiler.NewAntlrGalaParser()
	a := analyzer.NewGalaAnalyzer(p, getStdSearchPath())
	tr := transformer.NewGalaASTTransformer()
	g := generator.NewGoCodeGenerator()
	trans := transpiler.NewGalaToGoTranspiler(p, a, tr, g)

	src := `package main

sealed type Cmd[T any] {
    case NoCmd()
    case RunCmd(arg T)
}

func main() {
    val first Cmd[Int]    = NoCmd()
    val second Cmd[String] = NoCmd()
    val third Cmd[Bool]   = NoCmd()
    Println(first)
    Println(second)
    Println(third)
}`
	out, err := trans.Transpile(src, "")
	require.NoError(t, err)

	// Each NoCmd() should resolve to its own slot's type. If the stack
	// leaked across siblings, one of these would carry the prior frame's
	// type parameter. (GALA preserves the source-level `Int`/`String`/
	// `Bool` type aliases through codegen.)
	for _, want := range []string{"Cmd[Int]", "Cmd[String]", "Cmd[Bool]"} {
		assert.True(t, strings.Contains(out, want),
			"missing %q — sibling val decls must not contaminate each other's expected-type frames\n--- generated ---\n%s",
			want, out)
	}
	assert.False(t, strings.Contains(out, "Cmd[any]"),
		"Cmd[any] indicates downward inference leaked across siblings\n--- generated ---\n%s", out)
}
