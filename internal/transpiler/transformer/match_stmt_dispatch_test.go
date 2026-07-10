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

func newMatchDispatchTranspiler() *transpiler.GalaToGoTranspiler {
	p := transpiler.NewAntlrGalaParser()
	a := analyzer.NewGalaAnalyzer(p, getStdSearchPath())
	tr := transformer.NewGalaASTTransformer()
	g := generator.NewGoCodeGenerator()
	return transpiler.NewGalaToGoTranspiler(p, a, tr, g)
}

// TestStatementPositionMatchHeterogeneousArms covers the fix for a
// statement-position (value-discarded) match whose arms call methods with
// different return types. Because the match's value is discarded, the arms are
// pure side-effect dispatch and their result types need not unify — the match
// lowers to a void IIFE. Before the fix the transpiler ran arm-type
// unification unconditionally and rejected the match with "type mismatch in
// match expression ... All branches must return the same type", even though no
// value was ever consumed. This is the shape used by hand-written pull-parsers
// (e.g. a `Skip()` that dispatches on the next byte to `readString()` →
// string, `readBool()` → bool, etc.).
func TestStatementPositionMatchHeterogeneousArms(t *testing.T) {
	trans := newMatchDispatchTranspiler()

	input := `package main

func si() string = "x"
func bi() bool = true
func vi() { Println("v") }

func dispatch(c int) {
    c match {
        case 1 => { si() }
        case 2 => { bi() }
        case 3 => { vi() }
        case _ => { Println("other") }
    }
}

func main() { dispatch(1) }`

	out, err := trans.Transpile(input, "stmt_match_test.gala")
	require.NoError(t, err, "statement-position match with heterogeneous arm types must transpile")
	// The discarded arm values must NOT be wrapped in `return ...` (Go rejects
	// `return vi()` for a void call), i.e. the match lowered as a void IIFE.
	assert.NotContains(t, out, "return si()")
	assert.NotContains(t, out, "return bi()")
}

// TestStatementMatchAfterValueReturningMethod is a regression test for a
// source-order state leak: a block-bodied, value-returning function/method
// declared BEFORE a statement-position match left the transformer's
// "block's last statement is the value" flag set to true, and the later
// (void) method inherited it — so its trailing bare `match` was not recognized
// as statement-position and its side-effect-dispatch arms were wrongly forced
// to unify to one type. The function-declaration transform now restores the
// flag after each body. The motivating real-world case was the JSON
// pull-parser's Skip(), declared after the value-returning ReadBool().
func TestStatementMatchAfterValueReturningMethod(t *testing.T) {
	trans := newMatchDispatchTranspiler()

	input := `package main

type Dec struct {
    var pos int
}

// value-returning, block body, declared BEFORE the statement-position match
func (d *Dec) readBool() bool {
    if d.pos > 0 {
        return true
    }
    Panic("eof")
}

func (d *Dec) readStr() string = "s"

func (d *Dec) skip(c int) {
    c match {
        case 1 => { d.readStr() }
        case 2 => { d.readBool() }
        case _ => { d.pos = d.pos + 1 }
    }
}

func main() {
    val d = &Dec(pos = 1)
    d.skip(2)
}`

	out, err := trans.Transpile(input, "stmt_match_order_test.gala")
	require.NoError(t, err, "statement-position match must be recognized even when a value-returning method precedes it")
	assert.NotContains(t, out, "return d.readStr()",
		"discarded arm value must not be wrapped in return (void IIFE expected)")
}

// Note on scope: discard position is recognized for a match that is itself a
// statement (the json pull-parser's flat `Skip()`/`writeEscapedJsonString`
// dispatch). A *nested* match whose value flows up through an enclosing arm
// block to a discarded outer match is still treated as value-producing (the
// outer match resets the statement-position marker for its arm bodies), so its
// arms must still unify. Threading discard-ness through block-value chains is a
// possible future extension.

// TestGuardedWildcardBeforeDefault covers the fix for a guarded wildcard
// (`case _ if g`) followed by a real default (`case _`). A guarded wildcard is
// conditional — control falls through to later cases when the guard is false —
// so it is NOT a catch-all default. Before the fix the transpiler counted the
// guarded `case _` as a default and rejected the following `case _` with
// GALA-E0006 "multiple default cases".
func TestGuardedWildcardBeforeDefault(t *testing.T) {
	trans := newMatchDispatchTranspiler()

	input := `package main

func classify(c int) string {
    var out = ""
    c match {
        case 0 => { out = "zero" }
        case _ if c < 10 => { out = "small" }
        case _ => { out = "big" }
    }
    return out
}

func main() {
    Println(classify(0))
    Println(classify(5))
    Println(classify(99))
}`

	out, err := trans.Transpile(input, "guarded_wildcard_test.gala")
	require.NoError(t, err, "guarded wildcard before a default must transpile")
	// The guard must lower to a real conditional on the guard expression.
	assert.True(t, strings.Contains(out, "c < 10"),
		"guarded wildcard arm must emit its guard condition; got:\n%s", out)
}

// TestGuardedBindingNotDefault verifies a guarded binding pattern
// (`case x if g`) is likewise conditional, so a following unguarded default is
// allowed rather than rejected as a duplicate default.
func TestGuardedBindingNotDefault(t *testing.T) {
	trans := newMatchDispatchTranspiler()

	input := `package main

func label(n int) string {
    var out = ""
    n match {
        case v if v < 0 => { out = "neg" }
        case _ => { out = "nonneg" }
    }
    return out
}

func main() { Println(label(-1)) }`

	_, err := trans.Transpile(input, "guarded_binding_test.gala")
	require.NoError(t, err, "guarded binding pattern before a default must transpile")
}
