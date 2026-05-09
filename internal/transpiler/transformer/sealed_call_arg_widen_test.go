package transformer_test

import (
	"strings"
	"testing"

	"martianoff/gala/internal/transpiler"
	"martianoff/gala/internal/transpiler/analyzer"
	"martianoff/gala/internal/transpiler/generator"
	"martianoff/gala/internal/transpiler/transformer"

	"github.com/stretchr/testify/assert"
)

// TestSealedCaseConstructorWidensAtCallArgSite verifies that a sealed
// case constructor used as a function-call argument widens to the
// parent sealed type, regardless of whether the call uses positional
// args, named args, or zero args.
//
// The gala_team report (FIX-013) was that `f(StRecovering(Attempt = a,
// Max = m))` failed to widen at call-arg sites for multi-arg cases
// while `return StRecovering(Attempt = a, Max = m)` did. The
// workaround was to wrap construction in a return-typed helper. This
// regression test locks the call-arg widening path so the workaround
// stays unnecessary.
//
// All variants must transpile to `<Variant>{}.Apply(...)` (which
// returns the parent sealed type), not to a direct struct literal that
// would fail to widen.
func TestSealedCaseConstructorWidensAtCallArgSite(t *testing.T) {
	p := transpiler.NewAntlrGalaParser()
	a := analyzer.NewGalaAnalyzer(p, getStdSearchPath())
	tr := transformer.NewGalaASTTransformer()
	g := generator.NewGoCodeGenerator()
	trans := transpiler.NewGalaToGoTranspiler(p, a, tr, g)

	src := `package main

sealed type Status {
    case StIdle()
    case StWorking()
    case StRecovering(Attempt int, Max int)
    case StHelpWaiting(Question string)
}

func describe(s Status) string = s match {
    case StIdle()           => "idle"
    case StWorking()        => "working"
    case StRecovering(a, m) => s"r $a/$m"
    case StHelpWaiting(q)   => s"h $q"
}

func main() {
    Println(describe(StIdle()))
    Println(describe(StWorking()))
    Println(describe(StHelpWaiting("?")))
    Println(describe(StHelpWaiting(Question = "why")))
    Println(describe(StRecovering(1, 2)))
    Println(describe(StRecovering(Attempt = 3, Max = 4)))
}
`

	got, err := trans.Transpile(src, "")
	assert.NoError(t, err, "transpilation should succeed")
	gen := stripGeneratedHeader(got)

	// Each call site must lower to a companion-Apply call (which
	// returns the parent Status type), so widening is automatic.
	mustHave := []string{
		"describe(StIdle{}.Apply())",
		"describe(StWorking{}.Apply())",
		`describe(StHelpWaiting{}.Apply("?"))`,
		`describe(StHelpWaiting{}.Apply("why"))`,
		"describe(StRecovering{}.Apply(1, 2))",
		"describe(StRecovering{}.Apply(3, 4))",
	}
	for _, frag := range mustHave {
		assert.True(t, strings.Contains(gen, frag),
			"expected generated code to contain %q\n--- generated ---\n%s", frag, gen)
	}

	// A direct composite literal like `Status{...}` at the call site
	// would NOT widen — make sure none of the call sites took that
	// path. (The struct itself is constructed inside Apply, not at
	// the call site.)
	mustMiss := []string{
		"describe(Status{",
	}
	for _, frag := range mustMiss {
		assert.False(t, strings.Contains(gen, frag),
			"expected generated code to NOT contain %q\n--- generated ---\n%s", frag, gen)
	}
}
