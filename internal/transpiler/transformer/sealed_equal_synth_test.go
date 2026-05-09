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

// TestSealedEqualSynthesized verifies that the transpiler auto-generates
// an Equal(other) method on every sealed parent type — the same way it
// does for regular structs (see GALA.MD §4 "Automatic Copy and Equal
// Methods"). The body must compare every field including _variant via
// std.Equal, so that distinct variants are inequal even when their
// non-_variant fields happen to share zero values.
//
// gala_team (FocusEquals comment in app/ui/view.gala:728-730) reported
// having to hand-write equality dispatch for sealed types because Equal
// "isn't synthesised for sealed cases by default." This regression test
// locks the synthesis so the manual helper stays unnecessary.
func TestSealedEqualSynthesized(t *testing.T) {
	p := transpiler.NewAntlrGalaParser()
	a := analyzer.NewGalaAnalyzer(p, getStdSearchPath())
	tr := transformer.NewGalaASTTransformer()
	g := generator.NewGoCodeGenerator()
	trans := transpiler.NewGalaToGoTranspiler(p, a, tr, g)

	tests := []struct {
		name     string
		input    string
		mustHave []string
	}{
		{
			name: "zero-field sealed type — Equal compares only _variant",
			input: `package main

sealed type FocusState {
    case FocusComposer()
    case FocusSidebar()
}

func main() {
    val a FocusState = FocusComposer()
    val b FocusState = FocusComposer()
    Println(a.Equal(b))
}
`,
			mustHave: []string{
				"func (s FocusState) Equal(other FocusState) bool",
				"std.Equal(s._variant, other._variant)",
			},
		},
		{
			name: "multi-field sealed type — Equal dispatches field-wise",
			input: `package main

sealed type Status {
    case StIdle()
    case StRecovering(Attempt int, Max int)
    case StHelpWaiting(Question string)
}

func main() {
    val a Status = StRecovering(1, 2)
    val b Status = StRecovering(1, 2)
    Println(a.Equal(b))
}
`,
			mustHave: []string{
				"func (s Status) Equal(other Status) bool",
				"std.Equal(s.Attempt, other.Attempt)",
				"std.Equal(s.Max, other.Max)",
				"std.Equal(s.Question, other.Question)",
				"std.Equal(s._variant, other._variant)",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := trans.Transpile(tt.input, "")
			assert.NoError(t, err, "transpilation should succeed")
			gen := stripGeneratedHeader(got)
			for _, frag := range tt.mustHave {
				assert.True(t, strings.Contains(gen, frag),
					"expected generated code to contain %q\n--- generated ---\n%s", frag, gen)
			}
		})
	}
}
