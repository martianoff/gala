package gala2lean

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestIsTypingGap checks which Lean errors count as a type Core JSON does not
// carry: an Option or Either, or one of the program's generic declarations
// where Lean names what it could not infer; never a generic name that only
// appears elsewhere in the message.
func TestIsTypingGap(t *testing.T) {
	const stuck = "Prog1.lean:10:18: error: typeclass instance problem is stuck\n  "
	const synth = "Prog1.lean:10:18: error: don't know how to synthesize implicit argument 'T'\n  "
	generic := []string{"Chain", "first"}
	cases := []struct {
		name, msg string
		want      bool
	}{
		{"an Option's element type", stuck + "GShow (GOption ?m.3)", true},
		{"a generic type's argument", stuck + "GShow (Chain ?m.3)", true},
		{"a generic function's type argument", stuck + "GShow ?m.3", true},
		{"a generic constructor's implicit argument", synth + "@Chain.End ?m.5\ncontext: ...", true},
		{"a generic function's implicit argument", synth + "@P.first ?m.3 ?m.4 x", true},
		{"a generic name only in the context", synth + "@P.other ?m.3\ncontext:\nx : Chain Int64", false},
		{"a generic name inside the stuck type", stuck + "GShow (Other (Chain ?m.3))", false},
		{"a non-generic type", stuck + "GShow (Pair ?m.3)", false},
		{"another error", "Prog1.lean:1:1: error: unknown identifier 'Chain'", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			require.Equal(t, c.want, isTypingGap(c.msg, generic))
		})
	}
	require.False(t, isTypingGap(stuck+"GShow ?m.3", nil), "a bare metavariable in a program without generics")
}
