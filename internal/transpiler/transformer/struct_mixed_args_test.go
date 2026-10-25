package transformer_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"martianoff/gala/galaerr"
)

// TestStructPositionalThenNamedArgs pins struct construction with positional
// arguments followed by named ones: the positional ones bind the leading
// fields, the named ones their fields, and the rest take their defaults — the
// rule a function call follows. The positional ones used to be dropped, so
// `Cfg("a", Loud = true)` reported GALA-E0045 for the "Name" it was given.
// examples/struct_mixed_args.gala runs the same shapes.
func TestStructPositionalThenNamedArgs(t *testing.T) {
	const decls = `package main

import . "martianoff/gala/go_interop"

struct Cfg(Name string, Tries int = 3, Loud bool = false)

// An Apply makes a positional call with fewer arguments than fields go to it;
// a call naming a field is still a construction.
struct Opt(Name Option[string], Tries int = 3, Loud bool = false)

func (o Opt) Apply(n int) int = n
`
	cases := []struct {
		name      string
		body      string
		contains  []string
		wantCode  galaerr.ErrorCode
		wantError string
	}{
		{
			name:     "a positional argument and a named one",
			body:     `func main() { Println(Cfg("a", Loud = true)) }`,
			contains: []string{`Name: std.NewImmutable("a")`, `Loud: std.NewImmutable(true)`, `Tries: std.NewImmutable(3)`},
		},
		{
			name:     "a positional argument takes its field's type on a struct with an Apply",
			body:     `func main() { Println(Opt(None(), Loud = true)) }`,
			contains: []string{`Name: std.NewImmutable(std.None[string]{}.Apply())`, `Loud: std.NewImmutable(true)`},
		},
		{
			name:      "a field given by position and by name",
			body:      `func main() { Println(Cfg("a", Name = "b")) }`,
			wantCode:  galaerr.CodeMissingStructField,
			wantError: `main.gala:12:31 field "Name" of "Cfg" is given both by position and by name`,
		},
		{
			name:      "more arguments than fields",
			body:      `func main() { Println(Cfg("a", 1, true, "x", Tries = 2)) }`,
			wantCode:  galaerr.CodeArgumentCountMismatch,
			wantError: `too many arguments in call to Cfg: expected at most 3, got 5`,
		},
		{
			name:      "a positional argument after a named one",
			body:      `func main() { Println(Cfg(Tries = 1, "a")) }`,
			wantCode:  galaerr.CodeMissingStructField,
			wantError: `a positional argument follows a named one in construction of "Cfg"`,
		},
		{
			name: "a spread positional argument",
			body: `func main() {
    val names = SliceOf("a")
    Println(Cfg(names..., Loud = true))
}`,
			wantError: `a spread argument cannot fill field "Name" in construction of "Cfg"`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := transpileBareVariant(t, decls+tc.body)
			if tc.wantError != "" {
				require.Error(t, err)
				require.Contains(t, err.Error(), string(tc.wantCode))
				require.Contains(t, err.Error(), tc.wantError)
				return
			}
			require.NoError(t, err)
			for _, want := range tc.contains {
				require.Contains(t, out, want)
			}
		})
	}
}
